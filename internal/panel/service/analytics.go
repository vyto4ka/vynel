package service

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// StatsRange is a period of the statistics page.
type StatsRange struct {
	Key    string
	Span   time.Duration
	Bucket int64 // seconds
}

// StatsRanges are the periods the statistics page offers.
var StatsRanges = map[string]StatsRange{
	"24h": {"24h", 24 * time.Hour, 3600},
	"7d":  {"7d", 7 * 24 * time.Hour, 3600},
	"30d": {"30d", 30 * 24 * time.Hour, 86400},
	"90d": {"90d", 90 * 24 * time.Hour, 86400},
}

// Stats is everything the statistics page draws for one period.
type Stats struct {
	Range         string
	Bucket        int64
	From, To      int64 // [From, To), bucket-aligned
	Traffic       []store.TrafficPoint
	Total         int64 // traffic in the period
	PrevTotal     int64 // in the period before, for the delta
	ActiveUsers   int
	PrevActive    int
	PeakOnline    int64
	OnlineNow     int
	Nodes         []NodeSeries
	Online        []ValuePoint // users online over time (sum of nodes)
	TopUsers      []store.UserTotal
	Heatmap       [7][24]int64 // last 7 days: weekday (Mon=0) × hour, in the panel's time zone
	TimeZone      string
	Platforms     []store.Count
	Apps          []store.Count
	UsersBy       map[string]int
	MetricsSince  int64 // node metrics are kept 7 days: earlier buckets have none
	MetricsBucket int64 // bucket of Online, CPU and Mem (hourly over the last week for long periods)
	MetricsFrom   int64
}

// NodeSeries is one node's traffic and load over the period.
type NodeSeries struct {
	ID      int64
	Code    string
	Name    string
	Country string
	Total   int64
	Traffic []ValuePoint
	CPU     []ValuePoint
	Mem     []ValuePoint
}

// ValuePoint is a value in a bucket; Has is false where nothing was recorded.
type ValuePoint struct {
	T   int64
	V   float64
	Has bool
}

// Statistics computes the statistics page for a period key (24h, 7d, 30d, 90d).
func (s *Service) Statistics(ctx context.Context, key string) (*Stats, error) {
	rg, ok := StatsRanges[key]
	if !ok {
		return nil, invalid("unknown period %q (24h, 7d, 30d, 90d)", key)
	}
	q := s.st.DB
	now := s.now().Unix()
	to := now - now%rg.Bucket + rg.Bucket // the current bucket included
	span := int64(rg.Span / time.Second)
	from := to - span
	st := &Stats{Range: key, Bucket: rg.Bucket, From: from, To: to, MetricsSince: now - 7*86400}

	pts, err := store.UserTrafficSeries(ctx, q, from, to, rg.Bucket, 0)
	if err != nil {
		return nil, err
	}
	st.Traffic = fillTraffic(pts, from, to, rg.Bucket)
	for _, p := range st.Traffic {
		st.Total += p.Up + p.Down
	}
	// Hourly rows are kept 7 days: the week before a 7-day view is only in the daily table.
	prevBucket := rg.Bucket
	if span >= 7*86400 {
		prevBucket = 86400
	}
	prev, err := store.UserTrafficSeries(ctx, q, store.Day(from-span), store.Day(from), prevBucket, 0)
	if err != nil {
		return nil, err
	}
	for _, p := range prev {
		st.PrevTotal += p.Up + p.Down
	}
	if st.ActiveUsers, err = store.ActiveUsers(ctx, q, from, to); err != nil {
		return nil, err
	}
	if st.PrevActive, err = store.ActiveUsers(ctx, q, from-span, from); err != nil {
		return nil, err
	}
	if st.OnlineNow, err = store.CountOnlineSince(ctx, q, now-int64(OnlineWindow/time.Second)); err != nil {
		return nil, err
	}

	nodes, err := store.ListNodes(ctx, q)
	if err != nil {
		return nil, err
	}
	st.Nodes = make([]NodeSeries, len(nodes))
	byID := map[int64]*NodeSeries{}
	for i, n := range nodes {
		st.Nodes[i] = NodeSeries{ID: n.ID, Code: n.Code, Name: n.Name, Country: xrayconf.CountryFlag(n.Country)}
		byID[n.ID] = &st.Nodes[i]
	}
	traffic := map[int64]map[int64]float64{}
	np, err := store.NodeTrafficSeries(ctx, q, from, to, rg.Bucket)
	if err != nil {
		return nil, err
	}
	for _, p := range np {
		if traffic[p.NodeID] == nil {
			traffic[p.NodeID] = map[int64]float64{}
		}
		traffic[p.NodeID][p.T] += float64(p.Bytes)
	}
	// Load and online are kept 7 days: longer periods show the last week hourly instead of a
	// mostly empty 90-day axis.
	mb, mfrom, mend := rg.Bucket, from, to
	if span > 7*86400 {
		mb, mend = 3600, now-now%3600+3600
		mfrom = mend - 7*86400
	}
	st.MetricsBucket, st.MetricsFrom = mb, mfrom
	mp, err := store.MetricsSeries(ctx, q, mfrom, mend, mb)
	if err != nil {
		return nil, err
	}
	cpu, mem := map[int64]map[int64]float64{}, map[int64]map[int64]float64{}
	online := map[int64]float64{}
	onlineHas := map[int64]bool{}
	for _, p := range mp {
		if cpu[p.NodeID] == nil {
			cpu[p.NodeID], mem[p.NodeID] = map[int64]float64{}, map[int64]float64{}
		}
		cpu[p.NodeID][p.T], mem[p.NodeID][p.T] = p.CPU, p.Mem
		online[p.T] += float64(p.Online)
		onlineHas[p.T] = true
		if p.Online > st.PeakOnline {
			st.PeakOnline = p.Online
		}
	}
	for id, ns := range byID {
		ns.Traffic = fill(traffic[id], nil, from, to, rg.Bucket, true)
		for _, p := range ns.Traffic {
			ns.Total += int64(p.V)
		}
		ns.CPU = fill(cpu[id], cpu[id], mfrom, mend, mb, false)
		ns.Mem = fill(mem[id], mem[id], mfrom, mend, mb, false)
	}
	sort.SliceStable(st.Nodes, func(i, j int) bool { return st.Nodes[i].Total > st.Nodes[j].Total })
	st.Online = fill(online, onlineMarks(onlineHas), mfrom, mend, mb, false)
	for _, p := range st.Online {
		if int64(p.V) > st.PeakOnline {
			st.PeakOnline = int64(p.V)
		}
	}

	if st.TopUsers, err = store.TopUsersBetween(ctx, q, from, to, 10); err != nil {
		return nil, err
	}
	loc := s.BotLocation(ctx)
	st.TimeZone = loc.String()
	hourly, err := store.UserTrafficSeries(ctx, q, now-now%3600-7*86400+3600, now-now%3600+3600, 3600, 0)
	if err != nil {
		return nil, err
	}
	for _, p := range hourly {
		t := time.Unix(p.T, 0).In(loc)
		wd := (int(t.Weekday()) + 6) % 7
		st.Heatmap[wd][t.Hour()] += p.Up + p.Down
	}
	if st.Platforms, err = store.DevicePlatforms(ctx, q); err != nil {
		return nil, err
	}
	for i := range st.Platforms {
		st.Platforms[i].Label = platformName(st.Platforms[i].Label)
	}
	st.Platforms = mergeCounts(st.Platforms)
	agents, err := store.SubscriptionAgents(ctx, q)
	if err != nil {
		return nil, err
	}
	for i := range agents {
		agents[i].Label = AppName(agents[i].Label)
	}
	st.Apps = mergeCounts(agents)
	if st.UsersBy, err = store.CountUsersByStatus(ctx, q); err != nil {
		return nil, err
	}
	return st, nil
}

func onlineMarks(has map[int64]bool) map[int64]float64 {
	m := map[int64]float64{}
	for t := range has {
		m[t] = 1
	}
	return m
}

// fill lays values onto every bucket of [from, to). present marks buckets with data; with
// zeroIsData every bucket counts as recorded (traffic: no row means zero).
func fill(vals, present map[int64]float64, from, to, bucket int64, zeroIsData bool) []ValuePoint {
	out := make([]ValuePoint, 0, (to-from)/bucket)
	for t := from; t < to; t += bucket {
		_, has := present[t]
		out = append(out, ValuePoint{T: t, V: vals[t], Has: has || zeroIsData})
	}
	return out
}

func fillTraffic(pts []store.TrafficPoint, from, to, bucket int64) []store.TrafficPoint {
	m := map[int64]store.TrafficPoint{}
	for _, p := range pts {
		m[p.T] = p
	}
	out := make([]store.TrafficPoint, 0, (to-from)/bucket)
	for t := from; t < to; t += bucket {
		p := m[t]
		p.T = t
		out = append(out, p)
	}
	return out
}

func mergeCounts(cs []store.Count) []store.Count {
	m := map[string]int{}
	for _, c := range cs {
		m[c.Label] += c.N
	}
	out := make([]store.Count, 0, len(m))
	for l, n := range m {
		out = append(out, store.Count{Label: l, N: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func platformName(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "":
		return "не указана"
	case "android":
		return "Android"
	case "ios", "iphone", "ipados":
		return "iOS"
	case "macos", "darwin", "mac":
		return "macOS"
	case "windows", "win":
		return "Windows"
	case "linux":
		return "Linux"
	}
	return p
}

var appRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9 ._-]*?)[/ ]`)

// AppName extracts the app from a subscription User-Agent ("Happ/3.1.0 ..." → "Happ").
func AppName(ua string) string {
	for _, k := range knownApps {
		if k.re.MatchString(ua) {
			return k.name
		}
	}
	if m := appRe.FindStringSubmatch(ua + " "); m != nil {
		return m[1]
	}
	return "другое"
}

var knownApps = func() []struct {
	re   *regexp.Regexp
	name string
} {
	list := []struct{ re, name string }{
		{`(?i)keqdroid|keqdis`, "KeqDroid"}, {`(?i)happ`, "Happ"}, {`(?i)v2raytun`, "v2RayTun"}, {`(?i)v2rayng`, "v2rayNG"},
		{`(?i)v2rayn`, "v2rayN"}, {`(?i)karing`, "Karing"}, {`(?i)hiddify`, "Hiddify"}, {`(?i)streisand`, "Streisand"},
		{`(?i)flclash`, "FlClash"}, {`(?i)clash-?verge`, "Clash Verge"}, {`(?i)mihomo|clash`, "Clash / Mihomo"},
		{`(?i)sing-?box|\bSF[AIMT]\b`, "sing-box"}, {`(?i)shadowrocket`, "Shadowrocket"}, {`(?i)incy`, "INCY"},
		{`(?i)nekobox|nekoray`, "NekoBox"}, {`(?i)mozilla`, "браузер"},
	}
	out := make([]struct {
		re   *regexp.Regexp
		name string
	}, len(list))
	for i, k := range list {
		out[i].re, out[i].name = regexp.MustCompile(k.re), k.name
	}
	return out
}()
