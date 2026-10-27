package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// SettingSubRouting holds how client apps route traffic (JSON ClientRouting; empty = the
// defaults). It goes into the formats that carry routing: Xray JSON, Mihomo and sing-box.
const SettingSubRouting = "sub.routing"

// ClientRouting is the routing a subscription gives client apps (docs/USER_GUIDE.md §5.1).
type ClientRouting struct {
	// RUDirect sends Russian sites (geosite:category-ru, .ru/.su/.рф) and addresses (geoip:ru)
	// past the tunnel.
	RUDirect bool `json:"ruDirect"`
	// DNSDirect resolve the names that go direct: "localhost" is the device's own resolver.
	DNSDirect []string `json:"dnsDirect"`
	// DNSProxy resolve everything else, through the tunnel: IPs, or DoH links (https://…).
	DNSProxy []string `json:"dnsProxy"`
	// Torrent is where BitTorrent goes: proxy, direct or block.
	Torrent  string `json:"torrent"`
	BlockAds bool   `json:"blockAds"` // geosite:category-ads-all
	// Own lists, in Xray syntax: domain:, full:, keyword:, regexp:, geosite:, geoip:, an IP or a
	// CIDR; a bare name means the domain and its subdomains. process: is an app by its file
	// name (FortniteClient-Win64-Shipping.exe) or Android package (com.example.app).
	Direct []string `json:"direct"`
	Proxy  []string `json:"proxy"` // through the tunnel even when RU or the direct list would match
	Block  []string `json:"block"`
	// Fragment cuts the TLS ClientHello into pieces on the way out (Xray JSON only).
	Fragment ClientFragment `json:"fragment"`
}

// ClientFragment is Xray's finalmask "fragment": the client sends its TLS ClientHello in small
// pieces with pauses, for networks that drop TCP 443 connections by it. It reaches apps in the
// Xray JSON format (KeqDroid, Happ, v2RayTun), for every server but Hysteria2 (UDP) and those
// with their own finalmask; links, Mihomo and sing-box cannot carry it.
type ClientFragment struct {
	Enabled bool   `json:"enabled"`
	Packets string `json:"packets"` // "tlshello", or the packets to cut: "1-3"
	Length  string `json:"length"`  // bytes per piece: "10-20"
	Delay   string `json:"delay"`   // milliseconds between pieces: "10-20"
}

// DefaultClientFragment is off, with the usual settings ready.
func DefaultClientFragment() ClientFragment {
	return ClientFragment{Packets: "tlshello", Length: "10-20", Delay: "10-20"}
}

// FinalMask is the streamSettings.finalmask that carries the fragment.
func (f ClientFragment) FinalMask() map[string]any {
	return map[string]any{"tcp": []any{map[string]any{"type": "fragment",
		"settings": map[string]any{"packets": f.Packets, "length": f.Length, "delay": f.Delay}}}}
}

// WithFragment gives the servers the subscription's fragment, when it is on: a copy of hosts in
// which every TCP server without its own finalmask has it.
func WithFragment(hosts []Host, f ClientFragment) []Host {
	if !f.Enabled {
		return hosts
	}
	out := slices.Clone(hosts)
	for i := range out {
		if h := &out[i]; h.FinalMask == nil && h.Raw == "" && h.Protocol != "hysteria" {
			h.FinalMask = f.FinalMask()
		}
	}
	return out
}

var rangeRe = regexp.MustCompile(`^(\d{1,5})(?:-(\d{1,5}))?$`)

// checkRange checks "N" or "N-M" (N <= M) with N at least least; field names it in the panel.
func checkRange(v string, least int, field string) error {
	m := rangeRe.FindStringSubmatch(v)
	if m == nil {
		return invalid("«%s»: число или диапазон, например 10-20", field)
	}
	from, _ := strconv.Atoi(m[1])
	to := from
	if m[2] != "" {
		to, _ = strconv.Atoi(m[2])
	}
	if from > to {
		return invalid("«%s»: начало диапазона больше конца", field)
	}
	if from < least {
		return invalid("«%s»: не меньше %d", field, least)
	}
	return nil
}

// checkFragment validates the fragment and fills empty fields with the defaults.
func checkFragment(f *ClientFragment) error {
	d := DefaultClientFragment()
	f.Packets, f.Length, f.Delay = strings.TrimSpace(f.Packets), strings.TrimSpace(f.Length), strings.TrimSpace(f.Delay)
	if f.Packets == "" {
		f.Packets = d.Packets
	}
	if f.Length == "" {
		f.Length = d.Length
	}
	if f.Delay == "" {
		f.Delay = d.Delay
	}
	if f.Packets != "tlshello" {
		if err := checkRange(f.Packets, 1, "Фрагментация: пакеты"); err != nil {
			return err
		}
	}
	if err := checkRange(f.Length, 1, "Фрагментация: длина куска"); err != nil {
		return err
	}
	return checkRange(f.Delay, 0, "Фрагментация: пауза")
}

// DefaultClientRouting is what subscriptions carry until changed: Russian sites direct with
// Yandex DNS, the rest through the tunnel with DoH.
func DefaultClientRouting() ClientRouting {
	return ClientRouting{
		RUDirect:  true,
		DNSDirect: []string{"localhost", "77.88.8.8"},
		DNSProxy:  []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"},
		Torrent:   "direct",
		Direct:    []string{}, Proxy: []string{}, Block: []string{},
		Fragment: DefaultClientFragment(),
	}
}

// ClientRouting returns the subscription routing (the defaults until changed).
func (s *Service) ClientRouting(ctx context.Context) (ClientRouting, error) {
	raw, err := s.Setting(ctx, SettingSubRouting, "")
	if err != nil || strings.TrimSpace(raw) == "" {
		return DefaultClientRouting(), err
	}
	r := DefaultClientRouting()
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return DefaultClientRouting(), nil
	}
	return r, nil
}

// SetClientRouting validates and stores the routing; nil restores the defaults.
func (s *Service) SetClientRouting(ctx context.Context, actor Actor, r *ClientRouting) error {
	if r == nil {
		return s.SetSetting(ctx, actor, SettingSubRouting, "")
	}
	if err := CheckClientRouting(r); err != nil {
		return err
	}
	b, _ := json.Marshal(r)
	return s.SetSetting(ctx, actor, SettingSubRouting, string(b))
}

var ruleWordRe = regexp.MustCompile(`^[a-z0-9!@._-]+$`)

// processRe is an app's file or package name: no paths, nothing that breaks a Mihomo rule.
var processRe = regexp.MustCompile(`^[\p{L}\p{N}_.+()-][\p{L}\p{N}_.+() -]{0,127}$`)

// CheckClientRouting validates and tidies a routing (empty lines dropped). Errors name the
// field and the line as the panel shows them.
func CheckClientRouting(r *ClientRouting) error {
	if err := checkFragment(&r.Fragment); err != nil {
		return err
	}
	switch r.Torrent {
	case "":
		r.Torrent = "proxy"
	case "proxy", "direct", "block":
	default:
		return invalid("«Торренты»: напрямую, через туннель или блок")
	}
	var err error
	if r.DNSDirect, err = cleanDNS(r.DNSDirect, true, "DNS для прямых сайтов"); err != nil {
		return err
	}
	if r.DNSProxy, err = cleanDNS(r.DNSProxy, false, "DNS через туннель"); err != nil {
		return err
	}
	for _, x := range []struct {
		name string
		l    *[]string
	}{{"Всегда напрямую", &r.Direct}, {"Всегда через туннель", &r.Proxy}, {"Блокировать", &r.Block}} {
		out := []string{}
		for i, e := range *x.l {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			if err := checkRuleEntry(e); err != nil {
				return InField(fmt.Sprintf("«%s», строка %d", x.name, i+1), err)
			}
			out = append(out, e)
		}
		*x.l = out
	}
	return nil
}

// cleanDNS checks a DNS list (field is its name in the panel; lines count from 1).
func cleanDNS(in []string, direct bool, field string) ([]string, error) {
	out := []string{}
	for i, d := range in {
		d = strings.TrimSpace(d)
		where := fmt.Sprintf("«%s», строка %d", field, i+1)
		switch {
		case d == "":
			continue
		case d == "localhost":
			if !direct {
				return nil, invalid("%s: localhost (DNS устройства) не ходит через туннель — укажите IP или DoH", where)
			}
		case net.ParseIP(d) != nil:
		case strings.HasPrefix(d, "https://"):
			u, err := url.Parse(d)
			if err != nil || u.Host == "" {
				return nil, invalid("%s: %q — неверная ссылка DoH", where, d)
			}
		default:
			return nil, invalid("%s: %q — нужен IP, ссылка DoH (https://…/dns-query) или localhost", where, d)
		}
		out = append(out, d)
	}
	return out, nil
}

// RuleEntry is one parsed list entry.
type RuleEntry struct {
	Kind  string // domain, full, keyword, regexp, geosite, geoip, ip, process
	Value string
}

// ParseRuleEntry reads one list entry; a bare name is a domain with its subdomains.
func ParseRuleEntry(e string) RuleEntry {
	if _, _, err := net.ParseCIDR(e); err == nil {
		return RuleEntry{"ip", e}
	}
	if net.ParseIP(e) != nil {
		return RuleEntry{"ip", e}
	}
	if k, v, ok := strings.Cut(e, ":"); ok {
		switch k {
		case "domain", "full", "keyword", "regexp", "geosite", "geoip", "process":
			return RuleEntry{k, v}
		}
	}
	return RuleEntry{"domain", strings.TrimPrefix(e, ".")}
}

func checkRuleEntry(e string) error {
	r := ParseRuleEntry(e)
	switch r.Kind {
	case "ip":
		return nil
	case "regexp":
		if _, err := regexp.Compile(r.Value); err != nil {
			return invalid("%q — неверное регулярное выражение: %v", e, err)
		}
		return nil
	case "process":
		if !processRe.MatchString(strings.TrimSpace(r.Value)) {
			return invalid("%q — после process: имя файла программы (game.exe) или пакета Android (com.example.app), без пути", e)
		}
		return nil
	}
	if r.Value == "" || !ruleWordRe.MatchString(strings.ToLower(r.Value)) {
		return invalid("%q — непонятная запись: можно domain:, full:, keyword:, regexp:, geosite:, geoip:, process:, IP или подсеть", e)
	}
	return nil
}

// ProfileClient is a profile's own client config (the "custom client profile" switch):
// either its own routing or a whole Xray client config. It applies to the Xray JSON format,
// which has one config per server; Mihomo and sing-box get one file for all servers and use
// the subscription routing.
type ProfileClient struct {
	Custom  bool           `json:"custom"`
	Mode    string         `json:"mode"` // routing | json
	Routing *ClientRouting `json:"routing,omitempty"`
	// Config is an Xray client config. Its outbound tagged "proxy" is replaced with the server's
	// (added first when there is none); "remarks" becomes the server's name.
	Config string `json:"config,omitempty"`
}

// CheckProfileClient validates a profile's client config.
func CheckProfileClient(c *ProfileClient) error {
	if !c.Custom {
		return nil
	}
	switch c.Mode {
	case "", "routing":
		c.Mode = "routing"
		if c.Routing == nil {
			r := DefaultClientRouting()
			c.Routing = &r
		}
		return CheckClientRouting(c.Routing)
	case "json":
		if _, err := ParseClientConfig(c.Config); err != nil {
			return err
		}
		return nil
	}
	return invalid("«Клиент»: режим — настройки или конфиг JSON")
}

// ParseClientConfig reads a custom Xray client config: an object, or an array of one.
func ParseClientConfig(src string) (map[string]any, error) {
	var v any
	if err := json.Unmarshal([]byte(src), &v); err != nil {
		var se *json.SyntaxError
		if errors.As(err, &se) {
			return nil, invalid("«Конфиг JSON», строка %d: ошибка JSON: %v", strings.Count(src[:min(int(se.Offset), len(src))], "\n")+1, err)
		}
		return nil, invalid("«Конфиг JSON»: ошибка JSON: %v", err)
	}
	if l, ok := v.([]any); ok {
		if len(l) != 1 {
			return nil, invalid("«Конфиг JSON»: нужен один объект конфига, а не список из %d", len(l))
		}
		v = l[0]
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, invalid("«Конфиг JSON»: нужен объект {…}")
	}
	if o, ok := m["outbounds"]; ok {
		if _, ok := o.([]any); !ok {
			return nil, invalid("«Конфиг JSON»: outbounds должен быть списком")
		}
	}
	if _, ok := m["routing"].(map[string]any); !ok && m["routing"] != nil {
		return nil, invalid("«Конфиг JSON»: routing должен быть объектом")
	}
	return m, nil
}

// CheckFinalMask checks a connection point's own finalmask as Xray reads it: tcp and udp lists of
// masks with a type, and quicParams.
func CheckFinalMask(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return invalid(`Final Mask: нужен JSON-объект, например {"tcp": [{"type": "fragment", "settings": {…}}]}`)
	}
	for k, x := range m {
		switch k {
		case "tcp", "udp":
			l, ok := x.([]any)
			if !ok {
				return invalid("Final Mask: «%s» должен быть списком", k)
			}
			for i, e := range l {
				o, ok := e.(map[string]any)
				if !ok || str(o["type"]) == "" {
					return invalid("Final Mask: «%s», элемент %d: нужен объект с полем type", k, i+1)
				}
				st, _ := o["settings"].(map[string]any)
				if _, has := st["interval"]; has && o["type"] == "fragment" {
					if _, ok := st["delay"]; !ok {
						return invalid("Final Mask: «%s», элемент %d: пауза между кусками в Xray называется delay, а не interval", k, i+1)
					}
				}
			}
		case "quicParams":
			if _, ok := x.(map[string]any); !ok {
				return invalid("Final Mask: «quicParams» должен быть объектом")
			}
		default:
			return invalid("Final Mask: неизвестное поле «%s» (есть tcp, udp и quicParams)", k)
		}
	}
	return nil
}
