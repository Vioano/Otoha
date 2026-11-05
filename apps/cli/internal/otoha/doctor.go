package otoha

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type doctorReport struct {
	checks []Object
	ok     bool
}

func (r *doctorReport) add(id, status, detail, remedy string) {
	r.checks = append(r.checks, Object{"id": id, "status": status, "detail": detail, "remedy": remedy})
	if status == "fail" {
		r.ok = false
	}
}

func privatePathCheck(path string, directory bool) (string, bool) {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "missing", false
	}
	if err != nil {
		return err.Error(), false
	}
	if directory && !st.IsDir() {
		return "path is not a directory", false
	}
	if !directory && st.IsDir() {
		return "path is a directory", false
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
		return fmt.Sprintf("permissions %o are broader than private", st.Mode().Perm()), false
	}
	return "private", true
}

func checkServerDatabase(ctx context.Context, state string, configurations ...TeamConfig) (string, bool) {
	cfg := TeamConfig{}
	if len(configurations) > 0 {
		cfg = configurations[0]
	}
	script, python := localTeamRuntime(cfg)
	if script == "" {
		return "server script unavailable", false
	}
	command, err := exec.LookPath(python)
	if err != nil {
		return err.Error(), false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, command, script, "check", "--state", state).CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), false
	}
	var result Object
	if json.Unmarshal(out, &result) == nil && S(result, "integrity") != "ok" {
		return "integrity check did not return ok", false
	}
	return "sqlite integrity and storage guard passed", true
}

func teamNamesForDoctor() []string {
	entries, err := os.ReadDir(filepath.Join(Home(), "teams"))
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && Name(entry.Name()) == nil {
			names = append(names, entry.Name())
		}
	}
	return names
}

// Doctor reports the current local CLI/runtime state without mutating config,
// starting a service, or attempting to repair an existing installation.
func Doctor(ctx context.Context) (Object, error) {
	report := &doctorReport{ok: true}
	homeDetail, homeOK := privatePathCheck(Home(), true)
	if homeDetail == "missing" {
		report.add("home", "warn", Home()+" does not exist yet", "run a team or identity setup command")
	} else if !homeOK {
		report.add("home", "fail", homeDetail, "chmod the OTOHA_HOME directory to 0700")
	} else {
		report.add("home", "pass", Home(), "")
	}
	if executable, err := os.Executable(); err != nil {
		report.add("cli", "fail", err.Error(), "build the native CLI")
	} else {
		report.add("cli", "pass", executable, "")
	}
	names := teamNamesForDoctor()
	_, legacyErr := os.Stat(filepath.Join(Home(), "config.json"))
	needsServerRuntime := legacyErr == nil && len(names) == 0
	if needsServerRuntime {
		if python, err := exec.LookPath(pythonCommand()); err != nil {
			report.add("python", "fail", err.Error(), "install Python 3.10 or set OTOHA_PYTHON for local hosting")
		} else {
			report.add("python", "pass", python, "")
		}
		if script := serverScript(); script == "" {
			report.add("collaboration_server", "fail", "server source is not discoverable", "set OTOHA_SERVER_SCRIPT for local hosting")
		} else if _, err := os.Stat(script); err != nil {
			report.add("collaboration_server", "fail", err.Error(), "restore services/collaboration/otoha_server.py")
		} else {
			report.add("collaboration_server", "pass", script, "")
		}
	}

	teamReports := []Object{}
	for _, name := range names {
		teamCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		teamReport, err := teamDoctor(teamCtx, name)
		cancel()
		if err != nil {
			report.add("team:"+name, "fail", err.Error(), "repair or remove the incomplete team metadata")
			continue
		}
		teamReports = append(teamReports, teamReport)
		if !B(teamReport, "ok") {
			report.add("team:"+name, "fail", "team checks contain failures", "run otoha team doctor "+name)
		} else {
			report.add("team:"+name, "pass", S(teamReport, "url"), "")
		}
		cfg, cfgErr := loadTeamConfig(name)
		if cfgErr != nil || cfg.Mode != "local" {
			continue
		}
		detail, dbOK := checkServerDatabase(ctx, cfg.State, cfg)
		if dbOK {
			report.add("database:"+name, "pass", detail, "")
		} else {
			report.add("database:"+name, "fail", detail, "run the service check and inspect the team log")
		}
	}
	if len(teamReports) == 0 {
		report.add("teams", "warn", "no local team configuration found", "run otoha join --invite FILE, or otoha team init NAME to host locally")
	} else {
		report.add("teams", "pass", fmt.Sprintf("%d team configuration(s)", len(teamReports)), "")
	}

	if status, err := DaemonStatus(); err == nil {
		reportStatus := "warn"
		detail := "daemon is not installed"
		if B(status, "managed") {
			reportStatus = "pass"
			detail = fmt.Sprint(status["manager"], " ", status["manager_state"])
			if receiver := O(status, "receiver"); B(receiver, "alive") {
				detail += "; receiver alive"
			} else {
				reportStatus = "warn"
				detail += "; receiver is not alive"
			}
		}
		report.add("daemon", reportStatus, detail, "run otoha daemon install or inspect otoha daemon status")
	} else {
		report.add("daemon", "fail", err.Error(), "run otoha daemon status")
	}
	if _, err := Load(filepath.Join(Home(), "config.json")); err == nil {
		report.add("legacy_profile", "pass", "legacy profile exists", "")
	} else if os.IsNotExist(err) {
		if len(names) == 0 {
			report.add("legacy_profile", "warn", "no project or legacy profile configured", "run otoha join --invite FILE")
		}
	} else {
		report.add("legacy_profile", "fail", err.Error(), "repair the private profile or run otoha import")
	}
	if state, err := Load(filepath.Join(Home(), "receiver-status.json")); err == nil {
		// receiver-status has no alive field in older versions; processAlive is
		// the source of truth and avoids treating stale JSON as a live daemon.
		alive := processAlive(N(state, "pid"))
		if alive {
			report.add("receiver", "pass", "managed receiver heartbeat is live", "")
		} else {
			report.add("receiver", "warn", "receiver status is stale", "run otoha daemon restart")
		}
	} else {
		report.add("receiver", "warn", "no active receiver heartbeat", "run otoha daemon status; joined projects start their local service through otoha page open")
	}

	return Object{"ok": report.ok, "platform": runtime.GOOS, "home": Home(), "checks": report.checks, "teams": teamReports, "daemon": func() Object { s, _ := DaemonStatus(); return s }()}, nil
}
