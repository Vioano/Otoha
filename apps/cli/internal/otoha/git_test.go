package otoha

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyProxyExistingTokenAndLFSActionStayOnLoopback(t *testing.T) {
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "captain" || p != "existing-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/otoha-git/captain/private.git/info/lfs/objects/batch":
			// Gitea retains its pre-HTTPS ROOT_URL for old clients.
			json.NewEncoder(w).Encode(Object{"objects": []Object{{"actions": Object{"download": Object{
				"href":   "http://127.0.0.1:13000/captain/private.git/info/lfs/objects/fixture",
				"header": Object{"Authorization": "Bearer existing-action"},
			}}}}})
		case "/otoha-git/captain/private.git/info/lfs/objects/fixture":
			w.Write([]byte("private object"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL + "/otoha-git")
	local := Object{}
	proxy := httptest.NewServer(NewGitReverseProxy(target, Object{"username": "captain", "token": "existing-token"}, http.DefaultTransport, local))
	defer proxy.Close()
	local["address"] = strings.TrimPrefix(proxy.URL, "http://")
	response, err := http.Post(proxy.URL+"/captain/private.git/info/lfs/objects/batch", "application/vnd.git-lfs+json", bytes.NewBufferString(`{"operation":"download"}`))
	if err != nil {
		t.Fatal(err)
	}
	var batch Object
	err = json.NewDecoder(response.Body).Decode(&batch)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("legacy private batch: HTTP %d, %v", response.StatusCode, err)
	}
	action := O(O(Items(batch, "objects")[0], "actions"), "download")
	if !strings.HasPrefix(S(action, "href"), proxy.URL+"/") {
		t.Fatal("LFS action escaped the original loopback proxy")
	}
	request, _ := http.NewRequest("GET", S(action, "href"), nil)
	request.Header.Set("Authorization", S(O(action, "header"), "Authorization"))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || string(payload) != "private object" {
		t.Fatalf("legacy download: HTTP %d", response.StatusCode)
	}
}

func TestGitProxyPrivateLFSWithExistingTokenOrPassword(t *testing.T) {
	for _, credentials := range []struct{ name, token, password string }{
		{"token-and-password", "existing-token", "existing-password"},
		{"token-only", "existing-token", ""},
		{"password", "", "existing-password"},
	} {
		t.Run(credentials.name, func(t *testing.T) {
			t.Setenv("OTOHA_HOME", t.TempDir())
			payload := []byte("private LFS payload")
			wantPassword := credentials.password
			if credentials.token != "" {
				wantPassword = credentials.token
			}
			var upstream *httptest.Server
			upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				u, p, ok := r.BasicAuth()
				if !ok || u != "captain" || p != wantPassword {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				switch r.URL.Path {
				case "/captain/private.git/info/lfs/objects/batch":
					json.NewEncoder(w).Encode(Object{"objects": []Object{{"actions": Object{"download": Object{
						"href":   upstream.URL + "/captain/private.git/info/lfs/objects/fixture",
						"header": Object{"Authorization": "Bearer fixture-action"},
					}}}}})
				case "/captain/private.git/info/lfs/objects/fixture":
					w.Write(payload)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer upstream.Close()
			identity := filepath.Join(Home(), "identity.json")
			if err := Save(identity, Object{"url": upstream.URL, "gitea": Object{
				"url": upstream.URL, "username": "captain", "password": credentials.password, "token": credentials.token,
			}}); err != nil {
				t.Fatal(err)
			}
			if err := Save(filepath.Join(Home(), "config.json"), Object{"mode": "member", "profile": identity}); err != nil {
				t.Fatal(err)
			}
			local, err := proxyConfig()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- GitProxy(ctx) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Error("Git proxy failed to stop")
				}
			}()
			client := &http.Client{Timeout: 2 * time.Second}
			authorization := "Basic " + base64.StdEncoding.EncodeToString([]byte(S(local, "username")+":"+S(local, "password")))
			var response *http.Response
			deadline := time.Now().Add(3 * time.Second)
			for {
				request, _ := http.NewRequest("POST", "http://"+S(local, "address")+"/captain/private.git/info/lfs/objects/batch", bytes.NewBufferString(`{"operation":"download"}`))
				request.Header.Set("Authorization", authorization)
				response, err = client.Do(request)
				if err == nil || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("private LFS batch returned HTTP %d", response.StatusCode)
			}
			var body Object
			if err = json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			action := O(O(Items(body, "objects")[0], "actions"), "download")
			request, _ := http.NewRequest("GET", S(action, "href"), nil)
			request.Header.Set("Authorization", S(O(action, "header"), "Authorization"))
			response, err = client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			actual, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(actual, payload) {
				t.Fatalf("private LFS download failed: HTTP %d, error %v", response.StatusCode, err)
			}
		})
	}
}
