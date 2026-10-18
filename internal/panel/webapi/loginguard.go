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
	MaxFails int
	Window   time.Duration
	Block    time.Duration
	MaxBlock time.Duration
	// OnBlock is told when an address gets blocked (audit, Telegram).
	OnBlock func(ip string, fails int, until time.Time)

	now func() time.Time
	sem chan struct{}

	mu  sync.Mutex
	ips map[string]*loginFails
}

type loginFails struct {
	n       int       // failures in the current window
	first   time.Time // start of the window
	until   time.Time // refused until
	strikes int       // blocks so far: each one doubles the next
	last    time.Time
}

func newLoginGuard() *loginGuard {
	return &loginGuard{MaxFails: 5, Window: 15 * time.Minute, Block: time.Minute, MaxBlock: time.Hour,
		now: time.Now, sem: make(chan struct{}, 4), ips: map[string]*loginFails{}}
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

// begin admits one sign-in attempt from ip. done must be called with its outcome.
func (g *loginGuard) begin(ip string) (done func(ok bool), err error) {
	g.mu.Lock()
	if f := g.ips[ip]; f != nil && g.now().Before(f.until) {
		wait := f.until.Sub(g.now())
		g.mu.Unlock()
		return nil, &loginBlocked{wait}
	}
	g.mu.Unlock()
	select {
	case g.sem <- struct{}{}:
	default:
		return nil, errLoginBusy
	}
	return func(ok bool) {
		<-g.sem
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

// prune forgets addresses quiet for a day, so the map does not grow without bound.
func (g *loginGuard) prune(now time.Time) {
	if len(g.ips) < 1000 {
		return
	}
	for ip, f := range g.ips {
		if now.Sub(f.last) > 24*time.Hour && now.After(f.until) {
			delete(g.ips, ip)
		}
	}
}

// writeLoginError answers a refused attempt; blocked ones get Retry-After.
func writeLoginError(w http.ResponseWriter, err error) {
	var b *loginBlocked
	if errors.As(err, &b) {
		w.Header().Set("Retry-After", strconv.Itoa(int(b.wait.Seconds())+1))
		writeError(w, &httpError{http.StatusTooManyRequests, b.Error()})
		return
	}
	var h *httpError
	if errors.As(err, &h) {
		writeError(w, h)
		return
	}
	writeError(w, &httpError{http.StatusInternalServerError, "internal error"})
}

// loginFailDelay keeps a failed attempt from answering at once. It runs after the slot is
// given back, so a slow failure never holds up anyone else.
const loginFailDelay = 700 * time.Millisecond
