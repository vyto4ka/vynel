//go:build windows

package xray

import (
	"os"
	"syscall"
)

// processGroup: windows processes are killed directly, there are no process groups.
func processGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }

// killGroup kills the process itself on windows (syscall.Kill is unix-only).
func killGroup(pid int, _ syscall.Signal) {
	p, err := os.FindProcess(pid)
	if err == nil {
		_ = p.Kill()
	}
}
