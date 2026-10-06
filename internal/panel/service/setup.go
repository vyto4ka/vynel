package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vyto4ka/vpn/internal/panel/store"
)

// SetupInput describes an all-in-one server (docs/ALL_IN_ONE.md).
type SetupInput struct {
	NodeName  string
	Country   string
	Domain    string // node domain: Reality SNI + self-steal site
	SubDomain string // subscription domain; empty = Domain (the one-domain minimum)
	Email     string // ACME email, optional
}

// SetupResult tells what Setup did.
type SetupResult struct {
	Node    *store.Node
	Profile *store.Profile
	Inbound *store.NodeInbound
	Created []string
}

// RealityProfileName is the profile Setup creates.
const RealityProfileName = "Reality"

// SetupAllInOne makes the local node serve Reality self-steal with subscriptions on the same
// server. It is idempotent: running it again only fills in what is missing and updates domains.
func (s *Service) SetupAllInOne(ctx context.Context, actor Actor, in SetupInput) (*SetupResult, error) {
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
	in.SubDomain = strings.ToLower(strings.TrimSpace(in.SubDomain))
	if in.Domain == "" {
		return nil, invalid("a domain is required (A record to this server)")
	}
	if in.SubDomain == "" {
		in.SubDomain = in.Domain
	}
	res := &SetupResult{}
	node, err := s.EnsureLocalNode(ctx, NodeInput{Name: in.NodeName, Country: in.Country, Domain: in.Domain})
	if err != nil {
		return nil, err
	}
	if node.Domain != in.Domain {
		if node, err = s.UpdateNode(ctx, actor, node.ID, NodeInput{Domain: in.Domain}, node.Enabled); err != nil {
			return nil, err
		}
	}
	res.Node = node

	settings := map[string]string{SettingSubDomain: in.SubDomain}
	if in.Email != "" {
		settings[SettingCaddyEmail] = in.Email
	}
	for k, v := range settings {
		if cur, _ := s.Setting(ctx, k, ""); cur != v {
			if err := s.SetSetting(ctx, actor, k, v); err != nil {
				return nil, err
			}
		}
	}

	prof, err := s.ProfileByName(ctx, RealityProfileName)
	if errors.Is(err, store.ErrNotFound) {
		if prof, err = s.CreateProfile(ctx, actor, ProfileInput{Name: RealityProfileName, TemplateID: "vless-reality-selfsteal"}); err != nil {
			return nil, err
		}
		res.Created = append(res.Created, "profile "+RealityProfileName)
		groups, err := s.Groups(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			if g.Name == "Основная" {
				if err := s.GrantAccess(ctx, actor, g.ID, store.AccessProfile, prof.ID); err != nil {
					return nil, err
				}
			}
		}
	} else if err != nil {
		return nil, err
	}
	res.Profile = prof

	inbounds, err := s.NodeInbounds(ctx, node.ID)
	if err != nil {
		return nil, err
	}
	for _, ni := range inbounds {
		if ni.ProfileID == prof.ID {
			res.Inbound = ni
		}
	}
	if res.Inbound == nil {
		if res.Inbound, err = s.AttachProfile(ctx, actor, AttachInput{NodeID: node.ID, ProfileID: prof.ID}); err != nil {
			return nil, fmt.Errorf("attach %s: %w", RealityProfileName, err)
		}
		res.Created = append(res.Created, "inbound "+res.Inbound.Tag)
	}
	return res, nil
}
