package reconciler

import (
	"strconv"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

func TestDelta(t *testing.T) {
	in := []service.DesiredInbound{{Tag: "VLESS_NL", Protocol: "vless", Flow: "xtls-rprx-vision"}}
	from := &service.DesiredState{Hash: "a", Inbounds: in, Users: map[string][]xrayconf.Client{
		"VLESS_NL": {{Email: "1", ID: "u1"}, {Email: "2", ID: "u2"}, {Email: "3", ID: "u3"}},
	}}
	to := &service.DesiredState{Hash: "b", Inbounds: in, Users: map[string][]xrayconf.Client{
		"VLESS_NL": {{Email: "1", ID: "u1"}, {Email: "2", ID: "u2-new"}, {Email: "4", ID: "u4"}},
	}}
	d := Delta(from, to, 7)
	if d.BaseHash != "a" || d.Hash != "b" || d.Revision != 7 {
		t.Fatalf("header %+v", d)
	}
	got := map[string]nodev1.UserOp_Kind{}
	for _, op := range d.Ops {
		got[op.User.Email] = op.Kind
		if op.Kind == nodev1.UserOp_KIND_UPSERT && op.Flow != "xtls-rprx-vision" {
			t.Fatalf("upsert without flow: %+v", op)
		}
	}
	want := map[string]nodev1.UserOp_Kind{"2": nodev1.UserOp_KIND_UPSERT, "4": nodev1.UserOp_KIND_UPSERT, "3": nodev1.UserOp_KIND_REMOVE}
	if len(got) != len(want) {
		t.Fatalf("ops %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("op for %s: got %v want %v", k, got[k], v)
		}
	}
}

func TestSnapshotKeepsInboundOrder(t *testing.T) {
	ds := &service.DesiredState{Hash: "h", Config: []byte("{}"),
		Inbounds: []service.DesiredInbound{{Tag: "B"}, {Tag: "A"}},
		Users:    map[string][]xrayconf.Client{"A": {{Email: "1", ID: "x"}}},
	}
	s := Snapshot(ds, 3)
	if s.Inbounds[0].Tag != "B" || len(s.Inbounds[1].Users) != 1 || s.Revision != 3 {
		t.Fatalf("snapshot %+v", s)
	}
}

func bigDesired(users int, hash string) *service.DesiredState {
	ds := &service.DesiredState{Config: []byte(`{}`), Hash: hash, Users: map[string][]xrayconf.Client{},
		Inbounds: []service.DesiredInbound{{Tag: "VLESS_NL", Protocol: "vless"}}}
	for i := 0; i < users; i++ {
		ds.Users["VLESS_NL"] = append(ds.Users["VLESS_NL"], xrayconf.Client{Email: strconv.Itoa(1000000 + i), ID: "00000000-0000-4000-8000-" + strconv.Itoa(100000000000+i)})
	}
	return ds
}

// Agents with "chunks" get a big snapshot in parts, older ones whole; a delta that grew as big
// as a snapshot (a bulk change) goes in parts too.
func TestNextSendsParts(t *testing.T) {
	r := New(nil, nil)
	s := &session{r: r, nodeID: 1}
	ds := bigDesired(100000, "big")
	r.desired[1], r.revision[1] = ds, 3

	s.hello = &nodev1.Hello{}
	if msgs := s.next(); len(msgs) != 1 || msgs[0].GetSnapshot().GetParts() != 0 {
		t.Fatalf("old agent: %d messages", len(msgs))
	}
	s.pending, s.hello = nil, &nodev1.Hello{Capabilities: []string{CapChunks}}
	msgs := s.next()
	if len(msgs) < 4 {
		t.Fatalf("agent with chunks: %d messages", len(msgs))
	}
	for i, m := range msgs {
		if sn := m.GetSnapshot(); sn == nil || sn.Part != int32(i) || sn.Parts != int32(len(msgs)) || sn.Hash != "big" {
			t.Fatalf("part %d: %+v", i, m)
		}
	}
	if s.pending != ds {
		t.Fatal("the snapshot is not pending as a whole")
	}
	// The node acknowledged the small state; now almost every user changes.
	s.pending, s.applied, s.appliedH = nil, bigDesired(10, "small"), "small"
	if msgs := s.next(); len(msgs) < 4 || msgs[0].GetSnapshot() == nil {
		t.Fatalf("huge delta: %d messages, first %T", len(msgs), msgs[0].Msg)
	}
	// A small change stays a delta.
	r.desired[1] = bigDesired(11, "small+1")
	s.pending = nil
	if msgs := s.next(); len(msgs) != 1 || msgs[0].GetDelta() == nil {
		t.Fatalf("small delta: %d messages", len(msgs))
	}
}
