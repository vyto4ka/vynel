package service

import (
	"context"
	"sort"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// GatewayAccess says who may reach the node gateway port. Nodes connect from the addresses the
// gateway saw them on; the port is open to everyone only while that is not enough: an install
// token is waiting (a new node joins from an address nobody knows yet) or a node has not
// connected since the panel started recording addresses.
type GatewayAccess struct {
	Open bool     `json:"open"`
	Why  string   `json:"why,omitempty"`
	IPs  []string `json:"ips"`
}

// GatewayAccess computes the gateway allowlist.
func (s *Service) GatewayAccess(ctx context.Context) (GatewayAccess, error) {
	q := s.st.DB
	nodes, err := store.ListNodes(ctx, q)
	if err != nil {
		return GatewayAccess{}, err
	}
	acc := GatewayAccess{IPs: []string{}}
	seen := map[string]bool{}
	for _, n := range nodes {
		if n.Local {
			continue
		}
		if len(n.PeerIPs) == 0 && n.CertSerial != "" && !acc.Open {
			acc.Open, acc.Why = true, "нода "+n.Code+" ещё не подключалась с тех пор, как панель запоминает адреса"
		}
		for _, ip := range n.PeerIPs {
			if !seen[ip] {
				seen[ip] = true
				acc.IPs = append(acc.IPs, ip)
			}
		}
	}
	sort.Strings(acc.IPs)
	if pending, err := store.PendingInstallTokens(ctx, q); err != nil {
		return GatewayAccess{}, err
	} else if pending > 0 {
		acc.Open, acc.Why = true, "есть неиспользованный токен установки ноды"
	}
	return acc, nil
}
