package reconciler

import (
	"testing"

	"github.com/vyto4ka/vynnel/internal/panel/service"
	nodev1 "github.com/vyto4ka/vynnel/internal/proto/vynnel/node/v1"
	"github.com/vyto4ka/vynnel/internal/xrayconf"
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
