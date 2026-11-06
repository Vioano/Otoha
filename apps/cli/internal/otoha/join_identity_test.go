package otoha

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestJoinedCheckoutCanCommitWithEmptyGitHome(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "empty-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "no-global-config"))
	t.Setenv("OTOHA_HOME", filepath.Join(root, "otoha"))
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	source := filepath.Join(root, "source")
	git("init", "--initial-branch=main", source)
	git("-C", source, "-c", "user.name=Otoha fixture", "-c", "user.email=fixture@otoha.invalid", "commit", "--allow-empty", "-m", "fixture seed")
	checkout := filepath.Join(root, "checkout")
	git("clone", source, checkout)
	profile := filepath.Join(Home(), "identity.json")
	if err := Save(profile, Object{"actor": "new-member", "name": "New member"}); err != nil {
		t.Fatal(err)
	}
	if err := ensureJoinedRepositoryIdentity(context.Background(), checkout, profile); err != nil {
		t.Fatal(err)
	}
	ident := git("-C", checkout, "var", "GIT_AUTHOR_IDENT")
	if !strings.Contains(ident, "New member <new-member@otoha.invalid>") {
		t.Fatal("identity was not explicitly tied to this checkout's member", ident)
	}
	if err := os.WriteFile(filepath.Join(checkout, "member.txt"), []byte("fixture contribution\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("-C", checkout, "add", "member.txt")
	git("-C", checkout, "commit", "-m", "member can commit without global configuration")
	for _, name := range []string{".gitconfig", "no-global-config"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatal("global Git configuration was created", name, err)
		}
	}
	git("-C", checkout, "config", "--local", "user.name", "Existing author")
	git("-C", checkout, "config", "--local", "user.email", "existing@example.invalid")
	if err := ensureJoinedRepositoryIdentity(context.Background(), checkout, profile); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(git("-C", checkout, "config", "--get", "user.name")) != "Existing author" || strings.TrimSpace(git("-C", checkout, "config", "--get", "user.email")) != "existing@example.invalid" {
		t.Fatal("existing author identity was overwritten")
	}
}
