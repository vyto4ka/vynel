package decoy

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// forbiddenCustomRoots are system directories that must never be exposed as a decoy root.
var forbiddenCustomRoots = []string{
	"/etc", "/proc", "/sys", "/dev", "/root", "/boot", "/run", "/var/lib/vynel",
}

// IsHTMLFile reports whether p looks like an HTML file path.
func IsHTMLFile(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".html" || ext == ".htm"
}

// ValidateCustomPath checks that p is a safe absolute path suitable for nginx/Caddy/Go decoy
// serving (no relative segments, no config-breaking characters, not a system directory).
func ValidateCustomPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	if !strings.HasPrefix(p, "/") {
		return "", errors.New("path must be absolute")
	}
	if strings.ContainsAny(p, " \t\r\n;{}\"'\\$") || strings.ContainsRune(p, 0) {
		return "", errors.New("path contains forbidden characters")
	}
	if strings.Contains(p, "..") {
		return "", errors.New("path must not contain '..'")
	}
	cleaned := filepath.Clean(p)
	if cleaned == "/" {
		return "", errors.New("root directory '/' is not allowed")
	}
	for _, bad := range forbiddenCustomRoots {
		if cleaned == bad || strings.HasPrefix(cleaned, bad+"/") {
			return "", fmt.Errorf("system directory %s is not allowed", bad)
		}
	}
	return cleaned, nil
}

// CheckCustomPath validates p and verifies that it exists on disk as either an HTML file
// (.html/.htm) or a directory containing index.html.
func CheckCustomPath(p string) (cleaned string, isFile bool, err error) {
	cleaned, err = ValidateCustomPath(p)
	if err != nil || cleaned == "" {
		return cleaned, false, err
	}
	st, err := os.Stat(cleaned)
	if err != nil {
		return "", false, fmt.Errorf("path is not accessible: %w", err)
	}
	if st.IsDir() {
		idx := filepath.Join(cleaned, "index.html")
		ist, err := os.Stat(idx)
		if err != nil || ist.IsDir() {
			return "", false, errors.New("directory must contain index.html")
		}
		return cleaned, false, nil
	}
	if !IsHTMLFile(cleaned) {
		return "", false, errors.New("file must have .html or .htm extension")
	}
	return cleaned, true, nil
}

// NotFoundCustom returns the 404 body from a custom decoy path (404.html in a directory, or
// the HTML file itself), falling back to the built-in decoy when unavailable.
func NotFoundCustom(customPath, fallbackName, seed string) []byte {
	cleaned, err := ValidateCustomPath(customPath)
	if err == nil && cleaned != "" {
		if st, err := os.Stat(cleaned); err == nil {
			if !st.IsDir() && IsHTMLFile(cleaned) {
				if b, err := os.ReadFile(cleaned); err == nil {
					return b
				}
			}
			if st.IsDir() {
				if b, err := os.ReadFile(filepath.Join(cleaned, "404.html")); err == nil {
					return b
				}
				if b, err := os.ReadFile(filepath.Join(cleaned, "index.html")); err == nil {
					return b
				}
			}
		}
	}
	return NotFoundWithSeed(fallbackName, seed)
}

// ServeCustom serves a custom decoy HTML file or directory from disk with the same clean-URL
// semantics and security headers as Serve. If customPath is invalid or missing, it falls back
// to ServeWithSeed.
func ServeCustom(w http.ResponseWriter, r *http.Request, customPath, fallbackName, seed string) {
	cleaned, err := ValidateCustomPath(customPath)
	if err != nil || cleaned == "" {
		ServeWithSeed(w, r, fallbackName, seed)
		return
	}
	st, err := os.Stat(cleaned)
	if err != nil {
		ServeWithSeed(w, r, fallbackName, seed)
		return
	}
	SetSecurityHeaders(w)
	if !st.IsDir() {
		if !IsHTMLFile(cleaned) {
			ServeWithSeed(w, r, fallbackName, seed)
			return
		}
		b, err := os.ReadFile(cleaned)
		if err != nil {
			ServeWithSeed(w, r, fallbackName, seed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		mtime := st.ModTime().Truncate(time.Second).UTC()
		w.Header().Set("Last-Modified", mtime.Format(http.TimeFormat))
		w.Header().Set("Etag", `"`+strconv.FormatInt(mtime.Unix(), 16)+"-"+strconv.FormatInt(int64(len(b)), 16)+`"`)
		http.ServeContent(w, r, filepath.Base(cleaned), mtime, bytes.NewReader(b))
		return
	}

	rootFS := os.DirFS(cleaned)
	p := path.Clean("/" + r.URL.Path)
	for _, c := range []string{p, p + ".html", path.Join(p, "index.html")} {
		if strings.HasSuffix(c, "/404.html") {
			continue
		}
		rel := strings.TrimPrefix(c, "/")
		if rel == "" || rel == "." {
			continue
		}
		fst, err := fs.Stat(rootFS, rel)
		if err != nil || fst.IsDir() {
			continue
		}
		b, err := fs.ReadFile(rootFS, rel)
		if err != nil {
			continue
		}
		if ct := mime.TypeByExtension(path.Ext(rel)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		mtime := fst.ModTime().Truncate(time.Second).UTC()
		w.Header().Set("Last-Modified", mtime.Format(http.TimeFormat))
		w.Header().Set("Etag", `"`+strconv.FormatInt(mtime.Unix(), 16)+"-"+strconv.FormatInt(int64(len(b)), 16)+`"`)
		http.ServeContent(w, r, path.Base(rel), mtime, bytes.NewReader(b))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(NotFoundCustom(cleaned, fallbackName, seed))
}
