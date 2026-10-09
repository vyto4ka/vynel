package service

import (
	"context"
	"crypto/rand"
	"math/big"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// Role is what an address is used for (docs/INBOUNDS.md §2.2): roles are not stored, they
// follow from the settings and the inbounds bound to the address.
type Role struct {
	Kind string `json:"kind"`          // panel | sub | gateway | inbound | egress
	Tag  string `json:"tag,omitempty"` // inbound tag
	All  bool   `json:"all,omitempty"` // bound to every address of the node, not this one only
}

// NetAddress is a node address with its roles.
type NetAddress struct {
	ID          int64  `json:"id"`
	IP          string `json:"ip"`
	Interface   string `json:"interface"`
	OnInterface bool   `json:"onInterface"`
	Primary     bool   `json:"primary"`
	Roles       []Role `json:"roles"`
}

// NetNode is a node and its addresses.
type NetNode struct {
	ID        int64        `json:"id"`
	Code      string       `json:"code"`
	Name      string       `json:"name"`
	Country   string       `json:"country"`
	Domain    string       `json:"domain"`
	Local     bool         `json:"local"`
	Addresses []NetAddress `json:"addresses"`
	AllRoles  []Role       `json:"allRoles"` // roles bound to every address (0.0.0.0)
}

// DomainCheck tells whether a domain points at the server that serves it.
type DomainCheck struct {
	Name     string   `json:"name"`
	Purpose  string   `json:"purpose"` // sub | panel | node
	Node     string   `json:"node"`
	Resolved []string `json:"resolved"`
	Expected []string `json:"expected"`
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"`
}

// Network is the addresses page: nodes with their address roles and the domain checks.
type Network struct {
	Nodes   []NetNode     `json:"nodes"`
	Domains []DomainCheck `json:"domains"`
}

// Resolver looks domains up; tests replace it.
var Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
} = net.DefaultResolver

// Network collects address roles of every node and checks the DNS of every domain.
func (s *Service) Network(ctx context.Context) (*Network, error) {
	nodes, err := store.ListNodes(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	addrs, err := store.ListAddresses(ctx, s.st.DB, 0)
	if err != nil {
		return nil, err
	}
	inbounds, err := store.ListNodeInbounds(ctx, s.st.DB, 0, 0)
	if err != nil {
		return nil, err
	}
	get := func(k, def string) string { v, _ := s.Setting(ctx, k, def); return strings.TrimSpace(v) }
	subIP, webIP := get(SettingSubAddress, ""), get(SettingWebAddress, "")
	if webIP == "" {
		webIP = subIP
	}
	gwHost, _, _ := net.SplitHostPort(get(SettingGatewayAddr, ""))
	subDomain, webDomain := strings.ToLower(get(SettingSubDomain, "")), strings.ToLower(get(SettingWebDomain, ""))

	out := &Network{Nodes: []NetNode{}, Domains: []DomainCheck{}}
	byID := map[int64]int{}
	for _, n := range nodes {
		nn := NetNode{ID: n.ID, Code: n.Code, Name: n.Name, Country: n.Country, Domain: n.Domain, Local: n.Local, Addresses: []NetAddress{}, AllRoles: []Role{}}
		for _, a := range addrs {
			if a.NodeID == n.ID {
				nn.Addresses = append(nn.Addresses, NetAddress{ID: a.ID, IP: a.IP, Interface: a.Interface, OnInterface: a.OnInterface, Primary: a.IsPrimary, Roles: []Role{}})
			}
		}
		byID[n.ID] = len(out.Nodes)
		out.Nodes = append(out.Nodes, nn)
	}
	// bind puts a role on the address with that IP (or id), or on every address when unbound.
	bind := func(nn *NetNode, ip string, id *int64, r Role) {
		if ip == "" && id == nil {
			r.All = true
			nn.AllRoles = append(nn.AllRoles, r)
			return
		}
		for i := range nn.Addresses {
			a := &nn.Addresses[i]
			if (id != nil && a.ID == *id) || (ip != "" && a.IP == ip) {
				a.Roles = append(a.Roles, r)
			}
		}
	}
	for i := range out.Nodes {
		nn := &out.Nodes[i]
		if !nn.Local {
			continue
		}
		bind(nn, webIP, nil, Role{Kind: "panel"})
		bind(nn, subIP, nil, Role{Kind: "sub"})
		if gwHost != "" && net.ParseIP(gwHost) != nil {
			bind(nn, gwHost, nil, Role{Kind: "gateway"})
		} else {
			bind(nn, "", nil, Role{Kind: "gateway"})
		}
	}
	for _, ni := range inbounds {
		i, ok := byID[ni.NodeID]
		if !ok || !ni.Enabled {
			continue
		}
		nn := &out.Nodes[i]
		bind(nn, "", ni.ListenAddressID, Role{Kind: "inbound", Tag: ni.Tag})
		egress := ni.EgressAddressID
		if egress == nil {
			egress = ni.ListenAddressID
		}
		bind(nn, "", egress, Role{Kind: "egress", Tag: ni.Tag})
	}

	// Domains: the subscription and panel domains point at the panel's server (at the bound IP
	// when there is one), a node's domain at that node.
	expect := func(nn *NetNode, ip string) []string {
		if ip != "" {
			return []string{ip}
		}
		var ips []string
		for _, a := range nn.Addresses {
			ips = append(ips, a.IP)
		}
		return ips
	}
	var local *NetNode
	for i := range out.Nodes {
		if out.Nodes[i].Local {
			local = &out.Nodes[i]
		}
	}
	if local != nil {
		if subDomain != "" {
			out.Domains = append(out.Domains, DomainCheck{Name: subDomain, Purpose: "sub", Node: local.Code, Expected: expect(local, subIP)})
		}
		if webDomain != "" && webDomain != subDomain {
			out.Domains = append(out.Domains, DomainCheck{Name: webDomain, Purpose: "panel", Node: local.Code, Expected: expect(local, webIP)})
		}
	}
	for i := range out.Nodes {
		nn := &out.Nodes[i]
		if d := strings.ToLower(nn.Domain); d != "" && !slices.ContainsFunc(out.Domains, func(c DomainCheck) bool { return c.Name == d }) {
			out.Domains = append(out.Domains, DomainCheck{Name: d, Purpose: "node", Node: nn.Code, Expected: expect(nn, "")})
		}
	}
	var wg sync.WaitGroup
	for i := range out.Domains {
		wg.Add(1)
		go func(c *DomainCheck) {
			defer wg.Done()
			lctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			ips, err := Resolver.LookupHost(lctx, c.Name)
			c.Resolved = ips
			if c.Resolved == nil {
				c.Resolved = []string{}
			}
			if c.Expected == nil {
				c.Expected = []string{}
			}
			switch {
			case err != nil:
				c.Error = "не находится в DNS"
			case len(c.Expected) == 0:
				c.OK = true // the node has not reported its addresses yet
			default:
				for _, ip := range ips {
					if slices.Contains(c.Expected, ip) {
						c.OK = true
					}
				}
				if !c.OK {
					c.Error = "указывает не на этот сервер"
				}
			}
		}(&out.Domains[i])
	}
	wg.Wait()
	return out, nil
}

// LocalIPs lists the addresses of the panel's own server.
func (s *Service) LocalIPs(ctx context.Context) ([]string, error) {
	nodes, err := store.ListNodes(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	var ips []string
	for _, n := range nodes {
		if !n.Local {
			continue
		}
		as, err := store.ListAddresses(ctx, s.st.DB, n.ID)
		if err != nil {
			return nil, err
		}
		for _, a := range as {
			ips = append(ips, a.IP)
		}
	}
	return ips, nil
}

// Stealth moves the panel to a random high port and/or a new secret path and returns the
// new address (docs/STEALTH.md §1).
func (s *Service) Stealth(ctx context.Context, actor Actor, port, path bool) (string, error) {
	if !port && !path {
		return "", invalid("nothing to change")
	}
	if port {
		subPort, _ := s.Setting(ctx, SettingSubPort, "443")
		var p int64
		for {
			n, err := rand.Int(rand.Reader, big.NewInt(40000))
			if err != nil {
				return "", err
			}
			p = 20000 + n.Int64()
			if strconv.FormatInt(p, 10) != subPort {
				break
			}
		}
		if err := s.SetSetting(ctx, actor, SettingWebPort, strconv.FormatInt(p, 10)); err != nil {
			return "", err
		}
	}
	if path {
		r, err := randomString(12)
		if err != nil {
			return "", err
		}
		if err := s.SetSetting(ctx, actor, SettingWebPath, "/"+r+"/"); err != nil {
			return "", err
		}
	}
	return s.WebURL(ctx)
}
