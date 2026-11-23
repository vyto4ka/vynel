package warp

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/vyto4ka/vynel/internal/geostore"
)

const (
	// RuleModeSelected routes only entries listed in Rules through WARP.
	RuleModeSelected = "rules"
	// RuleModeAll routes all non-blocked node traffic through WARP.
	RuleModeAll = "all"
)

// GeoRef records a reference to a .dat file and category used by WARP rules.
type GeoRef struct {
	File     string
	Kind     geostore.Kind
	Category string
}

// ParsedRules holds validated Xray domain and IP rule arrays plus referenced geo assets.
type ParsedRules struct {
	Domains []string
	IPs     []string
	GeoRefs []GeoRef
}

var domainLikeRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

// ParseRules validates user-entered WARP rules and splits them into separate Xray domain and IP lists.
// resolveFileKind (optional) resolves the Kind of a custom .dat file when an ambiguous "ext:file:cat"
// prefix is used.
func ParseRules(raw []string, resolveFileKind func(file string) geostore.Kind) (*ParsedRules, error) {
	out := &ParsedRules{}
	seenDom := map[string]bool{}
	seenIP := map[string]bool{}
	seenRef := map[string]bool{}

	addDom := func(v string) {
		if !seenDom[v] {
			seenDom[v] = true
			out.Domains = append(out.Domains, v)
		}
	}
	addIP := func(v string) {
		if !seenIP[v] {
			seenIP[v] = true
			out.IPs = append(out.IPs, v)
		}
	}
	addRef := func(file string, kind geostore.Kind, cat string) {
		cat = strings.ToLower(strings.TrimSpace(cat))
		cat, _, _ = strings.Cut(cat, "@")
		cat = strings.TrimPrefix(cat, "!")
		if cat == "" {
			return
		}
		key := file + "\x00" + string(kind) + "\x00" + cat
		if !seenRef[key] {
			seenRef[key] = true
			out.GeoRefs = append(out.GeoRefs, GeoRef{File: file, Kind: kind, Category: cat})
		}
	}

	for i, line := range raw {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		lineNo := i + 1
		switch {
		case strings.HasPrefix(s, "geosite:"):
			rest := strings.TrimSpace(strings.TrimPrefix(s, "geosite:"))
			if rest == "" {
				return nil, fmt.Errorf("строка %d: пустая категория geosite", lineNo)
			}
			// Support shorthand geosite:custom.dat:cat by converting to ext-domain:custom.dat:cat.
			if file, cat, ok := strings.Cut(rest, ":"); ok && strings.HasSuffix(strings.ToLower(file), ".dat") {
				cleanFile, err := geostore.ValidateFileName(file)
				if err != nil {
					return nil, fmt.Errorf("строка %d: %w", lineNo, err)
				}
				if strings.TrimSpace(cat) == "" {
					return nil, fmt.Errorf("строка %d: пустая категория в %q", lineNo, s)
				}
				addDom("ext-domain:" + cleanFile + ":" + strings.TrimSpace(cat))
				addRef(cleanFile, geostore.KindGeoSite, cat)
				continue
			}
			addDom("geosite:" + rest)
			addRef(geostore.SystemGeoSite, geostore.KindGeoSite, rest)

		case strings.HasPrefix(s, "geoip:"):
			rest := strings.TrimSpace(strings.TrimPrefix(s, "geoip:"))
			if rest == "" {
				return nil, fmt.Errorf("строка %d: пустая категория geoip", lineNo)
			}
			addIP("geoip:" + rest)
			addRef(geostore.SystemGeoIP, geostore.KindGeoIP, rest)

		case strings.HasPrefix(s, "ext-domain:"):
			file, cat, ok := strings.Cut(strings.TrimPrefix(s, "ext-domain:"), ":")
			if !ok || strings.TrimSpace(cat) == "" {
				return nil, fmt.Errorf("строка %d: ожидается формат ext-domain:file.dat:category", lineNo)
			}
			cleanFile, err := geostore.ValidateFileName(file)
			if err != nil {
				return nil, fmt.Errorf("строка %d: %w", lineNo, err)
			}
			addDom("ext-domain:" + cleanFile + ":" + strings.TrimSpace(cat))
			addRef(cleanFile, geostore.KindGeoSite, cat)

		case strings.HasPrefix(s, "ext-ip:"):
			file, cat, ok := strings.Cut(strings.TrimPrefix(s, "ext-ip:"), ":")
			if !ok || strings.TrimSpace(cat) == "" {
				return nil, fmt.Errorf("строка %d: ожидается формат ext-ip:file.dat:category", lineNo)
			}
			cleanFile, err := geostore.ValidateFileName(file)
			if err != nil {
				return nil, fmt.Errorf("строка %d: %w", lineNo, err)
			}
			addIP("ext-ip:" + cleanFile + ":" + strings.TrimSpace(cat))
			addRef(cleanFile, geostore.KindGeoIP, cat)

		case strings.HasPrefix(s, "ext:"):
			file, cat, ok := strings.Cut(strings.TrimPrefix(s, "ext:"), ":")
			if !ok || strings.TrimSpace(cat) == "" {
				return nil, fmt.Errorf("строка %d: ожидается формат ext:file.dat:category", lineNo)
			}
			cleanFile, err := geostore.ValidateFileName(file)
			if err != nil {
				return nil, fmt.Errorf("строка %d: %w", lineNo, err)
			}
			kind := geostore.KindGeoSite
			if resolveFileKind != nil {
				if k := resolveFileKind(cleanFile); k != "" {
					kind = k
				}
			} else if strings.Contains(strings.ToLower(cleanFile), "geoip") {
				kind = geostore.KindGeoIP
			}
			if kind == geostore.KindGeoIP {
				addIP("ext-ip:" + cleanFile + ":" + strings.TrimSpace(cat))
				addRef(cleanFile, geostore.KindGeoIP, cat)
			} else {
				addDom("ext-domain:" + cleanFile + ":" + strings.TrimSpace(cat))
				addRef(cleanFile, geostore.KindGeoSite, cat)
			}

		case strings.HasPrefix(s, "domain:"), strings.HasPrefix(s, "full:"), strings.HasPrefix(s, "keyword:"):
			_, val, _ := strings.Cut(s, ":")
			if strings.TrimSpace(val) == "" {
				return nil, fmt.Errorf("строка %d: пустое значение правила %q", lineNo, s)
			}
			addDom(s)

		case strings.HasPrefix(s, "regexp:"):
			pat := strings.TrimPrefix(s, "regexp:")
			if pat == "" {
				return nil, fmt.Errorf("строка %d: пустое регулярное выражение", lineNo)
			}
			if _, err := regexp.Compile(pat); err != nil {
				return nil, fmt.Errorf("строка %d: некорректный regexp: %w", lineNo, err)
			}
			addDom(s)

		default:
			if ip := net.ParseIP(s); ip != nil {
				addIP(ip.String())
				continue
			}
			if _, cidr, err := net.ParseCIDR(s); err == nil {
				addIP(cidr.String())
				continue
			}
			if strings.Contains(s, ":") {
				return nil, fmt.Errorf("строка %d: неизвестный префикс правила %q", lineNo, s)
			}
			if !domainLikeRe.MatchString(s) {
				return nil, fmt.Errorf("строка %d: некорректный домен или IP %q", lineNo, s)
			}
			addDom("domain:" + strings.ToLower(s))
		}
	}
	return out, nil
}
