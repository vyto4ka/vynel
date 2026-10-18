package agent

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/vyto4ka/vynel/internal/panel/reconciler"
	"github.com/vyto4ka/vynel/internal/panel/service"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

func bigState(users int) *service.DesiredState {
	ds := &service.DesiredState{Config: []byte(`{"inbounds":[]}`), Nginx: strings.Repeat("# nginx\n", 2000), Hash: "h1",
		Users: map[string][]xrayconf.Client{}}
	for _, tag := range []string{"VLESS_NL", "HY2_NL", "EMPTY_NL"} {
		ds.Inbounds = append(ds.Inbounds, service.DesiredInbound{Tag: tag, Protocol: "vless", Flow: "xtls-rprx-vision"})
		if tag == "EMPTY_NL" {
			continue
		}
		for i := 0; i < users; i++ {
			ds.Users[tag] = append(ds.Users[tag], xrayconf.Client{Email: fmt.Sprint(100000 + i), ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i)})
		}
	}
	return ds
}

// A node with 50k users on two inbounds: well over gRPC's 4 MB in one message. In parts, each
// is about a megabyte, and the agent puts back exactly the whole snapshot.
func TestSnapshotPartsRoundTrip(t *testing.T) {
	ds := bigState(50000)
	whole := reconciler.Snapshot(ds, 7)
	if proto.Size(whole) < 4<<20 {
		t.Fatalf("test snapshot is only %d bytes", proto.Size(whole))
	}
	parts := reconciler.SnapshotParts(ds, 7, reconciler.PartBytes)
	if len(parts) < 4 {
		t.Fatalf("%d parts", len(parts))
	}
	var p snapshotParts
	var got *nodev1.Snapshot
	for i, s := range parts {
		if n := proto.Size(s); n > reconciler.PartBytes+64<<10 && i > 0 {
			t.Fatalf("part %d is %d bytes", i, n)
		}
		out, err := p.add(s)
		if err != nil {
			t.Fatal(err)
		}
		if (out != nil) != (i == len(parts)-1) {
			t.Fatalf("part %d: whole=%v", i, out != nil)
		}
		got = out
	}
	if !proto.Equal(got, whole) {
		t.Fatalf("reassembled snapshot differs: %d inbounds, %d/%d users", len(got.Inbounds), len(got.Inbounds[0].Users), len(got.Inbounds[1].Users))
	}
}

// A small snapshot goes whole, as before.
func TestSnapshotPartsSmall(t *testing.T) {
	ds := bigState(10)
	parts := reconciler.SnapshotParts(ds, 1, reconciler.PartBytes)
	if len(parts) != 1 || parts[0].Parts != 0 {
		t.Fatalf("small snapshot split: %d parts", len(parts))
	}
	var p snapshotParts
	if s, err := p.add(parts[0]); err != nil || !proto.Equal(s, reconciler.Snapshot(ds, 1)) {
		t.Fatalf("whole snapshot: %v", err)
	}
}

// A missing or foreign part is an error (the panel sends the snapshot again); a new part 0
// starts over.
func TestSnapshotPartsOutOfOrder(t *testing.T) {
	parts := reconciler.SnapshotParts(bigState(30000), 7, reconciler.PartBytes)
	var p snapshotParts
	if _, err := p.add(parts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := p.add(parts[2]); err == nil {
		t.Fatal("skipped part accepted")
	}
	if _, err := p.add(parts[1]); err == nil {
		t.Fatal("part after a broken run accepted")
	}
	other := reconciler.SnapshotParts(bigState(30000), 8, reconciler.PartBytes)
	if _, err := p.add(parts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := p.add(other[1]); err == nil {
		t.Fatal("part of another revision accepted")
	}
	var got *nodev1.Snapshot
	for _, s := range other {
		got, _ = p.add(s)
	}
	if got == nil || got.Revision != 8 {
		t.Fatal("a fresh run did not complete")
	}
}
