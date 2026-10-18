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
	check := func(what string, id int64, st *update.Status, online bool, version, want string) {
		t.Helper()
		if got, _, _, _ := u.state(id, st, online, version, now); got != want {
			t.Fatalf("%s: %q, want %q", what, got, want)
		}
	}
	old := &update.Status{OK: true, StartedAt: now.Add(-3 * time.Hour).Unix(), FinishedAt: now.Add(-3 * time.Hour).Unix()}
	check("nothing happened", 1, old, true, "v1", "")

	u.markSent(1, "v1", nil)
	check("pressed, node silent", 1, old, true, "v1", UpdSent)
	check("pressed, node went down", 1, old, false, "v1", UpdRestarting)
	running := &update.Status{Running: true, StartedAt: now.Unix(), Log: "downloading"}
	check("node reports running", 1, running, true, "v1", UpdRunning)
	check("running on the new binary already", 1, running, false, "v2", UpdRestarting)
	check("done", 1, &update.Status{OK: true, StartedAt: now.Unix(), FinishedAt: now.Unix()}, true, "v2", UpdDone)

	u.markSent(4, "v1", nil)
	check("failed", 4, &update.Status{StartedAt: now.Unix(), FinishedAt: now.Unix(), Log: "boom"}, true, "v1", UpdFailed)

	// An old agent never reports: after the wait it is a failure with an explanation...
	u.markSent(2, "v1", nil)
	u.jobs[2].sentAt = now.Add(-updateSentWait - time.Second)
	if st, log, _, _ := u.state(2, nil, true, "v1", now); st != UpdFailed || log != silentNode {
		t.Fatalf("silent node: %q %q", st, log)
	}
	// ...until the node comes back with another version (updated by hand): then it is done.
	if st, log, _, _ := u.state(2, nil, true, "v2", now); st != UpdDone || log != "Версия v1 → v2" {
		t.Fatalf("updated by hand: %q %q", st, log)
	}

	// The command itself failed (an agent that does not know the command); updated by hand later.
	u.markSent(3, "v1", errors.New("unknown action 5"))
	if st, log, _, _ := u.state(3, nil, true, "v1", now); st != UpdFailed || log == "" || log == "unknown action 5" {
		t.Fatalf("refused: %q %q", st, log)
	}
	check("refused, then updated by hand", 3, nil, true, "v2", UpdDone)

	// «Скрыть» hides a result for good, and a later update shows again.
	u.dismiss(3)
	check("dismissed", 3, nil, true, "v2", "")
	u.markSent(5, "v1", nil)
	u.dismiss(5)
	check("dismissed failure from the node", 5, &update.Status{StartedAt: now.Unix() - 5, FinishedAt: now.Unix() - 1, Log: "boom"}, true, "v1", "")
}
