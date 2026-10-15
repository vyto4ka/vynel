package service

import (
	"testing"
	"time"
)

func TestWebSessionRules(t *testing.T) {
	f := newFixture(t)
	start := func() string {
		w := must(f.s.StartWebSession(f.ctx, LoginPassword, "admin", "203.0.113.5", "test", "", time.Hour))
		return w.ID
	}

	// Background polling does not keep a session alive; an action does.
	id := start()
	f.now = f.now.Add(4 * time.Minute)
	if why := f.s.WebSessionCheck(f.ctx, id, "203.0.113.5", true); why != "" {
		t.Fatalf("action at 4 min: %q", why)
	}
	f.now = f.now.Add(4 * time.Minute)
	if why := f.s.WebSessionCheck(f.ctx, id, "203.0.113.5", false); why != "" {
		t.Fatalf("poll 4 min after the action: %q", why)
	}
	f.now = f.now.Add(2 * time.Minute)
	f.s.ExpireWebSessions(f.ctx)
	if why := f.s.WebSessionCheck(f.ctx, id, "203.0.113.5", true); why != EndIdle {
		t.Fatalf("6 min without actions: %q", why)
	}

	// A closed page ends the session after the grace; a reload (any request) keeps it.
	id = start()
	noErr(t, f.s.WebSessionClosing(f.ctx, id))
	f.now = f.now.Add(2 * time.Second)
	if why := f.s.WebSessionCheck(f.ctx, id, "203.0.113.5", false); why != "" {
		t.Fatalf("reload: %q", why)
	}
	f.now = f.now.Add(time.Minute)
	f.s.ExpireWebSessions(f.ctx)
	if why := f.s.WebSessionCheck(f.ctx, id, "203.0.113.5", true); why != "" {
		t.Fatalf("reloaded page ended: %q", why)
	}
	noErr(t, f.s.WebSessionClosing(f.ctx, id))
	f.now = f.now.Add(closingGrace)
	f.s.ExpireWebSessions(f.ctx)
	if why := f.s.WebSessionCheck(f.ctx, id, "203.0.113.5", true); why != EndClosed {
		t.Fatalf("closed page: %q", why)
	}

	// Another IP ends it at once, unless binding is off.
	id = start()
	if why := f.s.WebSessionCheck(f.ctx, id, "198.51.100.1", false); why != EndIP {
		t.Fatalf("another IP: %q", why)
	}
	noErr(t, f.s.SetSetting(f.ctx, ActorCLI, SettingWebBindIP, "false"))
	noErr(t, f.s.SetSetting(f.ctx, ActorCLI, SettingWebIdleMinutes, "0"))
	id = start()
	if why := f.s.WebSessionCheck(f.ctx, id, "198.51.100.1", false); why != "" {
		t.Fatalf("binding off: %q", why)
	}
	f.now = f.now.Add(10 * time.Hour / 24)
	f.s.ExpireWebSessions(f.ctx)
	if why := f.s.WebSessionCheck(f.ctx, id, "198.51.100.1", false); why != "" {
		t.Fatalf("idle limit off: %q", why)
	}

	// Every end is announced to the bot.
	ended := 0
	for len(f.s.Notices()) > 0 {
		if n := <-f.s.Notices(); n.Kind == "ended" {
			ended++
		}
	}
	if ended != 3 {
		t.Fatalf("%d end notices, want 3", ended)
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
