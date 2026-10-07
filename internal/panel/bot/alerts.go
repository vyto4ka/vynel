package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/backup"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// ---- node alerts ----
//
// A node that is away longer than bot.alert_delay gets ONE message per admin. While it stays
// down the message is edited (since when, for how long, last check); when it is back the same
// message turns into "back up, was down N min" and a short reply makes the phone ring.

const editEvery = time.Minute // Telegram rate limits: one edit per alert per minute is plenty

type nodeAlert struct {
	DownSince int64           `json:"downSince"` // first check that saw the node away
	LastSeen  int64           `json:"lastSeen"`  // last contact before it went away
	Messages  map[int64]int64 `json:"messages"`  // admin chat -> message id; empty = not sent yet
	lastEdit  time.Time
}

type alertState struct {
	nodes  map[int64]*nodeAlert // node id -> alert
	loaded bool
}

func (b *Bot) loadAlerts(ctx context.Context) {
	b.alerts.nodes = map[int64]*nodeAlert{}
	raw, _ := b.svc.Setting(ctx, service.SettingBotAlertState, "")
	if raw != "" {
		var m map[string]*nodeAlert
		if json.Unmarshal([]byte(raw), &m) == nil {
			for k, v := range m {
				if id, err := strconv.ParseInt(k, 10, 64); err == nil && v != nil {
					if v.Messages == nil {
						v.Messages = map[int64]int64{}
					}
					b.alerts.nodes[id] = v
				}
			}
		}
	}
	b.alerts.loaded = true
}

func (b *Bot) saveAlerts(ctx context.Context) {
	m := map[string]*nodeAlert{}
	for id, a := range b.alerts.nodes {
		m[strconv.FormatInt(id, 10)] = a
	}
	raw, _ := json.Marshal(m)
	_ = b.svc.SetSetting(ctx, service.Actor{Kind: "bot"}, service.SettingBotAlertState, string(raw))
}

func (b *Bot) alertDelay(ctx context.Context) time.Duration {
	v, _ := b.svc.Setting(ctx, service.SettingBotAlertDelay, "60")
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		n = 60
	}
	return time.Duration(n) * time.Second
}

func (b *Bot) checkNodes(ctx context.Context) {
	api := b.client()
	if api == nil || !b.alerts.loaded {
		return
	}
	if on, _ := b.svc.Setting(ctx, service.SettingBotAlerts, "true"); on == "false" {
		return
	}
	statuses, err := b.svc.NodeStatuses(ctx, b.cfg.Connected)
	if err != nil {
		return
	}
	admins, _ := b.svc.BotAdmins(ctx)
	now := b.cfg.Now()
	delay := b.alertDelay(ctx)
	changed := false
	seen := map[int64]bool{}
	for _, ns := range statuses {
		n := ns.Node
		seen[n.ID] = true
		st := ns.State()
		watched := n.Enabled && st != service.NodePending
		a := b.alerts.nodes[n.ID]
		down := watched && !ns.Connected
		switch {
		case down && a == nil:
			last := int64(0)
			if n.LastSeenAt != nil {
				last = *n.LastSeenAt
			}
			b.alerts.nodes[n.ID] = &nodeAlert{DownSince: now.Unix(), LastSeen: last, Messages: map[int64]int64{}}
			changed = true
		case down && len(a.Messages) == 0:
			if now.Sub(time.Unix(a.DownSince, 0)) >= delay {
				text := b.downText(ctx, n.Name, n.Code, n.Country, a, now)
				for _, ad := range admins {
					if m, err := api.send(ctx, ad.ID, text, nil); err == nil {
						a.Messages[ad.ID] = m.MessageID
					}
				}
				a.lastEdit = now
				changed = true
			}
		case down:
			if now.Sub(a.lastEdit) >= editEvery {
				text := b.downText(ctx, n.Name, n.Code, n.Country, a, now)
				for chat, msg := range a.Messages {
					_ = api.edit(ctx, chat, msg, text, nil)
				}
				a.lastEdit = now
			}
		case a != nil:
			// Back (or no longer watched): close the alert.
			if len(a.Messages) > 0 {
				text := b.upText(ctx, n.Name, n.Code, n.Country, a, now)
				for chat, msg := range a.Messages {
					_ = api.edit(ctx, chat, msg, text, nil)
					if watched {
						_ = api.reply(ctx, chat, msg, "🟢 "+xrayconf.CountryFlag(n.Country)+" "+esc(n.Name)+" снова работает")
					}
				}
			}
			delete(b.alerts.nodes, n.ID)
			changed = true
		}
	}
	for id := range b.alerts.nodes {
		if !seen[id] { // the node was deleted
			delete(b.alerts.nodes, id)
			changed = true
		}
	}
	if changed {
		b.saveAlerts(ctx)
	}
}

func (b *Bot) downText(ctx context.Context, name, code, country string, a *nodeAlert, now time.Time) string {
	since := time.Unix(a.DownSince, 0)
	var sb strings.Builder
	fmt.Fprintf(&sb, "🔴 <b>Нода %s %s (%s) недоступна</b>\n\n", xrayconf.CountryFlag(country), esc(name), code)
	fmt.Fprintf(&sb, "с %s (уже %s)\n", b.fmtTime(ctx, since.Unix(), "02.01 15:04"), duration(now.Sub(since)))
	if a.LastSeen > 0 {
		fmt.Fprintf(&sb, "последняя связь: %s\n", b.fmtTime(ctx, a.LastSeen, "02.01 15:04:05"))
	}
	fmt.Fprintf(&sb, "последняя проверка: %s\n\n", b.fmtTime(ctx, now.Unix(), "15:04:05"))
	sb.WriteString("<i>Сообщение обновляется, пока нода не вернётся. На ноде: journalctl -u vynel-node -e</i>")
	return sb.String()
}

func (b *Bot) upText(ctx context.Context, name, code, country string, a *nodeAlert, now time.Time) string {
	since := time.Unix(a.DownSince, 0)
	return fmt.Sprintf("🟢 <b>Нода %s %s (%s) снова в строю</b>\n\nбыла недоступна %s – %s (%s)",
		xrayconf.CountryFlag(country), esc(name), code, b.fmtTime(ctx, since.Unix(), "02.01 15:04"),
		b.fmtTime(ctx, now.Unix(), "15:04"), duration(now.Sub(since)))
}

// ---- backups ----

const keepLocal = 7 // nightly backups also kept on the server

type backupState struct {
	lastDay string // date (in the bot timezone) of the last nightly backup
}

// maybeBackup sends the nightly backup once a day at bot.backup_time.
func (b *Bot) maybeBackup(ctx context.Context) {
	at, _ := b.svc.Setting(ctx, service.SettingBotBackupTime, service.DefaultBackupTime)
	at = strings.TrimSpace(at)
	if at == "" || at == "off" {
		return
	}
	hm, err := time.Parse("15:04", at)
	if err != nil {
		return
	}
	now := b.cfg.Now().In(b.loc(ctx))
	day := now.Format("2006-01-02")
	if b.backup.lastDay == "" {
		b.backup.lastDay, _ = b.svc.Setting(ctx, "bot.backup_last", "")
	}
	due := time.Date(now.Year(), now.Month(), now.Day(), hm.Hour(), hm.Minute(), 0, 0, now.Location())
	// Within two hours after the time: a restart around the scheduled minute does not skip a day.
	if b.backup.lastDay == day || now.Before(due) || now.Sub(due) > 2*time.Hour {
		return
	}
	b.backup.lastDay = day
	_ = b.svc.SetSetting(ctx, service.Actor{Kind: "bot"}, "bot.backup_last", day)
	admins, _ := b.svc.BotAdmins(ctx)
	if err := b.sendBackup(ctx, b.client(), admins, "🌙 Ночной бэкап"); err != nil {
		b.log.Error("nightly backup", "err", err)
	}
}

// SendBackup sends a backup to every admin now (the web panel's button).
func (b *Bot) SendBackup(ctx context.Context) error {
	admins, err := b.svc.BotAdmins(ctx)
	if err != nil {
		return err
	}
	if len(admins) == 0 {
		return fmt.Errorf("no admins are bound to the bot")
	}
	return b.sendBackup(ctx, b.client(), admins, "💾 Бэкап из веб-панели")
}

// backupNow is the «Бэкап сейчас» button.
func (b *Bot) backupNow(ctx context.Context, api *client, chat int64) {
	if err := b.sendBackup(ctx, api, []service.BotAdmin{{ID: chat}}, "💾 Бэкап"); err != nil {
		_, _ = api.send(ctx, chat, "Бэкап не получился: "+esc(err.Error()), nil)
	}
}

// Backup writes a backup and keeps the last ones in <data>/backups. It is also used by the web
// panel («Скачать бэкап»).
func (b *Bot) Backup(ctx context.Context) (string, []byte, *backup.Meta, error) {
	var buf bytes.Buffer
	meta, err := backup.Create(ctx, b.svc.Store(), b.cfg.DataDir, b.cfg.Version, &buf)
	if err != nil {
		return "", nil, nil, err
	}
	name := backup.FileName(meta, b.loc(ctx))
	dir := filepath.Join(b.cfg.DataDir, "backups")
	if err := os.MkdirAll(dir, 0o700); err == nil {
		_ = os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o600)
		pruneBackups(dir, keepLocal)
	}
	return name, buf.Bytes(), meta, nil
}

func pruneBackups(dir string, keep int) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range es {
		if strings.HasSuffix(e.Name(), ".tar.gz") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // names end with the date: oldest first
	for len(names) > keep {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

func (b *Bot) sendBackup(ctx context.Context, api *client, to []service.BotAdmin, title string) error {
	if api == nil {
		return fmt.Errorf("the bot is not running")
	}
	name, data, meta, err := b.Backup(ctx)
	if err != nil {
		return err
	}
	caption := fmt.Sprintf("%s · %s\n%d пользователей, %d нод, %s\n\nВосстановить: <code>vynel restore %s</code> или установщик с <code>--restore</code>",
		title, b.fmtTime(ctx, meta.CreatedAt, "02.01.2006 15:04"), meta.Users, meta.Nodes, gb(int64(len(data))), esc(name))
	var lastErr error
	for _, a := range to {
		if err := api.sendDocument(ctx, a.ID, name, data, caption); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
