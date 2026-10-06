package xrayconf

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var placeholderRe = regexp.MustCompile(`\$\{(\w+)\}`)

// substitute replaces ${VAR} placeholders in a decoded JSON tree.
// A string that is exactly "${VAR}" becomes the typed value (number, object, list);
// placeholders inside longer strings are interpolated as text. Object keys are left alone.
func substitute(node any, vals map[string]any) (any, error) {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			r, err := substitute(v, vals)
			if err != nil {
				return nil, err
			}
			n[k] = r
		}
		return n, nil
	case []any:
		for i, v := range n {
			r, err := substitute(v, vals)
			if err != nil {
				return nil, err
			}
			n[i] = r
		}
		return n, nil
	case string:
		if m := placeholderRe.FindStringSubmatch(n); m != nil && m[0] == n {
			v, ok := vals[m[1]]
			if !ok {
				return nil, fmt.Errorf("unknown variable ${%s}", m[1])
			}
			return v, nil
		}
		return substString(n, vals)
	}
	return node, nil
}

func substString(s string, vals map[string]any) (string, error) {
	var err error
	out := placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		v, ok := vals[name]
		if !ok {
			err = fmt.Errorf("unknown variable ${%s}", name)
			return m
		}
		switch x := v.(type) {
		case string:
			return x
		case map[string]any, []any:
			b, _ := json.Marshal(x)
			return string(b)
		default:
			return fmt.Sprint(x)
		}
	})
	return strings.TrimSpace(out), err
}

// deepCopy clones a decoded JSON tree.
func deepCopy(v any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// MergePatch applies an RFC 7386 JSON merge patch to target in place and returns it.
// null in the patch deletes the key; objects merge recursively; everything else replaces.
func MergePatch(target map[string]any, patch map[string]any) map[string]any {
	if target == nil {
		target = map[string]any{}
	}
	for k, pv := range patch {
		if pv == nil {
			delete(target, k)
			continue
		}
		pm, isObj := pv.(map[string]any)
		if !isObj {
			target[k] = pv
			continue
		}
		tm, ok := target[k].(map[string]any)
		if !ok {
			tm = map[string]any{}
		}
		target[k] = MergePatch(tm, pm)
	}
	return target
}

// normalizeTree turns whole float64 numbers (from JSON decoding) back into int in place.
func normalizeTree(node any) any {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			n[k] = normalizeTree(v)
		}
	case []any:
		for i, v := range n {
			n[i] = normalizeTree(v)
		}
	default:
		return normalize(n)
	}
	return node
}
