package store

import (
	"context"
	"slices"
	"testing"
)

// The users table sorts on the server (it comes in pages): "never" goes last going up, a bad key
// falls back to the id.
func TestListUsersSort(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	day := int64(86400)
	exp := func(v int64) *int64 { return &v }
	for _, u := range []*User{
		{Username: "bob", Status: "expired", ExpireAt: exp(5 * day), TrafficUsedBytes: 300},
		{Username: "Alice", Status: "active", TrafficUsedBytes: 100},
		{Username: "carl", Status: "limited", ExpireAt: exp(9 * day), TrafficUsedBytes: 200},
	} {
		u.UUID, u.SubToken, u.ResetStrategy, u.ClientType = u.Username+"-uuid", u.Username+"-token", "no", "auto"
		if err := CreateUser(ctx, st.DB, u); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB.ExecContext(ctx, `UPDATE users SET status=?, traffic_used_bytes=? WHERE id=?`, u.Status, u.TrafficUsedBytes, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	names := func(f UserFilter) []string {
		t.Helper()
		us, err := ListUsers(ctx, st.DB, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, u := range us {
			out = append(out, u.Username)
		}
		return out
	}
	for _, c := range []struct {
		f    UserFilter
		want []string
	}{
		{UserFilter{Sort: "username"}, []string{"Alice", "bob", "carl"}},
		{UserFilter{Sort: "username", Desc: true}, []string{"carl", "bob", "Alice"}},
		{UserFilter{Sort: "status"}, []string{"Alice", "carl", "bob"}},
		{UserFilter{Sort: "traffic", Desc: true}, []string{"bob", "carl", "Alice"}},
		{UserFilter{Sort: "expire"}, []string{"bob", "carl", "Alice"}},
		{UserFilter{Sort: "expire", Desc: true}, []string{"Alice", "carl", "bob"}},
		{UserFilter{Sort: "status; DROP TABLE users"}, []string{"bob", "Alice", "carl"}},
		{UserFilter{Sort: "username", Limit: 1, Offset: 1}, []string{"bob"}},
	} {
		if got := names(c.f); !slices.Equal(got, c.want) {
			t.Fatalf("%+v: %v, want %v", c.f, got, c.want)
		}
	}
}

// 10-second samples fold into 5-minute rows, and a period reads them merged into its buckets.
func TestRollupMetrics(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	n := &Node{Name: "NL", Code: "NL", Country: "nl", Enabled: true}
	if err := CreateNode(ctx, st.DB, n); err != nil {
		t.Fatal(err)
	}
	base := int64(1_800_000_000)     // a multiple of 300
	for i := int64(0); i < 60; i++ { // 10 minutes
		cpu := 10.0
		if i >= 30 {
			cpu = 30
		}
		if err := AddMetrics(ctx, st.DB, Metrics{NodeID: n.ID, TS: base + i*10, CPU: cpu, Steal: 2, MemUsed: 1 << 30, MemTotal: 4 << 30, RxBps: 1000 * i}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 { // again: replaced, not added
		if err := RollupMetrics(ctx, st.DB, base, base+600); err != nil {
			t.Fatal(err)
		}
	}
	five, err := LoadSeries(ctx, st.DB, base, base+600, 300)
	if err != nil || len(five) != 2 || five[0].CPU != 10 || five[1].CPU != 30 || five[1].Steal != 2 || five[0].MemUsed != 1<<30 || five[1].RxMax != 59000 {
		t.Fatalf("5 min: %+v %v", five, err)
	}
	ten, _ := LoadSeries(ctx, st.DB, base, base+600, 600)
	if len(ten) != 1 || ten[0].CPU != 20 || ten[0].CPUMax != 30 {
		t.Fatalf("10 min: %+v", ten)
	}
}
