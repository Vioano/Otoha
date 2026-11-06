package otoha

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Commit identity is separate from authentication. Fill only missing fields
// inside this checkout; never edit global Git config or claim a GitHub login.
func ensureJoinedRepositoryIdentity(ctx context.Context, directory, profilePath string) error {
	profile, err := Load(profilePath)
	if err != nil {
		return err
	}
	actor := S(profile, "actor")
	if actor == "" {
		return errors.New("joined repository has no Otoha actor for its local Git identity")
	}
	display := S(profile, "name")
	if display == "" {
		display = actor
	}
	for _, setting := range []struct{ key, value string }{{"user.name", display}, {"user.email", actor + "@otoha.invalid"}} {
		output, getErr := exec.CommandContext(ctx, "git", "-C", directory, "config", "--get", setting.key).Output()
		if getErr == nil && strings.TrimSpace(string(output)) != "" {
			continue
		}
		if getErr != nil {
			var exit *exec.ExitError
			if !errors.As(getErr, &exit) || exit.ExitCode() != 1 {
				return fmt.Errorf("read checkout Git identity: %w", getErr)
			}
		}
		if err := exec.CommandContext(ctx, "git", "-C", directory, "config", "--local", setting.key, setting.value).Run(); err != nil {
			return fmt.Errorf("set checkout-local Git identity: %w", err)
		}
	}
	return nil
}
