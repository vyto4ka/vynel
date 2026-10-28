package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// apiEventTypes names the audit actions that other programs hear about. An action not listed
// is the panel's own business (sign-ins, settings, the Xray config).
var apiEventTypes = map[string]string{
	"user.create":        "user.created",
	"user.delete":        "user.deleted",
	"user.extend":        "user.extended",
	"user.set_enabled":   "user.disabled", // or user.enabled, by the diff
	"user.set_expiry":    "user.expiry_changed",
	"user.set_limit":     "user.limit_changed",
	"user.reset_traffic": "user.traffic_reset",
	"user.auto_reset":    "user.traffic_reset",
	"user.set_groups":    "user.groups_changed",
	"user.set_details":   "user.updated",
	"user.sub_token":     "user.credentials_changed",
	"user.reissue":       "user.credentials_changed",
	"user.status":        "user.status_changed",
	"node.create":        "node.created",
	"node.update":        "node.updated",
	"node.delete":        "node.deleted",
	"group.create":       "group.created",
	"group.update":       "group.updated",
	"group.delete":       "group.deleted",
}

// APIEventTypes is every event type the API can emit, for the documentation and the page.
func APIEventTypes() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range append([]string{"user.enabled", "node.online", "node.offline"}, mapValues(apiEventTypes)...) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// publishAudit turns an audit entry into an API event, in the same transaction.
func publishAudit(ctx context.Context, q DBTX, e AuditEntry) error {
	typ, ok := apiEventTypes[e.Action]
	if !ok {
		return nil
	}
	data := map[string]any{}
	_ = json.Unmarshal([]byte(e.Diff), &data)
	if data == nil {
		data = map[string]any{}
	}
	if e.Action == "user.set_enabled" {
		typ = "user.disabled"
		if on, _ := data["enabled"].(bool); on {
			typ = "user.enabled"
		}
	}
	name := func(query string, cols ...string) {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if q.QueryRowContext(ctx, query, e.EntityID).Scan(ptrs...) != nil {
			return
		}
		for i, c := range cols {
			if _, have := data[c]; !have && vals[i] != nil {
				data[c] = vals[i]
			}
		}
	}
	switch e.Entity {
	case "user":
		name(`SELECT username FROM users WHERE id=?`, "username")
	case "node":
		if e.Action != "node.delete" {
			data = map[string]any{} // the diff of a node is its form, not for outsiders
		}
		name(`SELECT code, name FROM nodes WHERE id=?`, "code", "name")
	case "group":
		name(`SELECT name FROM groups WHERE id=?`, "name")
	}
	b, _ := json.Marshal(data)
	actor := e.Actor
	if e.ActorID != "" {
		actor += ":" + e.ActorID
	}
	return PublishAPIEvent(ctx, q, &APIEvent{Type: typ, Entity: e.Entity, EntityID: e.EntityID, Actor: strings.TrimSuffix(actor, ":"), Data: string(b)})
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
