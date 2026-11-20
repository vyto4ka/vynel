package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
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

	// Real log is preserved even when version changed before FinishedAt caught up.
	u.markSent(6, "v1", nil)
	if st, log, _, _ := u.state(6, &update.Status{Running: false, StartedAt: now.Unix(), Log: "[12:00:01] ok"}, true, "v2", now); st != UpdDone || log != "[12:00:01] ok" {
		t.Fatalf("real log lost on version bump: %q %q", st, log)
	}
}

func TestMergeUpdateStatusDropsStaleLive(t *testing.T) {
	now := time.Now().Unix()
	staleLive := &update.Status{
		Running:    false,
		OK:         true,
		StartedAt:  now - 3600,
		FinishedAt: now - 3550,
		Version:    "v0.2.9",
		Log:        "old update log",
	}
	freshSent := &store.UpdateRun{
		NodeID:      3,
		FromVersion: "v0.2.9",
		State:       UpdSent,
		SentAt:      now,
	}
	if got := mergeUpdateStatus(staleLive, freshSent); got != nil {
		t.Fatalf("expected stale live status to be ignored on fresh trigger, got %+v", got)
	}

	// Once the new update starts and persists its log, a panel restart (live == nil) still serves it.
	persistedRunning := &store.UpdateRun{
		NodeID:      3,
		FromVersion: "v0.2.9",
		State:       UpdRunning,
		SentAt:      now,
		StartedAt:   now + 1,
		Log:         "[12:00:01] downloading vynel",
	}
	got := mergeUpdateStatus(nil, persistedRunning)
	if got == nil || !got.Running || got.Log != "[12:00:01] downloading vynel" {
		t.Fatalf("persisted running log lost: %+v", got)
	}
}

func TestRunUpdatesAndLogsAcrossPanelRestart(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureWebDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	pw, err := svc.SetAdminCredentials(ctx, service.ActorCLI, "boss")
	if err != nil {
		t.Fatal(err)
	}
	nl, _, err := svc.CreateNode(ctx, service.ActorCLI, service.NodeInput{Name: "Нидерланды", Country: "NL"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetNodeRuntime(ctx, st.DB, nl.ID, store.NodeRuntime{AgentVersion: "v0.2.9"}); err != nil {
		t.Fatal(err)
	}

	var liveStatus *update.Status
	makeClient := func(ver string) *client {
		base, _ := svc.WebPath(ctx)
		srv := New(Config{
			Service:      svc,
			Version:      ver,
			Connected:    func(id int64) bool { return id == nl.ID },
			UpdateStatus: func(id int64) *update.Status { return liveStatus },
			NodeAction: func(ctx context.Context, nodeID int64, action string) (string, error) {
				// Simulate node immediately streaming its first log line BEFORE NodeAction returns!
				now := time.Now().Unix()
				liveStatus = &update.Status{Running: true, StartedAt: now, Log: "[12:00:01] Нидерланды: скачиваем релиз"}
				_ = svc.RecordUpdateStatus(ctx, nodeID, liveStatus)
				return "the update is running", nil
			},
		})
		srv.upd.latest = "v0.3.0"
		srv.upd.checkedAt = time.Now()
		c := &client{t: t, srv: srv, base: strings.TrimSuffix(base, "/")}
		w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true)
		if w.Code != http.StatusOK {
			t.Fatalf("login: %d %s", w.Code, w.Body.String())
		}
		return c
	}

	c1 := makeClient("v0.2.9")
	w := c1.do("POST", "/api/updates/run", `{"target":"node","id":`+strconv.FormatInt(nl.ID, 10)+`}`, true)
	if w.Code != http.StatusOK {
		t.Fatalf("updates/run: %d %s", w.Code, w.Body.String())
	}

	// Simulate panel restart: new Server instance, in-memory liveStatus cleared until node reconnects.
	liveStatus = nil
	c2 := makeClient("v0.3.0")

	w = c2.do("GET", "/api/updates?logs=1", "", false)
	if w.Code != http.StatusOK {
		t.Fatalf("updates?logs=1: %d %s", w.Code, w.Body.String())
	}
	var info UpdateInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.Nodes) != 1 || !strings.Contains(info.Nodes[0].Log, "Нидерланды: скачиваем релиз") || !info.Nodes[0].Updating {
		t.Fatalf("expected persisted live log after panel restart, got %+v", info.Nodes)
	}
}
