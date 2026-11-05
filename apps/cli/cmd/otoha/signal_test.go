package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReceiverSignalProcess(t *testing.T) {
	if os.Getenv("OTOHA_TEST_RECEIVER_CHILD") != "1" {
		return
	}
	args := []string{"receive"}
	if os.Getenv("OTOHA_TEST_RECEIVER_MODE") == "service" {
		args = []string{"service", "run", "--home", os.Getenv("OTOHA_HOME")}
	}
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestReceiverSIGTERMIsANormalExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows process termination does not deliver POSIX SIGTERM")
	}
	for _, mode := range []string{"receive", "service"} {
		t.Run(mode, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "isolated-receiver")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable, "-test.run=^TestReceiverSignalProcess$")
			cmd.Env = append(os.Environ(), "OTOHA_HOME="+home, "OTOHA_TEST_RECEIVER_CHILD=1", "OTOHA_TEST_RECEIVER_MODE="+mode)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			var waitErr error
			go func() { waitErr = cmd.Wait(); close(done) }()
			t.Cleanup(func() { cmd.Process.Kill(); <-done })
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(home, "page-runtime.json")); err == nil {
					break
				}
				select {
				case <-done:
					t.Fatalf("receiver exited before readiness: %v %s", waitErr, output.String())
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("isolated receiver did not start")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err = cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
				if waitErr != nil || strings.Contains(output.String(), "context canceled") {
					t.Fatalf("normal SIGTERM was treated as a crash: %v %s", waitErr, output.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("receiver did not finish after SIGTERM")
			}
		})
	}
}
