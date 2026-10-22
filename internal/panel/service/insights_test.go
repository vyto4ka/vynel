package service

import (
	"strconv"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
)

// Each inbound's traffic counts for its profile; the same user on two inbounds is online once.
func TestTrafficByProfile(t *testing.T) {
	f := newFixture(t)
	nl, de, prof, _ := f.setup(t)
	other := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "XHTTP", TemplateID: "vless-reality-selfsteal"}))
	nlIn := must(f.s.NodeInbounds(f.ctx, nl.ID))[0]
	deIn := must(f.s.NodeInbounds(f.ctx, de.ID))[0]
	xIn := must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: nl.ID, ProfileID: other.ID}))
	alice := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "alice"}))
	bob := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "bob"}))

	b := &nodev1.StatsBatch{Epoch: "e", Seq: 1, Ts: f.now.Unix(),
		Users: []*nodev1.UserTraffic{
			{Email: InboundEmail(alice.ID, nlIn.ID), Down: 100}, {Email: InboundEmail(alice.ID, xIn.ID), Down: 40},
			{Email: InboundEmail(bob.ID, nlIn.ID), Up: 5, Down: 5}, {Email: UserEmail(bob.ID), Down: 7},
		},
		Online:  []*nodev1.OnlineUser{{Email: InboundEmail(alice.ID, nlIn.ID)}, {Email: InboundEmail(alice.ID, xIn.ID)}},
		Metrics: &nodev1.Metrics{Cpu: 1},
	}
	if err := f.s.IngestStats(f.ctx, nl.ID, b); err != nil {
		t.Fatal(err)
	}
	b2 := &nodev1.StatsBatch{Epoch: "e", Seq: 1, Ts: f.now.Unix(), Users: []*nodev1.UserTraffic{{Email: InboundEmail(alice.ID, deIn.ID), Down: 60}}}
	if err := f.s.IngestStats(f.ctx, de.ID, b2); err != nil {
		t.Fatal(err)
	}
	if ns := must(f.s.NodeStatuses(f.ctx, nil)); ns[0].Metrics.Online != 1 {
		t.Fatalf("one user on two inbounds counts once: %d", ns[0].Metrics.Online)
	}

	tr := must(f.s.TrafficByProfile(f.ctx, "7d", 0))
	if len(tr.Days) != 7 || len(tr.Profiles) != 3 {
		t.Fatalf("%+v", tr)
	}
	p := tr.Profiles[0] // the largest: both nodes of the profile together
	if p.Label != prof.Name || p.Kind != "profile" || p.Total != 170 || p.Values[6] != 170 {
		t.Fatalf("profile %+v", p)
	}
	if tr.Profiles[1].Label != "XHTTP" || tr.Profiles[1].Total != 40 || tr.Profiles[2].Key != "old" || tr.Profiles[2].Total != 7 {
		t.Fatalf("profiles %+v", tr.Profiles)
	}
	if len(tr.Users) != 2 || tr.Users[0].Username != "alice" || tr.Users[0].Total != 200 || tr.Users[0].By["p"+itoa(other.ID)] != 40 ||
		tr.Users[1].By["old"] != 7 {
		t.Fatalf("users %+v", tr.Users)
	}
	if one := must(f.s.TrafficByProfile(f.ctx, "30d", bob.ID)); len(one.Users) != 1 || one.Users[0].Total != 17 || len(one.Profiles) != 2 {
		t.Fatalf("one user %+v", one)
	}
	if _, err := f.s.TrafficByProfile(f.ctx, "1y", 0); err == nil {
		t.Fatal("an unknown period accepted")
	}
}

// Addresses are kept with when they were first and last seen; one still online is written once
// a minute; the lists search by user and address.
func TestRecordIPsAndLists(t *testing.T) {
	f := newFixture(t)
	nl, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "NL", Country: "nl"}))
	alice := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "alice"}))
	bob := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "bob"}))
	start := f.now
	report := map[string][]string{InboundEmail(alice.ID, 1): {"203.0.113.5"}, InboundEmail(bob.ID, 1): {"[2001:db8::1]"}, "svc": {"198.51.100.1"}}
	if err := f.s.RecordIPs(f.ctx, nl.ID, report); err != nil {
		t.Fatal(err)
	}
	f.now = start.Add(30e9) // not yet a minute: nothing written
	_ = f.s.RecordIPs(f.ctx, nl.ID, report)
	ips := must(f.s.AllIPs(f.ctx, "", 0, 0))
	if ips.Total != 2 || ips.Items[0].LastAt != start.Unix() {
		t.Fatalf("%+v", ips.Items[0])
	}
	f.now = start.Add(90e9)
	_ = f.s.RecordIPs(f.ctx, nl.ID, report)
	ips = must(f.s.AllIPs(f.ctx, "alice", 0, 0))
	if ips.Total != 1 || ips.Items[0].IP != "203.0.113.5" || ips.Items[0].FirstAt != start.Unix() || ips.Items[0].LastAt != f.now.Unix() {
		t.Fatalf("%+v", ips.Items)
	}
	if v6 := must(f.s.AllIPs(f.ctx, "2001:db8", 0, 0)); v6.Total != 1 || v6.Items[0].IP != "2001:db8::1" {
		t.Fatalf("%+v", v6.Items)
	}

	for i, hw := range []string{"hw-a", "hw-b"} {
		d := &store.Device{UserID: alice.ID, HWID: hw, Platform: "android", Model: "Pixel " + itoa(int64(i)), FirstSeen: f.now.Unix(), LastSeen: f.now.Unix() + int64(i)}
		if err := store.AddDevice(f.ctx, f.s.Store().DB, d); err != nil {
			t.Fatal(err)
		}
	}
	devs := must(f.s.AllDevices(f.ctx, "", 1, 0))
	if devs.Total != 2 || len(devs.Items) != 1 || devs.Items[0].HWID != "hw-b" || devs.Items[0].Username != "alice" {
		t.Fatalf("%+v", devs)
	}
	if none := must(f.s.AllDevices(f.ctx, "100%", 0, 0)); none.Total != 0 {
		t.Fatal("a % in the search matched everything")
	}
	n := must(f.s.RecentNewcomers(f.ctx))
	if len(n.Days) != 30 || n.Devices[29] != 2 || n.IPs[29] != 2 {
		t.Fatalf("%+v", n)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
