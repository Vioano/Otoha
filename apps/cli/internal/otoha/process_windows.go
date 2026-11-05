//go:build windows

package otoha

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200, HideWindow: true}
}
func lockReceiver(f *os.File) error {
	var o windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o)
}
func processAlive(pid int) bool {
	// Windows does not expose the Unix signal-0 probe used by the local
	// receiver. Preserve the existing Windows contract: a recorded positive
	// PID is treated as managed, while zero means no process was recorded.
	return pid > 0
}
func stopProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
