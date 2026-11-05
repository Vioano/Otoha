#!/usr/bin/env python3
"""A disposable backend experiment, not Otoha's production graph adapter.

Python standard library only. Starts one authenticated loopback-only SurrealDB
process with synthetic data, records queries/results, and removes its data on exit.
"""

import argparse
import base64
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.request


SCHEMA = """
DEFINE TABLE node TYPE NORMAL SCHEMALESS;
DEFINE TABLE edge TYPE RELATION IN node OUT node ENFORCED SCHEMALESS;
"""


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, ensure_ascii=False,
                                    separators=(",", ":")).encode()).hexdigest()


def reachable(graph, start):
    """Independent finite-graph oracle for the quotient counterexample."""
    seen, pending = {start}, [start]
    while pending:
        current = pending.pop()
        for edge in graph["edges"]:
            if edge["source"] == current and edge["target"] not in seen:
                seen.add(edge["target"])
                pending.append(edge["target"])
    return sorted(seen)


def rid(table, identifier):
    # Fixed synthetic fixture IDs only; this is not a public query compiler.
    if not re.fullmatch(r"[a-z][a-z0-9_]*", identifier):
        raise ValueError(identifier)
    return f"{table}:{identifier}"


def insert_graph(graph):
    statements = []
    for node in graph["nodes"]:
        statements.append(f"CREATE {rid('node', node['id'])} CONTENT "
                          + json.dumps({"p": node["p"]}, ensure_ascii=False) + ";")
    for edge in graph["edges"]:
        statements.append(f"RELATE {rid('node', edge['source'])}"
                          f"->{rid('edge', edge['id'])}->{rid('node', edge['target'])} CONTENT "
                          + json.dumps({"p": edge["p"]}, ensure_ascii=False) + ";")
    return "\n".join(statements)


class Probe:
    def __init__(self, binary, work, report):
        self.binary, self.work, self.report = binary, work, report
        self.process = None
        self.db = "core"
        self.password = secrets.token_urlsafe(24)
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def start(self):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        self.url = f"http://127.0.0.1:{port}"
        self.log = open(self.work / "server.log", "ab")
        env = dict(os.environ, SURREAL_USER="spike", SURREAL_PASS=self.password)
        started = time.perf_counter()
        self.process = subprocess.Popen(
            [str(self.binary), "start", "--no-banner", "--log", "warn", "--bind",
             f"127.0.0.1:{port}", f"surrealkv://{self.work / 'data'}"],
            env=env, stdout=self.log, stderr=self.log)
        deadline = time.monotonic() + 25
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise RuntimeError((self.work / "server.log").read_text(errors="replace"))
            try:
                with self.opener.open(self.url + "/health", timeout=0.5) as response:
                    if response.status == 200:
                        self.report["startup_ms"].append(round((time.perf_counter() - started) * 1000, 2))
                        return
            except OSError:
                time.sleep(0.05)
        raise RuntimeError("database did not become healthy within 25 seconds")

    def stop(self):
        if self.process is not None and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        if hasattr(self, "log"):
            self.log.close()

    def query(self, sql, allow_error=False):
        auth = base64.b64encode(f"spike:{self.password}".encode()).decode()
        request = urllib.request.Request(self.url + "/sql", data=sql.encode(), headers={
            "Authorization": f"Basic {auth}", "Accept": "application/json",
            "Content-Type": "text/plain", "surreal-ns": "otoha_spike", "surreal-db": self.db})
        started = time.perf_counter()
        with self.opener.open(request, timeout=20) as response:
            result = json.load(response)
        self.report["queries"].append({"database": self.db, "sql": sql, "response": result,
                                       "elapsed_ms": round((time.perf_counter() - started) * 1000, 3)})
        errors = [row for row in result if row.get("status") != "OK"]
        if errors and not allow_error:
            raise RuntimeError(json.dumps(errors, ensure_ascii=False))
        return result

    def last(self, sql):
        return self.query(sql)[-1]["result"]

    def check(self, name, condition, details=None):
        self.report["checks"].append({"name": name, "passed": bool(condition), "details": details})
        if not condition:
            raise AssertionError(f"{name}: {details}")

    def rejected(self, name, sql):
        result = self.query(sql, allow_error=True)
        self.check(name, any(row.get("status") == "ERR" for row in result), result)

    def snapshot(self):
        result = self.query("BEGIN; SELECT id, p FROM node ORDER BY id; "
                            "SELECT id, in, out, p FROM edge ORDER BY id; COMMIT;")
        rows = [row["result"] for row in result if isinstance(row.get("result"), list)]
        nodes, edges = rows[-2:]
        return {
            "nodes": [{"id": row["id"].split(":", 1)[1], "p": row["p"]} for row in nodes],
            "edges": [{"id": row["id"].split(":", 1)[1], "source": row["in"].split(":", 1)[1],
                       "target": row["out"].split(":", 1)[1], "p": row["p"]} for row in edges]}

    def run(self, fixture):
        self.start()
        self.query("DEFINE NAMESPACE otoha_spike; USE NS otoha_spike; DEFINE DATABASE core; "
                   "USE DB core; " + SCHEMA)
        self.check("empty_graph", self.snapshot() == {"nodes": [], "edges": []})
        self.query("BEGIN; " + insert_graph(fixture) + " COMMIT;")
        actual = self.snapshot()
        canonical = lambda graph: {key: sorted(value, key=lambda item: item["id"])
                                   for key, value in graph.items()}
        self.check("core_roundtrip_multiedges_loop_cycle_free_properties",
                   actual == canonical(fixture), actual)
        self.query("CREATE node:e1 CONTENT {p: {}};")
        self.check("node_edge_identity_namespaces_distinct",
                   self.last("RETURN {node_id: node:e1.id, edge_id: edge:e1.id};") ==
                   {"node_id": "node:e1", "edge_id": "edge:e1"})
        self.query("DELETE node:e1;")
        self.check("missing_and_null_distinct",
                   self.last("RETURN {present: node:b1.p.note = NULL, missing: node:c.p.note = NONE};")
                   == {"present": True, "missing": True})
        self.query("UPDATE node:b1 UNSET p.note;")
        self.check("prop_unset_removes_key", "note" not in self.last("SELECT VALUE p FROM ONLY node:b1;"))
        self.query("UPDATE node:b1 SET p.note = NULL; UPDATE node:loose SET p.experimental = [1, 'new', NULL];")
        self.check("new_property_without_ddl", self.last("RETURN node:loose.p.experimental;") == [1, "new", None])
        self.query("UPDATE node:loose UNSET p.experimental;")
        self.rejected("duplicate_node_rejected", "CREATE node:a CONTENT {p: {}};")
        self.rejected("dangling_edge_rejected_by_enforced_relation",
                      "RELATE node:a->edge:bad->node:missing CONTENT {p: {}};")
        before = self.snapshot()
        self.rejected("atomic_batch_reports_error", "BEGIN; UPDATE node:a SET p.partial = true; "
                      "CREATE node:rolledback CONTENT {p: {}}; THROW 'EXPECTED_ROLLBACK'; COMMIT;")
        self.check("atomic_batch_leaves_no_partial_state", self.snapshot() == before)

        # Minimal illustration of Otoha-owned preconditions; not a version/history implementation.
        self.query("CREATE meta:head SET revision = 0;")
        self.query("BEGIN; IF node:a.p.status != 'ready' { THROW 'STALE_GUARD'; }; "
                   "UPDATE node:a SET p.status = 'done'; UPDATE meta:head SET revision += 1; COMMIT;")
        accepted = self.snapshot()
        self.rejected("stale_precondition_rejected", "BEGIN; IF node:a.p.status != 'ready' "
                      "{ THROW 'STALE_GUARD'; }; UPDATE node:a SET p.status = 'overwritten'; "
                      "UPDATE meta:head SET revision += 1; COMMIT;")
        self.check("stale_request_did_not_advance_or_overwrite",
                   self.snapshot() == accepted and self.last("RETURN meta:head.revision;") == 1)

        before = self.snapshot()
        projected = self.query("BEGIN; SELECT p.group AS group, array::sort(array::group(id)) AS members "
                               "FROM node WHERE p.group != NONE GROUP BY group ORDER BY group; "
                               "SELECT in.p.group AS source_group, out.p.group AS target_group, "
                               "array::sort(array::group(id)) AS members, count() AS count "
                               "FROM edge GROUP BY source_group, target_group ORDER BY source_group, target_group; "
                               "SELECT id, p FROM node WHERE p.group = 'B' ORDER BY id; "
                               "SELECT id, in, out FROM edge WHERE in IN [node:b1,node:b2] OR out IN [node:b1,node:b2] "
                               "ORDER BY id; RETURN meta:head.revision; COMMIT;")
        # SurrealDB 3.3 returns explicit null entries for BEGIN and COMMIT.
        groups, quotient, expanded, boundary, revision = [row["result"] for row in projected[1:-1]]
        self.check("projection_group_source_members", groups == [
            {"group": "A", "members": ["node:a"]},
            {"group": "B", "members": ["node:b1", "node:b2"]},
            {"group": "C", "members": ["node:c"]}], groups)
        ab = next(row for row in quotient if row["source_group"] == "A" and row["target_group"] == "B")
        self.check("quotient_retains_parallel_edge_members", ab["members"] == ["edge:e1", "edge:e2"] and ab["count"] == 2)
        self.check("expand_and_boundary_ids", [row["id"] for row in expanded] == ["node:b1", "node:b2"]
                   and {row["id"] for row in boundary} == {"edge:e1", "edge:e2", "edge:e3", "edge:back"})
        two_hop = self.last("RETURN node:a->edge->node->edge->node;")
        self.check("quotient_false_path_not_source_two_hop_path",
                   any(row["source_group"] == "B" and row["target_group"] == "C" for row in quotient)
                   and "node:c" not in two_hop and "node:a" in two_hop, two_hop)
        source_reachable = reachable(before, "a")
        self.check("source_graph_oracle_has_no_path_from_a_to_c", source_reachable == ["a", "b1"], source_reachable)
        self.check("projection_did_not_mutate_core", self.snapshot() == before)
        self.report["projection"] = {"source_revision": revision, "groups": groups, "quotient": quotient,
                                      "expanded": expanded, "boundary_edges": boundary,
                                      "complete_for": "nodes with p.group; group B incident edges",
                                      "ungrouped_node_ids": ["node:loose"], "source_two_hop_from_a": two_hop}
        self.report["projection"]["source_reachable_from_a_oracle"] = source_reachable
        self.query("UPDATE meta:head SET revision += 1;")
        self.rejected("stale_projection_expansion_rejected",
                      "BEGIN; IF meta:head.revision != 1 { THROW 'STALE_PROJECTION'; }; "
                      "SELECT * FROM node WHERE p.group = 'B'; COMMIT;")

        native = self.query("UPDATE edge:e2 SET out = node:c;", allow_error=True)
        native_ok = all(row["status"] == "OK" for row in native)
        native_readback = self.last("SELECT id, in, out, p FROM ONLY edge:e2;")
        self.report["native_rewire"] = {"returned_ok": native_ok, "response": native,
                                         "readback": native_readback,
                                         "applied": native_readback["out"] == "node:c"}
        self.check("raw_endpoint_update_ignored_gap_recorded", native_readback["out"] == "node:b1", native_readback)
        # Explicit candidate operation: replace the physical edge atomically,
        # keeping Otoha's logical edge ID. This is not a silent retry/fallback.
        self.query("BEGIN; LET $p = edge:e2.p; DELETE edge:e2; "
                   "RELATE node:a->edge:e2->node:c CONTENT {p: $p}; COMMIT;")
        self.check("rewire_keeps_logical_edge_id_and_properties",
                   self.last("SELECT id, in, out, p FROM ONLY edge:e2;") ==
                   {"id": "edge:e2", "in": "node:a", "out": "node:c",
                    "p": next(edge["p"] for edge in fixture["edges"] if edge["id"] == "e2")})
        self.check("rewire_updates_graph_traversal", "node:c" in self.last("RETURN node:a->edge->node;"))
        self.query("BEGIN; LET $p = edge:e2.p; DELETE edge:e2; "
                   "RELATE node:a->edge:e2->node:b1 CONTENT {p: $p}; COMMIT;")
        self.check("rewire_back_removes_old_adjacency", "node:c" not in self.last("RETURN node:a->edge->node;"))

        before = self.snapshot()
        self.rejected("guarded_node_delete_rejects_incident_edges",
                      "BEGIN; IF array::len((SELECT VALUE id FROM edge WHERE in = node:a OR out = node:a)) > 0 "
                      "{ THROW 'INCIDENT_EDGES'; }; DELETE node:a; COMMIT;")
        self.check("guarded_delete_preserves_core", self.snapshot() == before)
        self.query("CREATE node:temp1 CONTENT {p: {}}; CREATE node:temp2 CONTENT {p: {}}; "
                   "RELATE node:temp1->edge:temporary->node:temp2 CONTENT {p: {}}; DELETE node:temp1;")
        self.check("raw_backend_delete_cascades_edges_gap_recorded",
                   self.last("SELECT * FROM edge:temporary;") == [])
        self.query("DELETE node:temp2;")
        final = self.snapshot()
        self.report["final_graph"] = final
        self.report["final_graph_sha256"] = digest(final)
        self.stop()
        self.start()
        self.check("graceful_restart_persists_exact_graph", self.snapshot() == final)
        self.query("DEFINE DATABASE roundtrip;")
        self.db = "roundtrip"
        self.query(SCHEMA + "BEGIN; " + insert_graph(final) + " COMMIT;")
        self.check("export_import_preserves_ids_endpoints_properties", self.snapshot() == final)
        self.report["data_bytes"] = sum(path.stat().st_size for path in (self.work / "data").rglob("*") if path.is_file())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    binary = args.binary.resolve()
    fixture_path = Path(__file__).with_name("fixture.json")
    fixture = json.loads(fixture_path.read_text())
    report = {"experiment": "otoha-core-surreal-minimal", "status": "running",
              "started_utc": datetime.now(timezone.utc).isoformat(),
              "platform": {"os": platform.system(), "architecture": platform.machine(), "python": platform.python_version()},
              "backend": subprocess.check_output([str(binary), "version"], text=True).strip(),
              "binary_bytes": binary.stat().st_size, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "probe_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              "fixture_sha256": hashlib.sha256(fixture_path.read_bytes()).hexdigest(),
              "startup_ms": [], "checks": [], "queries": []}
    with tempfile.TemporaryDirectory(prefix="otoha-surreal-spike-") as work:
        probe = Probe(binary, Path(work), report)
        try:
            probe.run(fixture)
            report["status"] = "passed"
        except Exception as error:
            report["status"] = "failed"
            report["error"] = str(error)
            log = Path(work) / "server.log"
            if log.exists():
                report["server_log"] = log.read_text(errors="replace")[-6000:]
        finally:
            probe.stop()
    report["finished_utc"] = datetime.now(timezone.utc).isoformat()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"status": report["status"], "passed": sum(row["passed"] for row in report["checks"]),
                      "checks": len(report["checks"]), "error": report.get("error"), "output": str(args.output)}, ensure_ascii=False))
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
