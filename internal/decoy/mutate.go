package decoy

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	brandAdjs = []string{
		"Swift", "Bright", "Lumen", "Nimbus", "Vivid", "Prime", "Atlas", "Pulse",
		"Nova", "Quartz", "Onyx", "Vertex", "Cobalt", "Ember", "Drift", "Solace",
		"Zephyr", "Apex", "Halcyon", "Meridian", "Aero", "Cedar", "Indigo", "Mistral",
	}
	brandNouns = []string{
		"Cloud", "Vault", "Hub", "Forge", "Works", "Studio", "Labs", "Stream",
		"Desk", "Space", "Grid", "Port", "Wave", "Loop", "Stack", "Nest",
		"Spark", "Core", "Pixel", "Harbor", "Bay", "Field", "Crest", "Point",
	}
)

// siteBrand lists the full and short brand strings embedded in each built-in decoy site.
var siteBrands = map[string][2]string{
	"cloud":   {"Nimbus Storage", "Nimbus"},
	"coffee":  {"Northbound Coffee", "Northbound"},
	"docs":    {"Stackdocs", "Stack"},
	"journal": {"Field Notes", "Field Notes"},
	"studio":  {"Lumen Studio", "Lumen"},
}

// NewSeed returns a random 16-byte hex seed used to make decoy sites unique per install.
func NewSeed() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to current time if crypto/rand ever fails.
		binary.LittleEndian.PutUint64(b[:8], uint64(time.Now().UnixNano()))
		binary.LittleEndian.PutUint64(b[8:], uint64(os.Getpid()))
	}
	return hex.EncodeToString(b)
}

// LoadOrCreateSeed reads a persistent decoy seed from path, or creates and saves a new one.
// If the filesystem is read-only or unavailable, it returns a freshly generated seed.
func LoadOrCreateSeed(p string) string {
	if b, err := os.ReadFile(p); err == nil {
		if s := strings.TrimSpace(string(b)); len(s) >= 16 {
			return s
		}
	}
	s := NewSeed()
	if p != "" {
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte(s+"\n"), 0o600)
	}
	return s
}

type siteProfile struct {
	brand    string
	short    string
	deg      int
	sat      int
	cssVer   string
	favHue   int
	favHue2  int
	favShape int
	epoch    time.Time
	seed     string
	name     string
}

func deriveProfile(name, seed string) siteProfile {
	sum := sha256.Sum256([]byte("vynel-decoy-v1:" + seed + ":" + name))
	u := func(off int) uint32 { return binary.BigEndian.Uint32(sum[off : off+4]) }

	adj := brandAdjs[int(u(0))%len(brandAdjs)]
	noun := brandNouns[int(u(4))%len(brandNouns)]
	deg := 30 + int(u(8))%300
	sat := 90 + int(u(12))%30
	favHue := int(u(16)) % 360
	favHue2 := (favHue + 35 + int(u(20))%60) % 360
	favShape := int(u(24)) % 3
	// Deterministic mtime 14..240 days before serveEpoch so Last-Modified and ETag do not
	// match the binary build date or other installations.
	offsetSec := int64(14*86400) + int64(u(28))%(226*86400)
	epoch := serveEpoch.Add(-time.Duration(offsetSec) * time.Second).Truncate(time.Second).UTC()

	return siteProfile{
		brand:    adj + " " + noun,
		short:    adj,
		deg:      deg,
		sat:      sat,
		cssVer:   hex.EncodeToString(sum[28:31]),
		favHue:   favHue,
		favHue2:  favHue2,
		favShape: favShape,
		epoch:    epoch,
		seed:     seed,
		name:     name,
	}
}

// noiseHex returns a deterministic variable-length hex token (12..36 chars) for a file/slot.
func (p siteProfile) noiseHex(filePath, slot string) string {
	sum := sha256.Sum256([]byte(p.seed + ":" + p.name + ":" + filePath + ":" + slot))
	n := 6 + int(sum[0])%13 // 6..18 bytes => 12..36 hex chars
	return hex.EncodeToString(sum[1 : 1+n])
}

func (p siteProfile) faviconSVG() []byte {
	var inner string
	switch p.favShape {
	case 0:
		inner = fmt.Sprintf(`<circle cx="32" cy="32" r="13" fill="hsl(%d,72%%,90%%)"/>`, p.favHue2)
	case 1:
		inner = fmt.Sprintf(`<rect x="19" y="19" width="26" height="26" rx="6" fill="hsl(%d,72%%,90%%)"/>`, p.favHue2)
	default:
		inner = fmt.Sprintf(`<polygon points="32,16 48,32 32,48 16,32" fill="hsl(%d,72%%,90%%)"/>`, p.favHue2)
	}
	return fmt.Appendf(nil,
		"<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 64 64\"><!-- %s --><rect width=\"64\" height=\"64\" rx=\"14\" fill=\"hsl(%d,68%%,50%%)\"/>%s</svg>\n",
		p.noiseHex("favicon.svg", "icon"), p.favHue, inner)
}

func (p siteProfile) mutateFile(rel string, data []byte) []byte {
	clean := strings.TrimPrefix(path.Clean("/"+rel), "/")
	if clean == "favicon.svg" {
		return p.faviconSVG()
	}
	ext := strings.ToLower(path.Ext(clean))
	switch ext {
	case ".html":
		s := string(data)
		if b, ok := siteBrands[p.name]; ok {
			if b[0] != "" {
				s = strings.ReplaceAll(s, b[0], p.brand)
			}
			if b[1] != "" && b[1] != b[0] {
				s = strings.ReplaceAll(s, b[1], p.short)
			}
		}
		// Cache-buster on local stylesheet & favicon references.
		s = strings.ReplaceAll(s, `href="/assets/site.css"`, `href="/assets/site.css?v=`+p.cssVer+`"`)
		s = strings.ReplaceAll(s, `href="/favicon.svg"`, `href="/favicon.svg?v=`+p.cssVer+`"`)
		// Per-install hue rotation + byte noise in <head> and before </body>.
		headInject := fmt.Sprintf("<style>html{filter:hue-rotate(%ddeg) saturate(%d%%)}img,picture,video,svg,canvas{filter:hue-rotate(-%ddeg)}</style><!-- %s -->\n</head>",
			p.deg, p.sat, p.deg, p.noiseHex(clean, "head"))
		if strings.Contains(s, "</head>") {
			s = strings.Replace(s, "</head>", headInject, 1)
		} else {
			s += "\n<!-- " + p.noiseHex(clean, "head") + " -->\n"
		}
		tailComment := "<!-- " + p.noiseHex(clean, "tail") + " -->\n</body>"
		if strings.Contains(s, "</body>") {
			s = strings.Replace(s, "</body>", tailComment, 1)
		}
		return []byte(s)
	case ".css", ".js":
		out := make([]byte, 0, len(data)+48)
		out = append(out, data...)
		out = append(out, "\n/* "+p.noiseHex(clean, "asset")+" */\n"...)
		return out
	case ".txt":
		out := make([]byte, 0, len(data)+48)
		out = append(out, data...)
		out = append(out, "\n# "+p.noiseHex(clean, "txt")+"\n"...)
		return out
	default:
		return data
	}
}

type mutatedSite struct {
	epoch time.Time
	files map[string][]byte // keyed by "/index.html", "/assets/site.css", etc.
}

var (
	mutCacheMu sync.RWMutex
	mutCache   = map[string]*mutatedSite{}
)

func getMutatedSite(name, seed string) *mutatedSite {
	key := seed + "\x00" + name
	mutCacheMu.RLock()
	ms := mutCache[key]
	mutCacheMu.RUnlock()
	if ms != nil {
		return ms
	}
	prof := deriveProfile(name, seed)
	files := map[string][]byte{}
	sub, _ := fs.Sub(sites, "sites/"+name)
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return nil
		}
		files["/"+p] = prof.mutateFile(p, b)
		return nil
	})
	ms = &mutatedSite{epoch: prof.epoch, files: files}
	mutCacheMu.Lock()
	if len(mutCache) >= 64 {
		mutCache = map[string]*mutatedSite{}
	}
	mutCache[key] = ms
	mutCacheMu.Unlock()
	return ms
}

// InstallWithSeed writes a built-in decoy site into dir/name. When seed is non-empty, each
// file is deterministically mutated (brand, hue shift, cache-buster, byte noise, fresh favicon
// and mtime) so two installations do not serve identical decoy bytes.
func InstallWithSeed(dir, name, seed string) (string, error) {
	if seed == "" {
		return Install(dir, name)
	}
	if !Valid(name) {
		return "", fmt.Errorf("unknown decoy site %q", name)
	}
	dst := filepath.Join(dir, name)
	prof := deriveProfile(name, seed)
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
		b = prof.mutateFile(p, b)
		if err := os.WriteFile(target, b, 0o644); err != nil {
			return err
		}
		_ = os.Chtimes(target, prof.epoch, prof.epoch)
		return nil
	})
	return dst, err
}

// NotFoundWithSeed returns the 404 page of a built-in site, mutated with seed when non-empty.
func NotFoundWithSeed(name, seed string) []byte {
	if seed == "" {
		return NotFound(name)
	}
	if !Valid(name) {
		name = Names()[0]
	}
	return getMutatedSite(name, seed).files["/404.html"]
}

// ServeWithSeed serves a built-in decoy site, mutated with seed when non-empty.
func ServeWithSeed(w http.ResponseWriter, r *http.Request, name, seed string) {
	if seed == "" {
		Serve(w, r, name)
		return
	}
	if !Valid(name) {
		name = Names()[0]
	}
	ms := getMutatedSite(name, seed)
	SetSecurityHeaders(w)
	p := path.Clean("/" + r.URL.Path)
	for _, c := range []string{p, p + ".html", path.Join(p, "index.html")} {
		if strings.HasSuffix(c, "/404.html") {
			continue
		}
		if b, ok := ms.files[c]; ok {
			if ct := mime.TypeByExtension(path.Ext(c)); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Header().Set("Last-Modified", ms.epoch.Format(http.TimeFormat))
			w.Header().Set("Etag", `"`+strconv.FormatInt(ms.epoch.Unix(), 16)+"-"+strconv.FormatInt(int64(len(b)), 16)+`"`)
			http.ServeContent(w, r, path.Base(c), ms.epoch, bytes.NewReader(b))
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(ms.files["/404.html"])
}
