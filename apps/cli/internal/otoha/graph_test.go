package otoha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func graphFixture(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	t.Setenv("OTOHA_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer original-graph-credential" {
			t.Error("graph did not reuse the enrolled project credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	profile := filepath.Join(Home(), "graph-profile.json")
	if err := Save(profile, Object{"url": server.URL, "actor": "member", "token": "original-graph-credential"}); err != nil {
		t.Fatal(err)
	}
	if err := saveTeamConfig(TeamConfig{Name: "graph-team", Mode: "member", URL: server.URL, Project: "project-core", Channel: "project-core-general", Profile: profile}); err != nil {
		t.Fatal(err)
	}
	return "graph-team"
}

func graphFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func graphReceipt(requestID string) Object {
	return Object{"schema": "otoha-graph/1", "graph_id": "project-core", "scope": Object{"project": "project-core"}, "complete": true, "truncated": false,
		"state": "accepted", "request_id": requestID, "expected_revision": 0, "source_version": 1, "request_sha256": "synthetic-intent-digest", "duplicate": false}
}

func TestGraphManifestAndQueryReuseJoinedIdentityAndKeepBoundaries(t *testing.T) {
	var requests atomic.Int32
	team := graphFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method == http.MethodGet && r.URL.Path == "/v1/graph/manifest" {
			if r.URL.Query().Get("project") != "project-core" {
				t.Error("manifest used a different project")
			}
			json.NewEncoder(w).Encode(Object{"schema": "otoha-graph/1", "source_version": 7, "scope": Object{"project": "project-core"}, "complete": true})
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/graph/query" {
			t.Error("unexpected endpoint", r.Method, r.URL.Path)
		}
		var input Object
		json.NewDecoder(r.Body).Decode(&input)
		// Match the API's per-mode whitelist: inspect has no limit field.
		for key := range input {
			if key != "project" && key != "mode" && key != "kind" && key != "id" && key != "source_version" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(Object{"error": "graph_query_field_not_supported"})
				return
			}
		}
		if S(input, "project") != "project-core" || S(input, "mode") != "inspect" || S(input, "id") != "first" {
			t.Error("query lost the explicit selection", input)
		}
		json.NewEncoder(w).Encode(Object{"schema": "otoha-graph/1", "source_version": 7, "scope": Object{"project": "project-core", "mode": "inspect"}, "complete": false, "truncated": true, "next_after": "edge-1", "nodes": []Object{{"id": "first", "properties": Object{"custom": "自由属性"}}}})
	})
	before, _ := os.ReadFile(filepath.Join(Home(), "graph-profile.json"))
	manifest, err := GraphCommand(context.Background(), []string{"manifest", team})
	if err != nil || N(manifest, "source_version") != 7 {
		t.Fatal(manifest, err)
	}
	result, err := GraphCommand(context.Background(), []string{"query", team, "--mode", "inspect", "--id", "first"})
	if err != nil || N(result, "source_version") != 7 || !B(result, "truncated") || B(result, "complete") || S(result, "next_after") != "edge-1" {
		t.Fatal("query hid projection boundaries", result, err)
	}
	wrongProject := graphFile(t, `{"project":"another-project","mode":"filter"}`)
	if _, err = GraphCommand(context.Background(), []string{"query", team, "--file", wrongProject}); err == nil || requests.Load() != 2 {
		t.Fatal("input project replaced the joined context", err, requests.Load())
	}
	after, _ := os.ReadFile(filepath.Join(Home(), "graph-profile.json"))
	if string(before) != string(after) {
		t.Fatal("graph changed the enrolled identity")
	}
}

func TestGraphAcceptedButLostResponseRecoversWithoutResubmission(t *testing.T) {
	var posts, lookups atomic.Int32
	requestID := "original-intent"
	team := graphFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			lookups.Add(1)
			if r.URL.Path != "/v1/graph/requests/"+requestID || r.URL.Query().Get("project") != "project-core" {
				t.Error("lookup lost the original ID or project", r.URL)
			}
			json.NewEncoder(w).Encode(graphReceipt(requestID))
			return
		}
		posts.Add(1)
		if r.URL.Path != "/v1/graph/mutations" {
			t.Error("wrong mutation endpoint", r.URL.Path)
		}
		// The original must already be durable when the server sees the POST.
		stored, err := graphLoadIntent(graphIntentPath("pending", "graph-team", requestID))
		if err != nil || S(O(stored, "request"), "request_id") != requestID || S(stored, "state") != "pending" {
			t.Error("request was not saved before its effect", stored, err)
		}
		var input Object
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		decoder.Decode(&input)
		operation := Items(input, "operations")[0]
		if O(operation, "properties")["large"].(json.Number).String() != "9007199254740991" {
			t.Error("caller number changed before submission", input)
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close() // accepted remotely; response is deliberately lost
	})
	file := graphFile(t, `{"request_id":"original-intent","expected_revision":0,"operations":[{"op":"node.add","id":"first","properties":{"large":9007199254740991}}]}`)
	result, err := GraphCommand(context.Background(), []string{"mutate", team, "--file", file})
	if err == nil || S(result, "state") != "pending" || B(result, "accepted") || posts.Load() != 1 {
		t.Fatal("unknown response was treated as accepted", result, err, posts.Load())
	}
	stored, err := graphLoadIntent(graphIntentPath("pending", team, requestID))
	if err != nil || S(O(stored, "request"), "request_id") != requestID {
		t.Fatal("original pending intent was lost", stored, err)
	}
	// A new command invocation uses only persisted intent and the authority.
	result, err = GraphCommand(context.Background(), []string{"result", team, requestID})
	if err != nil || S(result, "state") != "accepted" || posts.Load() != 1 || lookups.Load() != 1 {
		t.Fatal("receipt lookup resubmitted the mutation", result, err, posts.Load(), lookups.Load())
	}
	if _, err = os.Stat(graphIntentPath("pending", team, requestID)); !os.IsNotExist(err) {
		t.Fatal("accepted pending entry was not retired", err)
	}
	if _, err = graphLoadIntent(graphIntentPath("receipts", team, requestID)); err != nil {
		t.Fatal("accepted receipt was not durable", err)
	}
	result, err = GraphCommand(context.Background(), []string{"mutate", team, "--file", file})
	if err != nil || S(result, "state") != "accepted" || posts.Load() != 1 || lookups.Load() != 2 {
		t.Fatal("explicit retry did not check the original receipt", result, err)
	}
	changed := graphFile(t, `{"request_id":"original-intent","expected_revision":1,"operations":[{"op":"node.remove","id":"first"}]}`)
	if _, err = GraphCommand(context.Background(), []string{"mutate", team, "--file", changed}); err == nil || posts.Load() != 1 || lookups.Load() != 2 {
		t.Fatal("same ID changed its original intent", err)
	}
}

func TestGraphReaderWriteAndUnrelatedMemberReadAreRejected(t *testing.T) {
	var calls atomic.Int32
	team := graphFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		code := "project_forbidden"
		if r.Method == http.MethodPost {
			code = "graph_write_required"
		}
		json.NewEncoder(w).Encode(Object{"error": code})
	})
	if _, err := GraphCommand(context.Background(), []string{"manifest", team}); !IsStatus(err, 403) {
		t.Fatal("unrelated member read failure was hidden", err)
	}
	file := graphFile(t, `{"request_id":"reader-write","expected_revision":0,"operations":[{"op":"node.add","id":"first","properties":{}}]}`)
	result, err := GraphCommand(context.Background(), []string{"mutate", team, "--file", file})
	if !IsStatus(err, 403) || S(result, "state") != "rejected" || S(result, "error_code") != "graph_write_required" || B(result, "accepted") {
		t.Fatal("reader write was accepted or hidden", result, err)
	}
	stored, err := graphLoadIntent(graphIntentPath("pending", team, "reader-write"))
	if err != nil || S(stored, "state") != "rejected" || S(O(stored, "request"), "request_id") != "reader-write" || calls.Load() != 2 {
		t.Fatal("permission error discarded the original request", stored, err)
	}
}

func TestGraphMissingReceiptDoesNotSubmitUntilExplicitRetry(t *testing.T) {
	var posts, lookups atomic.Int32
	team := graphFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			lookups.Add(1)
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Object{"error": "graph_request_not_found"})
			return
		}
		if posts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(Object{"error": "graph_backend_unavailable"})
			return
		}
		json.NewEncoder(w).Encode(graphReceipt("resume-original"))
	})
	file := graphFile(t, `{"request_id":"resume-original","expected_revision":0,"operations":[{"op":"node.add","id":"first","properties":{}}]}`)
	result, err := GraphCommand(context.Background(), []string{"mutate", team, "--file", file})
	if err == nil || S(result, "state") != "pending" {
		t.Fatal(result, err)
	}
	result, err = GraphCommand(context.Background(), []string{"result", team, "resume-original"})
	if !IsStatus(err, 404) || S(result, "state") != "pending" || B(result, "receipt_found") || posts.Load() != 1 {
		t.Fatal("lookup of missing receipt caused a submission", result, err)
	}
	result, err = GraphCommand(context.Background(), []string{"mutate", team, "--file", file})
	if err != nil || S(result, "state") != "accepted" || posts.Load() != 2 || lookups.Load() != 2 {
		t.Fatal("explicit same-intent retry did not recover", result, err)
	}
}

func TestGraphChangedIdentityCannotReplaySavedIntent(t *testing.T) {
	var calls atomic.Int32
	team := graphFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(Object{"error": "graph_backend_unavailable"})
	})
	file := graphFile(t, `{"request_id":"identity-bound","expected_revision":0,"operations":[{"op":"node.add","id":"first","properties":{}}]}`)
	GraphCommand(context.Background(), []string{"mutate", team, "--file", file})
	profilePath := filepath.Join(Home(), "graph-profile.json")
	profile, _ := Load(profilePath)
	profile["actor"] = "another-member"
	Save(profilePath, profile)
	for _, args := range [][]string{{"mutate", team, "--file", file}, {"result", team, "identity-bound"}} {
		if _, err := GraphCommand(context.Background(), args); err == nil || !strings.Contains(err.Error(), "another project, identity or authority") {
			t.Fatal("saved intent crossed an identity change", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("changed identity used the saved request over the network", calls.Load())
	}
}

func TestGraphAcceptedReceiptMissingFromAuthorityCannotBeReplayed(t *testing.T) {
	var posts atomic.Int32
	team := graphFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Object{"error": "graph_request_not_found"})
			return
		}
		posts.Add(1)
		json.NewEncoder(w).Encode(graphReceipt("previously-accepted"))
	})
	file := graphFile(t, `{"request_id":"previously-accepted","expected_revision":0,"operations":[{"op":"node.add","id":"first","properties":{}}]}`)
	result, err := GraphCommand(context.Background(), []string{"mutate", team, "--file", file})
	if err != nil || S(result, "state") != "accepted" {
		t.Fatal(result, err)
	}
	result, err = GraphCommand(context.Background(), []string{"mutate", team, "--file", file})
	if err == nil || S(result, "state") != "unknown" || S(result, "local_state") != "accepted" || posts.Load() != 1 {
		t.Fatal("previously accepted request was replayed after missing receipt", result, err, posts.Load())
	}
	stored, err := graphExistingIntent(team, "previously-accepted")
	if err != nil || S(stored, "state") != "accepted" {
		t.Fatal("original acceptance evidence was replaced", stored, err)
	}
}

func TestGraphExplicitRetryPersistsPendingBeforeReattemptingRejectedWrite(t *testing.T) {
	var posts atomic.Int32
	team := graphFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Object{"error": "graph_request_not_found"})
			return
		}
		if posts.Add(1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(Object{"error": "graph_write_required"})
			return
		}
		stored, err := graphLoadIntent(graphIntentPath("pending", "graph-team", "retry-after-grant"))
		if err != nil || S(stored, "state") != "pending" {
			t.Error("explicit retry still persisted a rejected state", stored, err)
		}
		json.NewEncoder(w).Encode(graphReceipt("retry-after-grant"))
	})
	file := graphFile(t, `{"request_id":"retry-after-grant","expected_revision":0,"operations":[{"op":"node.add","id":"first","properties":{}}]}`)
	if _, err := GraphCommand(context.Background(), []string{"mutate", team, "--file", file}); !IsStatus(err, 403) {
		t.Fatal(err)
	}
	result, err := GraphCommand(context.Background(), []string{"mutate", team, "--file", file})
	if err != nil || S(result, "state") != "accepted" || posts.Load() != 2 {
		t.Fatal(result, err, posts.Load())
	}
}

func TestGraphInvalidAcceptanceVersionsKeepOriginalPending(t *testing.T) {
	for _, scenario := range []string{"different_expected_revision", "fractional_source_version", "missing_source_version"} {
		t.Run(scenario, func(t *testing.T) {
			team := graphFixture(t, func(w http.ResponseWriter, r *http.Request) {
				receipt := graphReceipt("invalid-receipt")
				switch scenario {
				case "different_expected_revision":
					receipt["expected_revision"] = 2
				case "fractional_source_version":
					receipt["source_version"] = 1.5
				default:
					delete(receipt, "source_version")
				}
				json.NewEncoder(w).Encode(receipt)
			})
			file := graphFile(t, `{"request_id":"invalid-receipt","expected_revision":0,"operations":[{"op":"node.add","id":"first","properties":{}}]}`)
			result, err := GraphCommand(context.Background(), []string{"mutate", team, "--file", file})
			if err == nil || S(result, "state") != "pending" || B(result, "accepted") {
				t.Fatal("mismatched receipt retired the pending intent", result, err)
			}
			if _, err = graphLoadIntent(graphIntentPath("pending", team, "invalid-receipt")); err != nil {
				t.Fatal("original intent was lost", err)
			}
		})
	}
}
