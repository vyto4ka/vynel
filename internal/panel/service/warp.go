package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/geostore"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/warp"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// DesiredGeoAsset describes one panel-managed .dat file that a node must have locally before
// starting Xray.
type DesiredGeoAsset struct {
	Name   string
	SHA256 string
	Size   int64
}

// NodeWarpSummary is the compact WARP badge payload embedded in GET /api/nodes.
type NodeWarpSummary struct {
	Enabled     bool   `json:"enabled"`
	RuleMode    string `json:"ruleMode"`
	RulesCount  int    `json:"rulesCount"`
	AccountType string `json:"accountType"`
	LastCheckOK bool   `json:"lastCheckOk"`
	LastCheckAt int64  `json:"lastCheckAt"`
	Colo        string `json:"colo,omitempty"`
}

// NodeWarpView is returned by GET /api/nodes/{id}/warp (secrets masked).
type NodeWarpView struct {
	NodeID         int64    `json:"nodeId"`
	Enabled        bool     `json:"enabled"`
	RuleMode       string   `json:"ruleMode"`
	CredMode       string   `json:"credMode"`
	HasCredentials bool     `json:"hasCredentials"`
	AccountType    string   `json:"accountType"`
	HasLicenseKey  bool     `json:"hasLicenseKey"`
	PublicKey      string   `json:"publicKey"`
	PeerPublicKey  string   `json:"peerPublicKey"`
	AddrV4         string   `json:"addrV4"`
	AddrV6         string   `json:"addrV6"`
	Reserved       []int    `json:"reserved"`
	Endpoint       string   `json:"endpoint"`
	MTU            int      `json:"mtu"`
	Keepalive      int      `json:"keepalive"`
	DomainStrategy string   `json:"domainStrategy"`
	Rules          []string `json:"rules"`
	LastCheckAt    int64    `json:"lastCheckAt"`
	LastCheckOK    bool     `json:"lastCheckOk"`
	LastCheckIP    string   `json:"lastCheckIp"`
	LastCheckColo  string   `json:"lastCheckColo"`
	LastCheckMs    int64    `json:"lastCheckMs"`
	LastCheckErr   string   `json:"lastCheckErr"`
	CanWarp        bool     `json:"canWarp"`
	CanGeo         bool     `json:"canGeo"`
	Connected      bool     `json:"connected"`
}

// NodeWarpInput is the payload for PUT /api/nodes/{id}/warp.
type NodeWarpInput struct {
	Enabled        bool     `json:"enabled"`
	RuleMode       string   `json:"ruleMode"` // "rules" | "all"
	CredMode       string   `json:"credMode"` // "auto" | "manual"
	LicenseKey     string   `json:"licenseKey"`
	SecretKey      string   `json:"secretKey"`
	PeerPublicKey  string   `json:"peerPublicKey"`
	PresharedKey   string   `json:"presharedKey"`
	AddrV4         string   `json:"addrV4"`
	AddrV6         string   `json:"addrV6"`
	Reserved       []int    `json:"reserved"`
	Endpoint       string   `json:"endpoint"`
	MTU            int      `json:"mtu"`
	Keepalive      int      `json:"keepalive"`
	DomainStrategy string   `json:"domainStrategy"`
	Rules          []string `json:"rules"`
	WireGuardConf  string   `json:"wireguardConf"`
}

// NodeWarpProvisionInput is the payload for POST /api/nodes/{id}/warp/provision.
type NodeWarpProvisionInput struct {
	LicenseKey    string `json:"licenseKey"`
	WireGuardConf string `json:"wireguardConf"`
	ForceNew      bool   `json:"forceNew"`
}

// NodeWarpSummaries returns compact WARP badges for all nodes (keyed by node_id).
func (s *Service) NodeWarpSummaries(ctx context.Context) (map[int64]*NodeWarpSummary, error) {
	rows, err := store.ListNodeWarps(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*NodeWarpSummary, len(rows))
	for id, w := range rows {
		if !w.Enabled && w.SecretKey == "" {
			continue
		}
		out[id] = &NodeWarpSummary{
			Enabled:     w.Enabled,
			RuleMode:    w.RuleMode,
			RulesCount:  len(w.Rules),
			AccountType: w.AccountType,
			LastCheckOK: w.LastCheckOK,
			LastCheckAt: w.LastCheckAt,
			Colo:        w.LastCheckColo,
		}
	}
	return out, nil
}

// NodeWarp returns the masked WARP view for one node.
func (s *Service) NodeWarp(ctx context.Context, nodeID int64) (*NodeWarpView, error) {
	n, err := store.GetNode(ctx, s.st.DB, nodeID)
	if err != nil {
		return nil, err
	}
	w, err := store.GetNodeWarp(ctx, s.st.DB, nodeID)
	if errors.Is(err, store.ErrNotFound) {
		w = &store.NodeWarp{
			NodeID:         nodeID,
			RuleMode:       warp.RuleModeSelected,
			CredMode:       "auto",
			PeerPublicKey:  warp.DefaultPeerPublicKey,
			Endpoint:       warp.DefaultEndpoint,
			MTU:            warp.DefaultMTU,
			Keepalive:      warp.DefaultKeepalive,
			DomainStrategy: warp.DefaultDomainStrategy,
			Rules:          []string{},
		}
	} else if err != nil {
		return nil, err
	}
	return s.nodeWarpToView(n, w), nil
}

func (s *Service) nodeWarpToView(n *store.Node, w *store.NodeWarp) *NodeWarpView {
	res := w.Reserved
	if res == nil {
		res = []int{}
	}
	rules := w.Rules
	if rules == nil {
		rules = []string{}
	}
	connected := s.NodeConnected != nil && s.NodeConnected(n.ID)
	return &NodeWarpView{
		NodeID:         n.ID,
		Enabled:        w.Enabled,
		RuleMode:       w.RuleMode,
		CredMode:       w.CredMode,
		HasCredentials: w.SecretKey != "",
		AccountType:    w.AccountType,
		HasLicenseKey:  w.LicenseKey != "",
		PublicKey:      w.PublicKey,
		PeerPublicKey:  w.PeerPublicKey,
		AddrV4:         w.AddrV4,
		AddrV6:         w.AddrV6,
		Reserved:       res,
		Endpoint:       warp.NormalizeEndpoint(w.Endpoint),
		MTU:            w.MTU,
		Keepalive:      w.Keepalive,
		DomainStrategy: w.DomainStrategy,
		Rules:          rules,
		LastCheckAt:    w.LastCheckAt,
		LastCheckOK:    w.LastCheckOK,
		LastCheckIP:    w.LastCheckIP,
		LastCheckColo:  w.LastCheckColo,
		LastCheckMs:    w.LastCheckMs,
		LastCheckErr:   w.LastCheckErr,
		CanWarp:        n.Can(CapWarp),
		CanGeo:         n.Can(CapGeo),
		Connected:      connected,
	}
}

// SaveNodeWarp validates and saves a node's WARP configuration. When enabled for the first time
// without credentials, it automatically provisions a Cloudflare WARP account (via the node agent
// with fallback to the panel).
func (s *Service) SaveNodeWarp(ctx context.Context, actor Actor, nodeID int64, in NodeWarpInput) (*NodeWarpView, error) {
	n, err := store.GetNode(ctx, s.st.DB, nodeID)
	if err != nil {
		return nil, err
	}
	w, err := store.GetNodeWarp(ctx, s.st.DB, nodeID)
	if errors.Is(err, store.ErrNotFound) {
		w = &store.NodeWarp{
			NodeID:         nodeID,
			RuleMode:       warp.RuleModeSelected,
			CredMode:       "auto",
			PeerPublicKey:  warp.DefaultPeerPublicKey,
			Endpoint:       warp.DefaultEndpoint,
			MTU:            warp.DefaultMTU,
			Keepalive:      warp.DefaultKeepalive,
			DomainStrategy: warp.DefaultDomainStrategy,
		}
	} else if err != nil {
		return nil, err
	}

	if in.RuleMode == "" {
		in.RuleMode = warp.RuleModeSelected
	}
	if in.RuleMode != warp.RuleModeSelected && in.RuleMode != warp.RuleModeAll {
		return nil, invalid("некорректный режим правил WARP: %q", in.RuleMode)
	}

	// Validate rules and check custom geo files exist.
	kinds := s.geoKindMap(ctx)
	parsed, err := warp.ParseRules(in.Rules, func(file string) geostore.Kind { return kinds[file] })
	if err != nil {
		return nil, invalid("правила WARP: %v", err)
	}
	if in.Enabled && in.RuleMode == warp.RuleModeSelected && len(parsed.Domains) == 0 && len(parsed.IPs) == 0 {
		return nil, invalid("добавьте хотя бы одно правило для WARP или выберите режим «Весь трафик сервера»")
	}

	// Check custom .dat files availability and node capability.
	geoByName := map[string]*store.GeoFile{}
	if gfs, err := store.ListGeoFiles(ctx, s.st.DB); err == nil {
		for _, gf := range gfs {
			if gf.Enabled {
				geoByName[gf.Name] = gf
			}
		}
	}
	if in.Enabled && in.RuleMode == warp.RuleModeSelected {
		for _, ref := range parsed.GeoRefs {
			isCustom := ref.File != geostore.SystemGeoSite && ref.File != geostore.SystemGeoIP
			if isCustom {
				if _, ok := geoByName[ref.File]; !ok {
					return nil, invalid("geo-файл %s не добавлен в «Ресурсы»", ref.File)
				}
				if !n.Can(CapGeo) && s.NodeConnected != nil && s.NodeConnected(n.ID) {
					return nil, invalid("агент ноды %s устарел и не поддерживает кастомные geo-файлы; обновите агент ноды", n.Code)
				}
			}
		}
	}

	// Apply WireGuard conf import or manual fields if supplied.
	if strings.TrimSpace(in.WireGuardConf) != "" {
		creds, err := warp.ParseWireGuardConf(in.WireGuardConf)
		if err != nil {
			return nil, invalid("%v", err)
		}
		applyCredsToStore(w, creds)
	} else if in.CredMode == "manual" {
		w.CredMode = "manual"
		if strings.TrimSpace(in.SecretKey) != "" {
			w.SecretKey = strings.TrimSpace(in.SecretKey)
		}
		if strings.TrimSpace(in.PeerPublicKey) != "" {
			w.PeerPublicKey = strings.TrimSpace(in.PeerPublicKey)
		}
		if strings.TrimSpace(in.PresharedKey) != "" {
			w.PresharedKey = strings.TrimSpace(in.PresharedKey)
		}
		if strings.TrimSpace(in.AddrV4) != "" {
			w.AddrV4 = strings.TrimSpace(in.AddrV4)
		}
		if strings.TrimSpace(in.AddrV6) != "" {
			w.AddrV6 = strings.TrimSpace(in.AddrV6)
		}
		if in.Reserved != nil {
			w.Reserved = in.Reserved
		}
		if w.AccountType == "" {
			w.AccountType = "custom"
		}
	}

	if ep := strings.TrimSpace(in.Endpoint); ep != "" {
		if _, _, err := net.SplitHostPort(ep); err != nil {
			return nil, invalid("некорректный endpoint WARP %q: нужен формат host:port", ep)
		}
		w.Endpoint = warp.NormalizeEndpoint(ep)
	}
	if in.MTU > 0 {
		if in.MTU < 576 || in.MTU > 9000 {
			return nil, invalid("MTU должен быть от 576 до 9000")
		}
		w.MTU = in.MTU
	}
	if in.Keepalive >= 0 && in.Keepalive <= 3600 {
		w.Keepalive = in.Keepalive
	}
	if ds := strings.TrimSpace(in.DomainStrategy); ds != "" {
		if !warp.ValidDomainStrategies[ds] {
			return nil, invalid("некорректная стратегия IP: %q", ds)
		}
		w.DomainStrategy = ds
	}

	// Auto-provision Cloudflare WARP credentials on first enable if none exist.
	if in.Enabled && w.SecretKey == "" {
		if w.CredMode == "manual" {
			return nil, invalid("в ручном режиме укажите SecretKey и Address (или импортируйте .conf)")
		}
		creds, err := s.provisionWarpCreds(ctx, n, strings.TrimSpace(in.LicenseKey), nil)
		if err != nil {
			return nil, invalid("не удалось зарегистрировать аккаунт Cloudflare WARP: %v", err)
		}
		applyCredsToStore(w, creds)
		if ep := strings.TrimSpace(in.Endpoint); ep != "" {
			w.Endpoint = warp.NormalizeEndpoint(ep)
		}
	} else if lk := strings.TrimSpace(in.LicenseKey); lk != "" && lk != w.LicenseKey && w.CredMode == "auto" && w.DeviceID != "" {
		creds, err := s.provisionWarpCreds(ctx, n, lk, w)
		if err != nil {
			return nil, invalid("не удалось привязать ключ WARP+: %v", err)
		}
		applyCredsToStore(w, creds)
	}

	w.Enabled = in.Enabled
	w.RuleMode = in.RuleMode
	w.Rules = cleanLines(in.Rules)

	err = s.mutate(ctx, change{
		actor: actor, action: "node.warp.save", entity: "node",
		entityID: func() int64 { return nodeID },
		event:    EvNodeChanged,
		diff:     map[string]any{"enabled": w.Enabled, "ruleMode": w.RuleMode, "rules": len(w.Rules)},
	}, func(q store.DBTX) error {
		return store.UpsertNodeWarp(ctx, q, w)
	})
	if err != nil {
		return nil, err
	}
	return s.nodeWarpToView(n, w), nil
}

// ProvisionNodeWarp registers a fresh Cloudflare WARP account, binds a WARP+ license, or imports a manual .conf.
func (s *Service) ProvisionNodeWarp(ctx context.Context, actor Actor, nodeID int64, in NodeWarpProvisionInput) (*NodeWarpView, error) {
	n, err := store.GetNode(ctx, s.st.DB, nodeID)
	if err != nil {
		return nil, err
	}
	w, err := store.GetNodeWarp(ctx, s.st.DB, nodeID)
	if errors.Is(err, store.ErrNotFound) {
		w = &store.NodeWarp{
			NodeID:         nodeID,
			RuleMode:       warp.RuleModeSelected,
			CredMode:       "auto",
			PeerPublicKey:  warp.DefaultPeerPublicKey,
			Endpoint:       warp.DefaultEndpoint,
			MTU:            warp.DefaultMTU,
			Keepalive:      warp.DefaultKeepalive,
			DomainStrategy: warp.DefaultDomainStrategy,
			Rules:          []string{},
		}
	} else if err != nil {
		return nil, err
	}

	if strings.TrimSpace(in.WireGuardConf) != "" {
		creds, err := warp.ParseWireGuardConf(in.WireGuardConf)
		if err != nil {
			return nil, invalid("%v", err)
		}
		applyCredsToStore(w, creds)
	} else {
		var existing *store.NodeWarp
		if !in.ForceNew && w.CredMode == "auto" && w.DeviceID != "" && strings.TrimSpace(in.LicenseKey) != "" {
			existing = w
		}
		creds, err := s.provisionWarpCreds(ctx, n, strings.TrimSpace(in.LicenseKey), existing)
		if err != nil {
			return nil, invalid("не удалось получить ключи Cloudflare WARP: %v", err)
		}
		applyCredsToStore(w, creds)
	}

	err = s.mutate(ctx, change{
		actor: actor, action: "node.warp.provision", entity: "node",
		entityID: func() int64 { return nodeID },
		event:    EvNodeChanged,
		diff:     map[string]any{"credMode": w.CredMode, "accountType": w.AccountType},
	}, func(q store.DBTX) error {
		return store.UpsertNodeWarp(ctx, q, w)
	})
	if err != nil {
		return nil, err
	}
	return s.nodeWarpToView(n, w), nil
}

// CheckNodeWarp runs a live cdn-cgi/trace check through the node's WARP tunnel via ACTION_WARP_CHECK.
func (s *Service) CheckNodeWarp(ctx context.Context, nodeID int64) (*warp.CheckResult, error) {
	n, err := store.GetNode(ctx, s.st.DB, nodeID)
	if err != nil {
		return nil, err
	}
	w, err := store.GetNodeWarp(ctx, s.st.DB, nodeID)
	if errors.Is(err, store.ErrNotFound) || w.SecretKey == "" {
		return nil, invalid("у ноды %s ещё нет ключей WARP", n.Code)
	}
	if err != nil {
		return nil, err
	}
	if s.WarpCheck == nil {
		return nil, invalid("проверка WARP недоступна в текущем режиме запуска")
	}
	if s.NodeConnected != nil && !s.NodeConnected(n.ID) {
		return nil, invalid("нода %s не на связи", n.Code)
	}
	if !n.Can(CapWarp) {
		return nil, invalid("агент ноды %s не поддерживает живую проверку WARP; обновите агент ноды", n.Code)
	}

	// Best-effort activation for devices registered before warp_enabled=true PATCH was added.
	if !w.LastCheckOK && w.CredMode == "auto" && w.DeviceID != "" && w.AccessToken != "" {
		enCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = warp.EnableWarp(enCtx, &http.Client{Timeout: 5 * time.Second}, "", w.DeviceID, w.AccessToken)
		cancel()
	}

	creds := warpCredsFromStore(w)
	res, err := s.WarpCheck(ctx, n.ID, warp.CheckRequest{
		SecretKey:      creds.SecretKey,
		Addresses:      creds.Addresses(),
		PeerPublicKey:  creds.PeerPublicKey,
		PresharedKey:   creds.PresharedKey,
		Reserved:       creds.Reserved,
		Endpoint:       creds.Endpoint,
		MTU:            creds.MTU,
		DomainStrategy: w.DomainStrategy,
	})
	if err != nil {
		_ = store.UpdateNodeWarpCheck(ctx, s.st.DB, n.ID, false, "", "", 0, err.Error())
		return &warp.CheckResult{OK: false, Error: err.Error()}, nil
	}
	_ = store.UpdateNodeWarpCheck(ctx, s.st.DB, n.ID, res.OK, res.IP, res.Colo, res.RTTMs, res.Error)
	return res, nil
}

func (s *Service) provisionWarpCreds(ctx context.Context, n *store.Node, licenseKey string, existing *store.NodeWarp) (*warp.Credentials, error) {
	// Prefer provisioning from the node's own IP when connected and capable.
	if s.WarpProvision != nil && n.Can(CapWarp) && (s.NodeConnected == nil || s.NodeConnected(n.ID)) {
		var prev *warp.Credentials
		if existing != nil {
			prev = warpCredsFromStore(existing)
		}
		if creds, err := s.WarpProvision(ctx, n.ID, licenseKey, prev); err == nil && creds != nil {
			return creds, nil
		}
	}
	// Fallback: provision directly from the panel.
	hc := &http.Client{Timeout: 20 * time.Second}
	if existing != nil && existing.DeviceID != "" && existing.AccessToken != "" && licenseKey != "" {
		c := warpCredsFromStore(existing)
		if err := warp.SetLicense(ctx, hc, "", c, licenseKey); err != nil {
			return nil, err
		}
		return c, nil
	}
	return warp.Provision(ctx, hc, "", licenseKey)
}

func applyCredsToStore(w *store.NodeWarp, c *warp.Credentials) {
	w.CredMode = c.CredMode
	w.DeviceID = c.DeviceID
	w.AccessToken = c.AccessToken
	w.LicenseKey = c.LicenseKey
	w.AccountType = c.AccountType
	w.SecretKey = c.SecretKey
	w.PublicKey = c.PublicKey
	if c.PeerPublicKey != "" {
		w.PeerPublicKey = c.PeerPublicKey
	}
	w.PresharedKey = c.PresharedKey
	w.AddrV4 = c.AddrV4
	w.AddrV6 = c.AddrV6
	w.Reserved = c.Reserved
	if c.Endpoint != "" {
		w.Endpoint = c.Endpoint
	}
	if c.MTU > 0 {
		w.MTU = c.MTU
	}
	if c.Keepalive > 0 {
		w.Keepalive = c.Keepalive
	}
}

func warpCredsFromStore(w *store.NodeWarp) *warp.Credentials {
	return &warp.Credentials{
		CredMode:      w.CredMode,
		DeviceID:      w.DeviceID,
		AccessToken:   w.AccessToken,
		LicenseKey:    w.LicenseKey,
		AccountType:   w.AccountType,
		SecretKey:     w.SecretKey,
		PublicKey:     w.PublicKey,
		PeerPublicKey: w.PeerPublicKey,
		PresharedKey:  w.PresharedKey,
		AddrV4:        w.AddrV4,
		AddrV6:        w.AddrV6,
		Reserved:      slices.Clone(w.Reserved),
		Endpoint:      warp.NormalizeEndpoint(w.Endpoint),
		MTU:           w.MTU,
		Keepalive:     w.Keepalive,
	}
}

func cleanLines(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

type warpPlan struct {
	configs  map[int64]*xrayconf.WarpConfig
	assets   map[int64][]DesiredGeoAsset
	problems map[int64][]string
}

// planWarp computes per-node WARP configs and required panel-managed geo assets in complete
// isolation so any WARP or missing-asset problem never drops a node's inbounds or users.
func (s *Service) planWarp(ctx context.Context, q store.DBTX, nodes []*store.Node) (*warpPlan, error) {
	plan := &warpPlan{
		configs:  map[int64]*xrayconf.WarpConfig{},
		assets:   map[int64][]DesiredGeoAsset{},
		problems: map[int64][]string{},
	}
	warps, err := store.ListNodeWarps(ctx, q)
	if err != nil {
		return nil, err
	}
	geoFiles, err := store.ListGeoFiles(ctx, q)
	if err != nil {
		return nil, err
	}
	geoByName := make(map[string]*store.GeoFile, len(geoFiles))
	kinds := map[string]geostore.Kind{
		geostore.SystemGeoSite: geostore.KindGeoSite,
		geostore.SystemGeoIP:   geostore.KindGeoIP,
	}
	for _, gf := range geoFiles {
		if gf.Enabled {
			geoByName[gf.Name] = gf
			kinds[gf.Name] = geostore.Kind(gf.Kind)
		}
	}

	for _, n := range nodes {
		if !n.Enabled {
			continue
		}
		assetSet := map[string]DesiredGeoAsset{}
		addAsset := func(name string) bool {
			gf, ok := geoByName[name]
			if !ok {
				return false
			}
			if s.Geo != nil && !s.Geo.HasCustomFile(gf.Name) {
				plan.problems[n.ID] = append(plan.problems[n.ID], fmt.Sprintf("geo: файл %s ещё не скачан на диск панели", gf.Name))
				return false
			}
			if !n.Can(CapGeo) {
				plan.problems[n.ID] = append(plan.problems[n.ID], fmt.Sprintf("geo: агент ноды %s не поддерживает доставку %s (обновите агент)", n.Code, gf.Name))
				return false
			}
			assetSet[gf.Name] = DesiredGeoAsset{Name: gf.Name, SHA256: gf.SHA256, Size: gf.SizeBytes}
			return true
		}

		// If the admin explicitly overrode system geosite.dat or geoip.dat, nodes with CapGeo get them.
		for _, sysName := range []string{geostore.SystemGeoSite, geostore.SystemGeoIP} {
			if gf, ok := geoByName[sysName]; ok && gf.IsSystemOverride && n.Can(CapGeo) {
				if s.Geo == nil || s.Geo.HasCustomFile(gf.Name) {
					assetSet[gf.Name] = DesiredGeoAsset{Name: gf.Name, SHA256: gf.SHA256, Size: gf.SizeBytes}
				}
			}
		}

		w := warps[n.ID]
		if w != nil && w.Enabled {
			if w.SecretKey == "" {
				plan.problems[n.ID] = append(plan.problems[n.ID], "warp: включён, но ключи не настроены")
			} else {
				creds := warpCredsFromStore(w)
				addrs := creds.Addresses()
				if len(addrs) == 0 {
					plan.problems[n.ID] = append(plan.problems[n.ID], "warp: не заданы адреса интерфейса")
				} else if w.RuleMode == warp.RuleModeAll {
					plan.configs[n.ID] = &xrayconf.WarpConfig{
						SecretKey:      creds.SecretKey,
						Addresses:      addrs,
						PeerPublicKey:  creds.PeerPublicKey,
						PresharedKey:   creds.PresharedKey,
						Reserved:       creds.Reserved,
						Endpoint:       creds.Endpoint,
						MTU:            creds.MTU,
						Keepalive:      creds.Keepalive,
						DomainStrategy: w.DomainStrategy,
						RouteAll:       true,
					}
				} else {
					parsed, err := warp.ParseRules(w.Rules, func(file string) geostore.Kind { return kinds[file] })
					if err != nil {
						plan.problems[n.ID] = append(plan.problems[n.ID], fmt.Sprintf("warp: %v", err))
					} else if len(parsed.Domains) > 0 || len(parsed.IPs) > 0 {
						assetsOK := true
						for _, ref := range parsed.GeoRefs {
							isCustom := ref.File != geostore.SystemGeoSite && ref.File != geostore.SystemGeoIP
							if isCustom {
								if !addAsset(ref.File) {
									assetsOK = false
								}
							} else if gf, ok := geoByName[ref.File]; ok && gf.IsSystemOverride {
								if !addAsset(ref.File) {
									assetsOK = false
								}
							}
						}
						if assetsOK {
							plan.configs[n.ID] = &xrayconf.WarpConfig{
								SecretKey:      creds.SecretKey,
								Addresses:      addrs,
								PeerPublicKey:  creds.PeerPublicKey,
								PresharedKey:   creds.PresharedKey,
								Reserved:       creds.Reserved,
								Endpoint:       creds.Endpoint,
								MTU:            creds.MTU,
								Keepalive:      creds.Keepalive,
								DomainStrategy: w.DomainStrategy,
								Domains:        parsed.Domains,
								IPs:            parsed.IPs,
							}
						}
					}
				}
			}
		}

		if len(assetSet) > 0 {
			list := make([]DesiredGeoAsset, 0, len(assetSet))
			for _, a := range assetSet {
				list = append(list, a)
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
			plan.assets[n.ID] = list
		}
	}
	return plan, nil
}
