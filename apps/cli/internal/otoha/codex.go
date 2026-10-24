package otoha

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func FindCodex() (string, error) {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			local = filepath.Join(home, "AppData", "Local")
		}
		// Desktop relocates its native executable here from WindowsApps.
		candidates = append(candidates, filepath.Join(local, "OpenAI", "Codex", "bin", "codex.exe"))
	case "darwin":
		for _, app := range []string{"ChatGPT.app", "Codex.app"} {
			resources := filepath.Join("/Applications", app, "Contents", "Resources")
			candidates = append(candidates,
				filepath.Join(resources, "codex-cli", "CodexCLI.app", "Contents", "MacOS", "codex"),
				filepath.Join(resources, "codex-cli", "bin", "codex"))
		}
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	path, err := exec.LookPath("codex")
	if err != nil {
		return "", fmt.Errorf("Codex Desktop's native CLI or codex in PATH is required for agent binding")
	}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe") {
		return "", fmt.Errorf("Codex native executable required; use Desktop's bundled CLI instead of the command-shell wrapper %s", path)
	}
	return path, nil
}
