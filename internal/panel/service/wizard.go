package service

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// The setup wizard's steps. The node's place in it is derived from its data, not stored:
// no profiles = step 2; node input variables missing = step 3; requirements not confirmed
// = step 4. A local node is set up from the start.
const (
	WizardDone     = 0
	WizardProfiles = 2
	WizardVars     = 3
	WizardChecks   = 4
)

// WizardStep is one requirement of the node's templates as the wizard shows it.
type WizardStep struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// nodeWizard inspects the node's profiles and templates inside a transaction.
func (s *Service) nodeWizard(ctx context.Context, q store.DBTX, n *store.Node) (step int, tpls []*xrayconf.Template, ports []string, dns []string, sysctl []string, missing []string, hasProfiles bool, err error) {
	nis, err := store.ListNodeInbounds(ctx, q, n.ID, 0)
	if err != nil {
		return 0, nil, nil, nil, nil, nil, false, err
	}
	hasProfiles = len(nis) > 0
	seen := map[string]bool{}
	missRe := regexp.MustCompile(`^template [^:]+: missing values for (.+)$`)
	for _, ni := range nis {
		p, err := store.GetProfile(ctx, q, ni.ProfileID)
		if err != nil {
			return 0, nil, nil, nil, nil, nil, false, err
		}
		tpl, err := s.template(ctx, p.TemplateID)
		if err != nil {
			return 0, nil, nil, nil, nil, nil, false, err
		}
		if !seen[tpl.ID] {
			seen[tpl.ID] = true
			tpls = append(tpls, tpl)
		}
		// Input variables the node does not have yet: the same "missing values" the
		// renderer reports, plus the ones a node.domain default would cover (empty domain).
		if _, err := s.renderNodeInbound(ctx, q, ni); err != nil {
			if m := missRe.FindStringSubmatch(err.Error()); m != nil {
				missing = append(missing, strings.Split(m[1], ", ")...)
			}
		}
		for _, v := range tpl.Variables {
			if v.Scope == xrayconf.ScopeNode && v.Source == xrayconf.SourceInput && !v.Optional && v.DefaultFrom == "node.domain" && n.Domain == "" {
				missing = append(missing, v.Name)
			}
		}
	}
	for _, tpl := range tpls {
		for _, p := range portStrings(tpl.Requirements["ports"]) {
			if !seen[p] {
				seen[p] = true
				ports = append(ports, p)
			}
		}
		for _, name := range dnsNames(tpl.Requirements["dns"]) {
			if !seen[name] {
				seen[name] = true
				dns = append(dns, name)
			}
		}
		for _, sc := range stringSlice(tpl.Requirements["sysctl"]) {
			if !seen[sc] {
				seen[sc] = true
				sysctl = append(sysctl, sc)
			}
		}
	}
	sort.Strings(ports)
	sort.Strings(dns)
	switch {
	case n.Local:
		return WizardDone, tpls, ports, dns, sysctl, nil, hasProfiles, nil
	case !hasProfiles:
		return WizardProfiles, tpls, ports, dns, sysctl, nil, hasProfiles, nil
	case len(missing) > 0:
		return WizardVars, tpls, ports, dns, sysctl, missing, hasProfiles, nil
	case n.RequirementsConfirmedAt == 0:
		return WizardChecks, tpls, ports, dns, sysctl, nil, hasProfiles, nil
	}
	return WizardDone, tpls, ports, dns, sysctl, nil, hasProfiles, nil
}

// portStrings formats the template requirements' ports list: "443/tcp" or plain "443".
func portStrings(v any) []string {
	var out []string
	for _, p := range stringSlice(v) {
		if strings.Contains(p, "/") {
			out = append(out, p)
		} else if p != "" {
			out = append(out, p+"/tcp")
		}
	}
	return out
}

// dnsNames lists the domains of the requirements' DNS records with ${NODE_DOMAIN} resolved.
func dnsNames(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, e := range list {
		m, _ := e.(map[string]any)
		name, _ := m["name"].(string)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// stringSlice turns a decoded YAML list into strings, numbers included (yaml 443 → int).
func stringSlice(v any) []string {
	var out []string
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		for _, e := range list {
			switch s := e.(type) {
			case string:
				if s != "" {
					out = append(out, s)
				}
			case int:
				out = append(out, fmt.Sprint(s))
			}
		}
	}
	return out
}

// NodeSetupStep returns the node's derived wizard step (0 = finished) and the count of
// remaining steps for the node card.
func (s *Service) NodeSetupStep(ctx context.Context, id int64) (int, error) {
	var step int
	err := s.st.Tx(ctx, func(q store.DBTX) error {
		n, err := store.GetNode(ctx, q, id)
		if err != nil {
			return err
		}
		step, _, _, _, _, _, _, err = s.nodeWizard(ctx, q, n)
		return err
	})
	return step, err
}

// NodeWizard describes the node's setup wizard: where it stands and the requirement checks
// of the profiles the node already has (ports, DNS A records, sysctl).
func (s *Service) NodeWizard(ctx context.Context, id int64) (step int, steps []WizardStep, err error) {
	err = s.st.Tx(ctx, func(q store.DBTX) error {
		n, err := store.GetNode(ctx, q, id)
		if err != nil {
			return err
		}
		st, _, ports, dns, sysctl, _, _, err := s.nodeWizard(ctx, q, n)
		if err != nil {
			return err
		}
		step = st
		steps = []WizardStep{}
		if n.Manual {
			steps = append(steps, WizardStep{Key: "manual", Title: "Manual node: check ports, DNS and sysctl on the server yourself",
				State: StepSkip, Detail: "the panel manages only the Xray config"})
		} else {
			for _, p := range ports {
				steps = append(steps, WizardStep{Key: "port " + p, Title: "Port " + p + " is free on the server and open in the firewall",
					State: StepSkip, Detail: "check on the server: ss -ltnup | grep :" + p[:strings.IndexByte(p, '/')]})
			}
			var want []string
			for _, a := range mustListAddresses(ctx, q, n.ID) {
				want = append(want, a.IP)
			}
			for _, name := range dns {
				name = strings.ReplaceAll(name, "${NODE_DOMAIN}", n.Domain)
				if n.Domain == "" {
					steps = append(steps, WizardStep{Key: "dns " + name, Title: "A-запись " + name + " → IP сервера", State: StepFail,
						Detail: "the node has no domain — set it in the node’s settings"})
					continue
				}
				detail, state := "", StepOK
				lctx, cancel := context.WithTimeout(ctx, 4*time.Second)
				ips, err := Resolver.LookupHost(lctx, name)
				cancel()
				switch {
				case err != nil:
					detail, state = name+" is not found in DNS: add an A record to the server’s IP", StepFail
				case len(want) > 0 && !containsAny(ips, want):
					detail, state = name+" → "+strings.Join(ips, ", ")+", but the server has "+strings.Join(want, ", ")+" (in Cloudflare the cloud must be grey)", StepFail
				default:
					detail = name + " → " + strings.Join(ips, ", ")
				}
				steps = append(steps, WizardStep{Key: "dns " + name, Title: "A-запись " + name + " → IP сервера", State: state, Detail: detail})
			}
			steps = append(steps, WizardStep{Key: "sysctl", Title: "sysctl: " + strings.Join(sysctl, ", "), State: StepSkip,
				Detail: "the vynel installer enables them itself; with your own setup, do it manually"})
		}
		return nil
	})
	return step, steps, err
}

// containsAny reports whether any of ips is in want.
func containsAny(ips, want []string) bool {
	for _, ip := range ips {
		for _, w := range want {
			if ip == w {
				return true
			}
		}
	}
	return false
}

// mustListAddresses is ListAddresses with the error propagated by the caller's transaction.
func mustListAddresses(ctx context.Context, q store.DBTX, nodeID int64) []*store.Address {
	list, err := store.ListAddresses(ctx, q, nodeID)
	if err != nil {
		panic(err) // unwinds to the transaction's error return
	}
	return list
}

// ConfirmNodeRequirements records that the admin confirmed the node's requirements
// (ts = 0 unconfirms).
func (s *Service) ConfirmNodeRequirements(ctx context.Context, actor Actor, id, ts int64) error {
	return s.mutate(ctx, change{actor: actor, action: "node.requirements", entity: "node", entityID: func() int64 { return id },
		event: EvNodeChanged, diff: map[string]int64{"requirements_confirmed_at": ts}},
		func(q store.DBTX) error {
			return store.SetNodeRequirementsConfirmed(ctx, q, id, ts)
		})
}
