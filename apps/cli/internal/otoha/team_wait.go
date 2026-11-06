package otoha

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func setupReadyCapability(manifest Object) bool {
	for _, capability := range Array(manifest["capabilities"]) {
		if capability == "setup_ready" {
			return true
		}
	}
	return false
}

func teamWaitExpected(cfg TeamConfig, profile Object, members string, explicit bool) ([]string, string, error) {
	owner := S(profile, "actor")
	if err := Name(owner); err != nil {
		return nil, "", fmt.Errorf("invalid saved setup identity: %w", err)
	}
	expected, seen := []string{owner}, map[string]bool{owner: true}
	add := func(actor string) {
		if !seen[actor] {
			expected, seen[actor] = append(expected, actor), true
		}
	}
	if explicit {
		for _, member := range strings.Split(members, ",") {
			actor := strings.TrimSpace(member)
			if actor == "" {
				continue
			}
			if err := Name(actor); err != nil {
				return nil, "", fmt.Errorf("--members requires actor IDs: %w", err)
			}
			add(actor)
		}
		return expected, "explicit", nil
	}
	session, err := Load(initSessionPath(cfg.Name))
	if os.IsNotExist(err) {
		return nil, "unknown", nil
	}
	if err != nil {
		return nil, "", err
	}
	if S(session, "team") != cfg.Name || S(session, "owner") != owner {
		return nil, "", errors.New("initialization session belongs to another team or owner")
	}
	answers := O(session, "answers")
	value, known := answers["members"]
	if !known {
		return nil, "unknown", nil
	}
	names, err := initMembers(value)
	if err != nil {
		return nil, "", err
	}
	for _, name := range names {
		add(initIdentifier(name, "member"))
	}
	return expected, "init_session", nil
}

func teamWaitResult(cfg TeamConfig, expected []string, source string, status Object) Object {
	expected = append([]string(nil), expected...)
	rosterValid := true
	if source == "init_session" || source == "explicit" {
		seen := map[string]bool{}
		for _, actor := range expected {
			seen[actor] = true
		}
		var current []any
		switch members := status["expected_members"].(type) {
		case []any:
			current = members
		case []string:
			for _, actor := range members {
				current = append(current, actor)
			}
		default:
			rosterValid = false
		}
		for _, value := range current {
			actor, ok := value.(string)
			if !ok || Name(actor) != nil {
				rosterValid = false
				continue
			}
			if !seen[actor] {
				expected, seen[actor] = append(expected, actor), true
			}
		}
	}
	result := Object{"schema": "otoha-team-setup/1", "team": cfg.Name, "project": cfg.Project,
		"state": "waiting", "ready": false, "team_ready": false, "checks_source": "self_report",
		"user_accepted": false, "expected_members": expected, "expected_source": source,
		"scope": status["scope"], "complete": B(status, "complete"), "truncated": B(status, "truncated"),
		"next": "otoha team wait " + cfg.Name + " --timeout 60",
		"note": "Setup receipts report each participant's local checks. They do not prove current activity, independent end-to-end verification, or user acceptance."}
	latest := map[string]Object{}
	for _, receipt := range Items(status, "receipts") {
		actor := S(receipt, "actor")
		reported, ok := receipt["reported_at"].(float64)
		if !ok || reported <= 0 {
			continue
		}
		previous, exists := latest[actor]
		if !exists || reported >= previous["reported_at"].(float64) {
			latest[actor] = receipt
		}
	}
	reports, pending := []Object{}, []Object{}
	for _, actor := range expected {
		receipt := latest[actor]
		if receipt == nil {
			pending = append(pending, Object{"actor": actor, "reason": "not_reported"})
			continue
		}
		reports = append(reports, receipt)
		checks := O(receipt, "checks")
		ready := S(receipt, "state") == "self_report" && receipt["ready"] == true
		for _, check := range []string{"repository", "service", "graph", "communication"} {
			ready = ready && checks[check] == true
		}
		if !ready {
			pending = append(pending, Object{"actor": actor, "reason": "checks_incomplete"})
		}
	}
	result["receipts"], result["pending_members"] = reports, pending
	if source == "unknown" {
		result["state"], result["reason"], result["receipts"] = "reported_only", "expected_members_unknown", status["receipts"]
		result["next"] = "Specify the intended actor IDs with otoha team wait " + cfg.Name + " --members ACTOR,ACTOR --timeout 60"
	} else if S(status, "scope") != "project" {
		result["state"], result["reason"] = "reported_only", "project_status_not_visible"
	} else if !rosterValid {
		result["state"], result["reason"] = "needs_action", "invalid_expected_members"
	} else if status["complete"] == true && status["truncated"] == false && len(pending) == 0 {
		result["state"], result["ready"], result["team_ready"] = "ready", true, true
		result["next"] = "Team setup is complete. Begin the team's work; no extra setup message or Git round trip is required."
	}
	return result
}

func teamWaitCommand(ctx context.Context, args []string) (Object, error) {
	if len(args) == 0 {
		return nil, errors.New("otoha team wait TEAM [--timeout 60] [--members ACTOR,ACTOR]")
	}
	team := args[0]
	if err := Name(team); err != nil {
		return nil, err
	}
	f := flag.NewFlagSet("team wait", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	timeout := f.Int("timeout", 60, "maximum wait in seconds; zero returns one snapshot")
	members := f.String("members", "", "additional expected actor IDs; current project members and this owner remain included")
	if err := f.Parse(args[1:]); err != nil {
		return nil, err
	}
	if f.NArg() != 0 || *timeout < 0 || *timeout > 300 {
		return nil, errors.New("team wait timeout must be between 0 and 300 seconds")
	}
	explicit := false
	f.Visit(func(value *flag.Flag) { explicit = explicit || value.Name == "members" })
	cfg, err := loadTeamConfig(team)
	if err != nil {
		return nil, err
	}
	profile, client, err := teamProfile(cfg)
	if err != nil {
		return nil, err
	}
	defer client.Transport.Close()
	expected, source, err := teamWaitExpected(cfg, profile, *members, explicit)
	if err != nil {
		return nil, err
	}
	budget := time.Duration(*timeout) * time.Second
	if *timeout == 0 {
		budget = 5 * time.Second
	}
	wait, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	result := teamWaitResult(cfg, expected, source, Object{})
	manifest, err := client.Call(wait, http.MethodGet, "/v1/manifest", nil)
	if err != nil {
		result["state"], result["error"] = "needs_action", err.Error()
		return result, err
	}
	if !setupReadyCapability(manifest) {
		result["state"], result["reason"] = "unsupported", "server_does_not_support_setup_ready"
		result["next"] = "This server cannot aggregate setup receipts; use its existing team status and doctor results as limited checks."
		return result, nil
	}
	for {
		status, callErr := client.Call(wait, http.MethodGet, "/v1/setup-ready?project="+url.QueryEscape(cfg.Project), nil)
		if callErr == nil {
			if S(status, "schema") != "otoha-setup-ready/1" || S(status, "project") != cfg.Project {
				result["state"], result["error"] = "needs_action", "setup status did not match this project"
				return result, errors.New(S(result, "error"))
			}
			result = teamWaitResult(cfg, expected, source, status)
			if B(result, "team_ready") || S(result, "state") == "reported_only" || *timeout == 0 {
				return result, nil
			}
		} else {
			result["last_error"] = callErr.Error()
			if pagePermanentError(callErr) {
				result["state"] = "needs_action"
				return result, callErr
			}
			if *timeout == 0 {
				result["state"] = "needs_action"
				return result, callErr
			}
		}
		if !pagePause(wait, time.Second) {
			result["state"], result["timed_out"] = "timed_out", true
			return result, fmt.Errorf("team setup is not complete: %w", wait.Err())
		}
	}
}
