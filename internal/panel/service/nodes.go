package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/vyto4ka/vpn/internal/panel/store"
)

// InstallTokenTTL is how long a node install token stays valid (docs/ARCHITECTURE.md §6.1).
const InstallTokenTTL = 24 * time.Hour

var codeRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,9}$`)

// NodeInput creates or edits a node.
type NodeInput struct {
	Name    string
	Code    string // optional; derived from Country
	Country string // ISO alpha-2
	Domain  string
	Local   bool
}

// CreateNode adds a node and returns it with a fresh install token (shown once).
func (s *Service) CreateNode(ctx context.Context, actor Actor, in NodeInput) (*store.Node, string, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Country = strings.ToUpper(strings.TrimSpace(in.Country))
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	if in.Name == "" {
		return nil, "", invalid("node name is required")
	}
	if in.Country != "" && !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(in.Country) {
		return nil, "", invalid("country must be a 2-letter code")
	}
	n := &store.Node{Name: in.Name, Country: in.Country, Domain: strings.ToLower(strings.TrimSpace(in.Domain)), Local: in.Local, Enabled: true}
	var token string
	err := s.mutate(ctx, change{actor: actor, action: "node.create", entity: "node", entityID: idOf(&n.ID), event: EvNodeChanged, diff: in},
		func(q store.DBTX) error {
			code, err := s.pickNodeCode(ctx, q, in.Code, in.Country)
			if err != nil {
				return err
			}
			n.Code = code
			if err := store.CreateNode(ctx, q, n); err != nil {
				return err
			}
			token, err = s.newInstallToken(ctx, q, n.ID)
			return err
		})
	if err != nil {
		return nil, "", err
	}
	return n, token, nil
}

func (s *Service) pickNodeCode(ctx context.Context, q store.DBTX, want, country string) (string, error) {
	if want != "" {
		if !codeRe.MatchString(want) {
			return "", invalid("node code must be 1-10 latin letters/digits starting with a letter")
		}
		if _, err := store.GetNodeByCode(ctx, q, want); err == nil {
			return "", fmt.Errorf("%w: node code %s", ErrConflict, want)
		}
		return want, nil
	}
	base := country
	if base == "" {
		base = "N"
	}
	for i := 1; i < 1000; i++ {
		code := base
		if i > 1 {
			code = fmt.Sprintf("%s%d", base, i)
		}
		if _, err := store.GetNodeByCode(ctx, q, code); errors.Is(err, store.ErrNotFound) {
			return code, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no free node code for %s", base)
}

// UpdateNode edits a node.
func (s *Service) UpdateNode(ctx context.Context, actor Actor, id int64, in NodeInput, enabled bool) (*store.Node, error) {
	var n *store.Node
	err := s.mutate(ctx, change{actor: actor, action: "node.update", entity: "node", entityID: func() int64 { return id }, event: EvNodeChanged, diff: in},
		func(q store.DBTX) error {
			var err error
			if n, err = store.GetNode(ctx, q, id); err != nil {
				return err
			}
			if in.Name != "" {
				n.Name = strings.TrimSpace(in.Name)
			}
			if in.Code != "" && strings.ToUpper(in.Code) != n.Code {
				if n.Code, err = s.pickNodeCode(ctx, q, strings.ToUpper(in.Code), ""); err != nil {
					return err
				}
			}
			if in.Country != "" {
				n.Country = strings.ToUpper(in.Country)
			}
			n.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
			n.Enabled = enabled
			return store.UpdateNode(ctx, q, n)
		})
	return n, err
}

// DeleteNode removes a node, its inbounds and the access rules pointing at them.
func (s *Service) DeleteNode(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "node.delete", entity: "node", entityID: func() int64 { return id }, event: EvNodeDeleted},
		func(q store.DBTX) error {
			inbounds, err := store.ListNodeInbounds(ctx, q, id, 0)
			if err != nil {
				return err
			}
			for _, ni := range inbounds {
				if err := store.DeleteAccessRulesByRef(ctx, q, store.AccessNodeInbound, ni.ID); err != nil {
					return err
				}
			}
			if err := store.DeleteAccessRulesByRef(ctx, q, store.AccessNode, id); err != nil {
				return err
			}
			return store.DeleteNode(ctx, q, id)
		})
}

// ReissueInstallToken revokes the node's certificate and returns a new install token.
func (s *Service) ReissueInstallToken(ctx context.Context, actor Actor, nodeID int64) (string, error) {
	var token string
	err := s.mutate(ctx, change{actor: actor, action: "node.reissue_token", entity: "node", entityID: func() int64 { return nodeID }, event: EvNodeChanged},
		func(q store.DBTX) error {
			if _, err := store.GetNode(ctx, q, nodeID); err != nil {
				return err
			}
			if err := store.SetNodeCert(ctx, q, nodeID, ""); err != nil {
				return err
			}
			var err error
			token, err = s.newInstallToken(ctx, q, nodeID)
			return err
		})
	return token, err
}

func (s *Service) newInstallToken(ctx context.Context, q store.DBTX, nodeID int64) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, store.CreateInstallToken(ctx, q, nodeID, HashToken(token), s.now().Add(InstallTokenTTL).Unix())
}

// HashToken is how secrets are stored (sha256 hex).
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ConsumeInstallToken validates a one-time install token and binds certSerial to its node.
// sign is called inside the transaction with the node to produce the certificate serial,
// so a failed signature leaves the token unused.
func (s *Service) ConsumeInstallToken(ctx context.Context, token string, sign func(*store.Node) (serial string, err error)) (*store.Node, error) {
	var node *store.Node
	err := s.mutate(ctx, change{actor: ActorSystem, action: "node.join", entity: "node", entityID: func() int64 { return node.ID }, event: EvNodeChanged},
		func(q store.DBTX) error {
			t, err := store.GetInstallToken(ctx, q, HashToken(token))
			if errors.Is(err, store.ErrNotFound) {
				return invalid("unknown install token")
			} else if err != nil {
				return err
			}
			if t.UsedAt != nil {
				return invalid("install token already used")
			}
			if s.now().Unix() > t.ExpiresAt {
				return invalid("install token expired")
			}
			if node, err = store.GetNode(ctx, q, t.NodeID); err != nil {
				return err
			}
			if err := store.MarkInstallTokenUsed(ctx, q, t.ID); err != nil {
				return err
			}
			serial, err := sign(node)
			if err != nil {
				return err
			}
			node.CertSerial = serial
			return store.SetNodeCert(ctx, q, node.ID, serial)
		})
	if err != nil {
		return nil, err
	}
	return node, nil
}

// ReportedAddress is an address the agent found on the node's interfaces.
type ReportedAddress struct {
	IP        string
	Interface string
	Primary   bool
}

// SyncAddresses records the addresses an agent reports. Private addresses are kept (a node behind
// NAT listens on them); loopback and link-local ones are skipped. Addresses added by hand that the
// agent does not see stay, flagged on_interface=false (docs/INBOUNDS.md §2.2).
func (s *Service) SyncAddresses(ctx context.Context, nodeID int64, reported []ReportedAddress) error {
	return s.st.Tx(ctx, func(q store.DBTX) error {
		present := make([]int64, 0, len(reported))
		for _, r := range reported {
			ip := net.ParseIP(r.IP)
			if ip == nil || !ip.IsGlobalUnicast() {
				continue
			}
			a := &store.Address{NodeID: nodeID, IP: ip.String(), Family: family(ip), Interface: r.Interface, OnInterface: true, IsPrimary: r.Primary}
			if err := store.UpsertAddress(ctx, q, a); err != nil {
				return err
			}
			present = append(present, a.ID)
		}
		if err := store.MarkAddressesMissing(ctx, q, nodeID, present); err != nil {
			return err
		}
		for _, r := range reported {
			if r.Primary {
				if _, err := q.ExecContext(ctx, `UPDATE node_addresses SET is_primary=1 WHERE node_id=? AND ip=?`, nodeID, net.ParseIP(r.IP).String()); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// AddAddress registers an address by hand (e.g. before it is configured on the interface).
func (s *Service) AddAddress(ctx context.Context, actor Actor, nodeID int64, ipStr, label string) (*store.Address, error) {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return nil, invalid("%q is not an IP address", ipStr)
	}
	a := &store.Address{NodeID: nodeID, IP: ip.String(), Family: family(ip), Label: label}
	err := s.mutate(ctx, change{actor: actor, action: "address.add", entity: "node", entityID: func() int64 { return nodeID }, event: EvNodeChanged, diff: map[string]string{"ip": a.IP}},
		func(q store.DBTX) error {
			if _, err := store.GetNode(ctx, q, nodeID); err != nil {
				return err
			}
			return store.UpsertAddress(ctx, q, a)
		})
	return a, err
}

func family(ip net.IP) string {
	if ip.To4() != nil {
		return "v4"
	}
	return "v6"
}

// Nodes lists nodes.
func (s *Service) Nodes(ctx context.Context) ([]*store.Node, error) {
	return store.ListNodes(ctx, s.st.DB)
}

// Node loads a node by id.
func (s *Service) Node(ctx context.Context, id int64) (*store.Node, error) {
	return store.GetNode(ctx, s.st.DB, id)
}

// NodeByCode loads a node by code.
func (s *Service) NodeByCode(ctx context.Context, code string) (*store.Node, error) {
	return store.GetNodeByCode(ctx, s.st.DB, strings.ToUpper(code))
}

// Addresses lists a node's addresses.
func (s *Service) Addresses(ctx context.Context, nodeID int64) ([]*store.Address, error) {
	return store.ListAddresses(ctx, s.st.DB, nodeID)
}

// EnsureLocalNode returns the node that runs inside the panel process, creating it on first use.
func (s *Service) EnsureLocalNode(ctx context.Context, in NodeInput) (*store.Node, error) {
	nodes, err := store.ListNodes(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.Local {
			return n, nil
		}
	}
	in.Local = true
	if in.Name == "" {
		in.Name = "Сервер панели"
	}
	n, _, err := s.CreateNode(ctx, ActorSystem, in)
	return n, err
}
