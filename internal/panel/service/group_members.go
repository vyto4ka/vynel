package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

type GroupInput struct {
	Name, Description string
	Defaults          *store.GroupDefaults
	SetDefaults       bool // false preserves the policy for old callers that only rename a group
}

func checkGroupDefaults(d *store.GroupDefaults) error {
	if d == nil {
		return nil
	}
	switch d.Expiry {
	case "", "keep", "never":
	case "date":
		if d.ExpireAt == nil || *d.ExpireAt <= 0 || *d.ExpireAt > 253402300799 {
			return invalid("group expiry date is required")
		}
	case "period":
		if d.Months < 0 || d.Months > 1200 || d.Days < 0 || d.Days > 36500 || d.Months+d.Days == 0 {
			return invalid("group duration must be positive (up to 1200 months and 36500 days)")
		}
	default:
		return invalid("group expiry: keep, date, period or never")
	}
	switch d.Traffic {
	case "", "keep", "unlimited":
	case "limit":
		if d.TrafficLimit <= 0 {
			return invalid("group traffic limit must be positive")
		}
	default:
		return invalid("group traffic: keep, limit or unlimited")
	}
	if d.Reset != "" {
		reset := d.Reset
		if err := checkReset(&reset); err != nil {
			return err
		}
	}
	for _, limit := range []*int64{d.HWIDLimit, d.IPLimit} {
		if limit != nil && (*limit < 0 || *limit > 100000) {
			return invalid("group device/IP limit must be between 0 and 100000")
		}
	}
	return nil
}

func (s *Service) SaveGroup(ctx context.Context, actor Actor, id int64, in GroupInput) (*store.Group, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, invalid("group name is required")
	}
	if in.SetDefaults {
		if err := checkGroupDefaults(in.Defaults); err != nil {
			return nil, err
		}
	}
	g := &store.Group{ID: id}
	action := "group.update"
	if id == 0 {
		action = "group.create"
	}
	err := s.mutate(ctx, change{actor: actor, action: action, entity: "group", entityID: idOf(&g.ID), event: EvGroupChanged, diff: in}, func(q store.DBTX) error {
		if id != 0 {
			old, err := store.GetGroup(ctx, q, id)
			if err != nil {
				return err
			}
			*g = *old
		}
		g.Name, g.Description = in.Name, in.Description
		if in.SetDefaults {
			g.Defaults = in.Defaults
		}
		if id == 0 {
			return store.CreateGroup(ctx, q, g)
		}
		return store.UpdateGroup(ctx, q, g)
	})
	return g, err
}

func (s *Service) Group(ctx context.Context, id int64) (*store.Group, error) {
	return store.GetGroup(ctx, s.st.DB, id)
}

func applyGroupDefaults(u *store.User, d *store.GroupDefaults, now time.Time) {
	if d == nil {
		return
	}
	switch d.Expiry {
	case "date":
		value := *d.ExpireAt
		u.ExpireAt = &value
	case "period":
		value := now.AddDate(0, d.Months, d.Days).Unix()
		u.ExpireAt = &value
	case "never":
		u.ExpireAt = nil
	}
	switch d.Traffic {
	case "limit":
		value := d.TrafficLimit
		u.TrafficLimitBytes = &value
	case "unlimited":
		u.TrafficLimitBytes = nil
	}
	if d.Reset != "" && d.Reset != u.ResetStrategy {
		u.ResetStrategy = d.Reset
		// A previously unscheduled counter must not be mistaken for a missed
		// calendar reset on the next maintenance tick. Keep usage, and start
		// the new schedule at enrollment; unchanged schedules retain their due date.
		at := now.Unix()
		u.LastResetAt = &at
	}
	if d.HWIDLimit != nil {
		value := *d.HWIDLimit
		u.HWIDLimit = &value
	}
	if d.IPLimit != nil {
		value := *d.IPLimit
		u.IPLimit = &value
	}
	u.Status = computeStatus(u, now)
}

type GroupMemberResult struct {
	Changed   []int64 `json:"changed"`
	Unchanged []int64 `json:"unchanged"`
}

// ChangeGroupMembers is atomic and idempotent. Adding keeps other groups; removing only
// removes this membership. A policy is copied only for newly added users, never on retries.
func (s *Service) ChangeGroupMembers(ctx context.Context, actor Actor, groupID int64, ids []int64, add, apply bool) (*GroupMemberResult, error) {
	if len(ids) == 0 || len(ids) > 500 {
		return nil, invalid("select between 1 and 500 users")
	}
	seen := map[int64]bool{}
	unique := []int64{}
	for _, id := range ids {
		if id <= 0 {
			return nil, invalid("user ID must be positive")
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	result := &GroupMemberResult{Changed: []int64{}, Unchanged: []int64{}}
	err := s.mutate(ctx, change{actor: actor, action: "group.members", entity: "group", entityID: func() int64 { return groupID }, event: EvGroupChanged, diff: map[string]any{"users": unique, "add": add, "applyDefaults": apply}}, func(q store.DBTX) error {
		group, err := store.GetGroup(ctx, q, groupID)
		if err != nil {
			return err
		}
		if apply {
			if err := checkGroupDefaults(group.Defaults); err != nil {
				return err
			}
		}
		for _, id := range unique {
			u, err := store.GetUser(ctx, q, id)
			if err != nil {
				return fmt.Errorf("user %d: %w", id, err)
			}
			groups, err := store.UserGroupIDs(ctx, q, id)
			if err != nil {
				return err
			}
			has := slices.Contains(groups, groupID)
			if has == add {
				result.Unchanged = append(result.Unchanged, id)
				continue
			}
			if add {
				groups = append(groups, groupID)
			} else {
				groups = slices.DeleteFunc(groups, func(id int64) bool { return id == groupID })
			}
			if err := store.SetUserGroups(ctx, q, id, groups); err != nil {
				return err
			}
			if add && apply {
				applyGroupDefaults(u, group.Defaults, s.now())
				if err := store.UpdateUser(ctx, q, u); err != nil {
					return err
				}
			}
			// Individual membership events remain visible to billing/API integrations too.
			if err := store.AddAudit(ctx, q, store.AuditEntry{Actor: actor.Kind, ActorID: actor.ID, Action: "user.set_groups", Entity: "user", EntityID: id, Diff: fmt.Sprintf(`{"group":%d,"added":%t}`, groupID, add)}); err != nil {
				return err
			}
			result.Changed = append(result.Changed, id)
		}
		return nil
	})
	return result, err
}
