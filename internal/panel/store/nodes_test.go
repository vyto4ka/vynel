package store

import (
	"context"
	"testing"
)

// The manual-node migration adds nodes.manual and round-trips it (0 by default).
func TestManualNodeMigration(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	n := &Node{Name: "Manual", Code: "MN", Enabled: true, Manual: true}
	if err := CreateNode(ctx, st.DB, n); err != nil {
		t.Fatal(err)
	}
	got, err := GetNode(ctx, st.DB, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Manual {
		t.Fatal("manual node did not round-trip")
	}
	if err := SetNodeManual(ctx, st.DB, n.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err = GetNode(ctx, st.DB, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Manual {
		t.Fatal("manual was not turned off")
	}

	plain := &Node{Name: "Auto", Code: "AT", Enabled: true}
	if err := CreateNode(ctx, st.DB, plain); err != nil {
		t.Fatal(err)
	}
	if got, err = GetNode(ctx, st.DB, plain.ID); err != nil {
		t.Fatal(err)
	}
	if got.Manual {
		t.Fatal("a node created without manual must not be manual")
	}
	// The column exists on the pre-migration table shape too: existing rows default to 0.
	var manual int
	if err := st.DB.QueryRow(`SELECT manual FROM nodes WHERE id=?`, plain.ID).Scan(&manual); err != nil || manual != 0 {
		t.Fatalf("default manual = %d, err %v", manual, err)
	}
}
