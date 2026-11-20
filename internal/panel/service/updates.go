package service

import (
	"context"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/update"
)

// UpdateRuns returns all persisted update runs indexed by node_id (0 = panel).
func (s *Service) UpdateRuns(ctx context.Context) (map[int64]*store.UpdateRun, error) {
	return store.ListUpdateRuns(ctx, s.st.DB)
}

// RecordUpdateSent persists that an update was triggered for nodeID (0 = panel).
func (s *Service) RecordUpdateSent(ctx context.Context, nodeID int64, fromVersion, errMsg string) error {
	now := s.now().Unix()
	prev, _ := store.GetUpdateRun(ctx, s.st.DB, nodeID)
	r := store.UpdateRun{
		NodeID:      nodeID,
		FromVersion: fromVersion,
		State:       "sent",
		SentAt:      now,
		UpdatedAt:   now,
	}
	if prev != nil {
		r.DismissedAt = prev.DismissedAt
		if errMsg == "" && prev.StartedAt >= now-10 && (prev.State == "running" || prev.State == "done") {
			r.State = prev.State
			r.OK = prev.OK
			r.StartedAt = prev.StartedAt
			r.FinishedAt = prev.FinishedAt
			r.ToVersion = prev.ToVersion
			r.Log = prev.Log
		}
	}
	if errMsg != "" {
		r.State = "failed"
		r.StartedAt = now
		r.FinishedAt = now
		r.Log = errMsg
	}
	return store.UpsertUpdateRun(ctx, s.st.DB, r)
}

// RecordUpdateStatus persists a status/log snapshot reported by a node (or the panel, nodeID = 0).
func (s *Service) RecordUpdateStatus(ctx context.Context, nodeID int64, st *update.Status) error {
	if st == nil {
		return nil
	}
	now := s.now().Unix()
	prev, err := store.GetUpdateRun(ctx, s.st.DB, nodeID)
	if err != nil {
		return err
	}
	r := store.UpdateRun{NodeID: nodeID, UpdatedAt: now}
	if prev != nil {
		r = *prev
		r.UpdatedAt = now
	}
	// Do not overwrite a newer run's log with stale status from before sent_at.
	if r.SentAt > 0 && st.StartedAt > 0 && st.StartedAt < r.SentAt-30 {
		return nil
	}
	if st.StartedAt > 0 {
		r.StartedAt = st.StartedAt
	}
	if st.FinishedAt > 0 {
		r.FinishedAt = st.FinishedAt
	} else if st.Running {
		r.FinishedAt = 0
	}
	if st.Version != "" {
		r.ToVersion = st.Version
	}
	r.OK = st.OK
	switch {
	case st.Running:
		r.State = "running"
	case st.FinishedAt > 0 && st.OK:
		r.State = "done"
	case st.FinishedAt > 0 && !st.OK:
		r.State = "failed"
	}
	if strings.TrimSpace(st.Log) != "" {
		r.Log = st.Log
	}
	return store.UpsertUpdateRun(ctx, s.st.DB, r)
}

// DismissUpdateRun hides the current finished/failed update state until a newer update starts.
func (s *Service) DismissUpdateRun(ctx context.Context, nodeID int64) error {
	return store.DismissUpdateRun(ctx, s.st.DB, nodeID, s.now().Unix())
}
