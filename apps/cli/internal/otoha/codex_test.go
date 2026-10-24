package otoha

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	if output := os.Getenv("OTOHA_TEST_QUEUE_ARGV"); output != "" {
		data, _ := json.Marshal(os.Args[1:])
		if err := os.WriteFile(output, data, 0600); err != nil {
			os.Exit(2)
		}
		if history := os.Getenv("OTOHA_TEST_QUEUE_HISTORY"); history != "" {
			file, err := os.OpenFile(history, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(2)
			}
			_, err = file.Write(append(data, '\n'))
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				os.Exit(2)
			}
		}
		fmt.Println("Queued fixture")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func copyQueueFixture(t *testing.T, target string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	from, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	to, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(to, from)
	closeErr := to.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
}

func TestNativeQueuePreservesLiteralArguments(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "path with spaces")
	executable := filepath.Join(dir, "codex")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	copyQueueFixture(t, executable)
	output := filepath.Join(t.TempDir(), "argv.json")
	t.Setenv("OTOHA_TEST_QUEUE_ARGV", output)
	thread, source := "target", "01a135e4-9094-7dd0-b7d1-c2e57d6d4934"
	text := "中文 & <xml> \"quotes\" %PATH% !bang!\nsecond line \\path"
	if _, err := QueueCodex(context.Background(), executable, thread, source, text); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err = json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	want := []string{"queue", "--thread", thread, "--message", DeliveryText(source, text)}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("message was interpreted or changed by the process boundary: %#v", args)
	}
}

func TestWindowsFindsDesktopWithoutPathCLI(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Desktop installation layout")
	}
	local := filepath.Join(t.TempDir(), "Local AppData")
	native := filepath.Join(local, "OpenAI", "Codex", "bin", "codex.exe")
	copyQueueFixture(t, native)
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("PATH", t.TempDir())
	actual, err := FindCodex()
	if err != nil || actual != native {
		t.Fatalf("Desktop native CLI was not selected without PATH: %q %v", actual, err)
	}
}
