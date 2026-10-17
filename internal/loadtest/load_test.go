//go:build loadtest

// Package loadtest measures the panel under many users (go test -tags loadtest -run . -v
// ./internal/loadtest/; LOAD_USERS=1000,10000,50000 picks the sizes). It is not part of CI.
package loadtest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/reconciler"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/panel/subscription"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

var actor = service.Actor{Kind: "load"}

func sizes() []int {
	v := os.Getenv("LOAD_USERS")
	if v == "" {
		v = "1000,10000,50000"
	}
	var out []int
	for _, s := range strings.Split(v, ",") {
		n, _ := strconv.Atoi(strings.TrimSpace(s))
		out = append(out, n)
	}
	return out
}

type world struct {
	svc    *service.Service
	tokens []string
	ids    []int64
}

// seed builds 3 nodes with Reality and Hysteria2 (6 inbounds) and n users in the main group.
func seed(t *testing.T, n int) *world {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := service.New(st)
	must(t, svc.EnsureDefaults(ctx))
	must(t, svc.SetSetting(ctx, actor, service.SettingSubDomain, "sub.example.com"))
	main, err := svc.GroupByName(ctx, "Основная")
	must(t, err)
	var profs []*store.Profile
	for _, p := range []service.ProfileInput{{Name: "Reality", TemplateID: "vless-reality-selfsteal"}, {Name: "HY2", TemplateID: "hysteria2"}} {
		prof, err := svc.CreateProfile(ctx, actor, p)
		must(t, err)
		must(t, svc.GrantAccess(ctx, actor, main.ID, store.AccessProfile, prof.ID))
		profs = append(profs, prof)
	}
	for _, c := range []string{"nl", "de", "fi"} {
		node, _, err := svc.CreateNode(ctx, actor, service.NodeInput{Name: strings.ToUpper(c), Country: c, Domain: c + ".example.com"})
		must(t, err)
		for _, p := range profs {
			_, err := svc.AttachProfile(ctx, actor, service.AttachInput{NodeID: node.ID, ProfileID: p.ID})
			must(t, err)
		}
	}
	w := &world{svc: svc}
	start := time.Now()
	must(t, st.Tx(ctx, func(q store.DBTX) error {
		for i := 0; i < n; i++ {
			uuid, _ := xrayconf.NewUUID()
			u := &store.User{Username: "u" + strconv.Itoa(i), UUID: uuid, SubToken: "tok" + strconv.Itoa(i) + "-" + uuid[:8], Status: store.StatusActive,
				ResetStrategy: "no", ClientType: "auto", CreatedBy: "load"}
			if err := store.CreateUser(ctx, q, u); err != nil {
				return err
			}
			if err := store.SetUserGroups(ctx, q, u.ID, []int64{main.ID}); err != nil {
				return err
			}
			w.tokens = append(w.tokens, u.SubToken)
			w.ids = append(w.ids, u.ID)
		}
		return nil
	}))
	t.Logf("seeded %d users in %v", n, time.Since(start).Round(time.Millisecond))
	return w
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func heapMB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1 << 20)
}

func timeIt(f func()) time.Duration {
	s := time.Now()
	f()
	return time.Since(s)
}

type result struct {
	n             int
	rps           float64
	p50, p99, max time.Duration
	errors        int64
}

// hammer runs fn from workers goroutines for d and reports throughput and latency.
func hammer(workers int, d time.Duration, fn func(r *rand.Rand) error) result {
	var mu sync.Mutex
	var lat []time.Duration
	var errs atomic.Int64
	deadline := time.Now().Add(d)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			r := rand.New(rand.NewPCG(seed, seed))
			var local []time.Duration
			for time.Now().Before(deadline) {
				s := time.Now()
				if err := fn(r); err != nil {
					errs.Add(1)
				}
				local = append(local, time.Since(s))
			}
			mu.Lock()
			lat = append(lat, local...)
			mu.Unlock()
		}(uint64(w + 1))
	}
	wg.Wait()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	res := result{n: len(lat), errors: errs.Load(), rps: float64(len(lat)) / d.Seconds()}
	if len(lat) > 0 {
		res.p50, res.p99, res.max = lat[len(lat)/2], lat[len(lat)*99/100], lat[len(lat)-1]
	}
	return res
}

func (r result) String() string {
	return fmt.Sprintf("%6.0f req/s  p50 %-8v p99 %-8v max %-8v errors %d", r.rps, r.p50.Round(10*time.Microsecond), r.p99.Round(10*time.Microsecond), r.max.Round(time.Millisecond), r.errors)
}

func TestLoad(t *testing.T) {
	ctx := context.Background()
	for _, n := range sizes() {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			w := seed(t, n)
			svc := w.svc
			runtime.GC()
			before := heapMB()

			// 1. The desired state of every node: computed after every change.
			var states map[int64]*service.DesiredState
			d := timeIt(func() {
				var err error
				states, err = svc.DesiredStates(ctx)
				must(t, err)
			})
			t.Logf("DesiredStates: %v, heap +%.0f MB", d.Round(time.Millisecond), heapMB()-before)
			var ds *service.DesiredState
			for _, s := range states {
				ds = s
				break
			}
			parts := reconciler.SnapshotParts(ds, 1, reconciler.PartBytes)
			t.Logf("snapshot of one node: %d parts, %.1f MB config+users", len(parts), float64(sizeOf(parts))/(1<<20))

			// 2. One user changes: the delta.
			d = timeIt(func() {
				_, err := svc.SetUserEnabled(ctx, actor, w.ids[0], false)
				must(t, err)
			})
			states2, _ := svc.DesiredStates(ctx)
			delta := reconciler.Delta(ds, states2[ds.NodeID], 2)
			t.Logf("disable one user: write %v, delta ops %d", d.Round(time.Millisecond), len(delta.Ops))

			// 3. Subscriptions: apps fetching at random, as Happ/v2rayNG (links) and KeqDroid (Xray JSON).
			h := subscription.NewHandler(svc, nil)
			get := func(ua string) func(r *rand.Rand) error {
				return func(r *rand.Rand) error {
					i := r.IntN(len(w.tokens))
					req := httptest.NewRequest(http.MethodGet, "/s/"+w.tokens[i], nil)
					req.Header.Set("User-Agent", ua)
					req.Header.Set("x-hwid", "hw-"+strconv.Itoa(i))
					req.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:1000", i>>16&255, i>>8&255, i&255)
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					if rec.Code != 200 || rec.Body.Len() < 100 {
						return fmt.Errorf("status %d", rec.Code)
					}
					return nil
				}
			}
			t.Logf("subscription v2rayNG x32: %v", hammer(32, 5*time.Second, get("v2rayNG/1.9")))
			t.Logf("subscription KeqDroid x32: %v", hammer(32, 5*time.Second, get("keqdroid/0.25")))

			// 4. Admin changes while apps fetch: how long a change waits.
			stop := make(chan struct{})
			var bg sync.WaitGroup
			for i := 0; i < 16; i++ {
				bg.Add(1)
				go func(seed uint64) {
					defer bg.Done()
					r := rand.New(rand.NewPCG(seed, 1))
					f := get("v2rayNG/1.9")
					for {
						select {
						case <-stop:
							return
						default:
							_ = f(r)
						}
					}
				}(uint64(i))
			}
			t.Logf("extend user under subscription load x4: %v", hammer(4, 5*time.Second, func(r *rand.Rand) error {
				_, err := svc.ExtendUser(ctx, actor, w.ids[r.IntN(len(w.ids))], 0, 1)
				return err
			}))
			close(stop)
			bg.Wait()

			// 5. A node reports traffic of every user (one stats batch).
			b := &nodev1.StatsBatch{Epoch: "e", Seq: 1, Ts: time.Now().Unix()}
			for _, id := range w.ids {
				b.Users = append(b.Users, &nodev1.UserTraffic{Email: service.UserEmail(id), Up: 1000, Down: 100000})
				if len(b.Online) < n/2 {
					b.Online = append(b.Online, &nodev1.OnlineUser{Email: service.UserEmail(id), Ips: 1})
				}
			}
			d = timeIt(func() { must(t, svc.IngestStats(ctx, ds.NodeID, b)) })
			t.Logf("stats batch, %d users: %v", n, d.Round(time.Millisecond))

			// 6. Online addresses of half the users on 3 nodes, every 5 s.
			tr := svc.IPs()
			online := map[string][]string{}
			for i, id := range w.ids[:n/2] {
				online[service.UserEmail(id)] = []string{fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255)}
			}
			d = timeIt(func() {
				for node := int64(1); node <= 3; node++ {
					tr.Report(node, online)
				}
			})
			t.Logf("IP tracker, %d online on 3 nodes: %v per round", n/2, d.Round(time.Millisecond))

			// 7. The web panel: the users list as it was (all users, groups one by one) and as a page.
			d = timeIt(func() {
				us, err := svc.Users(ctx, store.UserFilter{})
				must(t, err)
				for _, u := range us {
					_, err := svc.UserGroupIDs(ctx, u.ID)
					must(t, err)
				}
			})
			t.Logf("users list, old way (all + groups each): %v", d.Round(time.Millisecond))
			d = timeIt(func() {
				us, err := svc.Users(ctx, store.UserFilter{Limit: 200})
				must(t, err)
				_, err = svc.UsersGroups(ctx, us)
				must(t, err)
				_, err = svc.CountUsers(ctx, store.UserFilter{})
				must(t, err)
			})
			t.Logf("users list, a page of 200: %v", d.Round(time.Millisecond))
			d = timeIt(func() {
				_, err := svc.Overview(ctx)
				must(t, err)
			})
			t.Logf("dashboard: %v", d.Round(time.Millisecond))
			for _, k := range []string{"24h", "7d", "90d"} {
				d = timeIt(func() {
					_, err := svc.Statistics(ctx, k)
					must(t, err)
				})
				t.Logf("statistics %s: %v", k, d.Round(time.Millisecond))
			}
			t.Logf("heap now %.0f MB", heapMB())
		})
	}
}

func sizeOf(parts []*nodev1.Snapshot) int {
	n := 0
	for _, p := range parts {
		n += len(p.XrayConfig)
		for _, in := range p.Inbounds {
			n += len(in.Users) * 60
		}
	}
	return n
}

// TestSubProfile hammers subscriptions only (go test -cpuprofile).
func TestSubProfile(t *testing.T) {
	w := seed(t, 2000)
	h := subscription.NewHandler(w.svc, nil)
	t.Logf("%v", hammer(32, 5*time.Second, func(r *rand.Rand) error {
		i := r.IntN(len(w.tokens))
		req := httptest.NewRequest(http.MethodGet, "/s/"+w.tokens[i], nil)
		req.Header.Set("User-Agent", "v2rayNG/1.9")
		req.Header.Set("x-hwid", "hw-"+strconv.Itoa(i))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return nil
	}))
}

// TestDesiredProfile computes the node states repeatedly (go test -cpuprofile).
func TestDesiredProfile(t *testing.T) {
	w := seed(t, 50000)
	start := time.Now()
	for i := 0; i < 5; i++ {
		if _, err := w.svc.DesiredStates(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("DesiredStates x5: %v", time.Since(start).Round(time.Millisecond))
}
