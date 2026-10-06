// Package decoy holds the static decoy sites shown to anyone who is not a VPN client: behind
// Reality (self-steal), on the subscription domain and for unknown subscription tokens
// (docs/STEALTH.md §2.3).
package decoy

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

//go:embed sites
var sites embed.FS

// Names lists the available decoy sites.
func Names() []string {
	entries, _ := sites.ReadDir("sites")
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Valid reports whether name is a known site.
func Valid(name string) bool {
	_, err := fs.Stat(sites, "sites/"+name+"/index.html")
	return err == nil
}

// NotFound returns the 404 page of a site (falls back to the first site).
func NotFound(name string) []byte {
	if !Valid(name) {
		name = Names()[0]
	}
	b, _ := sites.ReadFile("sites/" + name + "/404.html")
	return b
}

// Install writes a site into dir/name and returns that path.
func Install(dir, name string) (string, error) {
	if !Valid(name) {
		return "", fmt.Errorf("unknown decoy site %q", name)
	}
	dst := filepath.Join(dir, name)
	sub, _ := fs.Sub(sites, "sites/"+name)
	err := fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, p)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	return dst, err
}
