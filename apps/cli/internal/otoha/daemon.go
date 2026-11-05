package otoha

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func daemonFlagSet(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}

func daemonLabel() string {
	if runtime.GOOS == "darwin" {
		return "top.otoha." + serviceName()
	}
	return serviceName()
}

func daemonServicePath() string {
	h, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(h, "Library", "LaunchAgents", daemonLabel()+".plist")
	case "linux":
		return filepath.Join(h, ".config", "systemd", "user", serviceName()+".service")
	case "windows":
		return filepath.Join(Home(), "receiver-task.xml")
	default:
		return ""
	}
}

func daemonManagerStatus() (string, string) {
	name := serviceName()
	switch runtime.GOOS {
	case "darwin":
		domain := "gui/" + strconv.Itoa(os.Getuid())
		if err := exec.Command("launchctl", "print", domain+"/"+daemonLabel()).Run(); err == nil {
			return "active", "launchd"
		}
		return "inactive", "launchd"
	case "linux":
		if err := exec.Command("systemctl", "--user", "is-active", name+".service").Run(); err == nil {
			return "active", "systemd-user"
		}
		return "inactive", "systemd-user"
	case "windows":
		if err := exec.Command("schtasks", "/Query", "/TN", name).Run(); err == nil {
			return "installed", "task-scheduler"
		}
		return "absent", "task-scheduler"
	default:
		return "unsupported", runtime.GOOS
	}
}

func daemonManaged() (Object, bool) {
	managed, err := Load(filepath.Join(Home(), "receiver-service.json"))
	if err != nil || !B(managed, "managed") {
		return Object{}, false
	}
	return managed, true
}

func DaemonStatus() (Object, error) {
	metadata, managed := daemonManaged()
	status, manager := daemonManagerStatus()
	result := Object{
		"managed":       managed,
		"name":          serviceName(),
		"label":         daemonLabel(),
		"manager":       manager,
		"manager_state": status,
		"service_file":  daemonServicePath(),
		"log":           filepath.Join(Home(), "receiver.log"),
	}
	if managed {
		result["proxy_fingerprint"] = metadata["proxy_fingerprint"]
	}
	if state, err := Load(filepath.Join(Home(), "receiver-status.json")); err == nil {
		result["receiver"] = Object{"pid": N(state, "pid"), "heartbeat": state["heartbeat"], "bindings": state["bindings"], "alive": processAlive(N(state, "pid"))}
	} else {
		result["receiver"] = Object{"alive": false, "status_file": "missing"}
	}
	if page, err := Load(filepath.Join(Home(), "page-runtime.json")); err == nil {
		status := Object{"alive": processAlive(N(page, "pid")), "pid": N(page, "pid"), "started_at": page["started_at"]}
		if parsed, err := url.Parse(S(page, "url")); err == nil && parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1") {
			status["base_url"] = parsed.Scheme + "://" + parsed.Host
		}
		result["page"] = status
	} else {
		result["page"] = Object{"alive": false, "status_file": "missing"}
	}
	return result, nil
}

func daemonStart() error {
	if _, managed := daemonManaged(); !managed {
		return errors.New("Otoha daemon is not installed; join or initialize a team, then run otoha daemon install")
	}
	if runtime.GOOS == "darwin" {
		domain := "gui/" + strconv.Itoa(os.Getuid())
		if err := exec.Command("launchctl", "print", domain+"/"+daemonLabel()).Run(); err != nil {
			output, bootstrapErr := exec.Command("launchctl", "bootstrap", domain, daemonServicePath()).CombinedOutput()
			if bootstrapErr != nil {
				return fmt.Errorf("start launchd receiver: %w: %s", bootstrapErr, strings.TrimSpace(string(output)))
			}
		}
		if output, err := exec.Command("launchctl", "kickstart", domain+"/"+daemonLabel()).CombinedOutput(); err != nil {
			return fmt.Errorf("kickstart launchd receiver: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	return StartReceiver()
}

func daemonStop() error {
	metadata, managed := daemonManaged()
	if !managed {
		return errors.New("Otoha daemon is not installed")
	}
	name := S(metadata, "name")
	if name == "" {
		name = serviceName()
	}
	switch runtime.GOOS {
	case "darwin":
		domain := "gui/" + strconv.Itoa(os.Getuid())
		out, err := exec.Command("launchctl", "bootout", domain+"/"+daemonLabel()).CombinedOutput()
		if err != nil {
			text := strings.TrimSpace(string(out))
			if !strings.Contains(strings.ToLower(text), "could not find service") && !strings.Contains(strings.ToLower(text), "no such process") {
				return fmt.Errorf("stop launchd receiver: %w: %s", err, text)
			}
		}
	case "linux":
		out, err := exec.Command("systemctl", "--user", "stop", name+".service").CombinedOutput()
		if err != nil {
			return fmt.Errorf("stop systemd receiver: %w: %s", err, strings.TrimSpace(string(out)))
		}
	case "windows":
		out, err := exec.Command("schtasks", "/End", "/TN", name).CombinedOutput()
		if err != nil {
			return fmt.Errorf("stop task-scheduler receiver: %w: %s", err, strings.TrimSpace(string(out)))
		}
	default:
		return fmt.Errorf("daemon management is unsupported on %s", runtime.GOOS)
	}
	return nil
}

func daemonRestart() error {
	if err := daemonStop(); err != nil {
		return err
	}
	return daemonStart()
}

func daemonUninstall() error {
	metadata, managed := daemonManaged()
	if !managed {
		return errors.New("Otoha daemon is not installed")
	}
	name := S(metadata, "name")
	if name == "" {
		name = serviceName()
	}
	switch runtime.GOOS {
	case "darwin":
		domain := "gui/" + strconv.Itoa(os.Getuid())
		exec.Command("launchctl", "bootout", domain+"/"+daemonLabel()).Run()
	case "linux":
		exec.Command("systemctl", "--user", "disable", "--now", name+".service").Run()
	case "windows":
		exec.Command("schtasks", "/End", "/TN", name).Run()
		exec.Command("schtasks", "/Delete", "/F", "/TN", name).Run()
	default:
		return fmt.Errorf("daemon management is unsupported on %s", runtime.GOOS)
	}
	if path := daemonServicePath(); path != "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Remove(filepath.Join(Home(), "receiver-service.json"))
}

func daemonLogs(follow bool) (Object, error) {
	path := filepath.Join(Home(), "receiver.log")
	if follow {
		if runtime.GOOS == "windows" {
			return nil, errors.New("--follow is not supported on Windows; read the log file directly")
		}
		cmd := exec.Command("tail", "-f", path)
		cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
		return nil, cmd.Run()
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Object{"path": path, "contents": ""}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > 256*1024 {
		b = b[len(b)-256*1024:]
	}
	return Object{"path": path, "contents": string(b)}, nil
}

// DaemonCommand controls the one existing Otoha receiver service. It never
// installs a second receiver when an older managed service is present.
func DaemonCommand(args []string) (Object, error) {
	if len(args) == 0 {
		return nil, errors.New("otoha daemon install|uninstall|start|stop|restart|status|logs")
	}
	switch args[0] {
	case "install":
		if err := InstallReceiver(); err != nil {
			return nil, err
		}
		return DaemonStatus()
	case "uninstall":
		if err := daemonUninstall(); err != nil {
			return nil, err
		}
		return Object{"uninstalled": true, "name": serviceName()}, nil
	case "start":
		if err := daemonStart(); err != nil {
			return nil, err
		}
		return DaemonStatus()
	case "stop":
		if err := daemonStop(); err != nil {
			return nil, err
		}
		return DaemonStatus()
	case "restart":
		if err := daemonRestart(); err != nil {
			return nil, err
		}
		return DaemonStatus()
	case "status":
		return DaemonStatus()
	case "logs":
		f := daemonFlagSet("daemon logs")
		follow := f.Bool("follow", false, "follow receiver log")
		if err := f.Parse(args[1:]); err != nil {
			return nil, err
		}
		result, err := daemonLogs(*follow)
		if err != nil {
			return nil, err
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unknown daemon action %q", args[0])
	}
}
