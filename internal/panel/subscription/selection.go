package subscription

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// ServerSummary contains no credentials or raw subscription links.
type ServerSummary struct {
	Name     string `json:"name"`
	Node     string `json:"node"`
	Protocol string `json:"protocol"`
	Network  string `json:"network"`
	Excluded string `json:"excluded,omitempty"` // hidden | xhttp | raw | format
}

type Selection struct {
	Format  Format                `json:"format"`
	Allowed bool                  `json:"allowed"`
	Reason  string                `json:"reason"`
	HWID    service.DeviceVerdict `json:"hwid"`
	Servers []ServerSummary       `json:"servers"`
	Hosts   []service.Host        `json:"-"`
	Headers []HeaderValue         `json:"-"`
}

func deviceHeaders(v service.DeviceVerdict) []HeaderValue {
	var hs []HeaderValue
	if !v.Off {
		hs = append(hs, HeaderValue{Name: "x-hwid-active", Value: "true"})
		if v.NoHWID && !v.Links {
			hs = append(hs, HeaderValue{Name: "x-hwid-not-supported", Value: "true"})
		}
		if v.Full {
			hs = append(hs, HeaderValue{Name: "x-hwid-max-devices-reached", Value: "true"})
		}
	}
	return hs
}

func mayUseLinks(explicit, clientType string) bool {
	_, own := ParseFormat(explicit)
	_, user := ParseFormat(clientType)
	return !own && !user
}

// Inspect uses the same access and filtering decisions as an actual request, but
// reads cached external subscriptions and never registers/touches a device.
func Inspect(ctx context.Context, svc *service.Service, u *store.User, r *http.Request, explicit string, format Format) (Selection, error) {
	return selectHosts(ctx, svc, u, r, format, mayUseLinks(explicit, u.ClientType), false)
}

func selectHosts(ctx context.Context, svc *service.Service, u *store.User, r *http.Request, format Format, mayLink, register bool) (Selection, error) {
	out := Selection{Format: format, Servers: []ServerSummary{}}
	switch u.Status {
	case store.StatusExpired:
		out.Reason = "Подписка истекла"
		if u.ExpireAt != nil {
			out.Reason += " " + time.Unix(*u.ExpireAt, 0).Format("02.01.2006")
		}
	case store.StatusLimited:
		out.Reason = "Трафик исчерпан"
	case store.StatusDisabled:
		out.Reason = "Подписка отключена"
	}
	if out.Reason != "" {
		return out, nil
	}
	if blocked, count, limit := svc.IPs().Verdict(u.ID, clientIP(r)); blocked {
		out.Reason = fmt.Sprintf("Превышен лимит подключений: %d/%d — отключите лишние устройства", count, limit)
		out.Headers = []HeaderValue{{Name: "x-hwid-max-devices-reached", Value: "true"}}
		return out, nil
	}
	d := service.DeviceInfo{HWID: r.Header.Get("x-hwid"), Platform: r.Header.Get("x-device-os"), OSVersion: r.Header.Get("x-ver-os"), Model: r.Header.Get("x-device-model"), UserAgent: r.UserAgent(), IP: clientIP(r)}
	var err error
	if register {
		out.HWID, err = svc.CheckDevice(ctx, u, d)
	} else {
		out.HWID, err = svc.PreviewDevice(ctx, u, d)
	}
	if err != nil {
		return out, err
	}
	out.Headers = deviceHeaders(out.HWID)
	if !out.HWID.OK {
		out.Reason = out.HWID.Reason
		return out, nil
	}
	// Karing imports routing profiles: absence of HWID must not strip their rules.
	// Keep the established link fallback for other clients and respect explicit formats.
	if out.HWID.Links && mayLink && !strings.Contains(strings.ToLower(r.UserAgent()), "karing") {
		out.Format = FormatBase64
	}
	var hosts []service.Host
	if register {
		hosts, err = svc.SubscriptionHosts(ctx, u, service.RequestDepth(r.Header.Get(service.DepthHeader)))
	} else {
		hosts, err = svc.CurrentHosts(ctx, u)
	}
	if err != nil {
		return out, err
	}
	for _, host := range hosts {
		s := ServerSummary{Name: host.Remark, Node: host.NodeCode, Protocol: host.Protocol, Network: host.Network}
		// KeqDroid's mixed payload keeps raw external links next to full Xray configs.
		mixed := mayLink && out.Format == FormatXray && keqClient.MatchString(r.UserAgent())
		switch {
		case host.Hidden:
			s.Excluded = "hidden"
		case host.Network == "xhttp" && singBoxCore.MatchString(r.UserAgent()):
			s.Excluded = "xhttp"
		case host.Raw != "" && !(out.Format.Supports(host) || mixed):
			s.Excluded = "raw"
		case !out.Format.Supports(host) && !mixed:
			s.Excluded = "format"
		default:
			out.Hosts = append(out.Hosts, host)
		}
		out.Servers = append(out.Servers, s)
	}
	out.Allowed = len(out.Hosts) > 0
	if !out.Allowed {
		out.Reason = "Нет серверов для этого приложения"
	}
	return out, nil
}
