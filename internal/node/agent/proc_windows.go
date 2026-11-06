//go:build windows

package agent

import "os"

// selfTerm stops our own process; windows has no signals, so a hard exit (the service
// supervisor restarts it, as on unix).
func selfTerm() { os.Exit(0) }
