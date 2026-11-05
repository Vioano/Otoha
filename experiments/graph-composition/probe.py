#!/usr/bin/env python3
"""Check graph-reference storage boundaries with disposable synthetic data.

This is an experiment, not an Otoha graph resolver. The ordinary p.ref property
and the explicit GraphID-to-database mapping are conventions of this sample.
"""

import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
from pathlib import Path
import platform
import subprocess
import sys
import tempfile


def load_helpers(path):
    # Keep loading this shared helper read-only, including bytecode output.
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location("otoha_graph_probe_helpers", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.Probe, module.SCHEMA


def run_sample(probe, schema, report):
    probe.start()
    probe.db = "enterprise"
    probe.query("DEFINE NAMESPACE otoha_spike; USE NS otoha_spike; "
                "DEFINE DATABASE enterprise; USE DB enterprise; " + schema)
    for database in ["shop", "pay"]:
        probe.query(f"DEFINE DATABASE {database}; USE DB {database}; " + schema)

    probe.db = "enterprise"
    probe.query("""
        CREATE node:shop CONTENT {p: {label: "Shop", ref: {graph_id: "g_shop"}}};
        CREATE node:pay CONTENT {p: {label: "Pay", ref: {graph_id: "g_pay"}}};
        CREATE node:pay_other CONTENT {p: {label: "Another Pay view", ref: {graph_id: "g_pay"}}};
        RELATE node:shop->edge:depends->node:pay CONTENT {p: {relation: "depends_on"}};
        CREATE node:shared CONTENT {p: {owner: "enterprise"}};
    """)
    references = probe.last('SELECT id, p FROM node WHERE p.ref.graph_id = "g_pay" ORDER BY id;')
    probe.check("two_ordinary_nodes_reference_same_logical_graph",
                len(references) == 2 and all(row["p"]["ref"]["graph_id"] == "g_pay" for row in references),
                references)

    probe.db = "shop"
    probe.query('CREATE node:shared CONTENT {p: {owner: "shop"}}; '
                'CREATE node:checkout CONTENT {p: {label: "Checkout"}};')
    probe.db = "pay"
    probe.query('CREATE node:shared CONTENT {p: {owner: "pay"}}; '
                'CREATE node:refund CONTENT {p: {label: "Refund API", version: 1}};')

    registry = {"g_enterprise": "enterprise", "g_shop": "shop", "g_pay": "pay"}
    report["explicit_test_registry"] = registry
    addresses = []
    for graph_id, database in registry.items():
        probe.db = database
        row = probe.last("SELECT id, p FROM ONLY node:shared;")
        addresses.append({"graph_id": graph_id, **row})
    probe.check("colliding_local_ids_preserved_in_separate_databases",
                [row["p"]["owner"] for row in addresses] == ["enterprise", "shop", "pay"], addresses)
    report["qualified_addresses"] = addresses

    target_graph = references[0]["p"]["ref"]["graph_id"]
    probe.db = registry[target_graph]
    refund = probe.last("SELECT id, p FROM ONLY node:refund;")
    probe.check("explicit_graph_id_to_database_routing_reads_target",
                refund == {"id": "node:refund", "p": {"label": "Refund API", "version": 1}}, refund)

    probe.db = "enterprise"
    probe.check("target_record_not_present_in_enterprise_scope",
                probe.last("SELECT * FROM node:refund;") == [])
    probe.rejected("native_enforced_edge_does_not_resolve_other_database_target",
                   "RELATE node:shop->edge:invalid_cross->node:refund CONTENT {p: {}};")
    probe.check("rejected_cross_database_edge_not_persisted",
                probe.last("SELECT * FROM edge:invalid_cross;") == [])
    probe.query("DELETE node:pay;")
    probe.check("deleting_one_reference_preserves_other_reference",
                probe.last("SELECT VALUE p.ref.graph_id FROM ONLY node:pay_other;") == "g_pay")
    probe.db = "pay"
    after = probe.last("SELECT id, p FROM ONLY node:refund;")
    probe.check("deleting_enterprise_reference_does_not_delete_target", after == refund, after)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    script = Path(__file__).resolve()
    helper = script.parent.parent / "graph-db-surreal" / "probe.py"
    binary = args.binary.resolve()
    report = {
        "experiment": "logical-graph-reference-storage-boundary",
        "status": "running",
        "started_utc": datetime.now(timezone.utc).isoformat(),
        "platform": {"os": platform.system(), "architecture": platform.machine(),
                     "python": platform.python_version()},
        "scope": "synthetic storage representation and explicit routing only; not an Otoha resolver, "
                 "authorization policy, graph revision protocol, federation or multi-database atomicity test",
        "helper": "experiments/graph-db-surreal/probe.py",
        "probe_sha256": hashlib.sha256(script.read_bytes()).hexdigest(),
        "startup_ms": [], "checks": [], "queries": [],
    }
    try:
        if not binary.is_file() or not helper.is_file():
            raise FileNotFoundError("required existing database binary or repository helper is missing")
        report["backend"] = subprocess.check_output([str(binary), "version"], text=True, timeout=10).strip()
        report["binary_bytes"] = binary.stat().st_size
        report["binary_sha256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
        report["helper_sha256"] = hashlib.sha256(helper.read_bytes()).hexdigest()
        probe_class, schema = load_helpers(helper)
        with tempfile.TemporaryDirectory(prefix="otoha-composition-spike-") as work:
            probe = probe_class(binary, Path(work), report)
            try:
                run_sample(probe, schema, report)
                report["status"] = "passed"
            finally:
                probe.stop()
    except FileNotFoundError as error:
        report["status"], report["error"] = "not_run", str(error)
    except Exception as error:
        report["status"], report["error"] = "failed", str(error)
    report["finished_utc"] = datetime.now(timezone.utc).isoformat()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"status": report["status"], "checks": len(report["checks"]),
                      "passed": sum(row["passed"] for row in report["checks"]),
                      "error": report.get("error"), "output": str(args.output)}, ensure_ascii=False))
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
