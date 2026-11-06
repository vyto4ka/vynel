package telegramrelay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
)

type result struct {
	conn net.Conn
	err  error
}
type pending struct {
	nodeID int64
	ready  chan result
}

// Broker pairs an authenticated node's new stream with the panel's pending dial. Tickets
// are random, one-use, short-lived, bound to the node, and never written to the database/log.
type Broker struct {
	mu      sync.Mutex
	pending map[string]*pending
}

func (b *Broker) Dial(ctx context.Context, nodeID int64, request func(string) error) (net.Conn, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	ticket := hex.EncodeToString(key)
	p := &pending{nodeID: nodeID, ready: make(chan result, 1)}
	b.mu.Lock()
	if len(b.pending) >= MaxTunnels {
		b.mu.Unlock()
		return nil, errors.New("too many pending Telegram connections")
	}
	if b.pending == nil {
		b.pending = map[string]*pending{}
	}
	b.pending[ticket] = p
	b.mu.Unlock()
	accepted := false
	defer func() {
		b.mu.Lock()
		delete(b.pending, ticket)
		b.mu.Unlock()
		if !accepted {
			select {
			case r := <-p.ready:
				if r.conn != nil {
					_ = r.conn.Close()
				}
			default:
			}
		}
	}()
	if err := request(ticket); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-p.ready:
		accepted = r.err == nil
		return r.conn, r.err
	}
}

// Accept must be called only after the gateway's certificate authentication. The caller owns
// conn when Accept fails, and runs Bridge for it after success.
func (b *Broker) Accept(nodeID int64, ticket string, conn net.Conn, connectErr string) error {
	if len(ticket) != 64 {
		return errors.New("invalid Telegram tunnel ticket")
	}
	if _, err := hex.DecodeString(ticket); err != nil {
		return errors.New("invalid Telegram tunnel ticket")
	}
	if len(connectErr) > 512 {
		return errors.New("invalid Telegram connection error")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.pending[ticket]
	if p == nil || p.nodeID != nodeID {
		return errors.New("unknown, expired or wrong-node Telegram tunnel ticket")
	}
	delete(b.pending, ticket)
	if connectErr != "" {
		p.ready <- result{err: fmt.Errorf("node cannot reach Telegram: %s", connectErr)}
	} else {
		p.ready <- result{conn: conn}
	}
	return nil
}
