//go:build loadtest

package agent

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/node/state"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

func loadUUID(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }

// vlessDial opens a plain VLESS connection as user i from a local address and leaves it open.
func vlessDial(from string, port, i int, target *net.TCPAddr) (net.Conn, error) {
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(from)}, Timeout: 5 * time.Second}
	c, err := d.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, err
	}
	id, _ := hex.DecodeString(strings.ReplaceAll(loadUUID(i), "-", ""))
	h := []byte{0}
	h = append(h, id...)
	h = append(h, 0, 1)
	h = binary.BigEndian.AppendUint16(h, uint16(target.Port))
	h = append(h, 1)
	h = append(h, target.IP.To4()...)
	h = append(h, "GET / HTTP/1.1\r\nHost: x\r\n\r\n"...)
	if _, err := c.Write(h); err != nil {
		c.Close()
		return nil, err
	}
	buf := make([]byte, 64)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(buf); err != nil {
		c.Close()
		return nil, err
	}
	_ = c.SetReadDeadline(time.Time{})
	return c, nil
}

// selfRSS is this process's resident memory (the agent; the test's client sockets are small).
func selfRSS() string {
	st, _ := os.ReadFile("/proc/self/status")
	for _, l := range strings.Split(string(st), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			return strings.TrimSpace(strings.TrimPrefix(l, "VmRSS:"))
		}
	}
	return "?"
}

func xrayRSS(t *testing.T, a *Agent) string {
	t.Helper()
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		cmd, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil || !strings.Contains(string(cmd), a.cfg.DataDir) {
			continue
		}
		st, _ := os.ReadFile("/proc/" + e.Name() + "/status")
		for _, l := range strings.Split(string(st), "\n") {
			if strings.HasPrefix(l, "VmRSS:") {
				return strings.TrimSpace(strings.TrimPrefix(l, "VmRSS:"))
			}
		}
	}
	return "?"
}

// TestNodeLoad: a node with many users and many of them online (XRAY_BIN; LOAD_USERS,
// LOAD_ONLINE).
func TestNodeLoad(t *testing.T) {
	bin := xraytest.Binary(t)
	ctx := context.Background()
	users, _ := strconv.Atoi(os.Getenv("LOAD_USERS"))
	if users == 0 {
		users = 50000
	}
	onlineN, _ := strconv.Atoi(os.Getenv("LOAD_ONLINE"))
	if onlineN == 0 {
		onlineN = 2000
	}
	// The site users reach runs in its own process: it keeps a connection per online user, and
	// in this process it would count as the agent's memory.
	ou := originProcess(t)
	port, apiPort := xraytest.FreePort(t), xraytest.FreePort(t)
	base, _ := xrayconf.ParseBase(`{"outbounds":[{"tag":"DIRECT","protocol":"freedom"}]}`)
	cfg, err := xrayconf.BuildConfig(xrayconf.NodeConfig{Base: base, APIAddr: "127.0.0.1:" + strconv.Itoa(apiPort)})
	if err != nil {
		t.Fatal(err)
	}
	cfg["inbounds"] = []any{map[string]any{"tag": "IN", "listen": "0.0.0.0", "port": port, "protocol": "vless",
		"settings": map[string]any{"clients": []any{}, "decryption": "none"}, "streamSettings": map[string]any{"network": "raw"}}}
	raw, _ := json.Marshal(cfg)
	a, err := New(Config{DataDir: t.TempDir(), Xray: bin})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	st := &state.State{Config: raw, Hash: "h0", Inbounds: []state.Inbound{{Tag: "IN", Protocol: "vless"}}}
	for i := 0; i < users; i++ {
		st.Inbounds[0].Users = append(st.Inbounds[0].Users, state.User{Email: strconv.Itoa(i + 1), ID: loadUUID(i)})
	}
	start := time.Now()
	a.mu.Lock()
	err = a.restartLocked(ctx, st)
	if err == nil {
		err = a.commitLocked(st)
	}
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	t.Logf("start Xray with %d users: %v, Xray RSS %s, agent RSS %s", users, time.Since(start).Round(time.Millisecond), xrayRSS(t, a), selfRSS())

	// Users added and removed through the API (a delta), e.g. a group given a new profile.
	const delta = 5000
	d := &nodev1.Delta{Revision: 2, BaseHash: "h0", Hash: "h1"}
	for i := users; i < users+delta; i++ {
		d.Ops = append(d.Ops, &nodev1.UserOp{Kind: nodev1.UserOp_KIND_UPSERT, Tag: "IN", User: &nodev1.User{Email: strconv.Itoa(i + 1), Id: loadUUID(i)}})
	}
	start = time.Now()
	if err := a.ApplyDelta(ctx, d); err != nil {
		t.Fatal(err)
	}
	t.Logf("delta of %d new users: %v", delta, time.Since(start).Round(time.Millisecond))

	// Many users online, each from its own address.
	var conns []net.Conn
	start = time.Now()
	for i := 0; i < onlineN; i++ {
		from := fmt.Sprintf("127.%d.%d.%d", 10+i/65000, i/250%250+1, i%250+1)
		c, err := vlessDial(from, port, i, ou)
		if err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	time.Sleep(2 * time.Second)
	t.Logf("%d users online in %v, Xray RSS %s", onlineN, time.Since(start).Round(time.Millisecond), xrayRSS(t, a))
	var msg *nodev1.OnlineIPs
	for try := 0; try < 3; try++ {
		start = time.Now()
		msg = a.onlineIPs(ctx)
		t.Logf("online addresses read: %d users in %v", len(msg.GetUsers()), time.Since(start).Round(time.Millisecond))
	}
	t.Logf("agent RSS with %d online (after reading them): %s", onlineN, selfRSS())
	start = time.Now()
	a.mu.Lock()
	a.collectLocked(ctx)
	a.mu.Unlock()
	t.Logf("stats collection with %d online: %v", onlineN, time.Since(start).Round(time.Millisecond))

	// Blocks for some of the online users.
	for _, n := range []int{10, 50, 100, 200, onlineN / 10} {
		var blocks []*nodev1.IPBlock
		for i := 0; i < n; i++ {
			blocks = append(blocks, &nodev1.IPBlock{UserId: int64(i + 1), Ips: []string{fmt.Sprintf("127.%d.%d.%d", 10+i/65000, i/250%250+1, i%250+1)}})
		}
		start = time.Now()
		a.SetIPBlocks(ctx, blocks)
		t.Logf("%d address blocks applied in %v", len(blocks), time.Since(start).Round(time.Millisecond))
	}
	a.mu.Lock()
	start = time.Now()
	err = a.cfg.Xray.SetRules(ctx, a.apiAddr, map[string]any{"rules": blockRules(a.ipBlocks, nil)})
	t.Logf("adrules alone, %d rules: %v %v", len(a.ipBlocks), time.Since(start).Round(time.Millisecond), err)
	start = time.Now()
	a.dropConnections(ctx, []string{"127.99.99.99"})
	t.Logf("one ss -K: %v", time.Since(start).Round(time.Millisecond))
	a.mu.Unlock()
	var busers, srcs []string
	for _, b := range a.ipBlocks {
		busers = append(busers, strconv.FormatInt(b.UserId, 10))
		srcs = append(srcs, b.Ips...)
	}
	for _, rs := range [][]any{
		{map[string]any{"ruleTag": "one", "user": busers, "source": srcs, "outboundTag": xrayconf.IPBlockTag}},
		blockRules(a.ipBlocks, nil),
	} {
		start = time.Now()
		err = a.cfg.Xray.SetRules(ctx, a.apiAddr, map[string]any{"rules": rs})
		t.Logf("adrules, %d rules for %d addresses: %v %v", len(rs), len(srcs), time.Since(start).Round(time.Millisecond), err)
	}
	if p := os.Getenv("LOAD_HOLD"); p != "" {
		_ = os.WriteFile(p, []byte(a.apiAddr), 0o644)
		time.Sleep(90 * time.Second)
	}
	_ = syscall.Getpid
}

// originProcess runs the target site in a child process (this test binary again) and returns
// its address.
func originProcess(t *testing.T) *net.TCPAddr {
	t.Helper()
	port := xraytest.FreePort(t)
	cmd := exec.Command(os.Args[0], "-test.run", "^TestOriginChild$")
	cmd.Env = append(os.Environ(), "LOAD_ORIGIN_PORT="+strconv.Itoa(port))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", addr.String()); err == nil {
			c.Close()
			return addr
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("origin did not start")
	return nil
}

// TestOriginChild is the target site of originProcess (runs only in the child).
func TestOriginChild(t *testing.T) {
	port := os.Getenv("LOAD_ORIGIN_PORT")
	if port == "" {
		t.Skip("child only")
	}
	_ = http.ListenAndServe("127.0.0.1:"+port, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
}
