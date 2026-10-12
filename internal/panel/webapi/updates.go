package webapi

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/update"
)

// How often the panel asks its release for the newest version, and how long a started update
// counts as running when the node has not come back with the new version yet.
const (
	updateCheckEvery = 30 * time.Minute
	updateWindow     = 10 * time.Minute
)

// updates keeps what the panel knows about new versions.
type updates struct {
	mu        sync.Mutex
	latest    string
	checkErr  string
	checkedAt time.Time
	started   map[int64]time.Time // node id -> update started (0 = the panel)
}

func (u *updates) check(ctx context.Context, force bool) {
	u.mu.Lock()
	fresh := !force && time.Since(u.checkedAt) < updateCheckEvery
	u.mu.Unlock()
	if fresh {
		return
	}
	v, err := update.Latest(ctx, update.LoadSource())
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checkedAt = time.Now()
	if err != nil {
		u.checkErr = err.Error()
		return
	}
	u.latest, u.checkErr = v, ""
}

func (u *updates) markStarted(id int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.started == nil {
		u.started = map[int64]time.Time{}
	}
	u.started[id] = time.Now()
}

// updating: the update of id started recently and the version has not changed to latest yet.
func (u *updates) updating(id int64, version string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	t, ok := u.started[id]
	return ok && time.Since(t) < updateWindow && (u.latest == "" || version != u.latest)
}

func newer(version, latest string) bool {
	return latest != "" && version != "" && version != "dev" && version != latest
}

// UpdateNode is one node in the update overview.
type UpdateNode struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Flag      string `json:"flag"`
	Local     bool   `json:"local"`
	Online    bool   `json:"online"`
	Version   string `json:"version"`
	Available bool   `json:"available"`
	Updating  bool   `json:"updating"`
}

// UpdateInfo is GET /api/updates.
type UpdateInfo struct {
	Release   string         `json:"release"`
	Latest    string         `json:"latest"`
	CheckErr  string         `json:"checkError,omitempty"`
	CheckedAt int64          `json:"checkedAt"`
	Panel     UpdateNode     `json:"panel"`
	LastPanel *update.Result `json:"lastPanel,omitempty"` // the panel server's last update
	Nodes     []UpdateNode   `json:"nodes"`               // remote nodes (the local one updates with the panel)
	Count     int            `json:"count"`               // how many (panel + nodes) have a newer version
}

func (s *Server) updateInfo(ctx context.Context) (*UpdateInfo, error) {
	s.upd.check(ctx, false)
	sts, err := s.svc.NodeStatuses(ctx, s.cfg.Connected)
	if err != nil {
		return nil, err
	}
	s.upd.mu.Lock()
	info := &UpdateInfo{Release: update.LoadSource().Release, Latest: s.upd.latest, CheckErr: s.upd.checkErr, Nodes: []UpdateNode{}}
	if !s.upd.checkedAt.IsZero() {
		info.CheckedAt = s.upd.checkedAt.Unix()
	}
	s.upd.mu.Unlock()
	info.Panel = UpdateNode{Name: "Панель", Local: true, Online: true, Version: s.cfg.Version,
		Available: newer(s.cfg.Version, info.Latest), Updating: s.panelUpdating()}
	if info.Panel.Available {
		info.Count++
	}
	info.LastPanel = update.LastResult()
	for _, ns := range sts {
		n := ns.Node
		if n.Local {
			continue
		}
		online := s.cfg.Connected != nil && s.cfg.Connected(n.ID)
		un := UpdateNode{ID: n.ID, Code: n.Code, Name: n.Name, Flag: n.Country, Online: online, Version: n.AgentVersion,
			Available: newer(n.AgentVersion, info.Latest), Updating: s.upd.updating(n.ID, n.AgentVersion)}
		if un.Available {
			info.Count++
		}
		info.Nodes = append(info.Nodes, un)
	}
	return info, nil
}

func (s *Server) panelUpdating() bool {
	return exec.Command("systemctl", "is-active", "--quiet", update.Unit).Run() == nil
}

func (s *Server) getUpdates(r *http.Request) (any, error) { return s.updateInfo(r.Context()) }

func (s *Server) checkUpdates(r *http.Request) (any, error) {
	s.upd.check(r.Context(), true)
	return s.updateInfo(r.Context())
}

// runUpdates: {"target": "all" | "panel" | "node", "id": N}. "all" starts every outdated remote
// node at once and then the panel, so the servers are down together for a minute instead of one
// after another.
func (s *Server) runUpdates(r *http.Request) (any, error) {
	var in struct {
		Target string `json:"target"`
		ID     int64  `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	info, err := s.updateInfo(ctx)
	if err != nil {
		return nil, err
	}
	if s.cfg.NodeAction == nil {
		return nil, badRequest("обновление нод недоступно")
	}
	var mu sync.Mutex
	started, failed := []string{}, map[string]string{}
	done := func(name string, id int64, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			failed[name] = updateError(err)
			return
		}
		started = append(started, name)
		s.upd.markStarted(id)
	}
	nodeUpdate := func(n UpdateNode) {
		_, err := s.cfg.NodeAction(ctx, n.ID, "update")
		done(n.Name, n.ID, err)
		res := "ok"
		if err != nil {
			res = err.Error()
		}
		_ = s.svc.LogNodeAction(ctx, actor(r), n.ID, "update", res)
	}
	switch in.Target {
	case "node":
		for _, n := range info.Nodes {
			if n.ID == in.ID {
				nodeUpdate(n)
			}
		}
	case "panel":
		done("Панель", 0, update.Start(ctx))
	case "all":
		var wg sync.WaitGroup
		for _, n := range info.Nodes {
			if n.Available && n.Online && !n.Updating {
				wg.Add(1)
				go func() { defer wg.Done(); nodeUpdate(n) }()
			}
		}
		wg.Wait()
		// The panel last: once it restarts, this request and the page go down with it.
		if info.Panel.Available && !info.Panel.Updating {
			done("Панель", 0, update.Start(ctx))
		}
	default:
		return nil, badRequest("что обновлять: all, panel или node")
	}
	return map[string]any{"started": started, "failed": failed}, nil
}

// updateError explains the usual failures in words.
func updateError(err error) string {
	switch {
	case errors.Is(err, update.ErrRunning):
		return "обновление уже идёт"
	case err.Error() == "unknown action 5":
		return "эта версия ноды не умеет обновляться из панели: один раз обновите её командой установки с --mode update"
	}
	return err.Error()
}
