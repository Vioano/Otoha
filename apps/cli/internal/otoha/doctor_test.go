package otoha

import (
	"context"
	"os"
	"testing"
)

func TestDoctorReportsFreshPrivateHomeWithoutMutatingIt(t *testing.T) {
	home := t.TempDir() + "/otoha"
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTOHA_HOME", home)
	t.Setenv("OTOHA_REPO_ROOT", repoRoot())
	result, err := Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !B(result, "ok") {
		t.Fatalf("fresh private home should not have hard failures: %#v", result)
	}
	if _, err := os.Stat(home + "/config.json"); !os.IsNotExist(err) {
		t.Fatalf("doctor mutated the home: %v", err)
	}
	status, err := DaemonCommand([]string{"status"})
	if err != nil {
		t.Fatal(err)
	}
	if B(status, "managed") {
		t.Fatalf("fresh home unexpectedly has a managed daemon: %#v", status)
	}
}
