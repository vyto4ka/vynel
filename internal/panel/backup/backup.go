// Package backup writes and restores the panel's data: a consistent snapshot of panel.db
// (users, nodes, keys, settings, statistics) plus the internal CA that nodes trust. With it a
// lost panel server is rebuilt and the nodes reconnect without being joined again.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// Meta describes a backup (meta.json inside the archive).
type Meta struct {
	Format    int    `json:"format"`
	CreatedAt int64  `json:"createdAt"`
	Version   string `json:"version"`
	Domain    string `json:"domain,omitempty"`
	Users     int    `json:"users"`
	Nodes     int    `json:"nodes"`
}

const format = 1

// Create writes a .tar.gz with meta.json, panel.db and ca/ to w. The database is copied with
// VACUUM INTO, which is consistent while the panel keeps running.
func Create(ctx context.Context, st *store.Store, dataDir, version string, w io.Writer) (*Meta, error) {
	tmp, err := os.MkdirTemp(dataDir, ".backup-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	snap := filepath.Join(tmp, "panel.db")
	if _, err := st.DB.ExecContext(ctx, `VACUUM INTO ?`, snap); err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	meta := &Meta{Format: format, CreatedAt: time.Now().Unix(), Version: version}
	_ = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&meta.Users)
	_ = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM nodes`).Scan(&meta.Nodes)
	_ = st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='sub.domain'`).Scan(&meta.Domain)

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	mj, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeBytes(tw, "meta.json", mj); err != nil {
		return nil, err
	}
	if err := writeFile(tw, "panel.db", snap); err != nil {
		return nil, err
	}
	caDir := filepath.Join(dataDir, "ca")
	err = filepath.WalkDir(caDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dataDir, path)
		return writeFile(tw, filepath.ToSlash(rel), path)
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("ca: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return meta, gz.Close()
}

// FileName is the archive name for a backup.
func FileName(m *Meta, loc *time.Location) string {
	name := "vynel-backup"
	if m.Domain != "" {
		name += "-" + m.Domain
	}
	return name + "-" + time.Unix(m.CreatedAt, 0).In(loc).Format("2006-01-02-1504") + ".tar.gz"
}

func writeBytes(tw *tar.Writer, name string, b []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), ModTime: time.Now()}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

func writeFile(tw *tar.Writer, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: st.Size(), ModTime: st.ModTime()}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// Inspect reads meta.json of an archive.
func Inspect(r io.Reader) (*Meta, error) {
	var meta *Meta
	err := walk(r, func(name string, _ *tar.Header, body io.Reader) error {
		if name == "meta.json" {
			meta = &Meta{}
			return json.NewDecoder(body).Decode(meta)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, errors.New("not a vynel backup: meta.json is missing")
	}
	return meta, nil
}

func walk(r io.Reader, fn func(name string, h *tar.Header, body io.Reader) error) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("not a .tar.gz: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if err := fn(h.Name, h, tr); err != nil {
			return err
		}
	}
}

// Restore unpacks an archive into dataDir. The panel must be stopped. Existing panel.db and ca/
// are moved to dataDir/.before-restore-<time> instead of being deleted. It returns where they went.
func Restore(archive, dataDir string) (*Meta, string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	meta, err := Inspect(f)
	if err != nil {
		return nil, "", err
	}
	if meta.Format > format {
		return nil, "", fmt.Errorf("the backup is from a newer vynel (format %d): update vynel first", meta.Format)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, "", err
	}
	stage, err := os.MkdirTemp(dataDir, ".restore-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(stage)
	gotDB := false
	err = walk(f, func(name string, _ *tar.Header, body io.Reader) error {
		clean := filepath.Clean(filepath.FromSlash(name))
		if clean == "meta.json" {
			return nil
		}
		if clean != "panel.db" && !strings.HasPrefix(clean, "ca"+string(filepath.Separator)) {
			return nil // unknown entries are ignored, nothing outside the stage is written
		}
		if strings.Contains(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("bad path in archive: %s", name)
		}
		dst := filepath.Join(stage, clean)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, body); err != nil {
			out.Close()
			return err
		}
		gotDB = gotDB || clean == "panel.db"
		return out.Close()
	})
	if err != nil {
		return nil, "", err
	}
	if !gotDB {
		return nil, "", errors.New("the backup has no panel.db")
	}
	aside := filepath.Join(dataDir, ".before-restore-"+time.Now().Format("20060102-150405"))
	moved := false
	for _, name := range []string{"panel.db", "panel.db-wal", "panel.db-shm", "ca"} {
		src := filepath.Join(dataDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.MkdirAll(aside, 0o700); err != nil {
			return nil, "", err
		}
		if err := os.Rename(src, filepath.Join(aside, name)); err != nil {
			return nil, "", err
		}
		moved = true
	}
	for _, name := range []string{"panel.db", "ca"} {
		src := filepath.Join(stage, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, filepath.Join(dataDir, name)); err != nil {
			return nil, "", err
		}
	}
	if !moved {
		aside = ""
	}
	return meta, aside, nil
}
