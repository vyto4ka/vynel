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
	Transport   string `json:"transport"`
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
	Transport      string   `json:"transport"`
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
	Transport      string   `json:"transport"` // "masque-h3" | "masque-h2" | "wireguard"
	RuleMode       string   `json:"ruleMode"`  // "rules" | "all"
	CredMode       string   `json:"credMode"`  // "auto" | "manual"
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
	Transport     string `json:"transport"`
	LicenseKey    string `json:"licenseKey"`
	WireGuardConf string `json:"wireguardConf"`
	ForceNew      bool   `json:"forceNew"`
}

func effectiveTransport(w *store.NodeWarp) string {
	if warp.ValidTransports[w.Transport] {
		return w.Transport
	}
	if w.SecretKey == "" || warp.IsMasqueKey(w.SecretKey) {
		return warp.TransportMasqueH3
	}
	return warp.TransportWireGuard
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
			Transport:   effectiveTransport(w),
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
			Transport:      warp.DefaultTransport,
			RuleMode:       warp.RuleModeSelected,
			CredMode:       "auto",
			Endpoint:       warp.DefaultMasqueEndpoint,
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
	tr := effectiveTransport(w)
	connected := s.NodeConnected != nil && s.NodeConnected(n.ID)
	return &NodeWarpView{
		NodeID:         n.ID,
		Enabled:        w.Enabled,
		Transport:      tr,
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
		Endpoint:       warp.NormalizeEndpointForTransport(w.Endpoint, tr),
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
// without credentials (or when switching between MASQUE and WireGuard in auto mode), it
// automatically provisions a Cloudflare WARP account via the node agent with fallback to the panel.
func (s *Service) SaveNodeWarp(ctx context.Context, actor Actor, nodeID int64, in NodeWarpInput) (*NodeWarpView, error) {
	n, err := store.GetNode(ctx, s.st.DB, nodeID)
	if err != nil {
		return nil, err
	}
	w, err := store.GetNodeWarp(ctx, s.st.DB, nodeID)
	if errors.Is(err, store.ErrNotFound) {
		w = &store.NodeWarp{
			NodeID:         nodeID,
			Transport:      warp.DefaultTransport,
			RuleMode:       warp.RuleModeSelected,
			CredMode:       "auto",
			Endpoint:       warp.DefaultMasqueEndpoint,
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
	targetTransport := effectiveTransport(w)
	if t := strings.TrimSpace(strings.ToLower(in.Transport)); t != "" {
		if !warp.ValidTransports[t] {
			return nil, invalid("некорректный режим правил WARP: %q", in.Transport)
		}
		targetTransport = t
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
		targetTransport = warp.TransportWireGuard
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
		if !warp.IsMasqueKey(w.SecretKey) {
			targetTransport = warp.TransportWireGuard
		}
	}

	w.Transport = targetTransport

	if ep := strings.TrimSpace(in.Endpoint); ep != "" {
		if _, _, err := net.SplitHostPort(ep); err != nil {
			return nil, invalid("некорректный endpoint WARP %q: нужен формат host:port", ep)
		}
		w.Endpoint = warp.NormalizeEndpointForTransport(ep, w.Transport)
	} else {
		w.Endpoint = warp.NormalizeEndpointForTransport(w.Endpoint, w.Transport)
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

	// Auto-provision when enabling without credentials OR when switching between MASQUE and WireGuard families.
	familyMismatch := w.SecretKey != "" && w.CredMode == "auto" && warp.IsMasqueTransport(w.Transport) != warp.IsMasqueKey(w.SecretKey)
	if (in.Enabled && w.SecretKey == "") || familyMismatch {
		if w.CredMode == "manual" {
			return nil, invalid("в ручном режиме укажите SecretKey и Address (или импортируйте .conf)")
		}
		lk := strings.TrimSpace(in.LicenseKey)
		if lk == "" {
			lk = w.LicenseKey
		}
		creds, err := s.provisionWarpCreds(ctx, n, w.Transport, lk, nil)
		if err != nil {
			return nil, invalid("не удалось зарегистрировать аккаунт Cloudflare WARP: %v", err)
		}
		applyCredsToStore(w, creds)
		if ep := strings.TrimSpace(in.Endpoint); ep != "" {
			w.Endpoint = warp.NormalizeEndpointForTransport(ep, w.Transport)
		}
	} else if lk := strings.TrimSpace(in.LicenseKey); lk != "" && lk != w.LicenseKey && w.CredMode == "auto" && w.DeviceID != "" {
		creds, err := s.provisionWarpCreds(ctx, n, w.Transport, lk, w)
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
		diff:     map[string]any{"enabled": w.Enabled, "transport": w.Transport, "ruleMode": w.RuleMode, "rules": len(w.Rules)},
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
			Transport:      warp.DefaultTransport,
			RuleMode:       warp.RuleModeSelected,
			CredMode:       "auto",
			Endpoint:       warp.DefaultMasqueEndpoint,
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
		targetTransport := strings.TrimSpace(strings.ToLower(in.Transport))
		if targetTransport != "" && !warp.ValidTransports[targetTransport] {
			return nil, invalid("некорректный режим правил WARP: %q", in.Transport)
		}
		if targetTransport == "" {
			// When reissuing an unhealthy legacy WireGuard account without forcing wireguard,
			// auto-upgrade to MASQUE (h3 -> h2 fallback).
			if w.Transport == warp.TransportWireGuard && w.LastCheckOK {
				targetTransport = warp.TransportWireGuard
			} else {
				targetTransport = warp.DefaultTransport
			}
		}
		var existing *store.NodeWarp
		if !in.ForceNew && w.CredMode == "auto" && w.DeviceID != "" && strings.TrimSpace(in.LicenseKey) != "" &&
			warp.IsMasqueTransport(targetTransport) == warp.IsMasqueKey(w.SecretKey) {
			existing = w
		}
		lk := strings.TrimSpace(in.LicenseKey)
		if lk == "" && in.ForceNew {
			lk = w.LicenseKey
		}
		creds, err := s.provisionWarpCreds(ctx, n, targetTransport, lk, existing)
		if err != nil {
			return nil, invalid("не удалось получить ключи Cloudflare WARP: %v", err)
		}
		applyCredsToStore(w, creds)
	}

	err = s.mutate(ctx, change{
		actor: actor, action: "node.warp.provision", entity: "node",
		entityID: func() int64 { return nodeID },
		event:    EvNodeChanged,
		diff:     map[string]any{"credMode": w.CredMode, "transport": w.Transport, "accountType": w.AccountType},
	}, func(q store.DBTX) error {
		return store.UpsertNodeWarp(ctx, q, w)
	})
	if err != nil {
		return nil, err
	}
	return s.nodeWarpToView(n, w), nil
}

// CheckNodeWarp runs a live cdn-cgi/trace check through the node's WARP tunnel via ACTION_WARP_CHECK.
// When an auto-managed tunnel fails (e.g. hoster blocks WireGuard UDP 2408 or QUIC UDP 443),
// CheckNodeWarp automatically heals it by switching to MASQUE HTTP/3 (UDP 443) or MASQUE HTTP/2 (TCP 443).
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

	curTransport := effectiveTransport(w)
	if curTransport == warp.TransportWireGuard && !w.LastCheckOK && w.CredMode == "auto" && w.DeviceID != "" && w.AccessToken != "" {
		enCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = warp.EnableWarp(enCtx, &http.Client{Timeout: 5 * time.Second}, "", w.DeviceID, w.AccessToken)
		cancel()
	}

	creds := warpCredsFromStore(w)
	res, err := s.WarpCheck(ctx, n.ID, warp.CheckRequest{
		Transport:      curTransport,
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
	if res.OK {
		_ = store.UpdateNodeWarpCheck(ctx, s.st.DB, n.ID, true, res.IP, res.Colo, res.RTTMs, "")
		return res, nil
	}

	// Auto-heal auto-managed nodes when UDP 2408 (WireGuard) or UDP 443 (MASQUE H3) is blocked by the hoster.
	if w.CredMode == "auto" {
		if healed := s.tryAutoHealWarp(ctx, n, w, curTransport); healed != nil && healed.OK {
			return healed, nil
		}
	}

	_ = store.UpdateNodeWarpCheck(ctx, s.st.DB, n.ID, false, "", "", 0, res.Error)
	return res, nil
}

func (s *Service) tryAutoHealWarp(ctx context.Context, n *store.Node, w *store.NodeWarp, curTransport string) *warp.CheckResult {
	saveHealed := func(res *warp.CheckResult) *warp.CheckResult {
		w.LastCheckAt = time.Now().Unix()
		w.LastCheckOK = true
		w.LastCheckIP = res.IP
		w.LastCheckColo = res.Colo
		w.LastCheckMs = res.RTTMs
		w.LastCheckErr = ""
		_ = s.mutate(ctx, change{
			actor: ActorSystem, action: "node.warp.autoheal", entity: "node",
			entityID: func() int64 { return n.ID },
			event:    EvNodeChanged,
			diff:     map[string]any{"transport": w.Transport, "endpoint": w.Endpoint},
		}, func(q store.DBTX) error {
			return store.UpsertNodeWarp(ctx, q, w)
		})
		return res
	}

	// 1. If MASQUE HTTP/3 (UDP 443) failed, try MASQUE HTTP/2 (TCP 443) with the same enrolled ECDSA key.
	if curTransport == warp.TransportMasqueH3 && warp.IsMasqueKey(w.SecretKey) {
		creds := warpCredsFromStore(w)
		ep := warp.NormalizeEndpointForTransport(w.Endpoint, warp.TransportMasqueH2)
		if res2, err := s.WarpCheck(ctx, n.ID, warp.CheckRequest{
			Transport:      warp.TransportMasqueH2,
			SecretKey:      creds.SecretKey,
			Addresses:      creds.Addresses(),
			PeerPublicKey:  creds.PeerPublicKey,
			Endpoint:       ep,
			MTU:            creds.MTU,
			DomainStrategy: w.DomainStrategy,
		}); err == nil && res2 != nil && res2.OK {
			w.Transport = warp.TransportMasqueH2
			w.Endpoint = ep
			res2.Transport = warp.TransportMasqueH2
			return saveHealed(res2)
		}
		return nil
	}

	// 2. If legacy auto WireGuard failed (UDP 2408 filtered), enroll MASQUE and test h3 -> h2.
	if curTransport == warp.TransportWireGuard {
		newCreds, err := s.provisionWarpCreds(ctx, n, warp.TransportMasqueH3, w.LicenseKey, nil)
		if err != nil || newCreds == nil || !warp.IsMasqueKey(newCreds.SecretKey) {
			return nil
		}
		candidates := []string{warp.TransportMasqueH3, warp.TransportMasqueH2}
		if newCreds.Transport == warp.TransportMasqueH2 {
			candidates = []string{warp.TransportMasqueH2, warp.TransportMasqueH3}
		}
		for _, cand := range candidates {
			ep := warp.NormalizeEndpointForTransport(newCreds.Endpoint, cand)
			resM, cerr := s.WarpCheck(ctx, n.ID, warp.CheckRequest{
				Transport:      cand,
				SecretKey:      newCreds.SecretKey,
				Addresses:      newCreds.Addresses(),
				PeerPublicKey:  newCreds.PeerPublicKey,
				Endpoint:       ep,
				MTU:            newCreds.MTU,
				DomainStrategy: w.DomainStrategy,
			})
			if cerr == nil && resM != nil && resM.OK {
				applyCredsToStore(w, newCreds)
				w.Transport = cand
				w.Endpoint = ep
				resM.Transport = cand
				return saveHealed(resM)
			}
		}
	}
	return nil
}

func (s *Service) provisionWarpCreds(ctx context.Context, n *store.Node, transport, licenseKey string, existing *store.NodeWarp) (*warp.Credentials, error) {
	// Prefer provisioning from the node's own IP when connected and capable.
	if s.WarpProvision != nil && n.Can(CapWarp) && (s.NodeConnected == nil || s.NodeConnected(n.ID)) {
		var prev *warp.Credentials
		if existing != nil {
			prev = warpCredsFromStore(existing)
		}
		if creds, err := s.WarpProvision(ctx, n.ID, transport, licenseKey, prev); err == nil && creds != nil {
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
	if transport == warp.TransportWireGuard {
		return warp.Provision(ctx, hc, "", licenseKey)
	}
	return warp.ProvisionMasque(ctx, hc, "", licenseKey, transport)
}

func applyCredsToStore(w *store.NodeWarp, c *warp.Credentials) {
	if warp.ValidTransports[c.Transport] {
		w.Transport = c.Transport
	} else if warp.IsMasqueKey(c.SecretKey) {
		w.Transport = warp.TransportMasqueH3
	} else {
		w.Transport = warp.TransportWireGuard
	}
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
		w.Endpoint = warp.NormalizeEndpointForTransport(c.Endpoint, w.Transport)
	}
	if c.MTU > 0 {
		w.MTU = c.MTU
	}
	if c.Keepalive > 0 {
		w.Keepalive = c.Keepalive
	}
}

func warpCredsFromStore(w *store.NodeWarp) *warp.Credentials {
	tr := effectiveTransport(w)
	return &warp.Credentials{
		Transport:     tr,
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
		Endpoint:      warp.NormalizeEndpointForTransport(w.Endpoint, tr),
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
						Transport:      creds.Transport,
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
								Transport:      creds.Transport,
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
