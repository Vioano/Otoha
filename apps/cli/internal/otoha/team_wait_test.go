package otoha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func setupWaitReceipt(actor string, at float64, ready bool) Object {
	return Object{"actor": actor, "device": "fixture", "reported_at": at, "state": "self_report", "ready": ready,
		"checks": Object{"repository": ready, "service": ready, "graph": ready, "communication": ready}, "user_accepted": false}
}

func setupWaitStatus(receipts ...Object) Object {
	return Object{"schema": "otoha-setup-ready/1", "project": "wait-team", "scope": "project", "complete": true, "truncated": false,
		"receipts": receipts, "expected_members": []string{}, "team_ready": true}
}

func setupWaitFixture(t *testing.T, capability bool, status func(int32) Object) *atomic.Int32 {
	t.Helper()
	t.Setenv("OTOHA_HOME", t.TempDir())
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("setup wait did not reuse the team's existing identity")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/manifest":
			capabilities := []string{"messages"}
			if capability {
				capabilities = append(capabilities, "setup_ready")
			}
			json.NewEncoder(w).Encode(Object{"capabilities": capabilities})
		case "/v1/setup-ready":
			if r.URL.Query().Get("project") != "wait-team" {
				t.Error("setup wait queried another project")
			}
			json.NewEncoder(w).Encode(status(requests.Add(1)))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	profile := filepath.Join(Home(), "identity.json")
	if err := Save(profile, Object{"actor": "captain", "token": "fixture-token", "url": server.URL}); err != nil {
		t.Fatal(err)
	}
	if err := saveTeamConfig(TeamConfig{Name: "wait-team", Mode: "local", URL: server.URL, Project: "wait-team", Channel: "general", Profile: profile}); err != nil {
		t.Fatal(err)
	}
	return requests
}

func setupWaitSession(t *testing.T, members []string) {
	t.Helper()
	if err := Save(initSessionPath("wait-team"), Object{"team": "wait-team", "owner": "captain", "answers": Object{"members": members}}); err != nil {
		t.Fatal(err)
	}
}

func TestTeamWaitUsesExpectedMembersAndLatestSelfReport(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  Object
		ready   bool
		pending int
	}{
		{"missing-member-despite-backend-ready", setupWaitStatus(setupWaitReceipt("captain", 1, true)), false, 1},
		{"all-reported", setupWaitStatus(setupWaitReceipt("captain", 1, true), setupWaitReceipt("alice", 2, true)), true, 0},
		{"latest-incomplete-replaces-old-ready", setupWaitStatus(setupWaitReceipt("captain", 1, true), setupWaitReceipt("alice", 2, true), setupWaitReceipt("alice", 3, false)), false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupWaitFixture(t, true, func(int32) Object { return tc.status })
			setupWaitSession(t, []string{"alice"})
			result, err := TeamCommand(context.Background(), []string{"wait", "wait-team", "--timeout", "0"})
			if err != nil || B(result, "ready") != tc.ready || B(result, "team_ready") != tc.ready || len(result["pending_members"].([]Object)) != tc.pending {
				t.Fatal("setup result did not follow the local expected roster and latest reports", result, err)
			}
			if S(result, "checks_source") != "self_report" || B(result, "user_accepted") || S(result, "expected_source") != "init_session" {
				t.Fatal("setup reports implied user acceptance or used an unrelated backend roster", result)
			}
		})
	}
}

func TestTeamWaitRequiresCompleteProjectView(t *testing.T) {
	for _, field := range []string{"complete", "truncated", "scope"} {
		t.Run(field, func(t *testing.T) {
			status := setupWaitStatus(setupWaitReceipt("captain", 1, true))
			switch field {
			case "complete":
				status[field] = false
			case "truncated":
				status[field] = true
			case "scope":
				status[field] = "self"
			}
			setupWaitFixture(t, true, func(int32) Object { return status })
			setupWaitSession(t, []string{})
			result, err := teamWaitCommand(context.Background(), []string{"wait-team", "--timeout", "0"})
			if err != nil || B(result, "team_ready") {
				t.Fatal("incomplete, truncated, or self-only status claimed full team readiness", result, err)
			}
		})
	}
}

func TestTeamWaitDistinguishesKnownSingletonFromUnknownRoster(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "known-empty"}[known], func(t *testing.T) {
			setupWaitFixture(t, true, func(int32) Object { return setupWaitStatus(setupWaitReceipt("captain", 1, true)) })
			if known {
				setupWaitSession(t, []string{})
			}
			result, err := teamWaitCommand(context.Background(), []string{"wait-team", "--timeout", "0"})
			if err != nil || B(result, "team_ready") != known || !known && S(result, "state") != "reported_only" {
				t.Fatal("unknown roster was treated as a known singleton", result, err)
			}
		})
	}
}

func TestTeamWaitExplicitRosterWithoutInitSession(t *testing.T) {
	for _, bobReady := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing-member-pending", true: "existing-member-reported"}[bobReady], func(t *testing.T) {
			status := setupWaitStatus(setupWaitReceipt("captain", 1, true), setupWaitReceipt("alice", 2, true))
			status["expected_members"] = []string{"bob"}
			if bobReady {
				status["receipts"] = append(status["receipts"].([]Object), setupWaitReceipt("bob", 3, true))
			}
			setupWaitFixture(t, true, func(int32) Object { return status })
			result, err := teamWaitCommand(context.Background(), []string{"wait-team", "--members", "alice", "--timeout", "0"})
			if err != nil || B(result, "team_ready") != bobReady || S(result, "expected_source") != "explicit" || len(result["expected_members"].([]string)) != 3 {
				t.Fatal("explicit expected members omitted this owner or an existing project member", result, err)
			}
		})
	}
}

func TestTeamWaitIncludesInvitationsAddedAfterInitialization(t *testing.T) {
	for _, bobReady := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-invitation-pending", true: "new-member-reported"}[bobReady], func(t *testing.T) {
			status := setupWaitStatus(setupWaitReceipt("captain", 1, true), setupWaitReceipt("alice", 2, true))
			status["expected_members"] = []string{"alice", "bob"}
			if bobReady {
				status["receipts"] = append(status["receipts"].([]Object), setupWaitReceipt("bob", 3, true))
			}
			setupWaitFixture(t, true, func(int32) Object { return status })
			setupWaitSession(t, []string{"alice"})
			result, err := teamWaitCommand(context.Background(), []string{"wait-team", "--timeout", "0"})
			if err != nil || B(result, "team_ready") != bobReady || len(result["expected_members"].([]string)) != 3 {
				t.Fatal("post-init invitation was omitted from the actual expected roster", result, err)
			}
			if !bobReady {
				pending := result["pending_members"].([]Object)
				if len(pending) != 1 || S(pending[0], "actor") != "bob" {
					t.Fatal("newly invited Bob was not shown as pending", result)
				}
			}
		})
	}
}

func TestTeamWaitRejectsInvalidCurrentExpectedRoster(t *testing.T) {
	setupWaitFixture(t, true, func(int32) Object {
		status := setupWaitStatus(setupWaitReceipt("captain", 1, true))
		status["expected_members"] = []any{"Invalid actor", 12}
		return status
	})
	setupWaitSession(t, []string{})
	result, err := teamWaitCommand(context.Background(), []string{"wait-team", "--timeout", "0"})
	if err != nil || B(result, "team_ready") || S(result, "reason") != "invalid_expected_members" {
		t.Fatal("invalid backend roster was silently treated as complete", result, err)
	}
}

func TestTeamWaitLegacyServerIsUnsupportedWithoutCallingNewAPI(t *testing.T) {
	requests := setupWaitFixture(t, false, func(int32) Object { t.Error("legacy server was queried for setup receipts"); return nil })
	setupWaitSession(t, []string{})
	result, err := teamWaitCommand(context.Background(), []string{"wait-team", "--timeout", "0"})
	if err != nil || S(result, "state") != "unsupported" || B(result, "team_ready") || requests.Load() != 0 {
		t.Fatal("legacy capability absence became success or an API error", result, err)
	}
}

func TestTeamWaitPollsOncePerSecondAndStopsAtDeadline(t *testing.T) {
	requests := setupWaitFixture(t, true, func(int32) Object { return setupWaitStatus(setupWaitReceipt("captain", 1, true)) })
	setupWaitSession(t, []string{"alice"})
	start := time.Now()
	result, err := teamWaitCommand(context.Background(), []string{"wait-team", "--timeout", "1"})
	if err == nil || S(result, "state") != "timed_out" || B(result, "team_ready") || time.Since(start) > 1600*time.Millisecond || requests.Load() > 2 {
		t.Fatal("setup wait was unbounded or over-polled", result, err, time.Since(start), requests.Load())
	}
}

func TestTeamWaitReturnsWhenExpectedMemberArrives(t *testing.T) {
	requests := setupWaitFixture(t, true, func(n int32) Object {
		if n == 1 {
			return setupWaitStatus(setupWaitReceipt("captain", 1, true))
		}
		return setupWaitStatus(setupWaitReceipt("captain", 1, true), setupWaitReceipt("alice", 2, true))
	})
	setupWaitSession(t, []string{"alice"})
	result, err := teamWaitCommand(context.Background(), []string{"wait-team", "--timeout", "3"})
	if err != nil || !B(result, "team_ready") || requests.Load() != 2 {
		t.Fatal("setup wait did not stop when the expected roster completed", result, err, requests.Load())
	}
}
