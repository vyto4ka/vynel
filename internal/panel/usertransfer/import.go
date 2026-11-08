package usertransfer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// Importer applies an export to a panel. It works on a bare store (the installer path, before
// the panel exists) and reuses the service later.
type Importer struct {
	st *store.Store
	// NewTokenFn mints a subscription token; LinkFn renders the subscription link of a user
	// (both set by the caller that has the service at hand).
	NewTokenFn func() (string, error)
	LinkFn     func(*store.User) (string, error)

	// OnUnknownGroup is told each group name the panel does not have (reporting); the report
	// records them too.
	OnUnknownGroup func(name string)

	unknowns map[string]bool
	report   *Report

	opt ImportOptions
}

// NewImporter binds an importer to a store.
func NewImporter(st *store.Store, opt ImportOptions) *Importer {
	if opt.Mode == "" {
		opt.Mode = ModeSkip
	}
	if opt.Actor == "" {
		opt.Actor = "cli"
	}
	return &Importer{st: st, opt: opt}
}

// ErrFormat describes an unreadable export.
type ErrFormat struct{ msg string }

func (e *ErrFormat) Error() string { return e.msg }

func errFormat(format string, a ...any) error {
	return &ErrFormat{msg: fmt.Sprintf(format, a...)}
}

// Import applies the users. On DryRun nothing is written; the report says what would happen.
// Each user is its own transaction: a bad one does not sink the rest.
func (im *Importer) Import(ctx context.Context, f *ExportFile) (*Report, error) {
	if im.opt.Mode != ModeSkip && im.opt.Mode != ModeUpsert && im.opt.Mode != ModeRename {
		return nil, errFormat("unknown import mode %q", im.opt.Mode)
	}
	rep := newReport()
	im.report = rep
	im.unknowns = map[string]bool{}
	for _, t := range f.Users {
		if err := im.one(ctx, t, rep); err != nil {
			rep.Failed++
			rep.Problems = append(rep.Problems, fmt.Sprintf("%s: %v", t.Username, err))
		}
	}
	return rep, nil
}

func (im *Importer) one(ctx context.Context, t TransferUser, rep *Report) error {
	if t.Username = strings.TrimSpace(t.Username); t.Username == "" {
		return errFormat("empty username")
	}
	if t.UUID == "" {
		return errFormat("no uuid")
	}
	existing, exErr := store.GetUserByUsername(ctx, im.st.DB, t.Username)
	if exErr != nil && !isNotFound(exErr) {
		return exErr
	}
	// A username may exist with another uuid: the same person on another server. For skip we
	// still skip (the name is taken); for upsert the export wins.
	if existing == nil {
		if byUUID, err := store.GetUserByUUID(ctx, im.st.DB, t.UUID); err != nil && !isNotFound(err) {
			return err
		} else if byUUID != nil {
			// Same uuid, different name: same person renamed on this panel. Upsert updates them.
			if im.opt.Mode == ModeUpsert {
				return im.upsert(ctx, byUUID, t, rep)
			}
			rep.Skipped++
			return nil
		}
		return im.create(ctx, t, rep)
	}
	switch im.opt.Mode {
	case ModeSkip:
		rep.Skipped++
		return nil
	case ModeUpsert:
		return im.upsert(ctx, existing, t, rep)
	default: // rename
		return im.create(ctx, renamed(t), rep)
	}
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }

func renamed(t TransferUser) TransferUser {
	t.Username = t.Username + "-2"
	return t
}

func (im *Importer) create(ctx context.Context, t TransferUser, rep *Report) error {
	u := t.user()
	if u.Status == "" {
		u.Status = store.StatusActive
	}
	if u.ResetStrategy == "" {
		u.ResetStrategy = "no"
	}
	if u.SubToken == "" || im.opt.ReissueTokens {
		tok, err := im.newToken()
		if err != nil {
			return err
		}
		u.SubToken = tok
	}
	if !im.opt.WithTraffic {
		u.TrafficUsedBytes = 0
	}
	u.CreatedBy = im.opt.Actor
	if im.opt.DryRun {
		rep.Created++
		return nil
	}
	err := im.st.Tx(ctx, func(q store.DBTX) error {
		if err := store.CreateUser(ctx, q, u); err != nil {
			return err
		}
		return im.applyGroups(ctx, q, u.ID, t.Groups)
	})
	if err != nil {
		return err
	}
	rep.Created++
	if im.opt.ReissueTokens {
		if link, err := im.link(u); err == nil {
			rep.Links = append(rep.Links, ReportLink{Username: u.Username, Link: link})
		}
	}
	return nil
}

func (im *Importer) upsert(ctx context.Context, existing *store.User, t TransferUser, rep *Report) error {
	u := t.user()
	u.ID = existing.ID
	u.TrafficUsedBytes = t.TrafficUsedBytes
	if !im.opt.WithTraffic {
		u.TrafficUsedBytes = existing.TrafficUsedBytes
	}
	if u.Status == "" {
		u.Status = existing.Status
	}
	if u.ResetStrategy == "" {
		u.ResetStrategy = "no"
	}
	if im.opt.ReissueTokens {
		if tok, err := im.newToken(); err == nil {
			u.SubToken = tok
		}
	}
	if im.opt.DryRun {
		rep.Updated++
		return nil
	}
	err := im.st.Tx(ctx, func(q store.DBTX) error {
		if err := store.UpdateUser(ctx, q, u); err != nil {
			return err
		}
		return im.applyGroups(ctx, q, u.ID, t.Groups)
	})
	if err != nil {
		return err
	}
	rep.Updated++
	if im.opt.ReissueTokens {
		if link, err := im.link(u); err == nil {
			rep.Links = append(rep.Links, ReportLink{Username: u.Username, Link: link})
		}
	}
	return nil
}

// applyGroups maps group names to this panel's groups; names it does not have are recorded in
// the report and the user lands without them (limits live on the user, nothing is lost).
func (im *Importer) applyGroups(ctx context.Context, q store.DBTX, userID int64, names []string) error {
	if len(names) == 0 {
		return nil
	}
	var ids []int64
	for _, n := range names {
		g, err := store.GetGroupByName(ctx, q, n)
		if isNotFound(err) {
			if im.report != nil {
				im.report.unknowns[n] = true
			}
			if im.OnUnknownGroup != nil {
				im.OnUnknownGroup(n)
			}
			continue
		}
		if err != nil {
			return err
		}
		ids = append(ids, g.ID)
	}
	return store.SetUserGroups(ctx, q, userID, ids)
}

// newToken and link are set by the caller that knows how (service or standalone).
func (im *Importer) newToken() (string, error) {
	if im.NewTokenFn == nil {
		return "", errFormat("no token source")
	}
	return im.NewTokenFn()
}

func (im *Importer) link(u *store.User) (string, error) {
	if im.LinkFn == nil {
		return "", nil
	}
	return im.LinkFn(u)
}
