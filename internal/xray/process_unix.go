//go:build !windows

package xray

import "syscall"

// processGroup makes the child its own process group, so killing it does not reach ours.
func processGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// killGroup kills the child's process group.
func killGroup(pid int, sig syscall.Signal) { _ = syscall.Kill(-pid, sig) }
