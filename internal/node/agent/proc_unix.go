//go:build !windows

package agent

import (
	"os"
	"syscall"
)

// selfTerm asks our own process to terminate (the service supervisor restarts it).
func selfTerm() { _ = syscall.Kill(os.Getpid(), syscall.SIGTERM) }
