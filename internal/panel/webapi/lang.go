package webapi

import (
	"encoding/json"
	"net/http"

	"github.com/vyto4ka/vynel/internal/i18n"
)

// localize says the texts of an answer in the language of the request. Only the named JSON keys are
// touched: they hold messages of the panel itself (checklist steps, labels), never what an admin typed.
func localize(r *http.Request, v any, keys ...string) any {
	lang := i18n.FromRequest(r)
	if lang == i18n.Default || v == nil {
		return v
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var tree any
	if json.Unmarshal(raw, &tree) != nil {
		return v
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			for k, e := range t {
				if s, ok := e.(string); ok && want[k] {
					t[k] = i18n.T(lang, s)
				} else {
					t[k] = walk(e)
				}
			}
		case []any:
			for i, e := range t {
				t[i] = walk(e)
			}
		}
		return x
	}
	return walk(tree)
}
