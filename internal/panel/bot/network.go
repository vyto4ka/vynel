package bot

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/vyto4ka/vynel/internal/telegramrelay"
)

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
