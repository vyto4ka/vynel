package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// Infrastructure billing (docs/USER_GUIDE.md §9.1): the providers the nodes are rented from,
// what each node costs, until when it is paid (the bot reminds 5 days before), and a monthly
// traffic limit after which the node is switched off until the next period.

// PayReminder is how long before the paid-until date the bot reminds.
const PayReminder = 5 * 24 * time.Hour

// LimitMargin is how far below its traffic limit a node is switched off ("a couple of
// gigabytes"); a small limit keeps a tenth of itself instead.
const LimitMargin = 2 << 30

// limitMargin is the margin for a limit.
func limitMargin(limit int64) int64 { return min(LimitMargin, limit/10) }

// ---- providers ----

// ProviderInput creates or edits a provider. An empty name or icon is taken from the site.
type ProviderInput struct {
	Name     string
	URL      string
	LoginURL string
}

func cleanURL(raw, field string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", invalid("%s: нужна ссылка вида https://example.com", field)
	}
	return u.String(), nil
}

// CreateProvider adds a provider, filling the name and the icon from its site.
func (s *Service) CreateProvider(ctx context.Context, actor Actor, in ProviderInput) (*store.Provider, error) {
	p := &store.Provider{}
	if err := s.fillProvider(ctx, p, in, true); err != nil {
		return nil, err
	}
	err := s.mutate(ctx, change{actor: actor, action: "provider.create", entity: "provider", entityID: func() int64 { return p.ID }, diff: in},
		func(q store.DBTX) error { return store.CreateProvider(ctx, q, p) })
	return p, err
}

// UpdateProvider edits a provider; refresh fetches the name (when empty) and the icon again.
func (s *Service) UpdateProvider(ctx context.Context, actor Actor, id int64, in ProviderInput, refresh bool) (*store.Provider, error) {
	p, err := store.GetProvider(ctx, s.st.DB, id)
	if err != nil {
		return nil, err
	}
	if err := s.fillProvider(ctx, p, in, refresh || p.Icon == ""); err != nil {
		return nil, err
	}
	err = s.mutate(ctx, change{actor: actor, action: "provider.update", entity: "provider", entityID: func() int64 { return id }, diff: in},
		func(q store.DBTX) error { return store.UpdateProvider(ctx, q, p) })
	return p, err
}

func (s *Service) fillProvider(ctx context.Context, p *store.Provider, in ProviderInput, fetch bool) error {
	site, err := cleanURL(in.URL, "Сайт")
	if err != nil {
		return err
	}
	login, err := cleanURL(in.LoginURL, "Вход")
	if err != nil {
		return err
	}
	if site == "" && login == "" && strings.TrimSpace(in.Name) == "" {
		return invalid("укажите сайт провайдера или его название")
	}
	if site != p.URL {
		p.Icon = ""
	}
	p.URL, p.LoginURL, p.Name = site, login, strings.TrimSpace(in.Name)
	if fetch && (p.URL != "" || p.LoginURL != "") {
		from := p.URL
		if from == "" {
			from = p.LoginURL
		}
		fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		info := FetchSiteInfo(fctx, from)
		cancel()
		if p.Name == "" {
			p.Name = info.Name
		}
		if info.Icon != "" {
			p.Icon = info.Icon
		}
	}
	if p.Name == "" {
		p.Name = hostName(p.URL + p.LoginURL)
	}
	return nil
}

// DeleteProvider removes a provider; its nodes keep their costs.
func (s *Service) DeleteProvider(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "provider.delete", entity: "provider", entityID: func() int64 { return id }},
		func(q store.DBTX) error { return store.DeleteProvider(ctx, q, id) })
}

// Providers lists the providers.
func (s *Service) Providers(ctx context.Context) ([]*store.Provider, error) {
	return store.ListProviders(ctx, s.st.DB)
}

// SiteInfo is what a site says about itself: its name and its icon as a data: URI.
type SiteInfo struct {
	Name string
	Icon string
}

// FetchPrivate lets FetchSiteInfo reach private addresses (tests); the panel does not look
// into the networks of its own server.
var FetchPrivate = false

// maxIcon caps an icon stored with a provider.
const maxIcon = 96 << 10

// FetchSiteInfo reads a site's name (og:site_name, application-name or the title) and its
// icon. Anything missing stays empty: the site may be down or block robots.
func FetchSiteInfo(ctx context.Context, site string) SiteInfo {
	hc := siteClient()
	var info SiteInfo
	page, base, _ := fetch(ctx, hc, site, 512<<10)
	var icons []string
	if page != nil {
		info.Name, icons = readHead(page, hostName(site))
	}
	if base == nil {
		if base, _ = url.Parse(site); base == nil {
			return info
		}
	}
	icons = append(icons, "/favicon.ico")
	for _, href := range icons {
		ref, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			continue
		}
		u := base.ResolveReference(ref)
		if u.Scheme == "data" {
			if strings.HasPrefix(href, "data:image/") && len(href) <= maxIcon {
				info.Icon = href
				break
			}
			continue
		}
		body, _, ctype := fetchType(ctx, hc, u.String(), maxIcon)
		if len(body) == 0 {
			continue
		}
		if mt := iconType(ctype, body); mt != "" {
			info.Icon = "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(body)
			break
		}
	}
	return info
}

// siteClient fetches public sites only: no private, loopback or link-local addresses.
func siteClient() *http.Client {
	d := &net.Dialer{Timeout: 6 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, _ := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if !FetchPrivate && (ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
			ip.IsLinkLocalMulticast() || ip.IsMulticast()) {
			return fmt.Errorf("address %s is not public", host)
		}
		return nil
	}}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 6 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	}}
}

func fetch(ctx context.Context, hc *http.Client, u string, limit int64) ([]byte, *url.URL, error) {
	body, final, _ := fetchType(ctx, hc, u, limit)
	if body == nil {
		return nil, nil, errors.New("nothing fetched")
	}
	return body, final, nil
}

func fetchType(ctx context.Context, hc *http.Client, u string, limit int64) (body []byte, final *url.URL, ctype string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130 Safari/537.36")
	req.Header.Set("Accept-Language", "ru,en;q=0.8")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, nil, ""
	}
	return b, resp.Request.URL, resp.Header.Get("Content-Type")
}

// readHead takes the site's name and its icon links (the larger first) from a page's head;
// host helps to find the brand in the title.
func readHead(page []byte, host string) (name string, icons []string) {
	var title, ogName, appName string
	var big, small []string
	z := html.NewTokenizer(bytes.NewReader(page))
	inTitle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			name = firstOf(ogName, appName, cleanTitle(title, host))
			return name, append(big, small...)
		case html.StartTagToken, html.SelfClosingTagToken:
			tn, hasAttr := z.TagName()
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				attrs[string(k)] = string(v)
			}
			switch string(tn) {
			case "title":
				inTitle = title == ""
			case "meta":
				switch strings.ToLower(attrs["property"] + attrs["name"]) {
				case "og:site_name":
					ogName = strings.TrimSpace(attrs["content"])
				case "application-name":
					appName = strings.TrimSpace(attrs["content"])
				}
			case "link":
				rel := " " + strings.ToLower(attrs["rel"]) + " "
				switch {
				case attrs["href"] == "":
				case strings.Contains(rel, " apple-touch-icon"):
					big = append(big, attrs["href"])
				case strings.Contains(rel, " icon "):
					small = append(small, attrs["href"])
				}
			case "body":
				name = firstOf(ogName, appName, cleanTitle(title, host))
				return name, append(big, small...)
			}
		case html.TextToken:
			if inTitle {
				title += string(z.Text())
			}
		case html.EndTagToken:
			if tn, _ := z.TagName(); string(tn) == "title" {
				inTitle = false
			}
		}
	}
}

// cleanTitle keeps the brand of a page title ("Аренда VPS | Nimbus", "Nimbus — Аренда VPS" → "Nimbus"):
// the part that is the site's domain, else the shortest one.
func cleanTitle(t, host string) string {
	t = strings.Join(strings.Fields(html.UnescapeString(t)), " ")
	label := strings.ToLower(strings.Split(host, ".")[0])
	for _, sep := range []string{" | ", " — ", " – ", " - ", " · ", " :: ", ": "} {
		parts := strings.Split(t, sep)
		if len(parts) < 2 {
			continue
		}
		best := ""
		for _, p := range parts {
			p = strings.TrimSpace(p)
			squashed := strings.ToLower(strings.ReplaceAll(p, " ", ""))
			if label != "" && (squashed == label || strings.HasPrefix(squashed, label) && len(strings.Fields(p)) <= 2) {
				return p
			}
			if p != "" && (best == "" || len(strings.Fields(p)) < len(strings.Fields(best))) {
				best = p
			}
		}
		return best
	}
	if len([]rune(t)) > 60 {
		t = string([]rune(t)[:60])
	}
	return t
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// iconType is an image's media type, from its bytes rather than a server's word.
func iconType(ctype string, b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG")):
		return "image/png"
	case bytes.HasPrefix(b, []byte{0, 0, 1, 0}):
		return "image/x-icon"
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("GIF8")):
		return "image/gif"
	case len(b) > 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	case strings.Contains(ctype, "svg") || bytes.Contains(b[:min(len(b), 512)], []byte("<svg")):
		return "image/svg+xml"
	}
	return ""
}

func hostName(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return u
	}
	return strings.TrimPrefix(p.Hostname(), "www.")
}

// ---- node billing ----

// NodeBillingInput changes a node's billing; nil fields stay as they are.
type NodeBillingInput struct {
	ProviderID   *int64 // 0 = none
	Cost         *float64
	Currency     *string
	CostPeriod   *string
	PaidUntil    *int64 // 0 = not tracked
	TrafficLimit *int64 // bytes, 0 = none
	ResetDay     *int
	TrafficCount *string
}

// SetNodeBilling changes a node's provider, cost, paid-until date or traffic limit.
func (s *Service) SetNodeBilling(ctx context.Context, actor Actor, nodeID int64, in NodeBillingInput) (*store.NodeBilling, error) {
	var b *store.NodeBilling
	reenable := false
	loc := s.BotLocation(ctx)
	err := s.mutate(ctx, change{actor: actor, action: "node.billing", entity: "node", entityID: func() int64 { return nodeID }, event: EvNodeChanged, diff: in},
		func(q store.DBTX) error {
			n, err := store.GetNode(ctx, q, nodeID)
			if err != nil {
				return err
			}
			if b, err = store.GetNodeBilling(ctx, q, nodeID); err != nil {
				return err
			}
			if in.ProviderID != nil {
				if *in.ProviderID == 0 {
					b.ProviderID = nil
				} else {
					if _, err := store.GetProvider(ctx, q, *in.ProviderID); err != nil {
						return invalid("провайдер #%d не найден", *in.ProviderID)
					}
					id := *in.ProviderID
					b.ProviderID = &id
				}
			}
			if in.Cost != nil {
				if *in.Cost < 0 || math.IsNaN(*in.Cost) || *in.Cost > 1e9 {
					return InField("Стоимость", invalid("нужно число от 0"))
				}
				b.Cost = math.Round(*in.Cost*100) / 100
			}
			if in.Currency != nil {
				c := strings.TrimSpace(*in.Currency)
				if c == "" || len([]rune(c)) > 8 {
					return InField("Валюта", invalid("от 1 до 8 символов"))
				}
				b.Currency = c
			}
			if in.CostPeriod != nil {
				if *in.CostPeriod != "month" && *in.CostPeriod != "year" {
					return InField("Период оплаты", invalid("month или year"))
				}
				b.CostPeriod = *in.CostPeriod
			}
			if in.PaidUntil != nil {
				if *in.PaidUntil <= 0 {
					b.PaidUntil = nil
				} else if b.PaidUntil == nil || *b.PaidUntil != *in.PaidUntil {
					v := *in.PaidUntil
					b.PaidUntil, b.RemindedFor = &v, 0
				}
			}
			if in.TrafficLimit != nil {
				if *in.TrafficLimit < 0 {
					return InField("Лимит трафика", invalid("нужно число от 0"))
				}
				b.TrafficLimit = *in.TrafficLimit
			}
			if in.ResetDay != nil {
				if *in.ResetDay < 1 || *in.ResetDay > 31 {
					return InField("День сброса", invalid("число от 1 до 31"))
				}
				b.ResetDay = *in.ResetDay
			}
			if in.TrafficCount != nil {
				switch *in.TrafficCount {
				case "both", "out", "in":
					b.TrafficCount = *in.TrafficCount
				default:
					return InField("Что считать", invalid("both, out или in"))
				}
			}
			// A node the limit switched off comes back when the limit is gone or raised past use.
			if b.OffPeriod != 0 && !n.Enabled {
				used, _, err := s.nodeUse(ctx, q, b, loc)
				if err != nil {
					return err
				}
				if b.TrafficLimit == 0 || used < b.TrafficLimit-limitMargin(b.TrafficLimit) {
					b.OffPeriod, reenable = 0, true
					if err := store.SetNodeEnabled(ctx, q, nodeID, true); err != nil {
						return err
					}
				}
			}
			return store.SaveNodeBilling(ctx, q, b)
		})
	if err == nil && reenable {
		s.notify(Notice{Kind: "billing", Text: fmt.Sprintf("✅ Нода <b>%s</b> снова включена: лимит трафика изменён.", esc(s.nodeLabel(ctx, nodeID)))})
	}
	return b, err
}

// BillingPeriod is the traffic period that contains now: from the reset day of this month
// (or of the last one) at midnight in loc, to the same day of the next month. A day past a
// month's end means its last day.
func BillingPeriod(now time.Time, loc *time.Location, resetDay int) (from, to time.Time) {
	now = now.In(loc)
	at := func(y int, m time.Month) time.Time {
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, loc).Day()
		return time.Date(y, m, min(max(resetDay, 1), last), 0, 0, 0, 0, loc)
	}
	from = at(now.Year(), now.Month())
	if now.Before(from) {
		from = at(now.Year(), now.Month()-1)
	}
	to = at(from.Year(), from.Month()+1)
	return from, to
}

// nodeUse is the traffic a node used in its current period as the provider counts it.
// loc is the bot's time zone, read before any transaction: settings are read through the
// database, whose one connection a transaction holds.
func (s *Service) nodeUse(ctx context.Context, q store.DBTX, b *store.NodeBilling, loc *time.Location) (used int64, period [2]int64, err error) {
	from, to := BillingPeriod(s.now(), loc, b.ResetDay)
	rx, tx, err := store.IfaceTraffic(ctx, q, b.NodeID, from.Unix())
	if err != nil {
		return 0, period, err
	}
	switch b.TrafficCount {
	case "out":
		used = tx
	case "in":
		used = rx
	default:
		used = rx + tx
	}
	return used, [2]int64{from.Unix(), to.Unix()}, nil
}

// NodeBill is one node on the billing page.
type NodeBill struct {
	Node        *store.Node
	Billing     *store.NodeBilling
	Used        int64 // traffic in the current period
	PeriodFrom  int64
	PeriodTo    int64
	OffByLimit  bool
	MonthlyCost float64 // the cost per month (a yearly one divided by 12)
}

// BillingOverview lists every node with its billing and traffic.
func (s *Service) BillingOverview(ctx context.Context) ([]NodeBill, error) {
	q := s.st.DB
	loc := s.BotLocation(ctx)
	nodes, err := store.ListNodes(ctx, q)
	if err != nil {
		return nil, err
	}
	all, err := store.ListNodeBilling(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]NodeBill, 0, len(nodes))
	for _, n := range nodes {
		b := all[n.ID]
		if b == nil {
			b = store.DefaultBilling(n.ID)
		}
		used, period, err := s.nodeUse(ctx, q, b, loc)
		if err != nil {
			return nil, err
		}
		monthly := b.Cost
		if b.CostPeriod == "year" {
			monthly = math.Round(b.Cost/12*100) / 100
		}
		out = append(out, NodeBill{Node: n, Billing: b, Used: used, PeriodFrom: period[0], PeriodTo: period[1],
			OffByLimit: b.OffPeriod != 0 && !n.Enabled, MonthlyCost: monthly})
	}
	return out, nil
}

// NodeProviders maps nodes to their providers, for the nodes page.
func (s *Service) NodeProviders(ctx context.Context) (map[int64]*store.Provider, error) {
	q := s.st.DB
	all, err := store.ListNodeBilling(ctx, q)
	if err != nil {
		return nil, err
	}
	providers, err := store.ListProviders(ctx, q)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*store.Provider{}
	for _, p := range providers {
		byID[p.ID] = p
	}
	out := map[int64]*store.Provider{}
	for id, b := range all {
		if b.ProviderID != nil && byID[*b.ProviderID] != nil {
			out[id] = byID[*b.ProviderID]
		}
	}
	return out, nil
}

// checkBilling runs with the maintenance: it switches off a node about to cross its traffic
// limit, back on when its period starts again, and reminds about payments due.
func (s *Service) checkBilling(ctx context.Context) error {
	q := s.st.DB
	loc := s.BotLocation(ctx)
	all, err := store.ListNodeBilling(ctx, q)
	if err != nil {
		return err
	}
	now := s.now()
	for _, b := range all {
		n, err := store.GetNode(ctx, q, b.NodeID)
		if err != nil {
			continue
		}
		if b.PaidUntil != nil && b.RemindedFor != *b.PaidUntil && time.Unix(*b.PaidUntil, 0).Sub(now) <= PayReminder {
			b.RemindedFor = *b.PaidUntil
			if err := store.SaveNodeBilling(ctx, q, b); err != nil {
				return err
			}
			s.notify(Notice{Kind: "billing", Text: s.payText(ctx, n, b)})
		}
		if b.TrafficLimit == 0 && b.OffPeriod == 0 {
			continue
		}
		used, period, err := s.nodeUse(ctx, q, b, loc)
		if err != nil {
			return err
		}
		switch {
		case b.OffPeriod != 0 && b.OffPeriod != period[0]:
			// A new period: the node the limit switched off comes back.
			b.OffPeriod = 0
			if err := s.setNodeByLimit(ctx, n, b, true, "node.limit_on"); err != nil {
				return err
			}
			if !n.Enabled {
				s.notify(Notice{Kind: "billing", Text: fmt.Sprintf("✅ Нода <b>%s</b> снова включена: начался новый период трафика.", esc(nodeTitle(n)))})
			}
		case b.TrafficLimit > 0 && b.OffPeriod == 0 && b.OverridePeriod != period[0] && n.Enabled &&
			used >= b.TrafficLimit-limitMargin(b.TrafficLimit):
			b.OffPeriod = period[0]
			if err := s.setNodeByLimit(ctx, n, b, false, "node.limit_off"); err != nil {
				return err
			}
			s.notify(Notice{Kind: "billing", Text: fmt.Sprintf("⛔ Нода <b>%s</b> выключена: израсходовано %s из %s трафика у провайдера. Включится %s, в новый период.\n\n<i>Включить раньше — переключатель ноды в панели или «Биллинг» → лимит.</i>",
				esc(nodeTitle(n)), gb(used), gb(b.TrafficLimit), time.Unix(period[1], 0).In(loc).Format("02.01 15:04"))})
		}
	}
	return nil
}

func (s *Service) setNodeByLimit(ctx context.Context, n *store.Node, b *store.NodeBilling, on bool, action string) error {
	return s.mutate(ctx, change{actor: ActorSystem, action: action, entity: "node", entityID: func() int64 { return n.ID }, event: EvNodeChanged},
		func(q store.DBTX) error {
			if err := store.SaveNodeBilling(ctx, q, b); err != nil {
				return err
			}
			if n.Enabled == on {
				return nil
			}
			return store.SetNodeEnabled(ctx, q, n.ID, on)
		})
}

// nodeSwitchedOn notes that the admin switched a node back on: a node the limit switched off
// stays on for the rest of its period.
func nodeSwitchedOn(ctx context.Context, q store.DBTX, nodeID int64) error {
	b, err := store.GetNodeBilling(ctx, q, nodeID)
	if err != nil || b.OffPeriod == 0 {
		return err
	}
	b.OverridePeriod, b.OffPeriod = b.OffPeriod, 0
	return store.SaveNodeBilling(ctx, q, b)
}

func (s *Service) payText(ctx context.Context, n *store.Node, b *store.NodeBilling) string {
	until := time.Unix(*b.PaidUntil, 0)
	days := int(math.Ceil(until.Sub(s.now()).Hours() / 24))
	when := fmt.Sprintf("через %d дн.", days)
	switch {
	case days < 0:
		when = "оплата просрочена"
	case days == 0:
		when = "сегодня"
	}
	text := fmt.Sprintf("💳 Оплата ноды <b>%s</b>: оплачена до %s — %s", esc(nodeTitle(n)), until.In(s.BotLocation(ctx)).Format("02.01.2006"), when)
	if b.Cost > 0 {
		text += fmt.Sprintf("\nСтоимость: %s %s", money(b.Cost), esc(b.Currency))
		if b.CostPeriod == "year" {
			text += " в год"
		} else {
			text += " в месяц"
		}
	}
	if b.ProviderID != nil {
		if p, err := store.GetProvider(ctx, s.st.DB, *b.ProviderID); err == nil {
			link := p.LoginURL
			if link == "" {
				link = p.URL
			}
			text += "\nПровайдер: " + esc(p.Name)
			if link != "" {
				text += fmt.Sprintf(" — <a href=\"%s\">войти</a>", esc(link))
			}
		}
	}
	return text
}

func (s *Service) nodeLabel(ctx context.Context, id int64) string {
	if n, err := store.GetNode(ctx, s.st.DB, id); err == nil {
		return nodeTitle(n)
	}
	return fmt.Sprintf("#%d", id)
}

func nodeTitle(n *store.Node) string {
	return strings.TrimSpace(xrayconf.CountryFlag(n.Country) + " " + n.Name + " (" + n.Code + ")")
}

func gb(b int64) string { return fmt.Sprintf("%.1f ГБ", float64(b)/(1<<30)) }

func money(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// esc escapes text for a Telegram HTML message.
func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
