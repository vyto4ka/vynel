package service

import (
	"slices"
	"testing"
	"time"
)

type ipClock struct{ t time.Time }

func (c *ipClock) now() time.Time              { return c.t }
func (c *ipClock) step(d time.Duration)        { c.t = c.t.Add(d) }
func online(ips ...string) map[string][]string { return map[string][]string{"7": ips} }

func newTestTracker() (*IPTracker, *ipClock) {
	c := &ipClock{t: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	t := newIPTracker(c.now)
	t.SetLimits(2, nil)
	return t, c
}

// report sends the same list from one node every 5 s for d.
func report(t *IPTracker, c *ipClock, d time.Duration, nodes map[int64]map[string][]string) map[int64][]string {
	var blocks map[int64][]string
	for end := c.t.Add(d); !c.t.After(end); c.step(5 * time.Second) {
		for n, o := range nodes {
			blocks, _ = t.Report(n, o)
		}
	}
	return blocks
}

func TestIPLimitFirstComeKeepSlots(t *testing.T) {
	tr, c := newTestTracker()
	report(tr, c, 20*time.Second, map[int64]map[string][]string{1: online("198.51.100.1", "198.51.100.2")})
	blocks := report(tr, c, 20*time.Second, map[int64]map[string][]string{1: online("198.51.100.1", "198.51.100.2", "198.51.100.3")})
	if !slices.Equal(blocks[7], []string{"198.51.100.3"}) {
		t.Fatalf("blocks %v", blocks)
	}
	if b, n, l := tr.Verdict(7, "198.51.100.3"); !b || n != 3 || l != 2 {
		t.Fatalf("verdict %v %d %d", b, n, l)
	}
	if b, _, _ := tr.Verdict(7, "198.51.100.1"); b {
		t.Fatal("a first address is blocked")
	}
	// The first one leaves: its slot is held for the grace period, then the waiting one gets it.
	blocks = report(tr, c, 20*time.Second, map[int64]map[string][]string{1: online("198.51.100.2", "198.51.100.3")})
	if len(blocks[7]) != 1 {
		t.Fatalf("slot freed too early: %v", blocks)
	}
	blocks = report(tr, c, 20*time.Second, map[int64]map[string][]string{1: online("198.51.100.2", "198.51.100.3")})
	if len(blocks) != 0 {
		t.Fatalf("not admitted after the grace: %v", blocks)
	}
}

// An app pinging every server: one address on many nodes, and short connections that show up in
// one report only, never take a slot or get blocked.
func TestIPLimitPingsAndManyNodes(t *testing.T) {
	tr, c := newTestTracker()
	blocks := report(tr, c, 20*time.Second, map[int64]map[string][]string{
		1: online("198.51.100.1"), 2: online("198.51.100.1"), 3: online("198.51.100.1", "198.51.100.2"),
	})
	if len(blocks) != 0 {
		t.Fatalf("one address on three nodes: %v", blocks)
	}
	// Two more addresses flash by for a single report each (pings through other networks).
	tr.Report(4, online("203.0.113.5", "203.0.113.6"))
	c.step(5 * time.Second)
	blocks, _ = tr.Report(4, online())
	blocks2 := report(tr, c, 20*time.Second, map[int64]map[string][]string{1: online("198.51.100.1"), 3: online("198.51.100.2")})
	if len(blocks) != 0 || len(blocks2) != 0 {
		t.Fatalf("pings blocked: %v %v", blocks, blocks2)
	}
}

func TestIPLimitFamiliesNodesAndUnlimited(t *testing.T) {
	tr, c := newTestTracker()
	tr.SetIgnored([]string{"192.0.2.10"})
	// Two dual-stack phones: two IPv4 and two /64 networks, within a limit of 2.
	blocks := report(tr, c, 20*time.Second, map[int64]map[string][]string{1: online(
		"198.51.100.1", "198.51.100.2", "2001:db8:1:1::5", "2001:db8:1:1::6", "2001:db8:2:2::1", "192.0.2.10")})
	if len(blocks) != 0 {
		t.Fatalf("dual stack counted twice: %v", blocks)
	}
	blocks = report(tr, c, 20*time.Second, map[int64]map[string][]string{1: online(
		"198.51.100.1", "198.51.100.2", "2001:db8:1:1::5", "2001:db8:2:2::1", "2001:db8:3:3::1")})
	if !slices.Equal(blocks[7], []string{"2001:db8:3:3::/64"}) {
		t.Fatalf("v6 blocks %v", blocks)
	}
	if b, _, _ := tr.Verdict(7, "2001:db8:3:3::99"); !b {
		t.Fatal("the /64 is not blocked")
	}
	// 0 = unlimited for this user.
	tr.SetLimits(2, map[int64]int{7: 0})
	if blocks, changed := tr.Evaluate(); len(blocks) != 0 || !changed {
		t.Fatalf("unlimited: %v %v", blocks, changed)
	}
	// A node gone silent stops counting.
	tr.SetLimits(1, nil)
	c.step(time.Minute)
	if blocks, _ := tr.Evaluate(); len(blocks) != 0 {
		t.Fatalf("stale report still counts: %v", blocks)
	}
}

func TestEmailUserAndAddrKey(t *testing.T) {
	for email, want := range map[string]int64{"7": 7, "7.c3": 7, "x": 0, "": 0, "-1": 0} {
		if got := EmailUser(email); got != want {
			t.Errorf("%q: %d", email, got)
		}
	}
	if k, v6 := AddrKey("[2001:db8::1]"); k != "2001:db8::/64" || !v6 {
		t.Errorf("v6 key %q", k)
	}
	if k, v6 := AddrKey("::ffff:198.51.100.1"); k != "198.51.100.1" || v6 {
		t.Errorf("mapped v4 key %q", k)
	}
}
