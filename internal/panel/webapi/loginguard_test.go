package webapi

import (
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoginGuardBlocks(t *testing.T) {
	g := newLoginGuard()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	var blocked []string
	g.OnBlock = func(ip string, fails int, until time.Time) {
		blocked = append(blocked, ip)
		if fails != 5 {
			t.Errorf("fails %d", fails)
		}
	}
	fail := func(ip string) error {
		done, err := g.begin(ip)
		if err != nil {
			return err
		}
		done(false)
		return nil
	}
	for i := 0; i < 5; i++ {
		if err := fail("198.51.100.7"); err != nil {
			t.Fatalf("attempt %d refused: %v", i+1, err)
		}
	}
	var b *loginBlocked
	if err := fail("198.51.100.7"); !errors.As(err, &b) || b.wait != time.Minute {
		t.Fatalf("not blocked after 5 failures: %v", err)
	}
	if _, err := g.begin("203.0.113.9"); err != nil {
		t.Fatalf("another address is refused too: %v", err)
	}
	// The next block is twice as long.
	now = now.Add(61 * time.Second)
	for i := 0; i < 5; i++ {
		if err := fail("198.51.100.7"); err != nil {
			t.Fatalf("after the block, attempt %d refused: %v", i+1, err)
		}
	}
	if err := fail("198.51.100.7"); !errors.As(err, &b) || b.wait != 2*time.Minute {
		t.Fatalf("second block: %v", err)
	}
	if len(blocked) != 2 {
		t.Fatalf("OnBlock calls: %v", blocked)
	}
	// A success clears the address, strikes included.
	now = now.Add(3 * time.Minute)
	done, err := g.begin("198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}
	done(true)
	if g.ips["198.51.100.7"] != nil {
		t.Fatal("success did not clear the address")
	}
	// Failures spread wider than the window never add up to a block.
	for i := 0; i < 12; i++ {
		now = now.Add(4 * time.Minute)
		if err := fail("192.0.2.50"); err != nil {
			t.Fatalf("slow failures blocked at %d: %v", i, err)
		}
	}
}

// A flood never queues: once the few check slots are taken, the rest are told to come back.
func TestLoginGuardBusy(t *testing.T) {
	g := newLoginGuard()
	var dones []func(bool)
	for i := 0; i < cap(g.sem); i++ {
		done, err := g.begin("192.0.2.1")
		if err != nil {
			t.Fatal(err)
		}
		dones = append(dones, done)
	}
	if _, err := g.begin("192.0.2.2"); !errors.Is(err, errLoginBusy) {
		t.Fatalf("full: %v", err)
	}
	dones[0](true)
	if _, err := g.begin("192.0.2.2"); err != nil {
		t.Fatalf("a slot came back: %v", err)
	}
}

// One address guessing gets blocked (429 with Retry-After, even with the right password); the
// admin on another address signs in as usual.
func TestLoginBlockedPerAddress(t *testing.T) {
	c, _, pw := newServer(t)
	login := func(ip, password string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", c.base+"/api/login", strings.NewReader(`{"login":"boss","password":"`+password+`"}`))
		r.Header.Set(csrfHeader, "1")
		r.RemoteAddr = ip + ":40000"
		w := httptest.NewRecorder()
		c.srv.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < 5; i++ {
		if w := login("198.51.100.7", "guess"); w.Code != 401 {
			t.Fatalf("guess %d: %d", i+1, w.Code)
		}
	}
	w := login("198.51.100.7", pw)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "мин") {
		t.Fatalf("blocked address: %d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
	if w := login("203.0.113.9", pw); w.Code != 200 {
		t.Fatalf("admin elsewhere: %d %s", w.Code, w.Body.String())
	}
}

// Garbage one-time tokens from many addresses do not hold up a real sign-in (they used to
// sleep under a shared lock, about 0.7 s each, in a queue).
func TestLoginFloodDoesNotDelay(t *testing.T) {
	c, _, pw := newServer(t)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := httptest.NewRequest("POST", c.base+"/api/login/magic", strings.NewReader(`{"token":"garbage"}`))
			r.Header.Set(csrfHeader, "1")
			r.RemoteAddr = "198.51.100." + itoa(int64(i)) + ":40000"
			c.srv.ServeHTTP(httptest.NewRecorder(), r)
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // the flood is in its delays now
	start := time.Now()
	r := httptest.NewRequest("POST", c.base+"/api/login", strings.NewReader(`{"login":"boss","password":"`+pw+`"}`))
	r.Header.Set(csrfHeader, "1")
	w := httptest.NewRecorder()
	c.srv.ServeHTTP(w, r)
	if w.Code != 200 || time.Since(start) > 2*time.Second {
		t.Fatalf("sign-in during a flood: %d after %v", w.Code, time.Since(start))
	}
	wg.Wait()
}
