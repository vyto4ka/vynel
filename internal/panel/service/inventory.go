package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/vyto4ka/vynel/internal/node/inventory"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

const (
	nodeInventorySettingPrefix = "node.inventory."
	maxNodeInventoryBytes      = 256 * 1024
)

func nodeInventoryKey(nodeID int64) string {
	return nodeInventorySettingPrefix + strconv.FormatInt(nodeID, 10)
}

// NodeInventoryView is the inventory payload returned for a node in the admin panel.
type NodeInventoryView struct {
	NodeID    int64               `json:"nodeId"`
	Supported bool                `json:"supported"`
	Live      bool                `json:"live"`
	Connected bool                `json:"connected"`
	Snapshot  *inventory.Snapshot `json:"snapshot,omitempty"`
}

// SaveNodeInventory validates and persists a node's inventory snapshot reported by the agent.
func (s *Service) SaveNodeInventory(ctx context.Context, nodeID int64, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxNodeInventoryBytes {
		return fmt.Errorf("node inventory payload exceeds %d bytes", maxNodeInventoryBytes)
	}
	var snap inventory.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return fmt.Errorf("parse node inventory: %w", err)
	}
	normalized, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return store.SetSetting(ctx, s.st.DB, nodeInventoryKey(nodeID), string(normalized))
}

// NodeInventory returns the node's inventory, fetching a fresh snapshot when the node is online
// and supports ACTION_INVENTORY (either on explicit live refresh or when the cached snapshot is stale).
func (s *Service) NodeInventory(ctx context.Context, nodeID int64, forceLive bool) (*NodeInventoryView, error) {
	n, err := store.GetNode(ctx, s.st.DB, nodeID)
	if err != nil {
		return nil, err
	}
	connected := s.NodeConnected != nil && s.NodeConnected(nodeID)
	canInv := n.Can("inventory")

	var cached *inventory.Snapshot
	if raw, ok, err := store.GetSetting(ctx, s.st.DB, nodeInventoryKey(nodeID)); err == nil && ok && raw != "" {
		var snap inventory.Snapshot
		if json.Unmarshal([]byte(raw), &snap) == nil {
			cached = &snap
		}
	}

	stale := cached == nil || s.now().Unix()-cached.CollectedAt >= 15
	if connected && canInv && s.NodeInventoryFetch != nil && (forceLive || stale) {
		if fresh, ferr := s.NodeInventoryFetch(ctx, nodeID); ferr == nil && fresh != nil {
			if b, merr := json.Marshal(fresh); merr == nil {
				_ = store.SetSetting(context.WithoutCancel(ctx), s.st.DB, nodeInventoryKey(nodeID), string(b))
			}
			return &NodeInventoryView{
				NodeID:    nodeID,
				Supported: true,
				Live:      true,
				Connected: true,
				Snapshot:  fresh,
			}, nil
		}
	}

	if n.Local && (forceLive || cached == nil) {
		fresh := inventory.Collect(ctx, "")
		if b, merr := json.Marshal(fresh); merr == nil {
			_ = store.SetSetting(context.WithoutCancel(ctx), s.st.DB, nodeInventoryKey(nodeID), string(b))
		}
		return &NodeInventoryView{
			NodeID:    nodeID,
			Supported: true,
			Live:      true,
			Connected: connected || n.Local,
			Snapshot:  &fresh,
		}, nil
	}

	return &NodeInventoryView{
		NodeID:    nodeID,
		Supported: canInv || cached != nil || n.Local,
		Live:      false,
		Connected: connected,
		Snapshot:  cached,
	}, nil
}
