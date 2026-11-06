package otoha

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func joinKeyCA(t *testing.T, server *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test-ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestJoinKeyResumesIdentityWithoutRedeemingClaimedKey(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	// A public join must not require an old, machine-specific SSH route.
	t.Setenv("OTOHA_GATEWAY_PROFILE", "/not/a/real/local-route.json")
	const key = "synthetic-personal-one-time-key"
	var resolutions, enrollments atomic.Int32
	var credential string
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/invitation-info":
			if resolutions.Add(1) > 1 {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(Object{"error": "invitation_already_used"})
				return
			}
			var body Object
			if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || json.NewDecoder(r.Body).Decode(&body) != nil || S(body, "invitation") != key {
				t.Error("invitation key was not sent solely in an unauthenticated POST body")
			}
			json.NewEncoder(w).Encode(Object{"protocol": "otoha-collaboration/1", "url": server.URL, "actor": "alice", "project": "demo", "channel": "demo-general", "grants": Object{"demo": "member"}, "expires": 2000000000})
		case "/v1/enroll":
			enrollments.Add(1)
			var body Object
			json.NewDecoder(r.Body).Decode(&body)
			credential = S(body, "credential")
			json.NewEncoder(w).Encode(Object{"actor": "alice", "device": "test-device"})
		case "/v1/manifest":
			if credential == "" || r.Header.Get("Authorization") != "Bearer "+credential {
				t.Error("joined identity did not authenticate with its own credential")
			}
			json.NewEncoder(w).Encode(Object{"actor": Object{"id": "alice"}, "grants": []Object{{"project": "demo", "role": "member"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	args := []string{"join", "--endpoint", server.URL, "--key", key, "--ca-file", joinKeyCA(t, server), "--device", "test-device"}
	first, err := TeamCommand(context.Background(), args)
	if err != nil || !B(first, "joined") || B(first, "resumed") {
		t.Fatalf("key join failed: %#v %v", first, err)
	}
	profileBefore, err := os.ReadFile(S(first, "profile"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := TeamCommand(context.Background(), args)
	if err != nil || !B(second, "resumed") || resolutions.Load() != 1 || enrollments.Load() != 1 {
		t.Fatalf("repeat key join did not resume: %#v %v resolutions=%d enrollments=%d", second, err, resolutions.Load(), enrollments.Load())
	}
	profileAfter, err := os.ReadFile(S(second, "profile"))
	if err != nil || string(profileBefore) != string(profileAfter) {
		t.Fatal("repeat key join replaced its existing identity")
	}
	output, _ := json.Marshal(second)
	if strings.Contains(string(output), key) || strings.Contains(string(output), credential) {
		t.Fatal("join result exposed a private key or credential")
	}
	paths, err := filepath.Glob(filepath.Join(Home(), "invitations", "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("private envelope was not retained: %v %v", paths, err)
	}
	info, err := os.Stat(paths[0])
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("invitation cache permissions are not private: %v %v", info, err)
	}
}

func TestJoinKeyRecoversLostEnrollmentResponseWithOriginalRequest(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	t.Setenv("OTOHA_GATEWAY_PROFILE", "")
	const key = "synthetic-enrollment-recovery-key"
	var resolutions, enrollments atomic.Int32
	var firstEnrollment Object
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/invitation-info":
			resolutions.Add(1)
			json.NewEncoder(w).Encode(Object{"protocol": "otoha-collaboration/1", "url": server.URL, "actor": "alice", "project": "demo", "channel": "demo-general", "grants": Object{"demo": "member"}})
		case "/v1/enroll":
			var body Object
			json.NewDecoder(r.Body).Decode(&body)
			if enrollments.Add(1) == 1 {
				firstEnrollment = body
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					conn.Close()
				}
				return
			}
			for _, field := range []string{"invitation", "credential", "enrollment_id", "device"} {
				if S(body, field) != S(firstEnrollment, field) {
					t.Errorf("recovery changed enrollment %s", field)
				}
			}
			json.NewEncoder(w).Encode(Object{"actor": "alice", "device": "test-device", "recovered": true})
		case "/v1/manifest":
			json.NewEncoder(w).Encode(Object{"actor": Object{"id": "alice"}, "grants": []Object{{"project": "demo", "role": "member"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	args := []string{"join", "--endpoint", server.URL, "--key", key, "--ca-file", joinKeyCA(t, server), "--device", "test-device"}
	if _, err := TeamCommand(context.Background(), args); err == nil {
		t.Fatal("a lost enrollment response was reported as a completed join")
	}
	result, err := TeamCommand(context.Background(), args)
	if err != nil || !B(result, "joined") || resolutions.Load() != 1 || enrollments.Load() != 2 {
		t.Fatalf("pending enrollment was not recovered: %#v %v resolutions=%d enrollments=%d", result, err, resolutions.Load(), enrollments.Load())
	}
}

func TestJoinKeyRefusesDifferentAuthorityAndRedirect(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	redirect := false
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirect {
			http.Redirect(w, r, server.URL+"/redirected", http.StatusTemporaryRedirect)
			return
		}
		json.NewEncoder(w).Encode(Object{"protocol": "otoha-collaboration/1", "url": "https://elsewhere.example.invalid", "actor": "alice", "project": "demo", "channel": "demo-general", "grants": Object{"demo": "member"}})
	}))
	defer server.Close()
	ca := joinKeyCA(t, server)
	if _, err := resolveJoinKey(context.Background(), server.URL, "synthetic-key", ca); err == nil {
		t.Fatal("key resolution accepted an unrelated enrollment authority")
	}
	redirect = true
	if _, err := resolveJoinKey(context.Background(), server.URL, "synthetic-key", ca); err == nil {
		t.Fatal("key resolution followed a redirect")
	}
	if _, err := resolveJoinKey(context.Background(), "http://127.0.0.1:1234", "synthetic-key", ""); err == nil {
		t.Fatal("public key join accepted plaintext HTTP")
	}
}
