package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// Subscription settings (docs/ARCHITECTURE.md §8, docs/STEALTH.md §2.4).
const (
	SettingSubDomain      = "sub.domain"       // required for subscription URLs
	SettingSubPort        = "sub.port"         // public port, default 443
	SettingSubPrefix      = "sub.prefix"       // path prefix, default /s/
	SettingSubListen      = "sub.listen"       // internal listener behind Caddy, default 127.0.0.1:2096
	SettingSubTitle       = "sub.title"        // profile-title shown by clients
	SettingSubUpdateHours = "sub.update_hours" // profile-update-interval
	SettingSubSupportURL  = "sub.support_url"
	SettingSubDecoy       = "sub.decoy"        // decoy site shown on the subscription domain
	SettingSubPageEnabled = "sub.page_enabled" // "false" = browsers get the decoy instead of the page
	SettingHWIDEnabled    = "hwid.enabled"
	SettingHWIDLimit      = "hwid.default_limit" // used when a user has no own limit; 0 = unlimited
	SettingHWIDAllowNone  = "hwid.allow_missing" // let clients without x-hwid through
)

// Defaults of the settings above.
const (
	DefaultSubPrefix = "/s/"
	DefaultSubListen = "127.0.0.1:2096"
)

// Host is one connection point in a subscription (Remnawave "host"), built automatically from
// the template, the rendered inbound and per-inbound overrides (docs/ARCHITECTURE.md §6.3).
type Host struct {
	Tag         string
	NodeCode    string
	Remark      string
	Protocol    string // vless
	Network     string // tcp | xhttp
	Security    string // reality | tls
	Address     string
	Port        int
	SNI         string
	HostHeader  string
	Path        string
	Mode        string // xhttp mode
	ALPN        []string
	Fingerprint string
	Flow        string
	PublicKey   string
	ShortID     string
	Extra       map[string]any // xhttp extra for the client (server extra + client-only xmux, or the template's full client extra)
	Hidden      bool
	// Mihomo: the host may be given to Mihomo/Clash Meta even when it is not plain TCP
	// (xhttp needs Mihomo 1.19+, so templates opt in). MLKEM: support-x25519mlkem768 in reality-opts.
	Mihomo bool
	MLKEM  bool
}

// UserInbounds returns the enabled node inbounds (on enabled nodes) a user can use.
func (s *Service) UserInbounds(ctx context.Context, userID int64) ([]*store.NodeInbound, error) {
	q := s.st.DB
	groups, err := store.UserGroupIDs(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	inbounds, err := store.ListNodeInbounds(ctx, q, 0, 0)
	if err != nil {
		return nil, err
	}
	rules, err := store.ListAccessRules(ctx, q, 0)
	if err != nil {
		return nil, err
	}
	nodes, err := store.ListNodes(ctx, q)
	if err != nil {
		return nil, err
	}
	enabled := map[int64]bool{}
	for _, n := range nodes {
		enabled[n.ID] = n.Enabled
	}
	access := ResolveAccess(rules, inbounds)
	var out []*store.NodeInbound
	for _, ni := range inbounds {
		if !ni.Enabled || !enabled[ni.NodeID] {
			continue
		}
		for _, g := range groups {
			if access[g][ni.ID] {
				out = append(out, ni)
				break
			}
		}
	}
	return out, nil
}

// UserHosts builds the connection points of a user, ordered by node and inbound.
// Inbounds that fail to render are skipped (the panel log and `node list` show why).
func (s *Service) UserHosts(ctx context.Context, userID int64) ([]Host, error) {
	inbounds, err := s.UserInbounds(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Host, 0, len(inbounds))
	for _, ni := range inbounds {
		h, err := s.HostFor(ctx, ni)
		if err != nil {
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

// HostFor builds the connection point of one node inbound.
func (s *Service) HostFor(ctx context.Context, ni *store.NodeInbound) (Host, error) {
	if err := s.syncTemplates(ctx); err != nil {
		return Host{}, err
	}
	q := s.st.DB
	r, err := s.renderNodeInbound(ctx, q, ni)
	if err != nil {
		return Host{}, err
	}
	node, err := store.GetNode(ctx, q, ni.NodeID)
	if err != nil {
		return Host{}, err
	}
	p, err := store.GetProfile(ctx, q, ni.ProfileID)
	if err != nil {
		return Host{}, err
	}
	tpl := r.Template // the profile's own connection point when it has one
	vals := copyMap(r.Values)
	port, _ := r.Inbound["port"].(int)
	vals["INBOUND_PORT"] = port
	hm, err := xrayconf.ExpandTree(tpl.Host, vals)
	if err != nil {
		return Host{}, err
	}
	if len(ni.Host) > 0 {
		hm = xrayconf.MergePatch(hm, copyMap(ni.Host))
	}
	remark, _ := xrayconf.Expand(p.RemarkPattern, vals)
	h := Host{
		Tag: ni.Tag, NodeCode: node.Code, Remark: strings.TrimSpace(remark), Protocol: r.Protocol, Flow: r.Flow,
		Address: str(hm["address"]), Port: toInt(hm["port"]), Security: str(hm["security"]),
		SNI: str(hm["sni"]), HostHeader: str(hm["host"]), Path: str(hm["path"]), Fingerprint: str(hm["fingerprint"]),
		Hidden: hm["hidden"] == true, Mihomo: hm["mihomo"] == true, MLKEM: hm["mihomo_x25519mlkem768"] == true,
	}
	if v := str(hm["remark"]); v != "" {
		h.Remark = v
	}
	if l, ok := hm["alpn"].([]any); ok {
		for _, a := range l {
			h.ALPN = append(h.ALPN, str(a))
		}
	}
	ss, _ := r.Inbound["streamSettings"].(map[string]any)
	h.Network = str(ss["network"])
	if h.Network == "raw" || h.Network == "" {
		h.Network = "tcp"
	}
	if h.Security == "reality" {
		rs, _ := ss["realitySettings"].(map[string]any)
		// The rendered inbound is the truth: profile overrides may change serverNames or shortIds.
		if _, overridden := ni.Host["sni"]; !overridden {
			if sn := firstString(rs["serverNames"]); sn != "" {
				h.SNI = sn
			}
		}
		h.ShortID = firstString(rs["shortIds"])
		priv := str(rs["privateKey"])
		if pub, err := xrayconf.X25519Public(priv); err == nil {
			h.PublicKey = pub
		}
	}
	if h.Network == "xhttp" {
		xs, _ := ss["xhttpSettings"].(map[string]any)
		h.Mode = str(xs["mode"])
		extra, _ := xs["extra"].(map[string]any)
		h.Extra = copyMap(extra)
		if full, ok := hm["xhttp_client_extra"].(map[string]any); ok {
			// A complete client object: the server extra holds keys a client must not get.
			h.Extra = full
		}
		if co, ok := hm["xhttp_client_only"].(map[string]any); ok {
			for k, v := range co {
				h.Extra[k] = v
			}
		}
	}
	if h.Address == "" || h.Port == 0 {
		return Host{}, fmt.Errorf("inbound %s: connection point has no address or port", ni.Tag)
	}
	return h, nil
}

// SetHostOverride changes the connection point overrides of a node inbound
// (keys: remark, address, port, sni, fingerprint, hidden; null deletes).
func (s *Service) SetHostOverride(ctx context.Context, actor Actor, niID int64, patch map[string]any) error {
	allowed := map[string]bool{"remark": true, "address": true, "port": true, "sni": true, "host": true, "fingerprint": true, "hidden": true, "alpn": true,
		"mihomo": true, "mihomo_x25519mlkem768": true}
	for k := range patch {
		if !allowed[k] {
			return invalid("connection point field %q cannot be overridden", k)
		}
	}
	return s.mutate(ctx, change{actor: actor, action: "host.update", entity: "node_inbound", entityID: func() int64 { return niID }, event: EvInboundChanged, diff: patch},
		func(q store.DBTX) error {
			ni, err := store.GetNodeInbound(ctx, q, niID)
			if err != nil {
				return err
			}
			ni.Host = xrayconf.MergePatch(ni.Host, patch)
			return store.UpdateNodeInbound(ctx, q, ni)
		})
}

// SubscriptionURL is the user's subscription link.
func (s *Service) SubscriptionURL(ctx context.Context, u *store.User) (string, error) {
	domain, err := s.Setting(ctx, SettingSubDomain, "")
	if err != nil {
		return "", err
	}
	if domain == "" {
		return "", fmt.Errorf("subscription domain is not set: `vynel admin setting %s sub.example.com`", SettingSubDomain)
	}
	port, _ := s.Setting(ctx, SettingSubPort, "443")
	prefix, _ := s.Setting(ctx, SettingSubPrefix, DefaultSubPrefix)
	host := domain
	if port != "" && port != "443" {
		host += ":" + port
	}
	return "https://" + host + prefix + u.SubToken, nil
}

// DeviceInfo is what a client tells about itself in subscription request headers.
type DeviceInfo struct {
	HWID, Platform, OSVersion, Model, UserAgent, IP string
}

// DeviceVerdict is the HWID decision for a subscription request.
type DeviceVerdict struct {
	OK     bool
	Off    bool   // HWID is not checked (globally or for this user)
	NoHWID bool   // the client sent no x-hwid
	Full   bool   // refused: the device limit is reached
	Reason string // shown to the user inside the stub subscription
	Count  int
	Limit  int
}

// CheckDevice applies the HWID policy (docs/ARCHITECTURE.md §8.2) and registers new devices.
func (s *Service) CheckDevice(ctx context.Context, u *store.User, d DeviceInfo) (DeviceVerdict, error) {
	enabled, _ := s.Setting(ctx, SettingHWIDEnabled, "true")
	if enabled != "true" || u.HWIDOff {
		return DeviceVerdict{OK: true, Off: true}, nil
	}
	limit := 3
	if v, _ := s.Setting(ctx, SettingHWIDLimit, "3"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	if u.HWIDLimit != nil {
		limit = int(*u.HWIDLimit)
	}
	if d.HWID == "" {
		allow, _ := s.Setting(ctx, SettingHWIDAllowNone, "false")
		if allow == "true" {
			return DeviceVerdict{OK: true, NoHWID: true, Limit: limit}, nil
		}
		return DeviceVerdict{NoHWID: true, Limit: limit,
			Reason: "Приложение не передаёт HWID: включите его в настройках профиля (Karing: X-HWID) или используйте Happ, v2RayTun, KeqDroid"}, nil
	}
	v := DeviceVerdict{Limit: limit}
	err := s.st.Tx(ctx, func(q store.DBTX) error {
		now := s.now().Unix()
		dev := &store.Device{UserID: u.ID, HWID: d.HWID, Platform: d.Platform, OSVersion: d.OSVersion, Model: d.Model,
			UserAgent: d.UserAgent, FirstSeen: now, LastSeen: now, LastIP: d.IP}
		known, err := store.TouchDevice(ctx, q, dev)
		if err != nil {
			return err
		}
		if v.Count, err = store.CountDevices(ctx, q, u.ID); err != nil {
			return err
		}
		if known {
			v.OK = true
			return nil
		}
		if limit > 0 && v.Count >= limit {
			v.Reason, v.Full = fmt.Sprintf("Лимит устройств: %d/%d", v.Count, limit), true
			return store.AddEvent(ctx, q, "device.limit_reached", u.ID, map[string]string{"hwid": d.HWID, "model": d.Model})
		}
		if err := store.AddDevice(ctx, q, dev); err != nil {
			return err
		}
		v.OK, v.Count = true, v.Count+1
		return store.AddEvent(ctx, q, "device.added", u.ID, map[string]string{"hwid": d.HWID, "model": d.Model})
	})
	return v, err
}

// Devices lists a user's devices.
func (s *Service) Devices(ctx context.Context, userID int64) ([]*store.Device, error) {
	return store.ListDevices(ctx, s.st.DB, userID)
}

// DeleteDevice frees a device slot.
func (s *Service) DeleteDevice(ctx context.Context, actor Actor, userID, deviceID int64) error {
	return s.mutate(ctx, change{actor: actor, action: "device.delete", entity: "user", entityID: func() int64 { return userID }, diff: map[string]int64{"device": deviceID}},
		func(q store.DBTX) error { return store.DeleteDevice(ctx, q, userID, deviceID) })
}

// TouchSubscription records a subscription fetch.
func (s *Service) TouchSubscription(ctx context.Context, userID int64, ua string) error {
	return store.TouchSubscription(ctx, s.st.DB, userID, s.now().Unix(), ua)
}

// UserBySubToken loads a user by subscription token.
func (s *Service) UserBySubToken(ctx context.Context, token string) (*store.User, error) {
	return store.GetUserBySubToken(ctx, s.st.DB, token)
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

func firstString(v any) string {
	if l, ok := v.([]any); ok && len(l) > 0 {
		s, _ := l[0].(string)
		return s
	}
	return ""
}
