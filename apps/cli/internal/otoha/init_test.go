package otoha

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInitQuestionsDoNotCreateHomeOrStartPreparation(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("OTOHA_HOME", home)
	previousResolve, previousAllocate := initResolveRuntimes, initAllocateRoute
	initResolveRuntimes = func(context.Context) (Object, error) {
		t.Error("questions attempted runtime preparation")
		return nil, errors.New("not called")
	}
	initAllocateRoute = func(context.Context, Object) (Object, error) {
		t.Error("questions attempted route allocation")
		return nil, errors.New("not called")
	}
	t.Cleanup(func() { initResolveRuntimes, initAllocateRoute = previousResolve, previousAllocate })
	for _, args := range [][]string{nil, {"--help"}, {"--name", "Example team"}, {"--owner", "Captain"}} {
		result, err := InitCommand(context.Background(), args)
		encoded, _ := json.Marshal(result)
		var wire Object
		json.Unmarshal(encoded, &wire)
		if err != nil || S(result, "state") != "needs_input" || S(result, "next") == "" || len(Items(wire, "questions")) == 0 {
			t.Fatal("incomplete setup did not return actionable questions", args, result, err)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("answer discovery created private initialization state", err)
	}
}

func TestInitAnswersAcceptsJSONAndFileWithExplicitOverrides(t *testing.T) {
	input := `{"name":"Sample team","owner":"Captain","members":["Alice","Bob"],"storage":"local"}`
	file := filepath.Join(t.TempDir(), "answers.json")
	if err := os.WriteFile(file, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	inline, err := initAnswers([]string{"--answers", input})
	if err != nil {
		t.Fatal(err)
	}
	fromFile, err := initAnswers([]string{"--answers", file})
	if err != nil || !reflect.DeepEqual(inline, fromFile) {
		t.Fatal("file answers differ from the same inline JSON", inline, fromFile, err)
	}
	overridden, err := initAnswers([]string{"--answers", file, "--owner", "Another captain", "--members", "Alice,Carol"})
	if err != nil || S(overridden, "owner") != "Another captain" || S(overridden, "name") != "Sample team" {
		t.Fatal("explicit answers did not override their selected fields", overridden, err)
	}
	members, err := initMembers(overridden["members"])
	if err != nil || !reflect.DeepEqual(members, []string{"Alice", "Carol"}) {
		t.Fatal(members, err)
	}
	if _, err := initAnswers([]string{"--answers", `{"name":"Sample","unknown":"value"}`}); err == nil {
		t.Fatal("unknown answers were silently ignored")
	}
}

func TestInitFailurePreservesRequestIdentityAndPortsAcrossResume(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("init prerequisite Git is not available in this test environment")
	}
	for _, failedPhase := range []string{"runtime", "route"} {
		t.Run(failedPhase, func(t *testing.T) {
			t.Setenv("OTOHA_HOME", filepath.Join(t.TempDir(), "private-home"))
			previousResolve, previousAllocate := initResolveRuntimes, initAllocateRoute
			var resolutions, allocations int
			var allocatedRequests []Object
			initResolveRuntimes = func(context.Context) (Object, error) {
				resolutions++
				if failedPhase == "runtime" {
					return nil, errors.New("isolated runtime download unavailable")
				}
				return Object{"server": "/synthetic/server.py", "python": "/synthetic/python", "surreal": "/synthetic/surreal", "frpc": "/synthetic/frpc", "ca_bundle": "/synthetic/ca.pem"}, nil
			}
			initAllocateRoute = func(_ context.Context, request Object) (Object, error) {
				allocations++
				allocatedRequests = append(allocatedRequests, Clone(request))
				return nil, errors.New("isolated route response unknown")
			}
			t.Cleanup(func() { initResolveRuntimes, initAllocateRoute = previousResolve, previousAllocate })
			name := "A deliberately named sample team"
			args := []string{"--name", name, "--owner", "Captain", "--members", "Alice,Bob"}
			first, err := InitCommand(context.Background(), args)
			if err == nil || S(first, "state") != "needs_action" {
				t.Fatal("failed preparation was declared ready", first, err)
			}
			team := S(first, "team")
			before, err := Load(initSessionPath(team))
			if err != nil || S(O(before, "request"), "request_id") == "" || len(S(O(before, "request"), "enrollment_secret")) < 40 || N(before, "center_port") == 0 || N(before, "surreal_port") == 0 {
				t.Fatal("initialization did not retain its resumable identity", before, err)
			}
			second, err := InitCommand(context.Background(), []string{"--name", name})
			if err == nil || S(second, "state") != "needs_action" {
				t.Fatal("name-only resume did not reuse the pending setup", second, err)
			}
			after, err := Load(initSessionPath(team))
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"request", "center_port", "surreal_port", "surreal_password", "owner", "started_at", "answers"} {
				if !reflect.DeepEqual(before[key], after[key]) {
					t.Fatalf("resume changed original %s: before=%v after=%v", key, before[key], after[key])
				}
			}
			if resolutions != 2 || failedPhase == "runtime" && allocations != 0 || failedPhase == "route" && allocations != 2 {
				t.Fatal("resume bypassed its real failing stage", resolutions, allocations)
			}
			if len(allocatedRequests) == 2 && !reflect.DeepEqual(allocatedRequests[0], allocatedRequests[1]) {
				t.Fatal("route retry invented a new request or enrollment secret")
			}
			encoded, _ := json.Marshal(second)
			for _, secret := range []string{S(O(before, "request"), "enrollment_secret"), S(before, "surreal_password")} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("public initialization result exposed a private secret")
				}
			}
			if _, err = InitCommand(context.Background(), []string{"--name", name, "--owner", "Different captain"}); err == nil {
				t.Fatal("resume silently replaced the original owner")
			}
		})
	}
}

func TestInitFailureStopsRuntimeBeforeTeamCanBeDiscovered(t *testing.T) {
	team, _ := initRuntimeFixture(t)
	status, err := EnsureInitRuntime(context.Background(), team)
	if err != nil {
		t.Fatal(err)
	}
	record := Object{"team": team, "request": Object{"request_id": "original-enrollment"}, "answers": Object{"name": team}}
	result, err := initFailure(record, errors.New("synthetic bootstrap failure"))
	if err == nil || S(result, "state") != "needs_action" {
		t.Fatal("bootstrap failure was not recoverable", result, err)
	}
	for _, kind := range []string{"surreal", "frpc"} {
		waitInitRuntimeProcessExit(t, N(O(O(status, "components"), kind), "pid"))
	}
	saved, err := Load(initSessionPath(team))
	if err != nil || S(O(saved, "request"), "request_id") != "original-enrollment" {
		t.Fatal("cleanup discarded the original initialization intent", saved, err)
	}
}

func TestInitLongValidNamesKeepDerivedChannelWithinNameLimit(t *testing.T) {
	for _, name := range []string{strings.Repeat("a", 60), strings.Repeat("b", 80), "企业内部协作团队"} {
		team := initIdentifier(name, "team")
		channel := team + "-general"
		if Name(team) != nil || Name(channel) != nil || len(channel) > 80 {
			t.Fatalf("valid input derived an unusable project/channel: %q %q", team, channel)
		}
		if initIdentifier(name, "team") != team {
			t.Fatal("repeat input changed its deterministic project name")
		}
	}
}
