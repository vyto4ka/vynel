package service

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestGroupEnrollmentCopiesIndividualTerms(t *testing.T) {
	f := newFixture(t)
	zero, two := int64(0), int64(2)
	d := &store.GroupDefaults{Expiry: "period", Months: 1, Days: 2, Traffic: "limit", TrafficLimit: 100, Reset: "month", HWIDLimit: &two, IPLimit: &zero}
	g := must(f.s.SaveGroup(f.ctx, ActorCLI, 0, GroupInput{Name: "Trial", Defaults: d, SetDefaults: true}))
	a := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "a"}))
	b := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "b"}))
	other := must(f.s.UserGroupIDs(f.ctx, a.ID))
	a.Disabled, a.TrafficUsedBytes, a.LifetimeUsedBytes = true, 70, 200
	if err := store.UpdateUser(f.ctx, f.s.st.DB, a); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.st.DB.ExecContext(f.ctx, `UPDATE users SET lifetime_used_bytes=200 WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	r := must(f.s.ChangeGroupMembers(f.ctx, ActorCLI, g.ID, []int64{a.ID, b.ID, a.ID}, true, true))
	if !reflect.DeepEqual(r.Changed, []int64{a.ID, b.ID}) {
		t.Fatalf("changed: %+v", r)
	}
	a = must(f.s.User(f.ctx, a.ID))
	b = must(f.s.User(f.ctx, b.ID))
	if *a.TrafficLimitBytes != 100 || *b.TrafficLimitBytes != 100 || *a.ExpireAt != f.now.AddDate(0, 1, 2).Unix() || *a.HWIDLimit != 2 || *a.IPLimit != 0 || a.ResetStrategy != "month" {
		t.Fatalf("terms not copied: %+v / %+v", a, b)
	}
	if a.TrafficUsedBytes != 70 || a.LifetimeUsedBytes != 200 || !a.Disabled || a.Status != store.StatusDisabled || b.TrafficUsedBytes != 0 || b.Status != store.StatusActive {
		t.Fatal("enrollment changed usage or enabled state / shared a quota")
	}
	if ids := must(f.s.UserGroupIDs(f.ctx, a.ID)); !reflect.DeepEqual(ids, append(other, g.ID)) {
		t.Fatalf("other groups lost: %v", ids)
	}
	// Saving or renaming the group does not retroactively overwrite subscriptions.
	d.TrafficLimit = 1
	must(f.s.SaveGroup(f.ctx, ActorCLI, g.ID, GroupInput{Name: "Trial2", Defaults: d, SetDefaults: true}))
	must(f.s.UpdateGroup(f.ctx, ActorCLI, g.ID, "Trial3", "keep policy"))
	if got := must(f.s.Group(f.ctx, g.ID)); got.Defaults == nil || got.Defaults.TrafficLimit != 1 {
		t.Fatal("legacy rename cleared policy")
	}
	f.now = f.now.Add(24 * time.Hour)
	r = must(f.s.ChangeGroupMembers(f.ctx, ActorCLI, g.ID, []int64{a.ID, b.ID}, true, true))
	if len(r.Changed) != 0 || len(r.Unchanged) != 2 || *must(f.s.User(f.ctx, b.ID)).TrafficLimitBytes != 100 {
		t.Fatal("retry reapplied terms")
	}
	must(f.s.ChangeGroupMembers(f.ctx, ActorCLI, g.ID, []int64{a.ID}, false, false))
	if ids := must(f.s.UserGroupIDs(f.ctx, a.ID)); !reflect.DeepEqual(ids, other) {
		t.Fatalf("removal changed other groups: %v", ids)
	}
	if u := must(f.s.User(f.ctx, a.ID)); *u.TrafficLimitBytes != 100 || u.TrafficUsedBytes != 70 {
		t.Fatal("removal reverted copied terms")
	}
	// Explicit add without applying a template keeps existing terms.
	must(f.s.ChangeGroupMembers(f.ctx, ActorCLI, g.ID, []int64{a.ID}, true, false))
	if *must(f.s.User(f.ctx, a.ID)).TrafficLimitBytes != 100 {
		t.Fatal("add without apply changed terms")
	}
}

func TestGroupMembershipAtomicAndValidated(t *testing.T) {
	f := newFixture(t)
	g := must(f.s.CreateGroup(f.ctx, ActorCLI, "Test", ""))
	u := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "member"}))
	before := must(f.s.UserGroupIDs(f.ctx, u.ID))
	audit := must(f.s.Audit(f.ctx, 100))
	if _, err := f.s.ChangeGroupMembers(f.ctx, ActorCLI, g.ID, []int64{u.ID, 999999}, true, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	if ids := must(f.s.UserGroupIDs(f.ctx, u.ID)); !reflect.DeepEqual(before, ids) {
		t.Fatal("partial membership committed")
	}
	if len(must(f.s.Audit(f.ctx, 100))) != len(audit) {
		t.Fatal("failed batch committed audit")
	}
	for _, ids := range [][]int64{nil, {0}, {-1}, make([]int64, 501)} {
		if _, err := f.s.ChangeGroupMembers(f.ctx, ActorCLI, g.ID, ids, true, true); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid batch %v: %v", ids, err)
		}
	}
	for _, d := range []*store.GroupDefaults{{Expiry: "bad"}, {Expiry: "date"}, {Expiry: "period"}, {Traffic: "limit"}, {Reset: "hour"}, {HWIDLimit: new(int64(-1))}} {
		if _, err := f.s.SaveGroup(f.ctx, ActorCLI, g.ID, GroupInput{Name: "Test", Defaults: d, SetDefaults: true}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid policy %+v: %v", d, err)
		}
	}
}

func TestCreateUserInGroupAndClearDefaults(t *testing.T) {
	f := newFixture(t)
	date := f.now.Add(7 * 24 * time.Hour).Unix()
	g := must(f.s.SaveGroup(f.ctx, ActorCLI, 0, GroupInput{Name: "Event", Defaults: &store.GroupDefaults{Expiry: "date", ExpireAt: &date, Traffic: "unlimited"}, SetDefaults: true}))
	u := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "event", GroupDefaultsID: g.ID}))
	if *u.ExpireAt != date || u.TrafficLimitBytes != nil || !reflect.DeepEqual(must(f.s.UserGroupIDs(f.ctx, u.ID)), []int64{g.ID}) {
		t.Fatalf("creation: %+v", u)
	}
	g = must(f.s.SaveGroup(f.ctx, ActorCLI, g.ID, GroupInput{Name: "Event", SetDefaults: true}))
	if g.Defaults != nil || must(f.s.Group(f.ctx, g.ID)).Defaults != nil {
		t.Fatal("clear policy did not persist")
	}
	if *must(f.s.User(f.ctx, u.ID)).ExpireAt != date {
		t.Fatal("clear changed existing user")
	}
	if _, err := f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "bad-group", GroupDefaultsID: 99999}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if got := must(f.s.CountUsers(f.ctx, store.UserFilter{Search: "bad-group"})); got != 0 {
		t.Fatal("user created outside transaction")
	}
	// Unlimited expiry is a distinct action from preserving the old expiry.
	g = must(f.s.SaveGroup(f.ctx, ActorCLI, g.ID, GroupInput{Name: "Event", Defaults: &store.GroupDefaults{Expiry: "never"}, SetDefaults: true}))
	v := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "forever", GroupDefaultsID: g.ID}))
	if v.ExpireAt != nil {
		t.Fatal("never expiry not applied")
	}
}

func TestGroupResetScheduleKeepsUsageUntilNextPeriod(t *testing.T) {
	for _, tc := range []struct{ from, to string }{{"no", "month"}, {"month", "week"}, {"week", "day"}} {
		t.Run(tc.from+"-to-"+tc.to, func(t *testing.T) {
			f := newFixture(t)
			u := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "existing", ResetStrategy: tc.from}))
			old := f.now.AddDate(0, -2, 0).Unix()
			if _, err := f.s.st.DB.ExecContext(f.ctx, `UPDATE users SET created_at=?, traffic_used_bytes=70, lifetime_used_bytes=120, last_reset_at=NULL WHERE id=?`, old, u.ID); err != nil {
				t.Fatal(err)
			}
			g := must(f.s.SaveGroup(f.ctx, ActorCLI, 0, GroupInput{Name: "New schedule", SetDefaults: true, Defaults: &store.GroupDefaults{Traffic: "limit", TrafficLimit: 100, Reset: tc.to}}))
			must(f.s.ChangeGroupMembers(f.ctx, ActorCLI, g.ID, []int64{u.ID}, true, true))
			// Running the actual background job right away used to erase all 70 bytes.
			if n := must(f.s.ResetDueTraffic(f.ctx)); n != 0 {
				t.Fatalf("enrollment caused %d immediate resets", n)
			}
			got := must(f.s.User(f.ctx, u.ID))
			if got.TrafficUsedBytes != 70 || got.LifetimeUsedBytes != 120 {
				t.Fatalf("usage changed: %+v", got)
			}
			// It still resets normally at the first calendar boundary after enrollment.
			f.now = f.now.AddDate(0, 1, 0)
			if n := must(f.s.ResetDueTraffic(f.ctx)); n != 1 {
				t.Fatalf("new schedule did not reset: %d", n)
			}
			got = must(f.s.User(f.ctx, u.ID))
			if got.TrafficUsedBytes != 0 || got.LifetimeUsedBytes != 120 {
				t.Fatalf("scheduled reset: %+v", got)
			}
		})
	}
}

func TestGroupEnrollmentDoesNotPostponeUnchangedReset(t *testing.T) {
	f := newFixture(t)
	u := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "monthly", ResetStrategy: "month"}))
	old := f.now.AddDate(0, -2, 0).Unix()
	if _, err := f.s.st.DB.ExecContext(f.ctx, `UPDATE users SET created_at=?, traffic_used_bytes=70, last_reset_at=? WHERE id=?`, old, old, u.ID); err != nil {
		t.Fatal(err)
	}
	g := must(f.s.SaveGroup(f.ctx, ActorCLI, 0, GroupInput{Name: "Same schedule", SetDefaults: true, Defaults: &store.GroupDefaults{Reset: "month"}}))
	must(f.s.ChangeGroupMembers(f.ctx, ActorCLI, g.ID, []int64{u.ID}, true, true))
	if got := must(f.s.User(f.ctx, u.ID)); got.LastResetAt == nil || *got.LastResetAt != old {
		t.Fatal("unchanged reset was postponed")
	}
	if n := must(f.s.ResetDueTraffic(f.ctx)); n != 1 {
		t.Fatalf("overdue reset should still run: %d", n)
	}
}
