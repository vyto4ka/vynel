package webapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/i18n"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/update"
)

// How often the panel asks its release for the newest version; how long after «Обновить» the
// panel waits for the node to say it started; how long a finished update stays on the card.
const (
	updateCheckEvery = 30 * time.Minute
	updateSentWait   = 2 * time.Minute
	updateShowDone   = 30 * time.Minute
)

// Update states, as the UI shows them.
const (
	UpdSent       = "sent"       // the command went out; the node has not said it started yet
	UpdRunning    = "running"    // the node reports its update running
	UpdRestarting = "restarting" // running, and the node (or the panel) is restarting
	UpdDone       = "done"       // finished, recently
	UpdFailed     = "failed"     // the command or the update failed, recently
)

// updates keeps what the panel knows about new versions and the updates it started.
type updates struct {
	mu        sync.Mutex
	latest    string
	checkErr  string
	checkedAt time.Time
	jobs      map[int64]*updJob // node id -> the update started from the panel (0 = the panel)
	hidden    map[int64]int64   // node id -> results that finished up to then are dismissed
}

// updJob is an update the panel started.
type updJob struct {
	sentAt     time.Time
	version    string // the server's version when «Обновить» was pressed
	failAt     time.Time
	failMsg    string    // the command itself failed
	doneAt     time.Time // the server came back with another version
	newVersion string
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

func (u *updates) markSent(id int64, version string, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.jobs == nil {
		u.jobs = map[int64]*updJob{}
	}
	j := &updJob{sentAt: time.Now(), version: version}
	if err != nil {
		j.failAt, j.failMsg = j.sentAt, updateError(err)
	}
	u.jobs[id] = j
}

// dismiss hides the update state of id until the next update.
func (u *updates) dismiss(id int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.jobs, id)
	if u.hidden == nil {
		u.hidden = map[int64]int64{}
	}
	u.hidden[id] = time.Now().Unix()
}

// silentNode explains an update the node never reported.
const silentNode = "Нода не сообщила о начале обновления за 2 минуты. Скорее всего, на ней версия, которая ещё не умеет обновляться из панели: обновите её один раз установщиком (--mode update), дальше обновления из панели будут видны. Лог на ноде: /var/log/vynel-update.log"

// state decides what to show for one server from what the panel did (its job), what the server
// reports (st; nil = nothing yet, or an agent too old to report) and the version it runs now.
func (u *updates) state(id int64, st *update.Status, online bool, version string, now time.Time) (state string, log string, started, finished int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if st != nil {
		log, started, finished = st.Log, st.StartedAt, st.FinishedAt
	}
	show := int64(updateShowDone / time.Second)
	j := u.jobs[id]
	// The server came back with another version: updated, whatever the panel saw on the way
	// (an old agent that never reported, or an update done by hand after a failed one).
	if j != nil && j.doneAt.IsZero() && version != "" && j.version != "" && version != j.version {
		j.doneAt, j.newVersion = now, version
	}
	running := st != nil && st.Running
	if j != nil && !j.doneAt.IsZero() && !running {
		if now.Sub(j.doneAt) >= updateShowDone {
			delete(u.jobs, id)
			return "", log, started, finished
		}
		if st != nil && st.FinishedAt >= j.sentAt.Unix()-30 {
			if !st.OK {
				return UpdFailed, log, started, finished
			}
			return UpdDone, log, started, finished
		}
		return UpdDone, "Версия " + j.version + " → " + j.newVersion, j.sentAt.Unix(), j.doneAt.Unix()
	}
	switch {
	case running && !online:
		return UpdRestarting, log, started, 0
	case running:
		return UpdRunning, log, started, 0
	}
	if j != nil {
		reported := st != nil && st.StartedAt >= j.sentAt.Unix()-30 // the node clock may lag a little
		switch {
		case j.failMsg != "" && now.Sub(j.failAt) < updateShowDone:
			return UpdFailed, j.failMsg, j.failAt.Unix(), j.failAt.Unix()
		case j.failMsg != "":
			delete(u.jobs, id)
		case !reported && now.Sub(j.sentAt) < updateSentWait:
			if !online {
				return UpdRestarting, "", j.sentAt.Unix(), 0
			}
			return UpdSent, "", j.sentAt.Unix(), 0
		case !reported && now.Sub(j.sentAt) < updateSentWait+updateShowDone:
			return UpdFailed, silentNode, j.sentAt.Unix(), j.sentAt.Add(updateSentWait).Unix()
		case !reported:
			delete(u.jobs, id)
		}
	}
	if st != nil && st.FinishedAt > u.hidden[id] && now.Unix()-st.FinishedAt < show {
		if st.OK {
			return UpdDone, log, started, finished
		}
		return UpdFailed, log, started, finished
	}
	return "", log, started, finished
}

func newer(version, latest string) bool {
	return latest != "" && version != "" && version != "dev" && version != latest
}

// UpdateNode is one server in the update overview (id 0 = the panel and its local node).
type UpdateNode struct {
	ID         int64  `json:"id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Flag       string `json:"flag"`
	Local      bool   `json:"local"`
	Online     bool   `json:"online"`
	Version    string `json:"version"`
	Available  bool   `json:"available"`
	Updating   bool   `json:"updating"` // state is sent, running or restarting
	State      string `json:"state"`
	StartedAt  int64  `json:"startedAt,omitempty"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
	Log        string `json:"log,omitempty"`
}

// UpdateInfo is GET /api/updates.
type UpdateInfo struct {
	Release   string       `json:"release"`
	Latest    string       `json:"latest"`
	CheckErr  string       `json:"checkError,omitempty"`
	CheckedAt int64        `json:"checkedAt"`
	Panel     UpdateNode   `json:"panel"`
	Nodes     []UpdateNode `json:"nodes"` // remote nodes (the local one updates with the panel)
	Count     int          `json:"count"` // how many (panel + nodes) have a newer version
}

func (n *UpdateNode) setState(state, log string, started, finished int64) {
	n.State, n.Log, n.StartedAt, n.FinishedAt = state, log, started, finished
	n.Updating = state == UpdSent || state == UpdRunning || state == UpdRestarting
}

func (s *Server) updateInfo(ctx context.Context, withLog bool) (*UpdateInfo, error) {
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
	now := time.Now()
	info.Panel = UpdateNode{Name: "Панель", Local: true, Online: true, Version: s.cfg.Version, Available: newer(s.cfg.Version, info.Latest)}
	info.Panel.setState(s.upd.state(0, update.LocalStatus(), true, s.cfg.Version, now))
	if info.Panel.Available {
		info.Count++
	}
	for _, ns := range sts {
		n := ns.Node
		if n.Local {
			continue
		}
		online := s.cfg.Connected != nil && s.cfg.Connected(n.ID)
		var st *update.Status
		if s.cfg.UpdateStatus != nil {
			st = s.cfg.UpdateStatus(n.ID)
		}
		un := UpdateNode{ID: n.ID, Code: n.Code, Name: n.Name, Flag: n.Country, Online: online, Version: n.AgentVersion,
			Available: newer(n.AgentVersion, info.Latest)}
		un.setState(s.upd.state(n.ID, st, online, n.AgentVersion, now))
		if un.Available {
			info.Count++
		}
		info.Nodes = append(info.Nodes, un)
	}
	if !withLog {
		info.Panel.Log = ""
		for i := range info.Nodes {
			info.Nodes[i].Log = ""
		}
	}
	return info, nil
}

func (s *Server) getUpdates(r *http.Request) (any, error) { return s.localUpdateInfo(r) }

func (s *Server) checkUpdates(r *http.Request) (any, error) {
	s.upd.check(r.Context(), true)
	return s.localUpdateInfo(r)
}

// localUpdateInfo is updateInfo with the panel's own entry named in the language of the request.
func (s *Server) localUpdateInfo(r *http.Request) (any, error) {
	info, err := s.updateInfo(r.Context(), false)
	if err != nil {
		return nil, err
	}
	info.Panel.Name = i18n.T(i18n.FromRequest(r), info.Panel.Name)
	return info, nil
}

// dismissUpdate is POST /api/updates/dismiss {"id": N}: hide a finished or failed update.
func (s *Server) dismissUpdate(r *http.Request) (any, error) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	s.upd.dismiss(in.ID)
	return nil, nil
}

// updateLog is GET /api/updates/log?id=N (0 = the panel): the server's state and its log.
func (s *Server) updateLog(r *http.Request) (any, error) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	info, err := s.updateInfo(r.Context(), true)
	if err != nil {
		return nil, err
	}
	if id == 0 {
		return info.Panel, nil
	}
	for _, n := range info.Nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, service.ErrNotFound
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
	info, err := s.updateInfo(ctx, false)
	if err != nil {
		return nil, err
	}
	if s.cfg.NodeAction == nil {
		return nil, badRequest("обновление нод недоступно")
	}
	var mu sync.Mutex
	started, failed := []string{}, map[string]string{}
	versionOf := map[int64]string{0: info.Panel.Version}
	for _, n := range info.Nodes {
		versionOf[n.ID] = n.Version
	}
	done := func(name string, id int64, err error) {
		s.upd.markSent(id, versionOf[id], err)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			failed[name] = updateError(err)
			return
		}
		started = append(started, name)
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
