// Package i18n translates the texts the panel's API sends to the web interface: errors, settings
// descriptions, checklist steps and the like. Russian is the source language: the code keeps its
// Russian messages, and a catalog (locales/<lang>.json) maps a Russian message, or a format string
// such as "строка %d: пустой User-Agent", to the same message in another language. To add a language,
// add locales/<code>.json; TestCatalogCoversSource keeps the catalog in step with the code.
package i18n

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var files embed.FS

// Default is the source language of the messages in the code.
const Default = "ru"

// Localizer is implemented by errors that can say themselves in another language.
type Localizer interface {
	Localize(lang string) string
}

type pattern struct {
	re     *regexp.Regexp
	to     string
	weight int // the amount of fixed text: the more specific pattern is tried first
	quoted []bool
}

type catalog struct {
	exact    map[string]string
	patterns []pattern
	prefixes []string // keys ending in ": " or " " that begin a longer message
	suffixes []string // keys beginning with a space that end a longer message
}

var (
	mu       sync.Mutex
	catalogs = map[string]*catalog{}
)

// Languages lists the languages with a catalog, Russian first.
func Languages() []string {
	out := []string{Default}
	entries, _ := files.ReadDir("locales")
	for _, e := range entries {
		if l := strings.TrimSuffix(e.Name(), ".json"); l != Default {
			out = append(out, l)
		}
	}
	return out
}

// Supported reports whether lang has a catalog (Russian needs none).
func Supported(lang string) bool {
	if lang == Default {
		return true
	}
	return load(lang) != nil
}

var verb = regexp.MustCompile(`%[-+# 0-9.]*[sdvqwf]`)

func load(lang string) *catalog {
	mu.Lock()
	defer mu.Unlock()
	if c, ok := catalogs[lang]; ok {
		return c
	}
	raw, err := files.ReadFile("locales/" + lang + ".json")
	if err != nil {
		catalogs[lang] = nil
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		panic("i18n: locales/" + lang + ".json: " + err.Error())
	}
	c := &catalog{exact: m}
	for k, v := range m {
		if locs := verb.FindAllStringIndex(k, -1); len(locs) > 0 {
			c.patterns = append(c.patterns, compile(k, v, locs))
			continue
		}
		if strings.HasSuffix(k, ": ") || strings.HasSuffix(k, " ") {
			c.prefixes = append(c.prefixes, k)
		}
		if strings.HasPrefix(k, " ") {
			c.suffixes = append(c.suffixes, k)
		}
	}
	sort.Slice(c.patterns, func(i, j int) bool { return c.patterns[i].weight > c.patterns[j].weight })
	sort.Slice(c.prefixes, func(i, j int) bool { return len(c.prefixes[i]) > len(c.prefixes[j]) })
	sort.Slice(c.suffixes, func(i, j int) bool { return len(c.suffixes[i]) > len(c.suffixes[j]) })
	catalogs[lang] = c
	return c
}

func compile(key, to string, locs [][]int) pattern {
	var b strings.Builder
	b.WriteString("(?s)^")
	last, weight := 0, 0
	var quoted []bool
	for _, l := range locs {
		fixed := key[last:l[0]]
		weight += len(fixed)
		b.WriteString(regexp.QuoteMeta(fixed))
		switch key[l[1]-1] {
		case 'd':
			b.WriteString(`(-?\d+)`)
			quoted = append(quoted, false)
		case 'f':
			b.WriteString(`(-?[\d.]+)`)
			quoted = append(quoted, false)
		case 'q':
			b.WriteString(`("(?:[^"\\]|\\.)*")`)
			quoted = append(quoted, true)
		default:
			b.WriteString(`(.*?)`)
			quoted = append(quoted, false)
		}
		last = l[1]
	}
	weight += len(key) - last
	b.WriteString(regexp.QuoteMeta(key[last:]))
	b.WriteString("$")
	return pattern{re: regexp.MustCompile(b.String()), to: to, weight: weight, quoted: quoted}
}

// T translates one message into lang; a message the catalog does not know comes back unchanged.
func T(lang, msg string) string {
	if lang == Default || msg == "" {
		return msg
	}
	c := load(lang)
	if c == nil {
		return msg
	}
	return c.translate(msg, 0)
}

func (c *catalog) translate(msg string, depth int) string {
	if depth > 4 {
		return msg
	}
	if to, ok := c.exact[msg]; ok {
		return to
	}
	for _, p := range c.patterns {
		m := p.re.FindStringSubmatch(msg)
		if m == nil {
			continue
		}
		args := make([]any, 0, len(m)-1)
		for i, a := range m[1:] {
			if !p.quoted[i] {
				a = c.translate(a, depth+1)
			}
			args = append(args, a)
		}
		return sprintf(p.to, args)
	}
	for _, k := range c.prefixes {
		if strings.HasPrefix(msg, k) && len(msg) > len(k) {
			return c.exact[k] + c.translate(msg[len(k):], depth+1)
		}
	}
	for _, k := range c.suffixes {
		if strings.HasSuffix(msg, k) && len(msg) > len(k) {
			return c.translate(strings.TrimSuffix(msg, k), depth+1) + c.exact[k]
		}
	}
	return msg
}

// sprintf fills the verbs of a catalog format with already-rendered text.
func sprintf(format string, args []any) string {
	i := 0
	return verb.ReplaceAllStringFunc(format, func(string) string {
		if i >= len(args) {
			return ""
		}
		i++
		return fmt.Sprint(args[i-1])
	})
}

// Error is the text of err in lang. Errors that know their own parts say them in lang; the rest are
// looked up by their text.
func Error(lang string, err error) string {
	var l Localizer
	if errors.As(err, &l) {
		return l.Localize(lang)
	}
	return T(lang, err.Error())
}

// FromRequest picks the language of a request: the X-Lang header the panel's page sends, then
// Accept-Language; an unknown language is Russian.
func FromRequest(r *http.Request) string {
	if l := r.Header.Get("X-Lang"); l != "" {
		if Supported(l) {
			return l
		}
		return Default
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		if tag == "" {
			continue
		}
		if i := strings.IndexAny(tag, "-_"); i > 0 {
			tag = tag[:i]
		}
		if Supported(tag) {
			return tag
		}
	}
	return Default
}

// Has reports whether the catalog of lang has the message as a key (the coverage test uses it).
func Has(lang, msg string) bool {
	c := load(lang)
	if c == nil {
		return false
	}
	_, ok := c.exact[msg]
	return ok
}

// Sprintf is fmt.Sprintf for a message of the code: the format and the arguments that are texts
// themselves (strings, or errors that can localize) come out in lang.
func Sprintf(lang, format string, args ...any) string {
	if lang != Default {
		format = T(lang, format)
	}
	out := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case string:
			out[i] = T(lang, v)
		case Localizer:
			out[i] = v.Localize(lang)
		default:
			out[i] = a
		}
	}
	return fmt.Sprintf(format, out...)
}
