package xray

import (
	"context"
	"fmt"
	"strings"
	"time"

	handler "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// API is a client for Xray's HandlerService and StatsService on 127.0.0.1.
type API struct {
	conn    *grpc.ClientConn
	handler handler.HandlerServiceClient
	stats   stats.StatsServiceClient
}

// DialAPI connects to the Xray API (the connection is lazy; calls fail until Xray is up).
func DialAPI(addr string) (*API, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &API{conn: conn, handler: handler.NewHandlerServiceClient(conn), stats: stats.NewStatsServiceClient(conn)}, nil
}

// Close closes the connection.
func (a *API) Close() error { return a.conn.Close() }

// WaitReady polls the API until it answers or ctx ends.
func (a *API) WaitReady(ctx context.Context) error {
	for {
		_, err := a.stats.GetSysStats(ctx, &stats.SysStatsRequest{})
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("xray api not ready: %w", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// AddVLESSUser adds a user to an inbound without restarting Xray.
// Adding an email that already exists is treated as success.
func (a *API) AddVLESSUser(ctx context.Context, tag, email, uuid, flow string) error {
	op := &handler.AddUserOperation{User: &protocol.User{
		Level:   0,
		Email:   email,
		Account: serial.ToTypedMessage(&vless.Account{Id: uuid, Flow: flow, Encryption: "none"}),
	}}
	_, err := a.handler.AlterInbound(ctx, &handler.AlterInboundRequest{Tag: tag, Operation: serial.ToTypedMessage(op)})
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}

// RemoveUser removes a user from an inbound. Removing a missing user is treated as success.
func (a *API) RemoveUser(ctx context.Context, tag, email string) error {
	op := &handler.RemoveUserOperation{Email: email}
	_, err := a.handler.AlterInbound(ctx, &handler.AlterInboundRequest{Tag: tag, Operation: serial.ToTypedMessage(op)})
	if err != nil && (strings.Contains(err.Error(), "not found") || status.Code(err) == codes.NotFound) {
		return nil
	}
	return err
}

// InboundUsers lists the emails currently configured on an inbound.
func (a *API) InboundUsers(ctx context.Context, tag string) ([]string, error) {
	resp, err := a.handler.GetInboundUsers(ctx, &handler.GetInboundUserRequest{Tag: tag})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.Users))
	for _, u := range resp.Users {
		out = append(out, u.Email)
	}
	return out, nil
}

// Stat is one counter, e.g. user>>>7>>>traffic>>>uplink.
type Stat struct {
	Name  string
	Value int64
}

// QueryStats returns counters matching pattern; with reset they are zeroed atomically.
func (a *API) QueryStats(ctx context.Context, pattern string, reset bool) ([]Stat, error) {
	resp, err := a.stats.QueryStats(ctx, &stats.QueryStatsRequest{Pattern: pattern, Reset_: reset})
	if err != nil {
		return nil, err
	}
	out := make([]Stat, 0, len(resp.Stat))
	for _, s := range resp.Stat {
		out = append(out, Stat{Name: s.Name, Value: s.Value})
	}
	return out, nil
}

// UserTraffic is a per-user traffic delta parsed from stats.
type UserTraffic struct {
	Email    string
	Uplink   int64
	Downlink int64
}

// UserTrafficDeltas reads and resets per-user traffic counters.
func (a *API) UserTrafficDeltas(ctx context.Context) ([]UserTraffic, error) {
	st, err := a.QueryStats(ctx, "user>>>", true)
	if err != nil {
		return nil, err
	}
	by := map[string]*UserTraffic{}
	var order []string
	for _, s := range st {
		parts := strings.Split(s.Name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}
		u, ok := by[parts[1]]
		if !ok {
			u = &UserTraffic{Email: parts[1]}
			by[parts[1]] = u
			order = append(order, parts[1])
		}
		switch parts[3] {
		case "uplink":
			u.Uplink += s.Value
		case "downlink":
			u.Downlink += s.Value
		}
	}
	out := make([]UserTraffic, 0, len(order))
	for _, e := range order {
		if u := by[e]; u.Uplink != 0 || u.Downlink != 0 {
			out = append(out, *u)
		}
	}
	return out, nil
}
