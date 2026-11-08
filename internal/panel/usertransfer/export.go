package usertransfer

import (
	"context"
	"encoding/json"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// Format is the version of the export file. Imports of older formats stay supported.
const Format = 1

// SourceVynel names vynel in ExportMeta.Source.
const SourceVynel = "vynel"

// ExportOptions narrow an export: which users and what of them.
type ExportOptions struct {
	Filter      store.UserFilter // applied as is; empty = everyone
	WithTraffic bool             // include TrafficUsedBytes (default: yes, it survives a move)
	WithTokens  bool             // include SubToken (default: yes; without it clients get new links)
}

// Export writes the users of this panel as an ExportFile.
func Export(ctx context.Context, st *store.Store, opt ExportOptions) (*ExportFile, error) {
	// Both are default-on for now; the flags exist so callers can tighten later formats.
	opt.WithTraffic, opt.WithTokens = true, true
	users, err := store.ListUsers(ctx, st.DB, opt.Filter)
	if err != nil {
		return nil, err
	}
	groups, err := store.UsersGroups(ctx, st.DB, userIDs(users))
	if err != nil {
		return nil, err
	}
	names, err := groupNames(ctx, st, groups)
	if err != nil {
		return nil, err
	}
	out := &ExportFile{Meta: ExportMeta{Format: Format, CreatedAt: unixNow(), Source: SourceVynel, Users: len(users)}}
	for _, u := range users {
		t := TransferUser{
			Username:          u.Username,
			UUID:              u.UUID,
			Status:            u.Status,
			ExpireAt:          u.ExpireAt,
			TrafficLimitBytes: u.TrafficLimitBytes,
			ResetStrategy:     u.ResetStrategy,
			HWIDLimit:         u.HWIDLimit,
			HWIDOff:           u.HWIDOff,
			IPLimit:           u.IPLimit,
			ClientType:        u.ClientType,
			TelegramID:        u.TelegramID,
			ExternalID:        u.ExternalID,
			Note:              u.Note,
		}
		if opt.WithTokens {
			t.SubToken = u.SubToken
		}
		if opt.WithTraffic {
			t.TrafficUsedBytes = u.TrafficUsedBytes
		}
		for _, gid := range groups[u.ID] {
			if n, ok := names[gid]; ok {
				t.Groups = append(t.Groups, n)
			}
		}
		out.Users = append(out.Users, t)
	}
	return out, nil
}

func userIDs(users []*store.User) []int64 {
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	return ids
}

func groupNames(ctx context.Context, st *store.Store, byUser map[int64][]int64) (map[int64]string, error) {
	all, err := store.ListGroups(ctx, st.DB)
	if err != nil {
		return nil, err
	}
	out := map[int64]string{}
	for _, g := range all {
		out[g.ID] = g.Name
	}
	return out, nil
}

// Marshal renders a file as indented JSON.
func (f *ExportFile) Marshal() ([]byte, error) {
	return json.MarshalIndent(f, "", "  ")
}

// Unmarshal reads a file from JSON and checks its format.
func Unmarshal(b []byte) (*ExportFile, error) {
	f := &ExportFile{}
	if err := json.Unmarshal(b, f); err != nil {
		return nil, err
	}
	if f.Meta.Format == 0 {
		return nil, errFormat("no meta.format: not a vynel users export")
	}
	if f.Meta.Format > Format {
		return nil, errFormat("export format %d is newer than this panel knows (%d); update vynel first", f.Meta.Format, Format)
	}
	if len(f.Users) == 0 {
		return nil, errFormat("the export has no users")
	}
	return f, nil
}
