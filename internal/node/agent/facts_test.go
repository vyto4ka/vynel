package agent_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/node/agent"
	"github.com/vyto4ka/vynel/internal/panel/reconciler"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
)

// TestDynamicFactsClearsBootNTPWarning verifies that when a node starts with a transient NTP
// warning right after reboot and NTP synchronizes later, the agent re-sends Hello on
// FactsInterval and the panel clears the warning without restarting vynel-node.
func TestDynamicFactsClearsBootNTPWarning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(st)
	node, _, err := svc.CreateNode(ctx, service.ActorCLI, service.NodeInput{Name: "DE", Country: "de", Domain: "de.example.com", Manual: true})
	if err != nil {
		t.Fatal(err)
	}

	rec := reconciler.New(svc, log)
	recDone := make(chan struct{})
	go func() {
		_ = rec.Run(ctx)
		close(recDone)
	}()

	var ntpSynced atomic.Bool
	ag, err := agent.New(agent.Config{
		DataDir:       filepath.Join(dir, "node"),
		Manual:        true,
		Addresses:     func() []*nodev1.Address { return nil },
		FactsInterval: 25 * time.Millisecond,
		Dial: func(dctx context.Context) (agent.Conn, error) {
			panelEnd, nodeEnd := reconciler.Pipe(dctx)
			go func() {
				_ = rec.Serve(node.ID, panelEnd)
				nodeEnd.Close()
			}()
			return nodeEnd, nil
		},
		CheckFacts: func() []string {
			if ntpSynced.Load() {
				return nil
			}
			return []string{"clock is not NTP-synchronized (Reality rejects clients when clocks drift)"}
		},
		Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	agDone := make(chan struct{})
	go func() {
		_ = ag.Run(ctx)
		close(agDone)
	}()
	defer func() {
		cancel()
		<-agDone
		<-recDone
		ag.Close()
	}()

	waitWarnings := func(want int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			got, err := store.GetNode(ctx, st.DB, node.ID)
			if err == nil && len(got.Warnings) == want {
				return
			}
			time.Sleep(15 * time.Millisecond)
		}
		got, _ := store.GetNode(ctx, st.DB, node.ID)
		t.Fatalf("node warnings = %v, want len %d", got.Warnings, want)
	}

	waitWarnings(1)
	ntpSynced.Store(true)
	waitWarnings(0)
}
