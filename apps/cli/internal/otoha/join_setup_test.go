package otoha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestJoinedSetupReportsOnlyActualChecksAndRefreshesReceipt(t *testing.T) {
	for _, scenario := range []string{"current", "legacy", "graph_unavailable", "report_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("OTOHA_HOME", t.TempDir())
			posts, graphCalls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-member" {
					t.Error("setup did not use the joined project identity")
				}
				switch r.URL.Path {
				case "/v1/manifest":
					capabilities := []string{}
					if scenario != "legacy" {
						capabilities = append(capabilities, "setup_ready")
					}
					json.NewEncoder(w).Encode(Object{"actor": Object{"id": "alice"}, "capabilities": capabilities})
				case "/v1/graph/manifest":
					graphCalls++
					if scenario == "graph_unavailable" {
						w.WriteHeader(503)
						json.NewEncoder(w).Encode(Object{"error": "graph_backend_unavailable"})
						return
					}
					json.NewEncoder(w).Encode(Object{"schema": "otoha-graph/1", "scope": Object{"project": "sample"}, "ready": true})
				case "/v1/setup-ready":
					posts++
					if r.Method != http.MethodPost || r.URL.Query().Get("project") != "sample" {
						t.Error("incorrect setup receipt route")
					}
					var body Object
					json.NewDecoder(r.Body).Decode(&body)
					if len(body) != 1 || len(O(body, "checks")) != 4 {
						t.Error("setup request did not match the strict four-check contract", body)
					}
					checks := O(body, "checks")
					if B(checks, "graph") != (scenario != "graph_unavailable") {
						t.Error("graph was reported without a successful actual manifest", checks)
					}
					if scenario == "report_unavailable" {
						w.WriteHeader(503)
						return
					}
					json.NewEncoder(w).Encode(Object{"schema": "otoha-setup-ready/1", "project": "sample", "receipt": Object{"actor": "alice", "device": "fixture-mac", "checks": checks, "ready": scenario != "graph_unavailable", "state": "self_report", "reported_at": 1700000000 + posts, "user_accepted": false}})
				default:
					t.Error("setup expanded into an unrelated API", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			cfg := TeamConfig{Name: "sample", Project: "sample", Profile: filepath.Join(Home(), "identity.json")}
			if err := Save(cfg.Profile, Object{"url": server.URL, "actor": "alice", "device": "fixture-mac", "token": "fixture-member"}); err != nil {
				t.Fatal(err)
			}
			for invocation := 0; invocation < 2; invocation++ {
				result, checks := Object{"ready": true}, Object{"repository": "ready", "service": "ready", "communication": "online"}
				err := reportJoinedSetup(context.Background(), cfg, result, checks)
				if scenario == "current" && (err != nil || !B(result, "ready") || !B(result, "setup_ready_reported") || S(result, "setup_state") != "complete" || B(result, "user_accepted")) {
					t.Fatal("current setup did not reach its native terminal state", result, err)
				}
				if scenario == "legacy" && (err != nil || !B(result, "ready") || S(result, "setup_state") != "unsupported") {
					t.Fatal("legacy authority join was broken by the new receipt API", result, err)
				}
				if (scenario == "graph_unavailable" || scenario == "report_unavailable") && (err == nil || B(result, "ready")) {
					t.Fatal("failed current setup was presented as complete", result, err)
				}
			}
			if scenario == "legacy" && (posts != 0 || graphCalls != 0) || scenario != "legacy" && (posts != 2 || graphCalls != 2) {
				t.Fatal("join resume did not obtain fresh actual checks/receipt, or legacy called an unsupported API", posts, graphCalls)
			}
		})
	}
}
