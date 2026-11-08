// Package usertransfer moves users between installations without touching nodes, keys or
// settings. An export is a small versioned JSON file: username, uuid, subscription token,
// limits and traffic. With it a panel is rebuilt on another server (a different IP or domain)
// and clients keep working, or users arrive from Remnawave and 3x-ui.
package usertransfer

import (
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// ExportMeta describes an export (the "meta" object of the file).
type ExportMeta struct {
	Format        int    `json:"format"`
	CreatedAt     int64  `json:"createdAt"`
	Source        string `json:"source"`
	SourceVersion string `json:"sourceVersion,omitempty"`
	Users         int    `json:"users"`
}

// ExportFile is the whole file: meta and the users.
type ExportFile struct {
	Meta  ExportMeta     `json:"meta"`
	Users []TransferUser `json:"users"`
}

// TransferUser is a user as it travels. Fields follow store.User; the store ID and timestamps
// of the old panel are left behind, everything a subscription needs is kept. Groups travel by
// name: IDs are local to an installation.
type TransferUser struct {
	Username          string   `json:"username"`
	UUID              string   `json:"uuid"`
	SubToken          string   `json:"subToken,omitempty"`
	Status            string   `json:"status"`
	ExpireAt          *int64   `json:"expireAt,omitempty"`
	TrafficLimitBytes *int64   `json:"trafficLimitBytes,omitempty"`
	TrafficUsedBytes  int64    `json:"trafficUsedBytes,omitempty"`
	ResetStrategy     string   `json:"resetStrategy,omitempty"`
	HWIDLimit         *int64   `json:"hwidLimit,omitempty"`
	HWIDOff           bool     `json:"hwidOff,omitempty"`
	IPLimit           *int64   `json:"ipLimit,omitempty"`
	ClientType        string   `json:"clientType,omitempty"`
	TelegramID        *int64   `json:"telegramId,omitempty"`
	ExternalID        *string  `json:"externalId,omitempty"`
	Note              string   `json:"note,omitempty"`
	Groups            []string `json:"groups,omitempty"`
}

// ImportOptions say what Import does with a user that is already there. Users are matched by
// username (the natural key), then by uuid. ReissueTokens gives every imported user a fresh
// subscription token: the old links stop working, the report lists the new ones.
type ImportOptions struct {
	Mode          string // skip (default), upsert or rename
	DryRun        bool
	WithTraffic   bool // copy TrafficUsedBytes (always true when the file has it and upsert)
	ReissueTokens bool
	CreateGroups  bool // create groups missing on this panel and grant them AccessAll
	Actor         string
}

// Import modes.
const (
	ModeSkip   = "skip"
	ModeUpsert = "upsert"
	ModeRename = "rename"
)

// Report is what Import did (or, on DryRun, would do).
type Report struct {
	Created       int
	Updated       int
	Skipped       int
	Renamed       int
	Failed        int
	Problems      []string        // one line per failed user
	Links         []ReportLink    // subscription links of the reissued users
	unknowns      map[string]bool // group names not found on this panel (computed)
	createdGroups map[string]bool // group names auto-created during import
}

// ReportLink is a new subscription link after ReissueTokens.
type ReportLink struct {
	Username string
	Link     string
}

func newReport() *Report {
	return &Report{unknowns: map[string]bool{}, createdGroups: map[string]bool{}}
}

// MissingGroups lists group names the export had that this panel does not (in stable order).
func (r *Report) MissingGroups() []string {
	if len(r.unknowns) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.unknowns))
	for g := range r.unknowns {
		out = append(out, g)
	}
	sortStrings(out)
	return out
}

// CreatedGroups lists group names created during import (in stable order).
func (r *Report) CreatedGroups() []string {
	if len(r.createdGroups) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.createdGroups))
	for g := range r.createdGroups {
		out = append(out, g)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// user transfers one user's limits and identity into a store.User shell. Shared by import
// paths so the file format and the Remnawave/3x-ui sources agree on defaults.
func (t TransferUser) user() *store.User {
	return &store.User{
		Username:          t.Username,
		UUID:              t.UUID,
		SubToken:          t.SubToken,
		Disabled:          t.Status == store.StatusDisabled,
		Status:            t.Status,
		ExpireAt:          t.ExpireAt,
		TrafficLimitBytes: t.TrafficLimitBytes,
		TrafficUsedBytes:  t.TrafficUsedBytes,
		ResetStrategy:     t.ResetStrategy,
		HWIDLimit:         t.HWIDLimit,
		HWIDOff:           t.HWIDOff,
		IPLimit:           t.IPLimit,
		ClientType:        t.ClientType,
		TelegramID:        t.TelegramID,
		ExternalID:        t.ExternalID,
		Note:              t.Note,
	}
}

// unixNow is swapped in tests.
var unixNow = func() int64 { return time.Now().Unix() }
