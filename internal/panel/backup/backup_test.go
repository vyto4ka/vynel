package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/ca"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestBackupRestore(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(src, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"alice", "bob"} {
		if _, err := svc.CreateUser(ctx, service.ActorCLI, service.CreateUserInput{Username: n}); err != nil {
			t.Fatal(err)
		}
	}
	_ = svc.SetSetting(ctx, service.ActorCLI, service.SettingSubDomain, "nl.example.com")
	authority, err := ca.LoadOrCreate(filepath.Join(src, "ca"))
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	meta, err := Create(ctx, st, src, "test", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Users != 2 || meta.Domain != "nl.example.com" {
		t.Fatalf("meta %+v", meta)
	}
	archive := filepath.Join(t.TempDir(), FileName(meta, nil2utc()))
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	// Into an empty directory, then over existing data (moved aside, not deleted).
	for i, dst := range []string{t.TempDir(), src} {
		if i == 1 {
			st.Close()
		}
		m, aside, err := Restore(archive, dst, "")
		if err != nil {
			t.Fatal(err)
		}
		if m.Users != 2 || (i == 0) != (aside == "") {
			t.Fatalf("restore %d: %+v aside=%q", i, m, aside)
		}
		st2, err := store.Open(ctx, filepath.Join(dst, "panel.db"))
		if err != nil {
			t.Fatal(err)
		}
		us, err := service.New(st2).Users(ctx, store.UserFilter{})
		st2.Close()
		if err != nil || len(us) != 2 {
			t.Fatalf("users after restore: %v %d", err, len(us))
		}
		ca2, err := ca.LoadOrCreate(filepath.Join(dst, "ca"))
		if err != nil || ca2.Fingerprint() != authority.Fingerprint() {
			t.Fatalf("CA not restored: %v", err)
		}
	}
}

func TestRestoreRejectsBadArchives(t *testing.T) {
	dir := t.TempDir()
	write := func(files map[string]string) string {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for name, body := range files {
			_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
			_, _ = tw.Write([]byte(body))
		}
		_ = tw.Close()
		_ = gz.Close()
		p := filepath.Join(t.TempDir(), "x.tar.gz")
		_ = os.WriteFile(p, buf.Bytes(), 0o600)
		return p
	}
	if _, _, err := Restore(write(map[string]string{"panel.db": "x"}), dir, ""); err == nil {
		t.Fatal("archive without meta.json accepted")
	}
	if _, _, err := Restore(write(map[string]string{"meta.json": `{"format":1}`}), dir, ""); err == nil {
		t.Fatal("archive without panel.db accepted")
	}
	if _, _, err := Restore(write(map[string]string{"meta.json": `{"format":1}`, "panel.db": "x", "ca/../../evil": "x"}), dir, ""); err != nil {
		t.Fatal(err) // the odd entry is ignored, nothing escapes
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
		t.Fatal("wrote outside the data directory")
	}
}

func nil2utc() *time.Location { return time.UTC }

// An encrypted backup (backup.password): an age file that needs the password to inspect and
// restore, and restores like a plain one with it.
func TestEncryptedBackup(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(src, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(ctx, service.ActorCLI, service.CreateUserInput{Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ca.LoadOrCreate(filepath.Join(src, "ca")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := Create(ctx, st, src, "test", &buf); err != nil {
		t.Fatal(err)
	}
	enc, err := Encrypt(buf.Bytes(), "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("SQLite format")) || bytes.Contains(enc, []byte("meta.json")) {
		t.Fatal("the encrypted backup shows its content")
	}
	archive := filepath.Join(t.TempDir(), "b.tar.gz"+EncryptedExt)
	if err := os.WriteFile(archive, enc, 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, _ := IsEncrypted(archive); !ok {
		t.Fatal("not recognized as encrypted")
	}
	if _, err := InspectFile(archive, ""); !errors.Is(err, ErrPasswordNeeded) {
		t.Fatalf("no password: %v", err)
	}
	if _, err := InspectFile(archive, "wrong password"); err == nil {
		t.Fatal("wrong password accepted")
	}
	dst := t.TempDir()
	if _, _, err := Restore(archive, dst, "wrong password"); err == nil {
		t.Fatal("restore with a wrong password")
	}
	m, _, err := Restore(archive, dst, "correct horse")
	if err != nil || m.Users != 1 {
		t.Fatalf("restore: %v %+v", err, m)
	}
	if _, err := os.Stat(filepath.Join(dst, "panel.db")); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(dst, ".restore-plain-*")); len(left) != 0 {
		t.Fatalf("decrypted copy left behind: %v", left)
	}
}
