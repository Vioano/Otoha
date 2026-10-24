package otoha

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func serviceName() string {
	sum := sha256.Sum256([]byte(Home()))
	return "otoha-" + hex.EncodeToString(sum[:4])
}
func InstalledExecutable() (string, bool, error) {
	source, e := os.Executable()
	if e != nil {
		return "", false, e
	}
	source, e = filepath.EvalSymlinks(source)
	if e != nil {
		return "", false, e
	}
	name := "otoha"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(Home(), "bin", name)
	if source == target {
		return target, false, nil
	}
	if e = PrivateDir(filepath.Dir(target)); e != nil {
		return "", false, e
	}
	if _, e = os.Stat(target); e == nil {
		a, ae := exec.Command(source, "version").Output()
		b, be := exec.Command(target, "version").Output()
		fromInfo, fe := os.Stat(source)
		toInfo, te := os.Stat(target)
		if ae == nil && be == nil && fe == nil && te == nil && fromInfo.Size() == toInfo.Size() && bytes.Equal(a, b) {
			return target, false, nil
		}
		if runtime.GOOS == "windows" {
			// Windows holds a running executable open. Stop only this Home's
			// native task before replacing its release, preserving old clients.
			exec.Command("schtasks", "/End", "/TN", serviceName()).Run()
		}
	}
	from, e := os.Open(source)
	if e != nil {
		return "", false, e
	}
	defer from.Close()
	tmp, e := os.CreateTemp(filepath.Dir(target), ".otoha-bin-*")
	if e != nil {
		return "", false, e
	}
	path := tmp.Name()
	defer os.Remove(path)
	_, e = io.Copy(tmp, from)
	if e == nil {
		e = tmp.Chmod(0700)
	}
	closeErr := tmp.Close()
	if e != nil {
		return "", false, e
	}
	if closeErr != nil {
		return "", false, closeErr
	}
	e = os.Rename(path, target)
	for attempt := 0; runtime.GOOS == "windows" && e != nil && attempt < 4; attempt++ {
		time.Sleep(time.Duration(100*(1<<attempt)) * time.Millisecond)
		e = os.Rename(path, target)
	}
	if e != nil {
		return "", false, e
	}
	return target, true, nil
}

func proxyFingerprint() (string, error) {
	settings, err := Load(filepath.Join(Home(), "config.json"))
	if err != nil {
		return "", err
	}
	p, c, err := OpenProfile(settings)
	if err != nil {
		return "", err
	}
	c.Transport.Close()
	g := Clone(O(p, "gitea"))
	if path := S(settings, "gitea_profile"); path != "" {
		g, err = Load(path)
		if err != nil {
			return "", err
		}
	}
	delete(g, "repository")
	data, err := json.Marshal(Object{"gateway": p["gateway"], "url": p["url"], "actor": p["actor"], "gitea": g})
	if err != nil {
		return "", err
	}
	// Compare only effective routing and Git authentication. Store a digest
	// rather than another credential copy; changing projects does not restart.
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
func xmlText(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// User services retain the receiver across logins and restart failed processes.
// No administrator access, system proxy changes, or cloud scheduler is needed.
func InstallReceiver() error {
	if _, e := proxyConfig(); e != nil {
		return e
	}
	fingerprint, e := proxyFingerprint()
	if e != nil {
		return e
	}
	previous, _ := Load(filepath.Join(Home(), "receiver-service.json"))
	routeChanged := S(previous, "proxy_fingerprint") != fingerprint
	metadata := Object{"managed": true, "name": serviceName(), "proxy_fingerprint": fingerprint}
	executable, changed, e := InstalledExecutable()
	if e != nil {
		return e
	}
	name := serviceName()
	h, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	switch runtime.GOOS {
	case "darwin":
		label := "top.otoha." + name
		path := filepath.Join(h, "Library", "LaunchAgents", label+".plist")
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		data := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>` + label + `</string><key>ProgramArguments</key><array><string>` + xmlText(executable) + `</string><string>receive</string></array><key>EnvironmentVariables</key><dict><key>OTOHA_HOME</key><string>` + xmlText(Home()) + `</string></dict><key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict><key>ThrottleInterval</key><integer>3</integer><key>StandardOutPath</key><string>` + xmlText(filepath.Join(Home(), "receiver.log")) + `</string><key>StandardErrorPath</key><string>` + xmlText(filepath.Join(Home(), "receiver.log")) + `</string></dict></plist>`
		if e = os.WriteFile(path, []byte(data), 0600); e != nil {
			return e
		}
		domain := "gui/" + strconv.Itoa(os.Getuid())
		check := exec.Command("launchctl", "print", domain+"/"+label)
		loaded := check.Run() == nil
		if loaded && !changed && !routeChanged {
			// Binding workers discover new projects without restarting the service.
			return Save(filepath.Join(Home(), "receiver-service.json"), metadata)
		}
		if loaded {
			if output, err := exec.Command("launchctl", "bootout", domain+"/"+label).CombinedOutput(); err != nil {
				return fmt.Errorf("receiver upgrade unload: %w: %s", err, strings.TrimSpace(string(output)))
			}
		}
		if state, e := Load(filepath.Join(Home(), "receiver-status.json")); e == nil && N(state, "pid") > 0 {
			heartbeat, e := time.Parse(time.RFC3339, S(state, "heartbeat"))
			if e == nil && time.Since(heartbeat) >= 0 && time.Since(heartbeat) < 10*time.Second {
				if process, e := os.FindProcess(N(state, "pid")); e == nil {
					process.Signal(os.Interrupt)
				}
			}
		}
		output, e := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput()
		// launchd can retain an unloaded job briefly. Retry this known transition
		// only after unloading our own managed job; initial registration is unchanged.
		for attempt := 0; loaded && e != nil && attempt < 4; attempt++ {
			time.Sleep(time.Duration(100*(1<<attempt)) * time.Millisecond)
			output, e = exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput()
		}
		if e != nil {
			return fmt.Errorf("receiver startup registration: %w: %s", e, strings.TrimSpace(string(output)))
		}
	case "linux":
		path := filepath.Join(h, ".config", "systemd", "user", name+".service")
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		quote := func(s string) string {
			return `"` + strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`), `%`, `%%`) + `"`
		}
		data := "[Unit]\nDescription=Otoha team receiver\nAfter=network-online.target\n[Service]\nExecStart=" + quote(executable) + " receive\nEnvironment=" + quote("OTOHA_HOME="+Home()) + "\nRestart=on-failure\nRestartSec=3\n[Install]\nWantedBy=default.target\n"
		if e = os.WriteFile(path, []byte(data), 0600); e != nil {
			return e
		}
		for _, args := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", name + ".service"}} {
			if output, e := exec.Command("systemctl", args...).CombinedOutput(); e != nil {
				return fmt.Errorf("receiver user service: %w: %s", e, strings.TrimSpace(string(output)))
			}
		}
		if B(previous, "managed") && (changed || routeChanged) {
			if output, err := exec.Command("systemctl", "--user", "restart", name+".service").CombinedOutput(); err != nil {
				return fmt.Errorf("receiver upgrade restart: %w: %s", err, strings.TrimSpace(string(output)))
			}
		}
	case "windows":
		if B(previous, "managed") && routeChanged {
			exec.Command("schtasks", "/End", "/TN", name).Run()
		}
		path := filepath.Join(Home(), "receiver-task.xml")
		who := os.Getenv("USERNAME")
		if domain := os.Getenv("USERDOMAIN"); domain != "" {
			who = domain + `\` + who
		}
		data := `<?xml version="1.0" encoding="UTF-8"?><Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + xmlText(who) + `</UserId></LogonTrigger></Triggers><Principals><Principal id="Author"><UserId>` + xmlText(who) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><RestartOnFailure><Interval>PT1M</Interval><Count>999</Count></RestartOnFailure></Settings><Actions Context="Author"><Exec><Command>` + xmlText(executable) + `</Command><Arguments>service run --home &quot;` + xmlText(Home()) + `&quot;</Arguments><WorkingDirectory>` + xmlText(Home()) + `</WorkingDirectory></Exec></Actions></Task>`
		if e = os.WriteFile(path, []byte(data), 0600); e != nil {
			return e
		}
		output, e := exec.Command("schtasks", "/Create", "/F", "/TN", name, "/XML", path).CombinedOutput()
		if e != nil {
			return fmt.Errorf("receiver login task: %w: %s", e, strings.TrimSpace(string(output)))
		}
		if output, e := exec.Command("schtasks", "/Run", "/TN", name).CombinedOutput(); e != nil {
			return fmt.Errorf("receiver login task start: %w: %s", e, strings.TrimSpace(string(output)))
		}
	}
	return Save(filepath.Join(Home(), "receiver-service.json"), metadata)
}
