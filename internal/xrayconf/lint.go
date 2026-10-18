package xrayconf

import (
	"errors"
	"fmt"
	"strings"
)

// LintError lists every rule a rendered inbound breaks.
type LintError struct {
	Tag      string
	Problems []string
}

func (e *LintError) Error() string {
	return fmt.Sprintf("inbound %s: %s", e.Tag, strings.Join(e.Problems, "; "))
}

// lintRules catch the mistakes from the "Если не работает" section of the VK CDN guide (docs/PROFILES.md §4.8).
var lintRules = map[string]func(in map[string]any) []string{
	"xhttp_no_nested_extra": func(in map[string]any) []string {
		extra := xhttpExtra(in)
		if _, nested := extra["extra"]; nested {
			return []string{`xhttpSettings.extra contains a nested "extra": Xray silently ignores it (invalid padding ... length:0)`}
		}
		return nil
	},
	"xhttp_cdn_get_only": func(in map[string]any) []string {
		if m, _ := xhttpExtra(in)["uplinkHTTPMethod"].(string); m != "" && m != "GET" {
			return []string{"uplinkHTTPMethod must be GET: VK CDN only passes GET/HEAD"}
		}
		return nil
	},
	"xhttp_no_header_placement": func(in map[string]any) []string {
		var out []string
		extra := xhttpExtra(in)
		for _, k := range []string{"xPaddingPlacement", "uplinkDataPlacement", "sessionPlacement", "seqPlacement"} {
			if v, _ := extra[k].(string); v == "header" {
				out = append(out, k+" must not be header: the CDN strips long custom headers")
			}
		}
		return out
	},
	"xhttp_server_max_header_bytes": func(in map[string]any) []string {
		n, err := toInt(xhttpExtra(in)["serverMaxHeaderBytes"])
		if err != nil || n < 65536 {
			return []string{"serverMaxHeaderBytes must be >= 65536: uplink data travels in cookies"}
		}
		return nil
	},
}

// commonChecks apply to every inbound.
func commonChecks(r *RenderedInbound) []string {
	var out []string
	if r.Tag == "" {
		out = append(out, "empty tag")
	}
	if strings.EqualFold(r.Tag, "api") {
		out = append(out, `tag "api" is reserved`)
	}
	if _, err := toInt(r.Inbound["port"]); err != nil {
		out = append(out, "port is not a number")
	}
	return out
}

// Lint runs the common checks and the template's lint rules.
func Lint(t *Template, r *RenderedInbound) error {
	problems := commonChecks(r)
	for _, name := range t.Lint {
		problems = append(problems, lintRules[name](r.Inbound)...)
	}
	if len(problems) == 0 {
		return nil
	}
	return &LintError{Tag: r.Tag, Problems: problems}
}

// IsLintError reports whether err is a lint failure.
func IsLintError(err error) bool {
	var le *LintError
	return errors.As(err, &le)
}

func xhttpExtra(in map[string]any) map[string]any {
	ss, _ := in["streamSettings"].(map[string]any)
	xs, _ := ss["xhttpSettings"].(map[string]any)
	extra, _ := xs["extra"].(map[string]any)
	return extra
}
