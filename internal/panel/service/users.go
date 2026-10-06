package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

var usernameRe = regexp.MustCompile(`^[\p{L}\p{N}_.\-@]{1,64}$`)

// ---- groups ----

// CreateGroup adds a group.
func (s *Service) CreateGroup(ctx context.Context, actor Actor, name, description string) (*store.Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, invalid("group name is required")
	}
	g := &store.Group{Name: name, Description: description}
	err := s.mutate(ctx, change{actor: actor, action: "group.create", entity: "group", entityID: idOf(&g.ID), event: EvGroupChanged, diff: map[string]string{"name": name}},
		func(q store.DBTX) error { return store.CreateGroup(ctx, q, g) })
	return g, err
}

// DeleteGroup removes a group.
func (s *Service) DeleteGroup(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "group.delete", entity: "group", entityID: func() int64 { return id }, event: EvGroupChanged},
		func(q store.DBTX) error { return store.DeleteGroup(ctx, q, id) })
}

// GrantAccess adds an access rule to a group (docs/INBOUNDS.md §1.5).
func (s *Service) GrantAccess(ctx context.Context, actor Actor, groupID int64, kind string, refID int64) error {
	return s.mutate(ctx, change{actor: actor, action: "group.grant", entity: "group", entityID: func() int64 { return groupID }, event: EvGroupChanged,
		diff: map[string]any{"kind": kind, "ref": refID}},
		func(q store.DBTX) error {
			if _, err := store.GetGroup(ctx, q, groupID); err != nil {
				return err
			}
			var err error
			switch kind {
			case store.AccessNodeInbound:
				_, err = store.GetNodeInbound(ctx, q, refID)
			case store.AccessProfile:
				_, err = store.GetProfile(ctx, q, refID)
			case store.AccessNode:
				_, err = store.GetNode(ctx, q, refID)
			default:
				return invalid("unknown access kind %q", kind)
			}
			if err != nil {
				return fmt.Errorf("%s %d: %w", kind, refID, err)
			}
			return store.AddAccessRule(ctx, q, &store.AccessRule{GroupID: groupID, Kind: kind, RefID: refID})
		})
}

// RevokeAccess removes an access rule.
func (s *Service) RevokeAccess(ctx context.Context, actor Actor, groupID int64, kind string, refID int64) error {
	return s.mutate(ctx, change{actor: actor, action: "group.revoke", entity: "group", entityID: func() int64 { return groupID }, event: EvGroupChanged,
		diff: map[string]any{"kind": kind, "ref": refID}},
		func(q store.DBTX) error { return store.RemoveAccessRule(ctx, q, groupID, kind, refID) })
}

// Groups lists groups.
func (s *Service) Groups(ctx context.Context) ([]*store.Group, error) {
	return store.ListGroups(ctx, s.st.DB)
}

// GroupByName loads a group by name.
func (s *Service) GroupByName(ctx context.Context, name string) (*store.Group, error) {
	return store.GetGroupByName(ctx, s.st.DB, name)
}

// AccessRules lists the rules of a group (0 = all).
func (s *Service) AccessRules(ctx context.Context, groupID int64) ([]*store.AccessRule, error) {
	return store.ListAccessRules(ctx, s.st.DB, groupID)
}

// ResolveAccess expands every group's rules into the set of node inbound ids it grants.
// Rules pointing at deleted objects are ignored.
func ResolveAccess(rules []*store.AccessRule, inbounds []*store.NodeInbound) map[int64]map[int64]bool {
	out := map[int64]map[int64]bool{}
	for _, r := range rules {
		set := out[r.GroupID]
		if set == nil {
			set = map[int64]bool{}
			out[r.GroupID] = set
		}
		for _, ni := range inbounds {
			switch {
			case r.Kind == store.AccessNodeInbound && ni.ID == r.RefID,
				r.Kind == store.AccessProfile && ni.ProfileID == r.RefID,
				r.Kind == store.AccessNode && ni.NodeID == r.RefID:
				set[ni.ID] = true
			}
		}
	}
	return out
}

// ---- user templates ----

// UserTemplateInput creates a user template.
type UserTemplateInput struct {
	Name              string
	IsDefault         bool
	ExpireMonths      int
	ExpireDays        int
	TrafficLimitBytes *int64
	ResetStrategy     string
	HWIDLimit         *int64
	ClientType        string
	GroupIDs          []int64
	Note              string
}

// CreateUserTemplate adds a template. The first template becomes the default.
func (s *Service) CreateUserTemplate(ctx context.Context, actor Actor, in UserTemplateInput) (*store.UserTemplate, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, invalid("template name is required")
	}
	if err := checkReset(&in.ResetStrategy); err != nil {
		return nil, err
	}
	if in.ExpireMonths < 0 || in.ExpireDays < 0 {
		return nil, invalid("expiry must not be negative")
	}
	t := &store.UserTemplate{Name: strings.TrimSpace(in.Name), IsDefault: in.IsDefault, ExpireMonths: in.ExpireMonths, ExpireDays: in.ExpireDays,
		TrafficLimitBytes: in.TrafficLimitBytes, ResetStrategy: in.ResetStrategy, HWIDLimit: in.HWIDLimit,
		ClientType: firstNonEmpty(in.ClientType, "auto"), GroupIDs: in.GroupIDs, Note: in.Note}
	err := s.mutate(ctx, change{actor: actor, action: "user_template.create", entity: "user_template", entityID: idOf(&t.ID), diff: in},
		func(q store.DBTX) error {
			for _, g := range t.GroupIDs {
				if _, err := store.GetGroup(ctx, q, g); err != nil {
					return fmt.Errorf("group %d: %w", g, err)
				}
			}
			if _, err := store.GetUserTemplate(ctx, q, 0); errors.Is(err, store.ErrNotFound) {
				t.IsDefault = true
			}
			return store.CreateUserTemplate(ctx, q, t)
		})
	return t, err
}

// UserTemplates lists templates.
func (s *Service) UserTemplates(ctx context.Context) ([]*store.UserTemplate, error) {
	return store.ListUserTemplates(ctx, s.st.DB)
}

// UserTemplateByName loads a template by name.
func (s *Service) UserTemplateByName(ctx context.Context, name string) (*store.UserTemplate, error) {
	return store.GetUserTemplateByName(ctx, s.st.DB, name)
}

func checkReset(r *string) error {
	switch *r {
	case "":
		*r = "no"
	case "no", "day", "week", "month":
	default:
		return invalid("reset strategy must be no, day, week or month")
	}
	return nil
}

// ---- users ----

// CreateUserInput creates a user. Only Username is required: the rest comes from the template
// (docs/ARCHITECTURE.md §7.1). Non-nil fields override the template.
type CreateUserInput struct {
	Username          string
	TemplateID        int64 // 0 = default template
	ExpireAt          *time.Time
	NeverExpires      bool
	TrafficLimitBytes *int64
	HWIDLimit         *int64
	GroupIDs          []int64 // nil = template's groups
	ClientType        string
	Note              string
	TelegramID        *int64
	ExternalID        *string
}

// CreateUser creates a user from a template.
func (s *Service) CreateUser(ctx context.Context, actor Actor, in CreateUserInput) (*store.User, error) {
	in.Username = strings.TrimSpace(in.Username)
	if !usernameRe.MatchString(in.Username) {
		return nil, invalid("username must be 1-64 letters, digits or _ . - @")
	}
	u := &store.User{Username: in.Username, Note: in.Note, TelegramID: in.TelegramID, ExternalID: in.ExternalID, CreatedBy: actor.Kind}
	var err error
	if u.UUID, err = xrayconf.NewUUID(); err != nil {
		return nil, err
	}
	if u.SubToken, err = newSubToken(); err != nil {
		return nil, err
	}
	err = s.mutate(ctx, change{actor: actor, action: "user.create", entity: "user", entityID: idOf(&u.ID), event: EvUserChanged, diff: map[string]any{"username": u.Username}},
		func(q store.DBTX) error {
			tpl, err := store.GetUserTemplate(ctx, q, in.TemplateID)
			switch {
			case errors.Is(err, store.ErrNotFound) && in.TemplateID == 0:
				tpl = &store.UserTemplate{ResetStrategy: "no", ClientType: "auto"} // no templates yet: unlimited, never expires
			case err != nil:
				return fmt.Errorf("template: %w", err)
			default:
				u.TemplateID = &tpl.ID
			}
			now := s.now()
			if tpl.ExpireMonths > 0 || tpl.ExpireDays > 0 {
				exp := now.AddDate(0, tpl.ExpireMonths, tpl.ExpireDays).Unix()
				u.ExpireAt = &exp
			}
			if in.ExpireAt != nil {
				exp := in.ExpireAt.Unix()
				u.ExpireAt = &exp
			}
			if in.NeverExpires {
				u.ExpireAt = nil
			}
			u.TrafficLimitBytes, u.ResetStrategy, u.HWIDLimit = tpl.TrafficLimitBytes, tpl.ResetStrategy, tpl.HWIDLimit
			if in.TrafficLimitBytes != nil {
				u.TrafficLimitBytes = nilIfNonPositive(in.TrafficLimitBytes)
			}
			if in.HWIDLimit != nil {
				u.HWIDLimit = in.HWIDLimit
			}
			u.ClientType = firstNonEmpty(in.ClientType, firstNonEmpty(tpl.ClientType, "auto"))
			if u.Note == "" {
				u.Note = tpl.Note
			}
			u.Status = computeStatus(u, now)
			if err := store.CreateUser(ctx, q, u); err != nil {
				return err
			}
			groups := tpl.GroupIDs
			if in.GroupIDs != nil {
				groups = in.GroupIDs
			}
			return store.SetUserGroups(ctx, q, u.ID, groups)
		})
	return u, err
}

// computeStatus is the user status machine (docs/ARCHITECTURE.md §7.2).
func computeStatus(u *store.User, now time.Time) string {
	switch {
	case u.Disabled:
		return store.StatusDisabled
	case u.ExpireAt != nil && now.Unix() >= *u.ExpireAt:
		return store.StatusExpired
	case u.TrafficLimitBytes != nil && u.TrafficUsedBytes >= *u.TrafficLimitBytes:
		return store.StatusLimited
	}
	return store.StatusActive
}

// updateUser loads a user, applies fn, recomputes the status and saves it.
func (s *Service) updateUser(ctx context.Context, actor Actor, id int64, action string, diff any, fn func(q store.DBTX, u *store.User) error) (*store.User, error) {
	var u *store.User
	err := s.mutate(ctx, change{actor: actor, action: action, entity: "user", entityID: func() int64 { return id }, event: EvUserChanged, diff: diff},
		func(q store.DBTX) error {
			var err error
			if u, err = store.GetUser(ctx, q, id); err != nil {
				return err
			}
			if err := fn(q, u); err != nil {
				return err
			}
			u.Status = computeStatus(u, s.now())
			return store.UpdateUser(ctx, q, u)
		})
	return u, err
}

// ExtendUser adds months/days counting from max(now, expire_at), so paid days are never lost.
func (s *Service) ExtendUser(ctx context.Context, actor Actor, id int64, months, days int) (*store.User, error) {
	if months < 0 || days < 0 || months+days == 0 {
		return nil, invalid("extension must be positive")
	}
	return s.updateUser(ctx, actor, id, "user.extend", map[string]int{"months": months, "days": days}, func(_ store.DBTX, u *store.User) error {
		from := s.now()
		if u.ExpireAt != nil && *u.ExpireAt > from.Unix() {
			from = time.Unix(*u.ExpireAt, 0)
		}
		exp := from.AddDate(0, months, days).Unix()
		u.ExpireAt = &exp
		return nil
	})
}

// SetUserExpiry sets an absolute expiry (nil = never).
func (s *Service) SetUserExpiry(ctx context.Context, actor Actor, id int64, at *time.Time) (*store.User, error) {
	return s.updateUser(ctx, actor, id, "user.set_expiry", map[string]any{"expire_at": at}, func(_ store.DBTX, u *store.User) error {
		if at == nil {
			u.ExpireAt = nil
		} else {
			v := at.Unix()
			u.ExpireAt = &v
		}
		return nil
	})
}

// SetUserEnabled enables or disables a user.
func (s *Service) SetUserEnabled(ctx context.Context, actor Actor, id int64, enabled bool) (*store.User, error) {
	return s.updateUser(ctx, actor, id, "user.set_enabled", map[string]bool{"enabled": enabled}, func(_ store.DBTX, u *store.User) error {
		u.Disabled = !enabled
		return nil
	})
}

// SetUserTrafficLimit sets the traffic limit (nil or <=0 = unlimited).
func (s *Service) SetUserTrafficLimit(ctx context.Context, actor Actor, id int64, limit *int64) (*store.User, error) {
	return s.updateUser(ctx, actor, id, "user.set_limit", map[string]any{"limit": limit}, func(_ store.DBTX, u *store.User) error {
		u.TrafficLimitBytes = nilIfNonPositive(limit)
		return nil
	})
}

// ResetUserTraffic zeroes the period counter.
func (s *Service) ResetUserTraffic(ctx context.Context, actor Actor, id int64) (*store.User, error) {
	return s.updateUser(ctx, actor, id, "user.reset_traffic", nil, func(_ store.DBTX, u *store.User) error {
		u.TrafficUsedBytes = 0
		now := s.now().Unix()
		u.LastResetAt = &now
		return nil
	})
}

// SetUserGroups replaces a user's groups.
func (s *Service) SetUserGroups(ctx context.Context, actor Actor, id int64, groupIDs []int64) (*store.User, error) {
	return s.updateUser(ctx, actor, id, "user.set_groups", map[string]any{"groups": groupIDs}, func(q store.DBTX, u *store.User) error {
		for _, g := range groupIDs {
			if _, err := store.GetGroup(ctx, q, g); err != nil {
				return fmt.Errorf("group %d: %w", g, err)
			}
		}
		return store.SetUserGroups(ctx, q, u.ID, groupIDs)
	})
}

// ReissueUser issues a new subscription token and/or VLESS UUID (leak response).
func (s *Service) ReissueUser(ctx context.Context, actor Actor, id int64, subToken, uuid bool) (*store.User, error) {
	return s.updateUser(ctx, actor, id, "user.reissue", map[string]bool{"sub_token": subToken, "uuid": uuid}, func(_ store.DBTX, u *store.User) error {
		var err error
		if subToken {
			if u.SubToken, err = newSubToken(); err != nil {
				return err
			}
		}
		if uuid {
			if u.UUID, err = xrayconf.NewUUID(); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteUser removes a user.
func (s *Service) DeleteUser(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "user.delete", entity: "user", entityID: func() int64 { return id }, event: EvUserDeleted},
		func(q store.DBTX) error { return store.DeleteUser(ctx, q, id) })
}

// RefreshStatuses recomputes statuses (expiry passes with time). It returns how many changed.
func (s *Service) RefreshStatuses(ctx context.Context) (int, error) {
	users, err := store.ListUsers(ctx, s.st.DB, store.UserFilter{})
	if err != nil {
		return 0, err
	}
	now := s.now()
	changed := 0
	for _, u := range users {
		st := computeStatus(u, now)
		if st == u.Status {
			continue
		}
		id, from := u.ID, u.Status
		if err := s.mutate(ctx, change{actor: ActorSystem, action: "user.status", entity: "user", entityID: func() int64 { return id }, event: EvUserChanged,
			diff: map[string]string{"from": from, "to": st}},
			func(q store.DBTX) error { return store.SetUserStatus(ctx, q, id, st) }); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// User loads a user by id.
func (s *Service) User(ctx context.Context, id int64) (*store.User, error) {
	return store.GetUser(ctx, s.st.DB, id)
}

// UserByUsername loads a user by username.
func (s *Service) UserByUsername(ctx context.Context, username string) (*store.User, error) {
	return store.GetUserByUsername(ctx, s.st.DB, username)
}

// Users lists users.
func (s *Service) Users(ctx context.Context, f store.UserFilter) ([]*store.User, error) {
	return store.ListUsers(ctx, s.st.DB, f)
}

// UserGroupIDs lists a user's groups.
func (s *Service) UserGroupIDs(ctx context.Context, id int64) ([]int64, error) {
	return store.UserGroupIDs(ctx, s.st.DB, id)
}

func newSubToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func nilIfNonPositive(p *int64) *int64 {
	if p == nil || *p <= 0 {
		return nil
	}
	return p
}
