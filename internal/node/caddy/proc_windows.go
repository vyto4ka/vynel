//go:build windows

package caddy

import (
	"os"
	"syscall"
)

// processGroup: windows processes are killed directly, there are no process groups.
func processGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }

// killGroup kills the process itself on windows (syscall.Kill is unix-only).
func killGroup(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
