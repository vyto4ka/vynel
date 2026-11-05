package telegramrelay

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
)

type memoryStream struct {
	ctx     context.Context
	in, out chan *nodev1.TelegramFrame
}

func (s memoryStream) Send(f *nodev1.TelegramFrame) error {
	select {
	case s.out <- f:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}
func (s memoryStream) Recv() (*nodev1.TelegramFrame, error) {
	select {
	case f := <-s.in:
		return f, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func TestBridgeLargeResponseAndGracefulEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	a, aw := net.Pipe()
	b, bw := net.Pipe()
	defer a.Close()
	defer b.Close()
	ab, ba := make(chan *nodev1.TelegramFrame, 1), make(chan *nodev1.TelegramFrame, 1)
	done := make(chan error, 2)
	go func() { done <- Bridge(ctx, memoryStream{ctx, ba, ab}, aw) }()
	go func() { done <- Bridge(ctx, memoryStream{ctx, ab, ba}, bw) }()
	response := bytes.Repeat([]byte("encrypted response"), 300000) // larger than gRPC's usual 4 MB limit
	server := make(chan error, 1)
	go func() {
		defer b.Close()
		request := make([]byte, 7)
		if _, err := io.ReadFull(b, request); err != nil {
			server <- err
			return
		}
		if string(request) != "request" {
			server <- io.ErrUnexpectedEOF
			return
		}
		_, err := b.Write(response)
		server <- err
	}()
	if _, err := a.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(a)
	if err != nil || !bytes.Equal(got, response) {
		t.Fatalf("response truncated: %d/%d bytes, %v", len(got), len(response), err)
	}
	if err := <-server; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("bridge did not close")
		}
	}
}

func TestBrokerBindsTicketsToTheNodeAndConsumesOnce(t *testing.T) {
	var broker Broker
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	tickets, result := make(chan string, 1), make(chan net.Conn, 1)
	go func() {
		c, _ := broker.Dial(ctx, 7, func(ticket string) error { tickets <- ticket; return nil })
		result <- c
	}()
	ticket := <-tickets
	local, peer := net.Pipe()
	defer local.Close()
	defer peer.Close()
	if err := broker.Accept(8, ticket, local, ""); err == nil {
		t.Fatal("another node stole the ticket")
	}
	if err := broker.Accept(7, ticket, local, ""); err != nil {
		t.Fatal(err)
	}
	if err := broker.Accept(7, ticket, local, ""); err == nil {
		t.Fatal("ticket was replayed")
	}
	if got := <-result; got != local {
		t.Fatal("wrong connection")
	}
	canceled, stop := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() {
		_, err := broker.Dial(canceled, 7, func(ticket string) error { tickets <- ticket; return nil })
		finished <- err
	}()
	late := <-tickets
	stop()
	<-finished
	if err := broker.Accept(7, late, local, ""); err == nil {
		t.Fatal("accepted an expired dial")
	}
}

func TestBridgeRejectsOversizedAndUnexpectedFrames(t *testing.T) {
	for _, f := range []*nodev1.TelegramFrame{{Data: make([]byte, ChunkSize+1)}, {Ticket: "replay"}, {Data: []byte("data"), Eof: true}, {}} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		a, b := net.Pipe()
		incoming := make(chan *nodev1.TelegramFrame, 1)
		incoming <- f
		err := Bridge(ctx, memoryStream{ctx, incoming, make(chan *nodev1.TelegramFrame, 1)}, b)
		cancel()
		_ = a.Close()
		if err == nil {
			t.Fatalf("accepted bad frame: %+v", f)
		}
	}
}
