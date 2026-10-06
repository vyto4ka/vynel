package reconciler

import (
	"context"
	"io"

	nodev1 "github.com/vyto4ka/vpn/internal/proto/vpn/node/v1"
)

// PanelEnd and NodeEnd connect an in-process agent to the reconciler without gRPC or TLS
// (`vpn panel --with-node`, docs/ARCHITECTURE.md §13).
type PanelEnd struct {
	ctx context.Context
	in  <-chan *nodev1.NodeMessage
	out chan<- *nodev1.PanelMessage
}

// NodeEnd is the agent side of a pipe.
type NodeEnd struct {
	ctx    context.Context
	cancel context.CancelFunc
	in     <-chan *nodev1.PanelMessage
	out    chan<- *nodev1.NodeMessage
}

// Pipe creates a connected pair; cancelling ctx or closing the node end ends both.
func Pipe(ctx context.Context) (*PanelEnd, *NodeEnd) {
	ctx, cancel := context.WithCancel(ctx)
	toPanel := make(chan *nodev1.NodeMessage, 8)
	toNode := make(chan *nodev1.PanelMessage, 8)
	return &PanelEnd{ctx: ctx, in: toPanel, out: toNode}, &NodeEnd{ctx: ctx, cancel: cancel, in: toNode, out: toPanel}
}

// Context implements Stream.
func (p *PanelEnd) Context() context.Context { return p.ctx }

// Send implements Stream.
func (p *PanelEnd) Send(m *nodev1.PanelMessage) error {
	select {
	case p.out <- m:
		return nil
	case <-p.ctx.Done():
		return io.EOF
	}
}

// Recv implements Stream.
func (p *PanelEnd) Recv() (*nodev1.NodeMessage, error) {
	select {
	case m := <-p.in:
		return m, nil
	case <-p.ctx.Done():
		return nil, io.EOF
	}
}

// Send sends to the panel.
func (n *NodeEnd) Send(m *nodev1.NodeMessage) error {
	select {
	case n.out <- m:
		return nil
	case <-n.ctx.Done():
		return io.EOF
	}
}

// Recv receives from the panel.
func (n *NodeEnd) Recv() (*nodev1.PanelMessage, error) {
	select {
	case m := <-n.in:
		return m, nil
	case <-n.ctx.Done():
		return nil, io.EOF
	}
}

// Close ends the pipe.
func (n *NodeEnd) Close() { n.cancel() }
