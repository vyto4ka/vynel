package bot

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/telegramrelay"
)

type networkError struct{ error }

func (e *networkError) Unwrap() error { return e.error }

// ConnectionIssue distinguishes a blocked/unreachable route from errors that a VPN
// cannot fix (for example an invalid token or another running bot instance).
func ConnectionIssue(err error) string {
	var ae *apiError
	if errors.As(err, &ae) {
		switch ae.Code {
		case http.StatusUnauthorized:
			return "token"
		case http.StatusConflict:
			return "conflict"
		default:
			return "telegram"
		}
	}
	var ne *networkError
	if errors.As(err, &ne) || errors.Is(err, context.DeadlineExceeded) {
		return "network"
	}
	return "route"
}

// CheckNetwork checks HTTPS reachability before a token is configured. No requests
// to geolocation services, bot credentials or messages are needed. A redirect is
// an answer from the endpoint too; it is never followed to another host.
func (b *Bot) CheckNetwork(ctx context.Context, nodeID int64) error {
	if err := b.svc.ValidateBotNode(ctx, nodeID); err != nil {
		return err
	}
	c, err := b.makeClient("", nodeID)
	if err != nil {
		return err
	}
	defer c.http.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	hc := *c.http
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, strings.TrimSuffix(c.base, "/bot")+"/", nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return &networkError{err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusProxyAuthRequired {
		return &apiError{Code: resp.StatusCode, Description: "Telegram API недоступен"}
	}
	return nil
}

func (b *Bot) makeClient(token string, nodeID int64) (*client, error) {
	api := b.cfg.API
	if nodeID != 0 {
		api = DefaultAPI
	}
	c := newClient(api, token)
	if nodeID == 0 {
		return c, nil
	}
	if b.cfg.DialTelegram == nil {
		return nil, errors.New("telegram node routing is unavailable")
	}
	tr := c.http.Transport.(*http.Transport)
	tr.Proxy = nil // a system HTTP_PROXY/NO_PROXY must never bypass the selected node
	tr.MaxConnsPerHost, tr.MaxIdleConnsPerHost = 4, 2
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != telegramrelay.Address {
			return nil, errors.New("telegram node routing only permits api.telegram.org:443")
		}
		return b.cfg.DialTelegram(ctx, nodeID)
	}
	c.http.Transport = tr
	return c, nil
}

// CheckConnection tests getMe using the saved token and a proposed route, without sending
// messages, changing the saved route, or interrupting the running bot.
func (b *Bot) CheckConnection(ctx context.Context, nodeID int64) (string, error) {
	if err := b.svc.ValidateBotNode(ctx, nodeID); err != nil {
		return "", err
	}
	token := b.svc.BotToken(ctx)
	if token == "" {
		return "", errors.New("сначала сохраните токен Telegram-бота")
	}
	c, err := b.makeClient(token, nodeID)
	if err != nil {
		return "", err
	}
	defer c.http.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	me, err := c.getMe(ctx)
	if err != nil {
		return "", err
	}
	return me.Username, nil
}
