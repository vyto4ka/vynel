package agent

import (
	"context"
	"encoding/hex"
	"net"
	"time"

	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/telegramrelay"
	"google.golang.org/grpc"
)

type remoteConn struct {
	Conn
	gateway nodev1.NodeGatewayClient
}

func (c *remoteConn) openTelegram(ctx context.Context) (grpc.BidiStreamingClient[nodev1.TelegramFrame, nodev1.TelegramFrame], error) {
	return c.gateway.TelegramTunnel(ctx)
}

type telegramOpener interface {
	openTelegram(context.Context) (grpc.BidiStreamingClient[nodev1.TelegramFrame, nodev1.TelegramFrame], error)
}

// telegramConnect dials a fixed destination; no address, port, credentials or bot token is
// accepted from the panel. The inner TLS session is between the panel and Telegram.
func (a *Agent) telegramConnect(ctx context.Context, opener telegramOpener, ticket string, slots chan struct{}) {
	if len(ticket) != 64 {
		return
	}
	if _, err := hex.DecodeString(ticket); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var target net.Conn
	connectErr := ""
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
		dialCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		dial := a.cfg.TelegramDial
		if dial == nil {
			dial = func(ctx context.Context) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", telegramrelay.Address)
			}
		}
		var err error
		target, err = dial(dialCtx)
		stop()
		if err != nil {
			connectErr = "cannot connect to api.telegram.org:443 (check the node's DNS and Internet access)"
		}
	default:
		connectErr = "too many active Telegram connections on this node"
	}
	if target != nil {
		defer target.Close()
	}
	stream, err := opener.openTelegram(ctx)
	if err != nil {
		return
	}
	if err := stream.Send(&nodev1.TelegramFrame{Ticket: ticket, Error: connectErr}); err != nil {
		return
	}
	if connectErr != "" {
		_ = stream.CloseSend()
		_, _ = stream.Recv() // let the panel consume the error before canceling the RPC
		return
	}
	if err := telegramrelay.Bridge(ctx, stream, target); err != nil && ctx.Err() == nil {
		a.log.Debug("Telegram tunnel ended", "err", err)
	}
}
