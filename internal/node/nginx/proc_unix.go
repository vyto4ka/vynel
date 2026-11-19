//go:build !windows

package nginx

import (
	"os"
	"syscall"
)

// processGroup makes the child its own process group, so killing it does not reach ours.
func processGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// killGroup kills the child's process group.
func killGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }

func fileInode(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}
