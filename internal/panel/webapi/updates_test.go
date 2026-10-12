package webapi

import (
	"errors"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/update"
)

func TestUpdateStates(t *testing.T) {
	now := time.Now()
	var u updates
	check := func(what string, id int64, st *update.Status, online bool, want string) {
		t.Helper()
		if got, _, _, _ := u.state(id, st, online, now); got != want {
			t.Fatalf("%s: %q, want %q", what, got, want)
		}
	}
	old := &update.Status{OK: true, StartedAt: now.Add(-3 * time.Hour).Unix(), FinishedAt: now.Add(-3 * time.Hour).Unix()}
	check("nothing happened", 1, old, true, "")

	u.markSent(1, nil)
	check("pressed, node silent", 1, old, true, UpdSent)
	check("pressed, node went down", 1, old, false, UpdRestarting)
	running := &update.Status{Running: true, StartedAt: now.Unix(), Log: "downloading"}
	check("node reports running", 1, running, true, UpdRunning)
	check("running, node restarting", 1, running, false, UpdRestarting)
	check("done", 1, &update.Status{OK: true, StartedAt: now.Unix(), FinishedAt: now.Unix()}, true, UpdDone)
	check("failed", 1, &update.Status{StartedAt: now.Unix(), FinishedAt: now.Unix(), Log: "boom"}, true, UpdFailed)

	// An old agent never reports: after the wait it is a failure with an explanation.
	u.sent[2] = now.Add(-updateSentWait - time.Second)
	if st, log, _, _ := u.state(2, nil, true, now); st != UpdFailed || log == "" {
		t.Fatalf("silent node: %q %q", st, log)
	}

	// The command itself failed.
	u.markSent(3, errors.New("unknown action 5"))
	if st, log, _, _ := u.state(3, nil, true, now); st != UpdFailed || log == "" || log == "unknown action 5" {
		t.Fatalf("refused: %q %q", st, log)
	}
}
