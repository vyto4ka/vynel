package usertransfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func i64(v int64) *int64 { return &v }

// An export reads back the same users: names, tokens, limits, traffic, groups by name.
func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	src, dst := seedPanel(t, st), testStore(t)

	f, err := Export(ctx, src, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Meta.Source != SourceVynel || f.Meta.Format != Format || f.Meta.Users != 2 {
		t.Fatalf("meta: %+v", f.Meta)
	}
	rep := importTo(t, dst, f, ImportOptions{})
	if rep.Created != 2 || rep.Failed != 0 || rep.Skipped != 0 {
		t.Fatalf("report: %+v", rep)
	}
	for _, name := range []string{"alice", "bob"} {
		u, err := store.GetUserByUsername(ctx, dst.DB, name)
		if err != nil {
			t.Fatalf("user %s: %v", name, err)
		}
		orig, _ := store.GetUserByUsername(ctx, src.DB, name)
		if u.UUID != orig.UUID || u.SubToken != orig.SubToken {
			t.Errorf("%s: uuid/subToken not preserved: %q/%q", name, u.UUID, u.SubToken)
		}
		if u.Status != orig.Status || deref(u.ExpireAt) != deref(orig.ExpireAt) || deref(u.TrafficLimitBytes) != deref(orig.TrafficLimitBytes) {
			t.Errorf("%s: fields not copied: %+v vs %+v", name, u, orig)
		}
	}
	// Groups by name: dst has a group with the same name.
	if err := store.CreateGroup(ctx, dst.DB, &store.Group{Name: "friends"}); err != nil {
		t.Fatal(err)
	}
	rep = importTo(t, dst, f, ImportOptions{Mode: ModeUpsert})
	if rep.Updated != 2 {
		t.Fatalf("upsert report: %+v", rep)
	}
	ids, err := store.UserGroupIDs(ctx, dst.DB, mustUser(t, dst, "alice").ID)
	if err != nil || len(ids) != 1 {
		t.Errorf("alice groups after import: %v (%v)", ids, err)
	}
}

func seedPanel(t *testing.T, st *store.Store) *store.Store {
	t.Helper()
	ctx := context.Background()
	if err := store.CreateGroup(ctx, st.DB, &store.Group{Name: "friends"}); err != nil {
		t.Fatal(err)
	}
	g, _ := store.GetGroupByName(ctx, st.DB, "friends")
	for _, u := range []*store.User{
		{Username: "alice", UUID: "uuid-alice", SubToken: "token-alice", Status: store.StatusActive, ExpireAt: i64(2e9), TrafficLimitBytes: i64(1 << 30), TrafficUsedBytes: 777, ResetStrategy: "no", ClientType: "auto"},
		{Username: "bob", UUID: "uuid-bob", SubToken: "token-bob", Status: store.StatusDisabled, ResetStrategy: "month", Note: "from bob's server"},
	} {
		if err := store.CreateUser(ctx, st.DB, u); err != nil {
			t.Fatal(err)
		}
		if err := store.SetUserGroups(ctx, st.DB, u.ID, []int64{g.ID}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func mustUser(t *testing.T, st *store.Store, name string) *store.User {
	t.Helper()
	u, err := store.GetUserByUsername(context.Background(), st.DB, name)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func importTo(t *testing.T, st *store.Store, f *ExportFile, opt ImportOptions) *Report {
	t.Helper()
	im := NewImporter(st, opt)
	im.NewTokenFn = func() (string, error) { return "minted-" + f.Meta.Source, nil }
	im.OnUnknownGroup = func(string) {}
	rep, err := im.Import(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// Import modes: skip leaves the local user, upsert rewrites him, rename lands both.
func TestImportModes(t *testing.T) {
	ctx := context.Background()
	f := &ExportFile{Meta: ExportMeta{Format: Format, Source: SourceVynel, Users: 1},
		Users: []TransferUser{{Username: "carl", UUID: "uuid-carl", SubToken: "token-carl", Status: store.StatusActive}}}

	local := func(t *testing.T) (*store.Store, *store.User) {
		st := testStore(t)
		u := &store.User{Username: "carl", UUID: "local-uuid", SubToken: "local-token", Status: store.StatusActive, ResetStrategy: "no"}
		if err := store.CreateUser(ctx, st.DB, u); err != nil {
			t.Fatal(err)
		}
		return st, u
	}

	t.Run("skip", func(t *testing.T) {
		st, before := local(t)
		rep := importTo(t, st, f, ImportOptions{})
		if rep.Skipped != 1 || rep.Created != 0 {
			t.Fatalf("%+v", rep)
		}
		if got := mustUser(t, st, "carl"); got.UUID != before.UUID {
			t.Errorf("local user rewritten on skip")
		}
	})
	t.Run("upsert", func(t *testing.T) {
		st, _ := local(t)
		rep := importTo(t, st, f, ImportOptions{Mode: ModeUpsert})
		if rep.Updated != 1 || rep.Created != 0 {
			t.Fatalf("%+v", rep)
		}
		if got := mustUser(t, st, "carl"); got.UUID != "uuid-carl" || got.SubToken != "token-carl" {
			t.Errorf("upsert did not take the export: %+v", got)
		}
	})
	t.Run("rename", func(t *testing.T) {
		st, _ := local(t)
		rep := importTo(t, st, f, ImportOptions{Mode: ModeRename})
		if rep.Created != 1 || rep.Renamed != 0 {
			t.Fatalf("%+v", rep)
		}
		if got := mustUser(t, st, "carl-2"); got.UUID != "uuid-carl" {
			t.Errorf("renamed user: %+v", got)
		}
	})
}

// DryRun touches nothing; reissue hands out new tokens and lists them.
func TestDryRunAndReissue(t *testing.T) {
	ctx := context.Background()
	f := &ExportFile{Meta: ExportMeta{Format: Format, Source: SourceVynel, Users: 1},
		Users: []TransferUser{{Username: "dana", UUID: "uuid-dana", SubToken: "token-dana", Status: store.StatusActive}}}

	st := testStore(t)
	rep := importTo(t, st, f, ImportOptions{DryRun: true})
	if rep.Created != 1 {
		t.Fatalf("dry-run report: %+v", rep)
	}
	if _, err := store.GetUserByUsername(ctx, st.DB, "dana"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("dry-run wrote a user: %v", err)
	}

	importTo(t, st, f, ImportOptions{ReissueTokens: true})
	d := mustUser(t, st, "dana")
	if d.SubToken == "token-dana" {
		t.Errorf("token not reissued: %q", d.SubToken)
	}
	if rep.Links != nil {
		t.Errorf("links without LinkFn: %v", rep.Links)
	}
}

// A missing uuid or name fails that user alone; the rest import.
func TestBadRowsDoNotSinkTheImport(t *testing.T) {
	st := testStore(t)
	f := &ExportFile{Meta: ExportMeta{Format: Format, Source: SourceVynel, Users: 3}, Users: []TransferUser{
		{Username: "ok-user", UUID: "uuid-ok", Status: store.StatusActive},
		{Username: "no-uuid", Status: store.StatusActive},
		{Username: "", UUID: "uuid-nameless"},
	}}
	rep := importTo(t, st, f, ImportOptions{})
	if rep.Created != 1 || rep.Failed != 2 || len(rep.Problems) != 2 {
		t.Fatalf("%+v %v", rep, rep.Problems)
	}
	if _, err := store.GetUserByUsername(context.Background(), st.DB, "ok-user"); err != nil {
		t.Errorf("ok-user missing: %v", err)
	}
}

// Unknown group names land in the report; the user gets the rest.
func TestUnknownGroupsReported(t *testing.T) {
	st := testStore(t)
	f := &ExportFile{Meta: ExportMeta{Format: Format, Source: SourceVynel, Users: 1},
		Users: []TransferUser{{Username: "erin", UUID: "uuid-erin", Status: store.StatusActive, Groups: []string{"nowhere"}}}}
	rep := importTo(t, st, f, ImportOptions{})
	if rep.Created != 1 {
		t.Fatalf("%+v", rep)
	}
	if miss := rep.MissingGroups(); len(miss) != 1 || miss[0] != "nowhere" {
		t.Fatalf("missing groups: %v", miss)
	}
}

// The file round-trips through JSON; future formats and junk are rejected.
func TestMarshalUnmarshal(t *testing.T) {
	f := &ExportFile{Meta: ExportMeta{Format: Format, CreatedAt: 100, Source: SourceVynel, Users: 1},
		Users: []TransferUser{{Username: "finn", UUID: "uuid-finn", SubToken: "token-finn", Status: store.StatusActive, TrafficUsedBytes: 5}}}
	b, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if back.Users[0].SubToken != "token-finn" || back.Users[0].TrafficUsedBytes != 5 {
		t.Fatalf("round trip: %+v", back.Users[0])
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Unmarshal([]byte(`{"meta":{"format":99,"source":"vynel"},"users":[{"username":"x","uuid":"u"}]}`)); err == nil {
		t.Error("future format accepted")
	}
	if _, err := Unmarshal([]byte(`{"meta":{"format":1},"users":[]}`)); err == nil {
		t.Error("empty export accepted")
	}
	if _, err := Unmarshal([]byte(`not json`)); err == nil {
		t.Error("junk accepted")
	}
}

func deref(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}
