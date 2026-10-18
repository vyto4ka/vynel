package store

import (
	"context"
	"testing"
)

// Events that do not change what nodes run (a new device, an address over the limit) must not
// move the id the reconciler watches: every new phone made the panel recompute every node.
func TestLastEventIDSkipsDeviceEvents(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if id, err := LastEventID(ctx, st.DB); err != nil || id != 0 {
		t.Fatalf("empty outbox: %d %v", id, err)
	}
	must := func(typ string) {
		t.Helper()
		if err := AddEvent(ctx, st.DB, typ, 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	must("user.changed")
	want, _ := LastEventID(ctx, st.DB)
	if want == 0 {
		t.Fatal("a state event is not seen")
	}
	for _, typ := range []string{"device.added", "device.limit_reached", "user.ip_limit"} {
		must(typ)
	}
	if id, err := LastEventID(ctx, st.DB); err != nil || id != want {
		t.Fatalf("device events moved the id: %d (want %d) %v", id, want, err)
	}
	must("node.changed")
	if id, _ := LastEventID(ctx, st.DB); id <= want {
		t.Fatalf("a later state event is not seen: %d", id)
	}
}
