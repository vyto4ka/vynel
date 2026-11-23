package geostore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// MaxGeoFileBytes caps downloaded .dat files at 96 MiB.
	MaxGeoFileBytes = 96 << 20

	// SystemGeoSite and SystemGeoIP are the standard Xray asset filenames.
	SystemGeoSite = "geosite.dat"
	SystemGeoIP   = "geoip.dat"

	// DefaultGeoSiteURL and DefaultGeoIPURL are used when a standalone panel has no local Xray assets.
	DefaultGeoSiteURL = "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat"
	DefaultGeoIPURL   = "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat"
)

var validNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}\.dat$`)

// ValidateFileName checks that a .dat filename is safe (no path traversal, ASCII only).
func ValidateFileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("имя geo-файла обязательно")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".dat") {
		name += ".dat"
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) || !validNameRe.MatchString(name) {
		return "", fmt.Errorf("некорректное имя файла %q: допустимы латиница, цифры, дефис, подчёркивание и .dat", name)
	}
	return name, nil
}

// SuggestFileName derives a safe .dat filename from a download URL.
func SuggestFileName(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "custom.dat"
	}
	base := filepath.Base(u.Path)
	if base == "" || base == "." || base == "/" {
		return "custom.dat"
	}
	var b strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	s := strings.TrimLeft(b.String(), ".-_")
	if s == "" {
		return "custom.dat"
	}
	if !strings.HasSuffix(strings.ToLower(s), ".dat") {
		s += ".dat"
	}
	if len(s) > 64 {
		s = s[:60] + ".dat"
	}
	return s
}

// DownloadResult describes a freshly downloaded and validated .dat file.
type DownloadResult struct {
	Name            string
	Kind            Kind
	SHA256          string
	SizeBytes       int64
	CategoriesCount int
	EntriesCount    int
	Index           *FileIndex
	NotModified     bool
	ETag            string
	LastModified    string
}

// Store manages .dat files on disk (<Dir>) plus read-only fallback to system Xray assets (<SystemDir>)
// and caches their lightweight category indexes in memory.
type Store struct {
	Dir         string       // <PanelDataDir>/geo
	SystemDir   string       // e.g. /usr/local/share/xray
	Client      *http.Client // SSRF-protected HTTP client (publicOnly)
	ProxyClient *http.Client // optional HTTP client for trusted VYNEL_GH_PROXY
	GHProxy     func() string

	mu    sync.RWMutex
	cache map[string]cachedIndex // abs path -> cached index
}

type cachedIndex struct {
	modTime int64
	size    int64
	index   *FileIndex
}

// New creates a Store for panel geo assets.
func New(dir, systemDir string, client *http.Client) *Store {
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	return &Store{
		Dir:       dir,
		SystemDir: systemDir,
		Client:    client,
		cache:     map[string]cachedIndex{},
	}
}

// CustomPath returns the path of a panel-managed .dat file.
func (s *Store) CustomPath(name string) string {
	return filepath.Join(s.Dir, name)
}

// SystemPath returns the path of a built-in Xray .dat file in SystemDir.
func (s *Store) SystemPath(name string) string {
	if s.SystemDir == "" {
		return ""
	}
	return filepath.Join(s.SystemDir, name)
}

// ResolvePath locates name in Dir first, falling back to SystemDir for standard geosite.dat/geoip.dat.
func (s *Store) ResolvePath(name string) (path string, isSystem bool, ok bool) {
	if name == "" {
		return "", false, false
	}
	p := s.CustomPath(name)
	if lfi, err := os.Lstat(p); err == nil && lfi.Mode().IsRegular() {
		return p, false, true
	}
	if (name == SystemGeoSite || name == SystemGeoIP) && s.SystemDir != "" {
		sp := s.SystemPath(name)
		if st, err := os.Stat(sp); err == nil && !st.IsDir() {
			return sp, true, true
		}
	}
	return "", false, false
}

// EnsureTesterDir ensures Dir exists and has symlinks to system geosite.dat/geoip.dat when not
// overridden in Dir, so `xray run -test` with XRAY_LOCATION_ASSET=Dir sees both custom and system files.
func (s *Store) EnsureTesterDir() error {
	if s.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	if s.SystemDir == "" || s.SystemDir == s.Dir {
		return nil
	}
	for _, name := range []string{SystemGeoSite, SystemGeoIP} {
		dst := s.CustomPath(name)
		if _, err := os.Lstat(dst); err == nil {
			continue // real override file or existing symlink
		}
		src := s.SystemPath(name)
		if st, err := os.Stat(src); err == nil && !st.IsDir() {
			_ = os.Symlink(src, dst)
		}
	}
	return nil
}

// HasCustomFile reports whether a real (non-symlink) .dat file exists in Dir.
func (s *Store) HasCustomFile(name string) bool {
	if s.Dir == "" {
		return false
	}
	fi, err := os.Lstat(s.CustomPath(name))
	if err != nil {
		return false
	}
	return fi.Mode().IsRegular()
}

// Index returns the cached or freshly parsed FileIndex for name.
func (s *Store) Index(name string) (path string, idx *FileIndex, isSystem bool, err error) {
	path, isSystem, ok := s.ResolvePath(name)
	if !ok {
		return "", nil, false, fmt.Errorf("geo-файл %q не найден на диске", name)
	}
	idx, err = s.indexPath(path, expectedKindByName(name))
	if err != nil {
		return "", nil, isSystem, err
	}
	return path, idx, isSystem, nil
}

func (s *Store) indexPath(path string, expected Kind) (*FileIndex, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	mod := st.ModTime().UnixNano()
	sz := st.Size()

	s.mu.RLock()
	if c, ok := s.cache[path]; ok && c.modTime == mod && c.size == sz && c.index != nil {
		s.mu.RUnlock()
		return c.index, nil
	}
	s.mu.RUnlock()

	idx, err := IndexFile(path, expected)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cache[path] = cachedIndex{modTime: mod, size: sz, index: idx}
	s.mu.Unlock()
	return idx, nil
}

// Invalidate drops cached index for a filename.
func (s *Store) Invalidate(name string) {
	s.mu.Lock()
	delete(s.cache, s.CustomPath(name))
	s.mu.Unlock()
}

// Remove deletes a custom .dat file from Dir and restores the system symlink if applicable.
func (s *Store) Remove(name string) error {
	p := s.CustomPath(name)
	s.Invalidate(name)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = s.EnsureTesterDir()
	return nil
}

// FileInfo computes sha256 and size for a file in Dir.
func (s *Store) FileInfo(name string) (hash string, size int64, err error) {
	p, _, ok := s.ResolvePath(name)
	if !ok {
		return "", 0, os.ErrNotExist
	}
	return HashFile(p)
}

// HashFile returns hex sha256 and size of a file on disk.
func HashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Download fetches rawURL into a temporary file, validates its protobuf structure, and atomically
// installs it as <Dir>/<name> if checkBeforeInstall (optional) returns nil.
func (s *Store) Download(ctx context.Context, rawURL, name string, expectedKind Kind, checkBeforeInstall func(*FileIndex) error) (*DownloadResult, error) {
	name, err := ValidateFileName(name)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("укажите корректную HTTP/HTTPS ссылку на .dat файл")
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, err
	}

	tmpPath, hashHex, sizeBytes, etag, lastMod, err := s.fetchToTemp(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	idx, err := IndexFile(tmpPath, expectedKind)
	if err != nil {
		return nil, err
	}
	if checkBeforeInstall != nil {
		if err := checkBeforeInstall(idx); err != nil {
			return nil, err
		}
	}

	dst := s.CustomPath(name)
	// Remove an existing symlink (e.g. tester symlink to SystemDir) before atomic rename.
	if fi, err := os.Lstat(dst); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(dst)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return nil, err
	}
	tmpPath = "" // moved

	if st, err := os.Stat(dst); err == nil {
		s.mu.Lock()
		s.cache[dst] = cachedIndex{modTime: st.ModTime().UnixNano(), size: st.Size(), index: idx}
		s.mu.Unlock()
	}

	return &DownloadResult{
		Name:            name,
		Kind:            idx.Kind,
		SHA256:          hashHex,
		SizeBytes:       sizeBytes,
		CategoriesCount: len(idx.Categories),
		EntriesCount:    idx.EntriesCount,
		Index:           idx,
		ETag:            etag,
		LastModified:    lastMod,
	}, nil
}

func (s *Store) fetchToTemp(ctx context.Context, rawURL string) (tmpPath, hashHex string, size int64, etag, lastMod string, err error) {
	tmpPath, hashHex, size, etag, lastMod, err = s.doFetch(ctx, s.Client, rawURL)
	if err == nil {
		return tmpPath, hashHex, size, etag, lastMod, nil
	}
	// Fallback through VYNEL_GH_PROXY for GitHub URLs when configured.
	if s.GHProxy != nil {
		if proxy := strings.TrimSpace(s.GHProxy()); proxy != "" && isGitHubURL(rawURL) {
			proxied := strings.TrimRight(proxy, "/") + "/" + strings.TrimLeft(rawURL, "/")
			c := s.ProxyClient
			if c == nil {
				c = s.Client
			}
			if tp, hh, sz, et, lm, perr := s.doFetch(ctx, c, proxied); perr == nil {
				return tp, hh, sz, et, lm, nil
			}
		}
	}
	return "", "", 0, "", "", err
}

func (s *Store) doFetch(ctx context.Context, client *http.Client, rawURL string) (tmpPath, hashHex string, size int64, etag, lastMod string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", 0, "", "", err
	}
	req.Header.Set("User-Agent", "vynel-geostore/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", 0, "", "", fmt.Errorf("ошибка скачивания: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", 0, "", "", fmt.Errorf("сервер вернул HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxGeoFileBytes {
		return "", "", 0, "", "", fmt.Errorf("файл слишком большой (%d МБ, лимит %d МБ)", resp.ContentLength>>20, MaxGeoFileBytes>>20)
	}

	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	tmpPath = filepath.Join(s.Dir, ".tmp-"+hex.EncodeToString(rnd[:]))
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return "", "", 0, "", "", err
	}
	cleanup := true
	defer func() {
		f.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	h := sha256.New()
	limited := io.LimitReader(resp.Body, MaxGeoFileBytes+1)
	n, err := io.Copy(io.MultiWriter(f, h), limited)
	if err != nil {
		return "", "", 0, "", "", fmt.Errorf("ошибка чтения ответа: %w", err)
	}
	if n > MaxGeoFileBytes {
		return "", "", 0, "", "", fmt.Errorf("файл превышает лимит %d МБ", MaxGeoFileBytes>>20)
	}
	if n == 0 {
		return "", "", 0, "", "", errors.New("сервер вернул пустой файл")
	}
	if err := f.Close(); err != nil {
		return "", "", 0, "", "", err
	}
	cleanup = false
	return tmpPath, hex.EncodeToString(h.Sum(nil)), n, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), nil
}

func isGitHubURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "github.com" || h == "raw.githubusercontent.com" || strings.HasSuffix(h, ".githubusercontent.com")
}

func expectedKindByName(name string) Kind {
	switch name {
	case SystemGeoSite:
		return KindGeoSite
	case SystemGeoIP:
		return KindGeoIP
	default:
		return ""
	}
}

// SearchHit represents one matched category across files.
type SearchHit struct {
	File       string   `json:"file"`
	Kind       Kind     `json:"kind"`
	Code       string   `json:"code"`
	Count      int      `json:"count"`
	Attributes []string `json:"attributes,omitempty"`
	Rule       string   `json:"rule"` // e.g. "geosite:openai" or "ext-domain:custom.dat:openai"
	IsSystem   bool     `json:"isSystem"`
}

// FormatRule returns the canonical Xray routing rule token for a file + category.
func FormatRule(file string, kind Kind, code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if file == SystemGeoSite && kind == KindGeoSite {
		return "geosite:" + code
	}
	if file == SystemGeoIP && kind == KindGeoIP {
		return "geoip:" + code
	}
	if kind == KindGeoIP {
		return "ext-ip:" + file + ":" + code
	}
	return "ext-domain:" + file + ":" + code
}

// SearchNames searches category names across the given files.
func (s *Store) SearchNames(files []string, query string, kindFilter Kind, limit int) []SearchHit {
	if limit <= 0 {
		limit = 100
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var exact, prefix, substr []SearchHit

	for _, name := range files {
		_, idx, isSys, err := s.Index(name)
		if err != nil {
			continue
		}
		if kindFilter != "" && idx.Kind != kindFilter {
			continue
		}
		for _, c := range idx.Categories {
			hit := SearchHit{
				File:       name,
				Kind:       idx.Kind,
				Code:       c.Code,
				Count:      c.Count,
				Attributes: c.Attributes,
				Rule:       FormatRule(name, idx.Kind, c.Code),
				IsSystem:   isSys,
			}
			switch {
			case q == "":
				prefix = append(prefix, hit)
			case c.Code == q:
				exact = append(exact, hit)
			case strings.HasPrefix(c.Code, q):
				prefix = append(prefix, hit)
			case strings.Contains(c.Code, q):
				substr = append(substr, hit)
			}
		}
	}

	out := make([]SearchHit, 0, min(limit, len(exact)+len(prefix)+len(substr)))
	for _, bucket := range [][]SearchHit{exact, prefix, substr} {
		sort.Slice(bucket, func(i, j int) bool {
			if bucket[i].IsSystem != bucket[j].IsSystem {
				return bucket[i].IsSystem
			}
			if bucket[i].Code != bucket[j].Code {
				return bucket[i].Code < bucket[j].Code
			}
			return bucket[i].File < bucket[j].File
		})
		for _, h := range bucket {
			if len(out) >= limit {
				return out
			}
			out = append(out, h)
		}
	}
	return out
}

// SearchByContent searches inside categories across files for a domain or IP.
func (s *Store) SearchByContent(files []string, query string, kindFilter Kind, limit int) []SearchHit {
	if limit <= 0 {
		limit = 50
	}
	var out []SearchHit
	for _, name := range files {
		if len(out) >= limit {
			break
		}
		p, idx, isSys, err := s.Index(name)
		if err != nil {
			continue
		}
		if kindFilter != "" && idx.Kind != kindFilter {
			continue
		}
		matches, err := SearchContent(p, idx, query, limit-len(out))
		if err != nil {
			continue
		}
		for _, c := range matches {
			out = append(out, SearchHit{
				File:       name,
				Kind:       idx.Kind,
				Code:       c.Code,
				Count:      c.Count,
				Attributes: c.Attributes,
				Rule:       FormatRule(name, idx.Kind, c.Code),
				IsSystem:   isSys,
			})
		}
	}
	return out
}
