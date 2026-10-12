package webapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

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
	sent      map[int64]time.Time // node id -> «Обновить» pressed (0 = the panel)
	failed    map[int64]sentFail  // node id -> the command itself failed
}

type sentFail struct {
	at  time.Time
	msg string
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

func (u *updates) markSent(id int64, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.sent == nil {
		u.sent, u.failed = map[int64]time.Time{}, map[int64]sentFail{}
	}
	if err != nil {
		u.failed[id] = sentFail{time.Now(), updateError(err)}
		delete(u.sent, id)
		return
	}
	u.sent[id] = time.Now()
	delete(u.failed, id)
}

// state decides what to show for one server from what the panel did (sent, failed) and what
// the server reports (st; nil = nothing yet, or an agent too old to report).
func (u *updates) state(id int64, st *update.Status, online bool, now time.Time) (state string, log string, started, finished int64) {
	u.mu.Lock()
	sentAt, wasSent := u.sent[id]
	fail, didFail := u.failed[id]
	u.mu.Unlock()
	if st != nil {
		log, started, finished = st.Log, st.StartedAt, st.FinishedAt
	}
	// The node clock may run a little behind the panel's.
	reportedSince := func(t time.Time) bool { return st != nil && st.StartedAt >= t.Unix()-30 }
	switch {
	case didFail && now.Sub(fail.at) < updateShowDone:
		return UpdFailed, fail.msg, fail.at.Unix(), fail.at.Unix()
	case st != nil && st.Running && !online:
		return UpdRestarting, log, started, 0
	case st != nil && st.Running:
		return UpdRunning, log, started, 0
	case wasSent && !reportedSince(sentAt) && now.Sub(sentAt) < updateSentWait:
		if !online {
			return UpdRestarting, "", sentAt.Unix(), 0
		}
		return UpdSent, "", sentAt.Unix(), 0
	case wasSent && !reportedSince(sentAt):
		return UpdFailed, "the node did not report the update within " + updateSentWait.String() +
			": its agent may be too old to report progress (see /var/log/vynel-update.log on the node)", sentAt.Unix(), now.Unix()
	case st != nil && st.FinishedAt > 0 && now.Unix()-st.FinishedAt < int64(updateShowDone/time.Second):
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
	info.Panel.setState(s.upd.state(0, update.LocalStatus(), true, now))
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
		un.setState(s.upd.state(n.ID, st, online, now))
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

func (s *Server) getUpdates(r *http.Request) (any, error) { return s.updateInfo(r.Context(), false) }

func (s *Server) checkUpdates(r *http.Request) (any, error) {
	s.upd.check(r.Context(), true)
	return s.updateInfo(r.Context(), false)
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
	done := func(name string, id int64, err error) {
		s.upd.markSent(id, err)
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
