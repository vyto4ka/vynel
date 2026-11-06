package reconciler

import (
	"context"
	"errors"
	"net"
	"slices"
	"time"

	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/telegramrelay"
)

// DialTelegram opens Telegram TCP through a node's reverse mTLS connection. Telegram TLS is
// performed by the bot on the panel, not by the node. It never falls back to a direct dial.
func (r *Reconciler) DialTelegram(ctx context.Context, nodeID int64) (net.Conn, error) {
	n, err := r.svc.Node(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if !n.Enabled || n.Local {
		return nil, errors.New("select an enabled remote node for Telegram")
	}
	r.mu.Lock()
	s := r.sessions[nodeID]
	r.mu.Unlock()
	if s == nil {
		return nil, errors.New("telegram route node is offline")
	}
	s.mu.Lock()
	supported := s.hello != nil && slices.Contains(s.hello.Capabilities, telegramrelay.Capability)
	s.mu.Unlock()
	if !supported {
		return nil, errors.New("update the selected node agent to support Telegram routing")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return r.telegram.Dial(ctx, nodeID, func(ticket string) error {
		msg := &nodev1.PanelMessage{Msg: &nodev1.PanelMessage_TelegramConnect{TelegramConnect: &nodev1.TelegramConnect{Ticket: ticket}}}
		select {
		case s.out <- msg:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-s.stream.Context().Done():
			return errors.New("telegram route node disconnected")
		}
	})
}

// AcceptTelegram is called by the authenticated gateway, after the node opens its new stream.
func (r *Reconciler) AcceptTelegram(nodeID int64, ticket string, conn net.Conn, connectErr string) error {
	return r.telegram.Accept(nodeID, ticket, conn, connectErr)
}
