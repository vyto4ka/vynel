package webapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// loginGuard slows down guessing at the sign-in endpoints (password, one-time link, Mini App).
// An address that fails MaxFails times within Window is refused for Block, twice as long on
// every repeat up to MaxBlock; a success clears it. Only a few checks run at once (bcrypt is
// slow on purpose): the rest are answered at once instead of queueing behind a flood.
type loginGuard struct {
	MaxFails         int
	MaxInFlightPerIP int
	Window           time.Duration
	Block            time.Duration
	MaxBlock         time.Duration
	// OnBlock is told when an address gets blocked (audit, Telegram).
	OnBlock func(ip string, fails int, until time.Time)

	now func() time.Time
	sem chan struct{} // bounds heavy password (bcrypt) checks only

	mu        sync.Mutex
	ips       map[string]*loginFails
	inFlight  map[string]int
	lastPrune time.Time
}

type loginFails struct {
	n       int       // failures in the current window
	first   time.Time // start of the window
	until   time.Time // refused until
	strikes int       // blocks so far: each one doubles the next
	last    time.Time
}

const maxTrackedLoginIPs = 32768

func newLoginGuard() *loginGuard {
	return &loginGuard{
		MaxFails: 5, MaxInFlightPerIP: 2,
		Window: 15 * time.Minute, Block: time.Minute, MaxBlock: time.Hour,
		now: time.Now, sem: make(chan struct{}, 8),
		ips: map[string]*loginFails{}, inFlight: map[string]int{},
	}
}

// errLoginBusy and loginBlocked are answered with 429.
type loginBlocked struct{ wait time.Duration }

func (e *loginBlocked) Error() string {
	return fmt.Sprintf("слишком много неудачных попыток входа с этого адреса — повторите через %s", waitText(e.wait))
}

var errLoginBusy = &httpError{http.StatusTooManyRequests, "сейчас слишком много попыток входа — повторите через несколько секунд"}

func waitText(d time.Duration) string {
	if m := int(d.Round(time.Minute) / time.Minute); m >= 1 {
		return strconv.Itoa(m) + " мин"
	}
	return strconv.Itoa(int(d.Round(time.Second)/time.Second)) + " с"
}

// begin admits one password (bcrypt) sign-in attempt from ip. done must be called with its outcome.
func (g *loginGuard) begin(ip string) (done func(ok bool), err error) {
	return g.admit(ip, true)
}

// beginFast admits a lightweight sign-in check (one-time link, Telegram Mini App HMAC) without
// competing for the bcrypt worker semaphore, while enforcing IP bans and per-IP concurrency.
func (g *loginGuard) beginFast(ip string) (done func(ok bool), err error) {
	return g.admit(ip, false)
}

func (g *loginGuard) admit(ip string, heavy bool) (done func(ok bool), err error) {
	g.mu.Lock()
	now := g.now()
	if f := g.ips[ip]; f != nil && now.Before(f.until) {
		wait := f.until.Sub(now)
		g.mu.Unlock()
		return nil, &loginBlocked{wait}
	}
	if g.inFlight == nil {
		g.inFlight = map[string]int{}
	}
	perIP := g.MaxInFlightPerIP
	if perIP <= 0 {
		perIP = 2
	}
	if g.inFlight[ip] >= perIP {
		g.mu.Unlock()
		return nil, errLoginBusy
	}
	g.inFlight[ip]++
	g.mu.Unlock()

	releaseIP := func() {
		g.mu.Lock()
		if g.inFlight[ip] <= 1 {
			delete(g.inFlight, ip)
		} else {
			g.inFlight[ip]--
		}
		g.mu.Unlock()
	}

	if heavy {
		select {
		case g.sem <- struct{}{}:
		default:
			releaseIP()
			return nil, errLoginBusy
		}
	}
	return func(ok bool) {
		if heavy {
			<-g.sem
		}
		releaseIP()
		g.record(ip, ok)
	}, nil
}

func (g *loginGuard) record(ip string, ok bool) {
	now := g.now()
	g.mu.Lock()
	if ok {
		delete(g.ips, ip)
		g.mu.Unlock()
		return
	}
	g.prune(now)
	f := g.ips[ip]
	if f == nil {
		if len(g.ips) >= maxTrackedLoginIPs {
			for k, old := range g.ips {
				if now.After(old.until) {
					delete(g.ips, k)
					break
				}
			}
			if len(g.ips) >= maxTrackedLoginIPs {
				g.mu.Unlock()
				return
			}
		}
		f = &loginFails{}
		g.ips[ip] = f
	}
	if f.n == 0 || now.Sub(f.first) > g.Window {
		f.n, f.first = 0, now
	}
	f.n++
	f.last = now
	if f.n < g.MaxFails {
		g.mu.Unlock()
		return
	}
	block := g.Block << f.strikes
	if block > g.MaxBlock || block <= 0 {
		block = g.MaxBlock
	}
	f.strikes++
	fails := f.n
	f.n, f.until = 0, now.Add(block)
	until := f.until
	g.mu.Unlock()
	if g.OnBlock != nil {
		g.OnBlock(ip, fails, until)
	}
}

// prune forgets addresses quiet for a day, throttled so a flood cannot trigger O(N^2) scans.
func (g *loginGuard) prune(now time.Time) {
	if len(g.ips) < 1000 {
		return
	}
	if len(g.ips) < maxTrackedLoginIPs && now.Sub(g.lastPrune) < time.Minute {
		return
	}
	g.lastPrune = now
	for ip, f := range g.ips {
		if now.Sub(f.last) > 24*time.Hour && now.After(f.until) {
			delete(g.ips, ip)
		}
	}
}

// writeLoginError answers a refused attempt; blocked ones get Retry-After.
func writeLoginError(w http.ResponseWriter, r *http.Request, err error) {
	var b *loginBlocked
	if errors.As(err, &b) {
		w.Header().Set("Retry-After", strconv.Itoa(int(b.wait.Seconds())+1))
		writeError(w, r, &httpError{http.StatusTooManyRequests, b.Error()})
		return
	}
	var h *httpError
	if errors.As(err, &h) {
		writeError(w, r, h)
		return
	}
	writeError(w, r, &httpError{http.StatusInternalServerError, "internal error"})
}

// loginFailDelay keeps a failed attempt from answering at once. It runs after the slot is
// given back, so a slow failure never holds up anyone else.
const loginFailDelay = 700 * time.Millisecond
