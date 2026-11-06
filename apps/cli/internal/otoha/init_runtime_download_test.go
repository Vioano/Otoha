package otoha

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func initRuntimeArchive(t *testing.T, extra *tar.Header) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	archive := tar.NewWriter(gz)
	for name, content := range map[string]string{
		"runtimes/frpc":                          "synthetic frpc",
		"runtimes/surreal":                       "synthetic surreal",
		"runtimes/python/bin/python3.12":         "synthetic python",
		"runtimes/ca-bundle.pem":                 "synthetic CA",
		"services/collaboration/otoha_server.py": "# synthetic server\n",
	} {
		if err := archive.WriteHeader(&tar.Header{Name: "fixture/" + name, Mode: 0700, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.WriteHeader(&tar.Header{Name: "fixture/runtimes/python/bin/python3", Mode: 0700, Typeflag: tar.TypeSymlink, Linkname: "python3.12"}); err != nil {
		t.Fatal(err)
	}
	if extra != nil {
		if err := archive.WriteHeader(extra); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestInitRuntimeDownloadVerifiesAndPreservesPythonSymlink(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	t.Setenv("OTOHA_RUNTIME_ROOT", "")
	archive := initRuntimeArchive(t, nil)
	digest := sha256.Sum256(archive)
	var downloads atomic.Int32
	var manifest Object
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/runtime.tar.gz" {
			downloads.Add(1)
			w.Write(archive)
			return
		}
		json.NewEncoder(w).Encode(manifest)
	}))
	defer server.Close()
	manifest = Object{"schema": "otoha-runtime/1", "version": "0.1.0-alpha.3-rc.1", "relay": Object{"url": "https://otoha.kessokuu.top", "dial_ip": "192.0.2.10"}, "platforms": Object{runtime.GOOS + "/" + runtime.GOARCH: Object{"url": server.URL + "/runtime.tar.gz", "sha256": hex.EncodeToString(digest[:]), "bytes": len(archive), "format": "tar.gz", "archive_root": "fixture"}}}
	t.Setenv("OTOHA_RUNTIME_MANIFEST", server.URL+"/manifest.json")
	first, err := resolveInitRuntimes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(S(first, "python")) || !strings.Contains(S(first, "root"), filepath.Join("runtimes", "0.1.0-alpha.3-rc.1", runtime.GOOS, runtime.GOARCH)) || S(first, "relay_dial_ip") != "192.0.2.10" {
		t.Fatal("runtime discovery lost installation or relay metadata", first)
	}
	if target, err := os.Readlink(S(first, "python")); err != nil || target != "python3.12" {
		t.Fatal("relocatable Python link was lost", target, err)
	}
	if _, err := os.Stat(S(first, "ca_bundle")); err != nil {
		t.Fatal(err)
	}
	second, err := resolveInitRuntimes(context.Background())
	if err != nil || S(first, "root") != S(second, "root") || downloads.Load() != 1 {
		t.Fatal("verified runtime was not reused", second, err, downloads.Load())
	}
}

func TestInitRuntimeBadChecksumDoesNotInstall(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	t.Setenv("OTOHA_RUNTIME_ROOT", "")
	archive := initRuntimeArchive(t, nil)
	var manifest Object
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/runtime.tar.gz" {
			w.Write(archive)
			return
		}
		json.NewEncoder(w).Encode(manifest)
	}))
	defer server.Close()
	manifest = Object{"schema": "otoha-runtime/1", "version": "bad-checksum", "platforms": Object{runtime.GOOS + "/" + runtime.GOARCH: Object{"url": server.URL + "/runtime.tar.gz", "sha256": strings.Repeat("0", 64), "bytes": len(archive), "format": "tar.gz", "archive_root": "fixture"}}}
	t.Setenv("OTOHA_RUNTIME_MANIFEST", server.URL)
	if _, err := resolveInitRuntimes(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatal("wrong runtime checksum was accepted", err)
	}
	if _, err := os.Stat(filepath.Join(Home(), "runtimes", "bad-checksum")); !os.IsNotExist(err) {
		t.Fatal("checksum failure installed runtime files", err)
	}
}

func TestInitRuntimeArchiveRejectsEscapingPathsAndSymlinks(t *testing.T) {
	for _, header := range []*tar.Header{
		{Name: "fixture/../../outside", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "fixture/runtimes/escape", Typeflag: tar.TypeSymlink, Linkname: "../../outside"},
		{Name: "fixture/runtimes/escape", Typeflag: tar.TypeSymlink, Linkname: "/tmp/outside"},
	} {
		if err := extractInitRuntime(initRuntimeArchive(t, header), t.TempDir(), "fixture"); err == nil {
			t.Fatal("escaping package entry was accepted", header.Name, header.Linkname)
		}
	}
}

func TestInitRuntimeDiscoveryUsesExecutablePackageNotWorkingCheckout(t *testing.T) {
	packageRoot := t.TempDir()
	executable := filepath.Join(packageRoot, "bin", "otoha")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(packageRoot, "runtime-manifest.json")
	if err := os.WriteFile(manifest, []byte(`{"schema":"otoha-runtime/1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	previous := executableForInitRuntime
	executableForInitRuntime = func() (string, error) { return executable, nil }
	t.Cleanup(func() { executableForInitRuntime = previous })
	expected, err := filepath.EvalSymlinks(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if actual := initRuntimeLocalManifest(); actual != expected {
		t.Fatal("runtime source was not bound to the running package", actual)
	}
}
