package sysctl

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCheckClockBootGrace(t *testing.T) {
	unsynced := func() ([]byte, error) { return []byte("no\n"), nil }
	synced := func() ([]byte, error) { return []byte("yes\n"), nil }
	missing := func() ([]byte, error) { return nil, errors.New("no timedatectl") }

	if w := checkClock(unsynced, func() (time.Duration, bool) { return 30 * time.Second, true }); w != "" {
		t.Fatalf("expected boot grace to suppress NTP warning at 30s, got %q", w)
	}
	if w := checkClock(unsynced, func() (time.Duration, bool) { return BootGrace, true }); !strings.Contains(w, "not NTP-synchronized") {
		t.Fatalf("expected NTP warning once boot grace elapsed, got %q", w)
	}
	if w := checkClock(unsynced, func() (time.Duration, bool) { return 0, false }); !strings.Contains(w, "not NTP-synchronized") {
		t.Fatalf("expected NTP warning when uptime is unavailable, got %q", w)
	}
	if w := checkClock(synced, func() (time.Duration, bool) { return 5 * time.Hour, true }); w != "" {
		t.Fatalf("expected no warning when synced, got %q", w)
	}
	if w := checkClock(missing, func() (time.Duration, bool) { return 5 * time.Hour, true }); w != "" {
		t.Fatalf("expected no warning when timedatectl is unavailable, got %q", w)
	}
}
