// Package decoy holds the static decoy sites shown to anyone who is not a VPN client: behind
// Reality (self-steal), on the subscription domain and for unknown subscription tokens
// (docs/STEALTH.md §2.3).
package decoy

import (
	"crypto/rand"
	"embed"
	"fmt"
	"io/fs"
	"math/big"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
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

// Random picks a site, for a new node or panel.
func Random() string {
	names := Names()
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(names))))
	if err != nil {
		return names[0]
	}
	return names[n.Int64()]
}

// Serve answers a request with a site the way nginx serves it: clean URLs (/about is
// about.html or about/index.html), the site's 404 page for anything else.
func Serve(w http.ResponseWriter, r *http.Request, name string) {
	if !Valid(name) {
		name = Names()[0]
	}
	p := path.Clean("/" + r.URL.Path)
	for _, c := range []string{p, p + ".html", path.Join(p, "index.html")} {
		f := "sites/" + name + c
		if st, err := fs.Stat(sites, f); err == nil && !st.IsDir() && !strings.HasSuffix(c, "/404.html") {
			b, _ := sites.ReadFile(f)
			if ct := mime.TypeByExtension(path.Ext(f)); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			_, _ = w.Write(b)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(NotFound(name))
}
