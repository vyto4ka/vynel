package webapi

import (
	"net/http"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// Infrastructure billing (docs/USER_GUIDE.md §9.1).

func (s *Server) billingRoutes() {
	s.handle("GET /api/billing", s.billing)
	s.handle("POST /api/providers", s.saveProvider)
	s.handle("PUT /api/providers/{id}", s.saveProvider)
	s.handle("DELETE /api/providers/{id}", s.deleteProvider)
	s.handle("PATCH /api/nodes/{id}/billing", s.nodeBilling)
}

// Provider is a hosting provider as the UI sees it.
type Provider struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	LoginURL string `json:"loginUrl"`
	Icon     string `json:"icon"`
}

func providerDTO(p *store.Provider) Provider {
	return Provider{ID: p.ID, Name: p.Name, URL: p.URL, LoginURL: p.LoginURL, Icon: p.Icon}
}

// billing serves the «Биллинг» page: GET /api/billing.
func (s *Server) billing(r *http.Request) (any, error) {
	ctx := r.Context()
	ps, err := s.svc.Providers(ctx)
	if err != nil {
		return nil, err
	}
	providers := make([]Provider, 0, len(ps))
	for _, p := range ps {
		providers = append(providers, providerDTO(p))
	}
	bills, err := s.svc.BillingOverview(ctx)
	if err != nil {
		return nil, err
	}
	nodes := make([]map[string]any, 0, len(bills))
	for _, nb := range bills {
		n, b := nb.Node, nb.Billing
		nodes = append(nodes, map[string]any{
			"id": n.ID, "code": n.Code, "name": n.Name, "flag": xrayconf.CountryFlag(n.Country), "enabled": n.Enabled,
			"providerId": b.ProviderID, "cost": b.Cost, "currency": b.Currency, "costPeriod": b.CostPeriod, "monthlyCost": nb.MonthlyCost,
			"paidUntil": b.PaidUntil, "trafficLimit": b.TrafficLimit, "resetDay": b.ResetDay, "trafficCount": b.TrafficCount,
			"used": nb.Used, "periodFrom": nb.PeriodFrom, "periodTo": nb.PeriodTo, "offByLimit": nb.OffByLimit,
			"overridden": b.OverridePeriod == nb.PeriodFrom && b.OverridePeriod != 0,
		})
	}
	return map[string]any{"providers": providers, "nodes": nodes, "margin": service.LimitMargin,
		"remindDays": int(service.PayReminder.Hours() / 24)}, nil
}

type providerReq struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	LoginURL string `json:"loginUrl"`
	Refresh  bool   `json:"refresh"` // fetch the name and the icon from the site again
}

// saveProvider creates (POST /api/providers) or edits (PUT /api/providers/{id}) a provider.
func (s *Server) saveProvider(r *http.Request) (any, error) {
	var req providerReq
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	in := service.ProviderInput{Name: req.Name, URL: req.URL, LoginURL: req.LoginURL}
	var p *store.Provider
	var err error
	if r.PathValue("id") == "" {
		p, err = s.svc.CreateProvider(r.Context(), actor(r), in)
	} else {
		id, perr := pathID(r, "id")
		if perr != nil {
			return nil, perr
		}
		p, err = s.svc.UpdateProvider(r.Context(), actor(r), id, in, req.Refresh)
	}
	if err != nil {
		return nil, err
	}
	return providerDTO(p), nil
}

func (s *Server) deleteProvider(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return nil, s.svc.DeleteProvider(r.Context(), actor(r), id)
}

type billingReq struct {
	ProviderID   *int64   `json:"providerId"`
	Cost         *float64 `json:"cost"`
	Currency     *string  `json:"currency"`
	CostPeriod   *string  `json:"costPeriod"`
	PaidUntil    *int64   `json:"paidUntil"`
	TrafficLimit *int64   `json:"trafficLimit"`
	ResetDay     *int     `json:"resetDay"`
	TrafficCount *string  `json:"trafficCount"`
}

// nodeBilling changes a node's provider, cost or traffic limit: PATCH /api/nodes/{id}/billing.
func (s *Server) nodeBilling(r *http.Request) (any, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	var req billingReq
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	_, err = s.svc.SetNodeBilling(r.Context(), actor(r), id, service.NodeBillingInput{ProviderID: req.ProviderID, Cost: req.Cost,
		Currency: req.Currency, CostPeriod: req.CostPeriod, PaidUntil: req.PaidUntil, TrafficLimit: req.TrafficLimit,
		ResetDay: req.ResetDay, TrafficCount: req.TrafficCount})
	return nil, err
}
