package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// Profile templates: the built-in ones are embedded (read only); templates written in the panel
// are stored in the database and checked by a sample render before they are saved.

type templateCache struct {
	mu     sync.Mutex
	stamp  int64 // store.ProfileTemplatesStamp of the loaded set; -1 = not loaded
	custom map[string]*xrayconf.Template
	broken map[string]string // id -> why the stored source does not parse (e.g. after an upgrade)
}

// syncTemplates reloads the custom templates when the database changed (also by another process).
func (s *Service) syncTemplates(ctx context.Context) error {
	stamp, err := store.ProfileTemplatesStamp(ctx, s.st.DB)
	if err != nil {
		return err
	}
	c := &s.tpl
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.custom != nil && c.stamp == stamp {
		return nil
	}
	rows, err := store.ListProfileTemplates(ctx, s.st.DB)
	if err != nil {
		return err
	}
	c.custom, c.broken = map[string]*xrayconf.Template{}, map[string]string{}
	for _, r := range rows {
		t, err := xrayconf.ParseTemplate([]byte(r.Source))
		if err != nil {
			c.broken[r.ID] = err.Error()
			continue
		}
		t.Custom = true
		c.custom[t.ID] = t
	}
	c.stamp = stamp
	return nil
}

// template returns a built-in or custom template from the cache. It never touches the database:
// it runs inside transactions and the store has a single connection. The cache is refreshed by
// syncTemplates before every transaction (mutate, dryRun) and in the read paths.
func (s *Service) template(_ context.Context, id string) (*xrayconf.Template, error) {
	if t, err := xrayconf.GetTemplate(id); err == nil {
		return t, nil
	}
	s.tpl.mu.Lock()
	defer s.tpl.mu.Unlock()
	if t, ok := s.tpl.custom[id]; ok {
		return t, nil
	}
	if why, ok := s.tpl.broken[id]; ok {
		return nil, invalid("template %s is broken: %s", id, why)
	}
	return nil, invalid("unknown template %q", id)
}

// TemplateInfo is a template with its usage.
type TemplateInfo struct {
	*xrayconf.Template
	Profiles []string `json:"profiles"` // names of profiles using it
	Broken   string   `json:"broken,omitempty"`
}

// ProfileTemplates lists built-in and custom templates.
func (s *Service) ProfileTemplates(ctx context.Context) ([]TemplateInfo, error) {
	builtin, err := xrayconf.Templates()
	if err != nil {
		return nil, err
	}
	if err := s.syncTemplates(ctx); err != nil {
		return nil, err
	}
	profiles, err := store.ListProfiles(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	used := map[string][]string{}
	for _, p := range profiles {
		used[p.TemplateID] = append(used[p.TemplateID], p.Name)
	}
	out := []TemplateInfo{}
	for _, t := range builtin {
		out = append(out, TemplateInfo{Template: t, Profiles: nonNil(used[t.ID])})
	}
	s.tpl.mu.Lock()
	defer s.tpl.mu.Unlock()
	rows, err := store.ListProfileTemplates(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if t, ok := s.tpl.custom[r.ID]; ok {
			out = append(out, TemplateInfo{Template: t, Profiles: nonNil(used[r.ID])})
		} else {
			out = append(out, TemplateInfo{Template: &xrayconf.Template{ID: r.ID, Title: r.ID, Custom: true, Source: r.Source},
				Profiles: nonNil(used[r.ID]), Broken: s.tpl.broken[r.ID]})
		}
	}
	return out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

var templateIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,47}$`)

// CheckTemplate parses a template and renders it with sample values.
func (s *Service) CheckTemplate(source string) (*xrayconf.Template, *xrayconf.RenderedInbound, error) {
	t, err := xrayconf.ParseTemplate([]byte(source))
	if err != nil {
		return nil, nil, invalid("%v", err)
	}
	if !templateIDRe.MatchString(t.ID) {
		return nil, nil, invalid("id must be 2–48 lowercase latin letters, digits and dashes, e.g. my-reality")
	}
	if strings.TrimSpace(t.TagPattern) == "" {
		return nil, nil, invalid("tag_pattern is required, e.g. VLESS_${NODE_CODE}")
	}
	r, err := xrayconf.SampleRender(t)
	if err != nil {
		return nil, nil, invalid("sample render: %v", err)
	}
	return t, r, nil
}

// SaveTemplate creates or replaces a custom template. oldID is the id being edited ("" = new);
// a template in use cannot be renamed. Every node inbound using it is re-rendered with the new
// version first, and any error aborts the change.
func (s *Service) SaveTemplate(ctx context.Context, actor Actor, oldID, source string) (*xrayconf.Template, error) {
	t, _, err := s.CheckTemplate(source)
	if err != nil {
		return nil, err
	}
	if xrayconf.IsBuiltin(t.ID) {
		return nil, invalid("%s is a built-in template: give the copy another id", t.ID)
	}
	err = s.mutate(ctx, change{actor: actor, action: "template.save", entity: "template", event: EvProfileChanged, diff: map[string]string{"id": t.ID, "old": oldID}},
		func(q store.DBTX) error {
			existing, err := store.ListProfileTemplates(ctx, q)
			if err != nil {
				return err
			}
			has := map[string]bool{}
			for _, r := range existing {
				has[r.ID] = true
			}
			if oldID == "" && has[t.ID] {
				return fmt.Errorf("%w: template %s already exists", ErrConflict, t.ID)
			}
			if oldID != "" && !has[oldID] {
				return store.ErrNotFound
			}
			profiles, err := store.ListProfiles(ctx, q)
			if err != nil {
				return err
			}
			for _, p := range profiles {
				if oldID != "" && oldID != t.ID && p.TemplateID == oldID {
					return invalid("template %s is used by profile %s and cannot be renamed", oldID, p.Name)
				}
				if p.TemplateID != t.ID {
					continue
				}
				inbounds, err := store.ListNodeInbounds(ctx, q, 0, p.ID)
				if err != nil {
					return err
				}
				for _, ni := range inbounds {
					if _, err := s.renderNodeInboundWith(ctx, q, ni, t); err != nil {
						return invalid("profile %s, inbound %s: %v", p.Name, ni.Tag, err)
					}
				}
			}
			if oldID != "" && oldID != t.ID {
				if err := store.DeleteProfileTemplate(ctx, q, oldID); err != nil {
					return err
				}
			}
			return store.SaveProfileTemplate(ctx, q, t.ID, source)
		})
	if err != nil {
		return nil, err
	}
	t.Custom = true
	return t, s.syncTemplates(ctx)
}

// DeleteTemplate removes an unused custom template.
func (s *Service) DeleteTemplate(ctx context.Context, actor Actor, id string) error {
	if xrayconf.IsBuiltin(id) {
		return invalid("built-in templates cannot be deleted")
	}
	err := s.mutate(ctx, change{actor: actor, action: "template.delete", entity: "template", diff: map[string]string{"id": id}},
		func(q store.DBTX) error {
			profiles, err := store.ListProfiles(ctx, q)
			if err != nil {
				return err
			}
			for _, p := range profiles {
				if p.TemplateID == id {
					return invalid("template %s is used by profile %s", id, p.Name)
				}
			}
			return store.DeleteProfileTemplate(ctx, q, id)
		})
	if err != nil {
		return err
	}
	return s.syncTemplates(ctx)
}

// errDryRun rolls back a preview transaction.
var errDryRun = errors.New("dry run")

// dryRun runs fn in a transaction that is always rolled back.
func (s *Service) dryRun(ctx context.Context, fn func(q store.DBTX) error) error {
	if err := s.syncTemplates(ctx); err != nil {
		return err
	}
	err := s.st.Tx(ctx, func(q store.DBTX) error {
		if err := fn(q); err != nil {
			return err
		}
		return errDryRun
	})
	if errors.Is(err, errDryRun) {
		return nil
	}
	return err
}
