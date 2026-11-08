// Package decoy holds the static decoy sites shown to anyone who is not a VPN client: behind
// Reality (self-steal), on the subscription domain and for unknown subscription tokens
// (docs/STEALTH.md §2.3).
package decoy

import (
	"bytes"
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
	"strconv"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/buildinfo"
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

// serveEpoch is the fixed mtime reported for every file: the sites are embedded, so the build
// date reads as a site deployed once and never touched since. If-Modified-Since before it gets
// 304, the way a real static server answers. Unix(0,0) would mean "no date" to net/http, so a
// missing or unparsable build date falls back to a fixed past date.
var serveEpoch = func() time.Time {
	if d, err := time.Parse(time.RFC3339, buildinfo.Date); err == nil && !d.IsZero() {
		return d.Truncate(time.Second).UTC()
	}
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}()

// securityHeaders are the headers a real site sends; security scanners check for them. They
// must not vary between the decoy and a real static site.
var securityHeaders = [][2]string{
	{"X-Content-Type-Options", "nosniff"},
	{"X-Frame-Options", "SAMEORIGIN"},
	{"Referrer-Policy", "strict-origin-when-cross-origin"},
	{"Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'self'"},
	{"Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()"},
	{"Cross-Origin-Opener-Policy", "same-origin"},
	{"Cross-Origin-Resource-Policy", "same-origin"},
}

// SetSecurityHeaders adds the same headers to a hand-written decoy response (the
// subscription handler's 404), so all decoy answers look alike.
func SetSecurityHeaders(w http.ResponseWriter) {
	for _, h := range securityHeaders {
		w.Header().Set(h[0], h[1])
	}
}

// Serve answers a request with a site the way nginx serves it: clean URLs (/about is
// about.html or about/index.html), the site's 404 page for anything else, Last-Modified and
// conditional requests on static files.
func Serve(w http.ResponseWriter, r *http.Request, name string) {
	if !Valid(name) {
		name = Names()[0]
	}
	for _, h := range securityHeaders {
		w.Header().Set(h[0], h[1])
	}
	p := path.Clean("/" + r.URL.Path)
	for _, c := range []string{p, p + ".html", path.Join(p, "index.html")} {
		f := "sites/" + name + c
		if st, err := fs.Stat(sites, f); err == nil && !st.IsDir() && !strings.HasSuffix(c, "/404.html") {
			b, _ := sites.ReadFile(f)
			if ct := mime.TypeByExtension(path.Ext(f)); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Header().Set("Last-Modified", serveEpoch.Format(http.TimeFormat))
			// nginx's etag format: hex of mtime and size. With the fixed epoch it is a stable
			// tag per file, exactly what a real static site sends.
			w.Header().Set("Etag", `"`+strconv.FormatInt(serveEpoch.Unix(), 16)+"-"+strconv.FormatInt(int64(len(b)), 16)+`"`)
			http.ServeContent(w, r, path.Base(f), serveEpoch, bytes.NewReader(b))
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(NotFound(name))
}
