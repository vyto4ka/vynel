package reconciler

import (
	"context"
	"strings"
	"testing"
	"time"

	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/update"
)

// A stale stored capability must not let a pin be silently ignored by the live agent.
func TestUpdateRequiresLiveXrayCapability(t *testing.T) {
	r := New(nil, nil)
	s := &session{out: make(chan *nodev1.PanelMessage, 1), hello: &nodev1.Hello{}}
	r.sessions[1] = s
	pin := "v26.9.30"
	if _, err := r.Command(t.Context(), 1, &nodev1.Command{Action: nodev1.Command_ACTION_UPDATE, XrayRelease: &pin}); err == nil || !strings.Contains(err.Error(), "--mode update") {
		t.Fatalf("old agent was not rejected clearly: %v", err)
	}
	if len(s.out) != 0 {
		t.Fatal("sent an unsupported command")
	}
	s.hello.Capabilities = []string{update.CapXrayRelease}
	latest := ""
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	answer := make(chan *nodev1.Command, 1)
	go func() {
		select {
		case msg := <-s.out:
			cmd := msg.GetCommand()
			answer <- cmd
			s.mu.Lock()
			waiter := s.waiters[cmd.Id]
			s.mu.Unlock()
			waiter <- &nodev1.CommandResult{Id: cmd.Id}
		case <-ctx.Done():
		}
	}()
	if _, err := r.Command(ctx, 1, &nodev1.Command{Action: nodev1.Command_ACTION_UPDATE, XrayRelease: &latest}); err != nil {
		t.Fatal(err)
	}
	cmd := <-answer
	if cmd.XrayRelease == nil || *cmd.XrayRelease != "" {
		t.Fatal("latest must be explicit to remove a pin")
	}
}
