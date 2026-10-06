package service

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/vyto4ka/vpn/internal/panel/store"
	"github.com/vyto4ka/vpn/internal/xrayconf"
)

// UserInbounds returns the enabled node inbounds a user can use (through their groups).
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
	access := ResolveAccess(rules, inbounds)
	var out []*store.NodeInbound
	for _, ni := range inbounds {
		if !ni.Enabled {
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

// Link is a ready-to-import client link for one inbound.
type Link struct {
	Tag string
	URL string // empty when the inbound type has no link format yet
	Why string // why URL is empty
}

// UserLinks builds vless:// links for a user. This is a preview until the subscription service
// (roadmap stage 5): only Reality inbounds are supported.
// address, when set, replaces the host in every link (e.g. the server IP while there is no DNS yet).
func (s *Service) UserLinks(ctx context.Context, userID int64, address string) ([]Link, error) {
	u, err := store.GetUser(ctx, s.st.DB, userID)
	if err != nil {
		return nil, err
	}
	inbounds, err := s.UserInbounds(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []Link
	for _, ni := range inbounds {
		link := Link{Tag: ni.Tag}
		r, err := s.renderNodeInbound(ctx, s.st.DB, ni)
		if err != nil {
			link.Why = err.Error()
			out = append(out, link)
			continue
		}
		link.URL, link.Why, err = s.realityLink(ctx, u, ni, r, address)
		if err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, nil
}

func (s *Service) realityLink(ctx context.Context, u *store.User, ni *store.NodeInbound, r *xrayconf.RenderedInbound, address string) (string, string, error) {
	ss, _ := r.Inbound["streamSettings"].(map[string]any)
	if ss["security"] != "reality" || r.Protocol != "vless" {
		return "", "links for this inbound type arrive with subscriptions (stage 5)", nil
	}
	rs, _ := ss["realitySettings"].(map[string]any)
	sni := firstString(rs["serverNames"])
	sid := firstString(rs["shortIds"])
	pbk, _ := r.Values["REALITY_PUBLIC_KEY"].(string)
	node, err := store.GetNode(ctx, s.st.DB, ni.NodeID)
	if err != nil {
		return "", "", err
	}
	host := node.Domain
	if address != "" {
		host = address
	}
	if host == "" {
		if listen, _ := r.Inbound["listen"].(string); listen != "" && listen != "0.0.0.0" {
			host = listen
		}
	}
	if host == "" {
		return "", "node has no domain and listens on all addresses: set a domain or a listen IP", nil
	}
	p, err := store.GetProfile(ctx, s.st.DB, ni.ProfileID)
	if err != nil {
		return "", "", err
	}
	remark, _ := xrayconf.Expand(p.RemarkPattern, r.Values)
	fp := "firefox"
	if t, err := xrayconf.GetTemplate(p.TemplateID); err == nil {
		if v, ok := t.Host["fingerprint"].(string); ok {
			fp = v
		}
	}
	port, _ := r.Inbound["port"].(int)
	q := url.Values{}
	q.Set("type", "tcp")
	q.Set("security", "reality")
	q.Set("encryption", "none")
	q.Set("sni", sni)
	q.Set("fp", fp)
	q.Set("pbk", pbk)
	q.Set("sid", sid)
	if r.Flow != "" {
		q.Set("flow", r.Flow)
	}
	link := fmt.Sprintf("vless://%s@%s?%s#%s", u.UUID, joinHostPort(host, port), q.Encode(), url.PathEscape(remark))
	return link, "", nil
}

func firstString(v any) string {
	if l, ok := v.([]any); ok && len(l) > 0 {
		s, _ := l[0].(string)
		return s
	}
	return ""
}

func joinHostPort(host string, port int) string {
	if ip := parseIPv6(host); ip {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
}

func parseIPv6(h string) bool {
	for _, c := range h {
		if c == ':' {
			return true
		}
	}
	return false
}
