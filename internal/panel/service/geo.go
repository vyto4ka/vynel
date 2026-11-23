package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/geostore"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/update"
	"github.com/vyto4ka/vynel/internal/warp"
)

// CapGeo is the capability of node agents that sync Snapshot.geo_assets via FetchGeoAsset.
const CapGeo = "geo"

// CapWarp is the capability of node agents that handle ACTION_WARP_PROVISION and ACTION_WARP_CHECK.
const CapWarp = "warp"

// ClientAssetResolver is reserved for future client-side custom geo asset delivery.
type ClientAssetResolver interface {
	ResolveClientAsset(ctx context.Context, name string) (path string, sha256 string, ok bool)
}

// GeoFileView is the panel API representation of a system or custom .dat file.
type GeoFileView struct {
	ID               int64    `json:"id"` // 0 for built-in system files without a DB override
	Name             string   `json:"name"`
	Title            string   `json:"title"`
	Kind             string   `json:"kind"` // geosite | geoip
	IsSystem         bool     `json:"isSystem"`
	IsSystemOverride bool     `json:"isSystemOverride"`
	SourceURL        string   `json:"sourceUrl"`
	AutoUpdate       bool     `json:"autoUpdate"`
	SHA256           string   `json:"sha256"`
	SizeBytes        int64    `json:"sizeBytes"`
	CategoriesCount  int      `json:"categoriesCount"`
	EntriesCount     int      `json:"entriesCount"`
	FetchedAt        int64    `json:"fetchedAt"`
	LastError        string   `json:"lastError"`
	Enabled          bool     `json:"enabled"`
	Available        bool     `json:"available"`
	UsedByNodes      []string `json:"usedByNodes"`
}

// GeoFileAddInput is the payload for adding a new .dat file by URL.
type GeoFileAddInput struct {
	URL           string `json:"url"`
	Name          string `json:"name"`
	Title         string `json:"title"`
	AutoUpdate    bool   `json:"autoUpdate"`
	ReplaceSystem bool   `json:"replaceSystem"`
}

// GeoFilePatchInput updates metadata of an existing geo_file.
type GeoFilePatchInput struct {
	Title      *string `json:"title"`
	SourceURL  *string `json:"sourceUrl"`
	AutoUpdate *bool   `json:"autoUpdate"`
	Enabled    *bool   `json:"enabled"`
}

// InitGeoStore attaches a geostore.Store backed by SSRF-protected HTTP clients.
func (s *Service) InitGeoStore(dir, systemDir string) error {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: publicOnly}
	pubClient := &http.Client{
		Transport: &http.Transport{
			DialContext:           d.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		Timeout: 90 * time.Second,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 6 {
				return errors.New("слишком много редиректов")
			}
			return nil
		},
	}
	// Separate client for admin-configured VYNEL_GH_PROXY (may live on 127.0.0.1 / private net).
	proxyClient := &http.Client{
		Timeout: 90 * time.Second,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 6 {
				return errors.New("слишком много редиректов")
			}
			return nil
		},
	}
	gs := geostore.New(dir, systemDir, pubClient)
	gs.ProxyClient = proxyClient
	gs.GHProxy = func() string { return update.LoadSource().GHProxy }
	s.Geo = gs
	return gs.EnsureTesterDir()
}

// OpenGeoAsset opens a panel-managed custom/overridden .dat file for streaming to a node.
func (s *Service) OpenGeoAsset(ctx context.Context, name, wantSHA string) (io.ReadCloser, int64, error) {
	if s.Geo == nil {
		return nil, 0, ErrNotFound
	}
	clean, err := geostore.ValidateFileName(name)
	if err != nil {
		return nil, 0, ErrNotFound
	}
	gf, err := store.GetGeoFileByName(ctx, s.st.DB, clean)
	if err != nil {
		return nil, 0, err
	}
	if !gf.Enabled {
		return nil, 0, ErrNotFound
	}
	if wantSHA != "" && gf.SHA256 != "" && gf.SHA256 != wantSHA {
		return nil, 0, fmt.Errorf("geo asset %q hash mismatch", clean)
	}
	p := s.Geo.CustomPath(clean)
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// ListGeoFiles returns all built-in system files and panel-managed geo_files with node usage.
func (s *Service) ListGeoFiles(ctx context.Context) ([]GeoFileView, error) {
	rows, err := store.ListGeoFiles(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	usage := s.geoUsageByNodes(ctx)
	byName := map[string]*store.GeoFile{}
	for _, r := range rows {
		byName[r.Name] = r
	}

	var out []GeoFileView
	// Always present system geosite.dat and geoip.dat entries at the top.
	for _, sys := range []struct {
		name  string
		kind  string
		title string
	}{
		{geostore.SystemGeoSite, string(geostore.KindGeoSite), "Системная база доменов (geosite.dat)"},
		{geostore.SystemGeoIP, string(geostore.KindGeoIP), "Системная база IP (geoip.dat)"},
	} {
		if ov, ok := byName[sys.name]; ok {
			out = append(out, s.geoFileToView(ov, usage[ov.Name]))
			delete(byName, sys.name)
			continue
		}
		v := GeoFileView{
			Name:        sys.name,
			Title:       sys.title,
			Kind:        sys.kind,
			IsSystem:    true,
			Enabled:     true,
			UsedByNodes: usage[sys.name],
		}
		if v.UsedByNodes == nil {
			v.UsedByNodes = []string{}
		}
		if s.Geo != nil {
			if p, idx, _, err := s.Geo.Index(sys.name); err == nil {
				v.Available = true
				v.CategoriesCount = len(idx.Categories)
				v.EntriesCount = idx.EntriesCount
				if st, err := os.Stat(p); err == nil {
					v.SizeBytes = st.Size()
					v.FetchedAt = st.ModTime().Unix()
				}
			}
		}
		out = append(out, v)
	}

	// Custom files sorted by name.
	var custom []*store.GeoFile
	for _, r := range rows {
		if _, remaining := byName[r.Name]; remaining {
			custom = append(custom, r)
		}
	}
	for _, r := range custom {
		out = append(out, s.geoFileToView(r, usage[r.Name]))
	}
	return out, nil
}

func (s *Service) geoFileToView(g *store.GeoFile, usedBy []string) GeoFileView {
	if usedBy == nil {
		usedBy = []string{}
	}
	avail := false
	if s.Geo != nil {
		avail = s.Geo.HasCustomFile(g.Name)
	}
	return GeoFileView{
		ID:               g.ID,
		Name:             g.Name,
		Title:            g.Title,
		Kind:             g.Kind,
		IsSystem:         g.IsSystemOverride,
		IsSystemOverride: g.IsSystemOverride,
		SourceURL:        g.SourceURL,
		AutoUpdate:       g.AutoUpdate,
		SHA256:           g.SHA256,
		SizeBytes:        g.SizeBytes,
		CategoriesCount:  g.CategoriesCount,
		EntriesCount:     g.EntriesCount,
		FetchedAt:        g.FetchedAt,
		LastError:        g.LastError,
		Enabled:          g.Enabled,
		Available:        avail,
		UsedByNodes:      usedBy,
	}
}

// AddGeoFile downloads a .dat file by URL, validates its protobuf structure, indexes it and saves metadata.
func (s *Service) AddGeoFile(ctx context.Context, actor Actor, in GeoFileAddInput) (*GeoFileView, error) {
	if s.Geo == nil {
		return nil, invalid("хранилище geo-файлов не инициализировано")
	}
	rawURL := strings.TrimSpace(in.URL)
	if rawURL == "" {
		return nil, invalid("укажите ссылку на .dat файл")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = geostore.SuggestFileName(rawURL)
	}
	cleanName, err := geostore.ValidateFileName(name)
	if err != nil {
		return nil, invalid("%v", err)
	}
	isSysName := cleanName == geostore.SystemGeoSite || cleanName == geostore.SystemGeoIP
	if isSysName && !in.ReplaceSystem {
		return nil, invalid("имя %s зарезервировано для системной базы; включите замену системной базы или укажите другое имя", cleanName)
	}
	var expectedKind geostore.Kind
	switch cleanName {
	case geostore.SystemGeoSite:
		expectedKind = geostore.KindGeoSite
	case geostore.SystemGeoIP:
		expectedKind = geostore.KindGeoIP
	}

	existing, err := store.GetGeoFileByName(ctx, s.st.DB, cleanName)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if existing != nil && !in.ReplaceSystem {
		return nil, invalid("файл %s уже добавлен", cleanName)
	}

	res, err := s.Geo.Download(ctx, rawURL, cleanName, expectedKind, nil)
	if err != nil {
		return nil, invalid("%v", err)
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = cleanName
	}

	var saved *store.GeoFile
	err = s.mutate(ctx, change{
		actor: actor, action: "geo.add", entity: "geo_file",
		entityID: func() int64 {
			if saved != nil {
				return saved.ID
			}
			return 0
		},
		event: EvSettingChanged,
		diff:  map[string]any{"name": cleanName, "kind": res.Kind, "url": rawURL},
	}, func(q store.DBTX) error {
		now := s.now().Unix()
		if existing != nil {
			existing.Title = title
			existing.Kind = string(res.Kind)
			existing.IsSystemOverride = isSysName
			existing.SourceURL = rawURL
			existing.AutoUpdate = in.AutoUpdate
			existing.SHA256 = res.SHA256
			existing.SizeBytes = res.SizeBytes
			existing.CategoriesCount = res.CategoriesCount
			existing.EntriesCount = res.EntriesCount
			existing.FetchedAt = now
			existing.LastError = ""
			existing.Enabled = true
			saved = existing
			return store.UpdateGeoFile(ctx, q, existing)
		}
		g := &store.GeoFile{
			Name:             cleanName,
			Title:            title,
			Kind:             string(res.Kind),
			IsSystemOverride: isSysName,
			SourceURL:        rawURL,
			AutoUpdate:       in.AutoUpdate,
			SHA256:           res.SHA256,
			SizeBytes:        res.SizeBytes,
			CategoriesCount:  res.CategoriesCount,
			EntriesCount:     res.EntriesCount,
			FetchedAt:        now,
			Enabled:          true,
		}
		if err := store.CreateGeoFile(ctx, q, g); err != nil {
			return err
		}
		saved = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	usage := s.geoUsageByNodes(ctx)
	v := s.geoFileToView(saved, usage[saved.Name])
	return &v, nil
}

// LoadDefaultGeoFiles downloads standard geosite.dat and geoip.dat into PanelDataDir/geo
// for standalone panels that have no local /usr/local/share/xray assets.
func (s *Service) LoadDefaultGeoFiles(ctx context.Context, actor Actor) ([]GeoFileView, error) {
	if s.Geo == nil {
		return nil, invalid("хранилище geo-файлов не инициализировано")
	}
	for _, item := range []struct {
		name, title, url string
	}{
		{geostore.SystemGeoSite, "Системная база доменов (geosite.dat)", geostore.DefaultGeoSiteURL},
		{geostore.SystemGeoIP, "Системная база IP (geoip.dat)", geostore.DefaultGeoIPURL},
	} {
		if _, _, ok := s.Geo.ResolvePath(item.name); ok {
			continue
		}
		if _, err := s.AddGeoFile(ctx, actor, GeoFileAddInput{
			URL:           item.url,
			Name:          item.name,
			Title:         item.title,
			AutoUpdate:    true,
			ReplaceSystem: true,
		}); err != nil {
			return nil, err
		}
	}
	return s.ListGeoFiles(ctx)
}

// UpdateGeoFile updates metadata (title, sourceUrl, autoUpdate, enabled) of an existing geo_file.
func (s *Service) UpdateGeoFile(ctx context.Context, actor Actor, id int64, in GeoFilePatchInput) (*GeoFileView, error) {
	var saved *store.GeoFile
	err := s.mutate(ctx, change{
		actor: actor, action: "geo.update", entity: "geo_file",
		entityID: func() int64 { return id },
		event:    EvSettingChanged,
		diff:     in,
	}, func(q store.DBTX) error {
		g, err := store.GetGeoFile(ctx, q, id)
		if err != nil {
			return err
		}
		if in.Title != nil {
			g.Title = strings.TrimSpace(*in.Title)
		}
		if in.SourceURL != nil {
			u := strings.TrimSpace(*in.SourceURL)
			if u == "" {
				return invalid("ссылка sourceUrl не может быть пустой")
			}
			g.SourceURL = u
		}
		if in.AutoUpdate != nil {
			g.AutoUpdate = *in.AutoUpdate
		}
		if in.Enabled != nil {
			if !*in.Enabled {
				if used := s.geoUsageByNodes(ctx)[g.Name]; len(used) > 0 {
					return invalid("нельзя отключить %s: файл используется на нодах %s", g.Name, strings.Join(used, ", "))
				}
			}
			g.Enabled = *in.Enabled
		}
		saved = g
		return store.UpdateGeoFile(ctx, q, g)
	})
	if err != nil {
		return nil, err
	}
	usage := s.geoUsageByNodes(ctx)
	v := s.geoFileToView(saved, usage[saved.Name])
	return &v, nil
}

// RefreshGeoFile re-downloads one geo_file from its source_url, verifies active categories are
// preserved, and triggers node reconciliation when the hash changes.
func (s *Service) RefreshGeoFile(ctx context.Context, actor Actor, id int64) (*GeoFileView, error) {
	if s.Geo == nil {
		return nil, invalid("хранилище geo-файлов не инициализировано")
	}
	g, err := store.GetGeoFile(ctx, s.st.DB, id)
	if err != nil {
		return nil, err
	}
	if g.SourceURL == "" {
		return nil, invalid("у файла %s не задана ссылка для обновления", g.Name)
	}

	requiredCats := s.usedCategoriesForFile(ctx, g.Name)
	res, dlErr := s.Geo.Download(ctx, g.SourceURL, g.Name, geostore.Kind(g.Kind), func(idx *geostore.FileIndex) error {
		var missing []string
		for _, cat := range requiredCats {
			if _, ok := idx.ByCode[cat]; !ok {
				missing = append(missing, cat)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("новая версия %s потеряла используемые категории: %s", g.Name, strings.Join(missing, ", "))
		}
		return nil
	})
	if dlErr != nil {
		// Record last_error without touching the working file on disk.
		_ = s.st.Tx(ctx, func(q store.DBTX) error {
			cur, err := store.GetGeoFile(ctx, q, id)
			if err != nil {
				return err
			}
			cur.LastError = dlErr.Error()
			return store.UpdateGeoFile(ctx, q, cur)
		})
		return nil, invalid("%v", dlErr)
	}

	changed := g.SHA256 != res.SHA256
	ev := ""
	if changed {
		ev = EvSettingChanged
	}
	var saved *store.GeoFile
	err = s.mutate(ctx, change{
		actor: actor, action: "geo.refresh", entity: "geo_file",
		entityID: func() int64 { return id },
		event:    ev,
		diff:     map[string]any{"name": g.Name, "changed": changed, "sha256": res.SHA256},
	}, func(q store.DBTX) error {
		cur, err := store.GetGeoFile(ctx, q, id)
		if err != nil {
			return err
		}
		cur.SHA256 = res.SHA256
		cur.SizeBytes = res.SizeBytes
		cur.CategoriesCount = res.CategoriesCount
		cur.EntriesCount = res.EntriesCount
		cur.FetchedAt = s.now().Unix()
		cur.LastError = ""
		saved = cur
		return store.UpdateGeoFile(ctx, q, cur)
	})
	if err != nil {
		return nil, err
	}
	usage := s.geoUsageByNodes(ctx)
	v := s.geoFileToView(saved, usage[saved.Name])
	return &v, nil
}

// DeleteGeoFile removes a custom geo_file (or removes a system override, falling back to /usr/local/share/xray).
func (s *Service) DeleteGeoFile(ctx context.Context, actor Actor, id int64) error {
	g, err := store.GetGeoFile(ctx, s.st.DB, id)
	if err != nil {
		return err
	}
	if !g.IsSystemOverride {
		if used := s.geoUsageByNodes(ctx)[g.Name]; len(used) > 0 {
			return invalid("нельзя удалить %s: файл используется в правилах WARP на нодах %s", g.Name, strings.Join(used, ", "))
		}
	}
	err = s.mutate(ctx, change{
		actor: actor, action: "geo.delete", entity: "geo_file",
		entityID: func() int64 { return id },
		event:    EvSettingChanged,
		diff:     map[string]string{"name": g.Name},
	}, func(q store.DBTX) error {
		return store.DeleteGeoFile(ctx, q, id)
	})
	if err != nil {
		return err
	}
	if s.Geo != nil {
		_ = s.Geo.Remove(g.Name)
	}
	return nil
}

// SearchGeo searches categories across active geo files by name or by content (domain/IP).
func (s *Service) SearchGeo(ctx context.Context, query, kind, fileFilter, mode string, limit int) ([]geostore.SearchHit, error) {
	if s.Geo == nil {
		return []geostore.SearchHit{}, nil
	}
	files := s.activeGeoFileNames(ctx, fileFilter)
	var k geostore.Kind
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "geosite":
		k = geostore.KindGeoSite
	case "geoip":
		k = geostore.KindGeoIP
	}
	if mode == "content" {
		hits := s.Geo.SearchByContent(files, query, k, limit)
		if hits == nil {
			hits = []geostore.SearchHit{}
		}
		return hits, nil
	}
	hits := s.Geo.SearchNames(files, query, k, limit)
	if hits == nil {
		hits = []geostore.SearchHit{}
	}
	return hits, nil
}

// InspectGeoCategory returns paginated items of one category inside fileName.
func (s *Service) InspectGeoCategory(ctx context.Context, fileName, code, query, attr string, offset, limit int) (*geostore.CategoryDetails, error) {
	if s.Geo == nil {
		return nil, ErrNotFound
	}
	clean, err := geostore.ValidateFileName(fileName)
	if err != nil {
		return nil, invalid("%v", err)
	}
	p, idx, _, err := s.Geo.Index(clean)
	if err != nil {
		return nil, ErrNotFound
	}
	det, err := geostore.InspectCategory(clean, p, idx, code, query, attr, offset, limit)
	if err != nil {
		return nil, ErrNotFound
	}
	return det, nil
}

// EnsureGeoFiles re-downloads any enabled geo_files missing from <PanelDataDir>/geo (e.g. after vynel restore).
func (s *Service) EnsureGeoFiles(ctx context.Context) {
	if s.Geo == nil {
		return
	}
	rows, err := store.ListGeoFiles(ctx, s.st.DB)
	if err != nil {
		return
	}
	for _, g := range rows {
		if !g.Enabled || g.SourceURL == "" || s.Geo.HasCustomFile(g.Name) {
			continue
		}
		_, _ = s.RefreshGeoFile(ctx, ActorSystem, g.ID)
	}
}

// refreshDueGeoFiles updates auto_update geo_files older than 24 hours (called from Maintenance).
func (s *Service) refreshDueGeoFiles(ctx context.Context) {
	if s.Geo == nil {
		return
	}
	rows, err := store.ListGeoFiles(ctx, s.st.DB)
	if err != nil {
		return
	}
	now := s.now().Unix()
	for _, g := range rows {
		if !g.Enabled || g.SourceURL == "" {
			continue
		}
		missing := !s.Geo.HasCustomFile(g.Name)
		due := g.AutoUpdate && (g.FetchedAt == 0 || now-g.FetchedAt >= 86400)
		if !missing && !due {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		_, _ = s.RefreshGeoFile(rctx, ActorSystem, g.ID)
		cancel()
	}
}

func (s *Service) activeGeoFileNames(ctx context.Context, fileFilter string) []string {
	if f := strings.TrimSpace(fileFilter); f != "" {
		if clean, err := geostore.ValidateFileName(f); err == nil {
			return []string{clean}
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	add(geostore.SystemGeoSite)
	add(geostore.SystemGeoIP)
	if rows, err := store.ListGeoFiles(ctx, s.st.DB); err == nil {
		for _, r := range rows {
			if r.Enabled {
				add(r.Name)
			}
		}
	}
	return out
}

func (s *Service) geoKindMap(ctx context.Context) map[string]geostore.Kind {
	m := map[string]geostore.Kind{
		geostore.SystemGeoSite: geostore.KindGeoSite,
		geostore.SystemGeoIP:   geostore.KindGeoIP,
	}
	if rows, err := store.ListGeoFiles(ctx, s.st.DB); err == nil {
		for _, r := range rows {
			m[r.Name] = geostore.Kind(r.Kind)
		}
	}
	return m
}

func (s *Service) geoUsageByNodes(ctx context.Context) map[string][]string {
	out := map[string][]string{}
	nodes, err := store.ListNodes(ctx, s.st.DB)
	if err != nil {
		return out
	}
	codeByID := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		codeByID[n.ID] = n.Code
	}
	warps, err := store.ListNodeWarps(ctx, s.st.DB)
	if err != nil {
		return out
	}
	kinds := s.geoKindMap(ctx)
	for nodeID, w := range warps {
		if !w.Enabled || w.RuleMode == warp.RuleModeAll {
			continue
		}
		parsed, err := warp.ParseRules(w.Rules, func(file string) geostore.Kind { return kinds[file] })
		if err != nil {
			continue
		}
		code := codeByID[nodeID]
		if code == "" {
			continue
		}
		for _, ref := range parsed.GeoRefs {
			if !slices.Contains(out[ref.File], code) {
				out[ref.File] = append(out[ref.File], code)
			}
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

func (s *Service) usedCategoriesForFile(ctx context.Context, fileName string) []string {
	warps, err := store.ListNodeWarps(ctx, s.st.DB)
	if err != nil {
		return nil
	}
	kinds := s.geoKindMap(ctx)
	set := map[string]bool{}
	for _, w := range warps {
		if !w.Enabled || w.RuleMode == warp.RuleModeAll {
			continue
		}
		parsed, err := warp.ParseRules(w.Rules, func(file string) geostore.Kind { return kinds[file] })
		if err != nil {
			continue
		}
		for _, ref := range parsed.GeoRefs {
			if ref.File == fileName {
				set[ref.Category] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
