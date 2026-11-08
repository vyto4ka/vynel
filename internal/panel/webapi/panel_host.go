package webapi

import (
	"context"
	"net/http"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

type PanelBilling struct {
	*store.PanelBilling
	Provider    *Provider `json:"provider"`
	MonthlyCost float64   `json:"monthlyCost"`
	LocalNodeID *int64    `json:"localNodeId"`
}

func (s *Server) panelBillingDTO(ctx context.Context) (*PanelBilling, error) {
	p, err := s.svc.PanelBilling(ctx)
	if err != nil {
		return nil, err
	}
	out := &PanelBilling{PanelBilling: p.Billing, MonthlyCost: p.MonthlyCost, LocalNodeID: p.LocalNodeID}
	if p.Provider != nil {
		pd := providerDTO(p.Provider)
		out.Provider = &pd
	}
	return out, nil
}

func (s *Server) panelBilling(r *http.Request) (any, error) {
	var in service.PanelBillingInput
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if _, err := s.svc.SetPanelBilling(r.Context(), actor(r), in); err != nil {
		return nil, err
	}
	return s.panelBillingDTO(r.Context())
}
