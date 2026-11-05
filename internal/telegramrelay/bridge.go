// Package telegramrelay carries a TCP connection to Telegram inside a dedicated mTLS gRPC
// stream. TLS to Telegram runs on the panel; the selected node only forwards encrypted bytes.
package telegramrelay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
)

const (
	Capability = "telegram-tunnel"
	Address    = "api.telegram.org:443"
	ChunkSize  = 32 << 10
	MaxTunnels = 8
)

// Stream is the data part of TelegramTunnel, after its one-use ticket was exchanged.
type Stream interface {
	Send(*nodev1.TelegramFrame) error
	Recv() (*nodev1.TelegramFrame, error)
}

// Bridge copies in both directions with bounded memory and gRPC's per-stream flow control.
// Its caller must cancel the client RPC or return from the server RPC when Bridge returns:
// that also unblocks a Send/Recv still waiting on gRPC. Close/deadlines on conn stop the bridge.
func Bridge(ctx context.Context, stream Stream, conn net.Conn) error {
	defer conn.Close()
	done := make(chan error, 2)
	go func() {
		buf := make([]byte, ChunkSize)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if sendErr := stream.Send(&nodev1.TelegramFrame{Data: append([]byte(nil), buf[:n]...)}); sendErr != nil {
					done <- sendErr
					return
				}
			}
			if err != nil {
				if closed(err) {
					if sendErr := stream.Send(&nodev1.TelegramFrame{Eof: true}); sendErr != nil {
						done <- sendErr
						return
					}
					done <- io.EOF
				} else {
					done <- err
				}
				return
			}
		}
	}()
	go func() {
		for {
			f, err := stream.Recv()
			if err != nil {
				done <- err
				return
			}
			if f == nil || len(f.Data) > ChunkSize || f.Ticket != "" || f.Error != "" || (len(f.Data) == 0 && !f.Eof) || (len(f.Data) != 0 && f.Eof) {
				done <- errors.New("invalid Telegram tunnel frame")
				return
			}
			if f.Eof {
				_ = conn.Close()
				done <- io.EOF
				return
			}
			n, err := conn.Write(f.Data)
			if err == nil && n != len(f.Data) {
				err = io.ErrShortWrite
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()
	// EOF is an explicit frame: wait for the other side before canceling gRPC, so its final
	// response bytes are consumed rather than discarded by RPC cancellation.
	for i := 0; i < 2; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			if !closed(err) {
				return fmt.Errorf("telegram tunnel: %w", err)
			}
		}
	}
	return nil
}

func closed(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe)
}
