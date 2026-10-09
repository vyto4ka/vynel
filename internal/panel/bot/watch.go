package bot

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// ---- sign-ins (docs/STEALTH.md §3.3) ----

var methodNames = map[string]string{service.LoginPassword: "по паролю", service.LoginLink: "по ссылке из бота", service.LoginTelegram: "из Telegram"}

// browser shortens a User-Agent to "Chrome · Windows".
func browser(ua string) string {
	l := strings.ToLower(ua)
	pick := func(pairs ...string) string {
		for i := 0; i+1 < len(pairs); i += 2 {
			if strings.Contains(l, pairs[i]) {
				return pairs[i+1]
			}
		}
		return ""
	}
	b := pick("telegram", "Telegram", "edg/", "Edge", "opr/", "Opera", "yabrowser", "Яндекс", "firefox", "Firefox", "chrome", "Chrome", "safari", "Safari")
	o := pick("android", "Android", "iphone", "iPhone", "ipad", "iPad", "windows", "Windows", "mac os", "macOS", "linux", "Linux")
	switch {
	case b != "" && o != "":
		return b + " · " + o
	case b+o != "":
		return b + o
	case ua == "":
		return "неизвестно"
	}
	return truncate(ua, 60)
}

func (b *Bot) sessionLine(ctx context.Context, w *store.WebSession) string {
	return fmt.Sprintf("%s · %s · <code>%s</code>", methodNames[w.Method], esc(browser(w.UserAgent)), esc(w.IP))
}

// onLogin tells every admin that someone signed into the panel, with a button to throw them out.
func (b *Bot) onLogin(ctx context.Context, w *store.WebSession) {
	api := b.client()
	if api == nil {
		return
	}
	admins, _ := b.svc.BotAdmins(ctx)
	text := fmt.Sprintf("🔐 <b>Вход в панель</b> %s\n%s\n\n<i>Не вы? Завершите сессию и смените пароль.</i>",
		b.fmtTime(ctx, w.CreatedAt, "02.01 15:04"), b.sessionLine(ctx, w))
	kb := Keyboard{row(btn("⛔ Завершить эту сессию", "sx:"+w.ID), btn("Все сессии", "ss"))}
	for _, a := range admins {
		_, _ = api.send(ctx, a.ID, text, kb)
	}
}

func (b *Bot) showSessions(ctx context.Context, api *client, chat, msg int64, notice string) {
	ws, err := b.svc.WebSessions(ctx)
	if err != nil {
		b.show(ctx, api, chat, msg, "Не получилось: "+esc(errText(err)), nil)
		return
	}
	var sb strings.Builder
	if notice != "" {
		sb.WriteString(notice + "\n\n")
	}
	fmt.Fprintf(&sb, "<b>Сессии веб-панели</b> · %d\n", len(ws))
	var kb Keyboard
	now := b.cfg.Now()
	for i, w := range ws {
		fmt.Fprintf(&sb, "\n%d. %s\n   вход %s, активность %s", i+1, b.sessionLine(ctx, w), b.fmtTime(ctx, w.CreatedAt, "02.01 15:04"), ago(now, &w.LastSeenAt))
		if i < 8 {
			kb = append(kb, row(btn(fmt.Sprintf("⛔ Завершить %d", i+1), "sx:"+w.ID)))
		}
	}
	if len(ws) == 0 {
		sb.WriteString("\nНикто не вошёл.")
	}
	if !b.svc.PasswordLoginEnabled(ctx) {
		sb.WriteString("\n\nВход по паролю выключен: войти можно только через бота.")
	}
	if len(ws) > 0 {
		kb = append(kb, row(btn("⛔ Завершить все", "sxa")))
	}
	kb = append(kb, row(btn("🔄 Обновить", "ss"), btn("☰ Меню", "menu")))
	b.show(ctx, api, chat, msg, sb.String(), kb)
}

// ---- watched state: apply errors and the panel address ----

type watchState struct {
	nodeErrs  map[int64]string // last apply error seen per node
	webURL    string
	summaryOn string // date of the last daily summary
}

// checkApply reports nodes that fail to apply their config, once per new error, and when they
// recover. Errors present at start are taken as known (no repeat after every panel restart).
func (b *Bot) checkApply(ctx context.Context) {
	api := b.client()
	if api == nil {
		return
	}
	statuses, err := b.svc.NodeStatuses(ctx, b.cfg.Connected)
	if err != nil {
		return
	}
	seed := b.watchSt.nodeErrs == nil
	if seed {
		b.watchSt.nodeErrs = map[int64]string{}
	}
	admins, _ := b.svc.BotAdmins(ctx)
	for _, ns := range statuses {
		n := ns.Node
		prev, cur := b.watchSt.nodeErrs[n.ID], n.LastError
		b.watchSt.nodeErrs[n.ID] = cur
		if seed || prev == cur || !n.Enabled {
			continue
		}
		name := xrayconf.CountryFlag(n.Country) + " " + esc(n.Name) + " (" + n.Code + ")"
		var text string
		if cur != "" {
			text = fmt.Sprintf("⚠️ <b>Нода %s не применила конфигурацию</b>\n\n<code>%s</code>\n\nНода работает на прежней конфигурации. Проверка: «Ноды» → 🔍 в панели.", name, esc(truncate(cur, 600)))
		} else {
			text = fmt.Sprintf("✅ Нода %s применила конфигурацию", name)
		}
		for _, a := range admins {
			_, _ = api.send(ctx, a.ID, text, nil)
		}
	}
}

// checkWebURL sends a fresh login button when the panel moves (secret path, port, domain).
func (b *Bot) checkWebURL(ctx context.Context) {
	api := b.client()
	if api == nil {
		return
	}
	url, err := b.svc.WebURL(ctx)
	if err != nil || url == "" {
		return
	}
	if b.watchSt.webURL == "" {
		b.watchSt.webURL = url
		return
	}
	if url == b.watchSt.webURL {
		return
	}
	b.watchSt.webURL = url
	admins, _ := b.svc.BotAdmins(ctx)
	for _, a := range admins {
		_, _ = api.send(ctx, a.ID, "🧭 <b>Панель переехала</b>\nНовый адрес: <code>"+esc(url)+"</code>\nСтарые закладки больше не откроются.",
			Keyboard{row(btn("🔑 Войти", "login")), row(Button{Text: "📱 Открыть в Telegram", WebApp: &WebApp{URL: url}})})
	}
}

// ---- daily summary ----

// maybeSummary sends the daily summary once a day at bot.summary_time.
func (b *Bot) maybeSummary(ctx context.Context) {
	at, _ := b.svc.Setting(ctx, service.SettingBotSummaryTime, service.DefaultSummaryTime)
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
	if b.watchSt.summaryOn == "" {
		b.watchSt.summaryOn, _ = b.svc.Setting(ctx, service.SettingBotSummaryLast, "")
	}
	due := time.Date(now.Year(), now.Month(), now.Day(), hm.Hour(), hm.Minute(), 0, 0, now.Location())
	if b.watchSt.summaryOn == day || now.Before(due) || now.Sub(due) > 2*time.Hour {
		return
	}
	b.watchSt.summaryOn = day
	_ = b.svc.SetSetting(ctx, service.Actor{Kind: "bot"}, service.SettingBotSummaryLast, day)
	api := b.client()
	if api == nil {
		return
	}
	text, err := b.summaryText(ctx)
	if err != nil {
		b.log.Error("daily summary", "err", err)
		return
	}
	admins, _ := b.svc.BotAdmins(ctx)
	for _, a := range admins {
		_, _ = api.send(ctx, a.ID, text, Keyboard{row(btn("☰ Меню", "menu"), btn("🛰 Ноды", "nodes"))})
	}
}

// summaryText is the last 24 hours in a few lines: traffic, users, who expires soon, nodes.
func (b *Bot) summaryText(ctx context.Context) (string, error) {
	st, err := b.svc.Statistics(ctx, "24h")
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("☀️ <b>Сводка за сутки</b>\n\n")
	delta := ""
	if st.PrevTotal > 0 {
		d := float64(st.Total-st.PrevTotal) / float64(st.PrevTotal) * 100
		delta = fmt.Sprintf(" (%+.0f%% к прошлым суткам)", d)
	}
	fmt.Fprintf(&sb, "📶 Трафик: <b>%s</b>%s\n", gb(st.Total), delta)
	fmt.Fprintf(&sb, "👥 Активных: <b>%d</b> · пик онлайна %d\n", st.ActiveUsers, st.PeakOnline)
	if len(st.TopUsers) > 0 {
		var top []string
		for i, u := range st.TopUsers {
			if i == 3 {
				break
			}
			top = append(top, fmt.Sprintf("%s %s", esc(u.Username), gb(u.Bytes)))
		}
		sb.WriteString("🏆 Больше всех: " + strings.Join(top, ", ") + "\n")
	}

	// Who runs out in the next three days, and who already ran out.
	users, err := b.svc.Users(ctx, store.UserFilter{})
	if err != nil {
		return "", err
	}
	now := b.cfg.Now().Unix()
	var soon []string
	limited := 0
	sort.Slice(users, func(i, j int) bool { return exp(users[i]) < exp(users[j]) })
	for _, u := range users {
		if u.Status == store.StatusLimited {
			limited++
		}
		if u.Status == store.StatusActive && u.ExpireAt != nil && *u.ExpireAt-now < 3*86400 {
			soon = append(soon, fmt.Sprintf("%s (%s)", esc(u.Username), b.fmtTime(ctx, *u.ExpireAt, "02.01")))
		}
	}
	if len(soon) > 0 {
		if len(soon) > 8 {
			soon = append(soon[:8], fmt.Sprintf("и ещё %d", len(soon)-8))
		}
		sb.WriteString("⏳ Заканчиваются за 3 дня: " + strings.Join(soon, ", ") + "\n")
	}
	if limited > 0 {
		fmt.Fprintf(&sb, "🚫 Исчерпали трафик: %d\n", limited)
	}

	statuses, err := b.svc.NodeStatuses(ctx, b.cfg.Connected)
	if err != nil {
		return "", err
	}
	up, all := 0, 0
	var bad []string
	for _, ns := range statuses {
		if !ns.Node.Enabled {
			continue
		}
		all++
		if ns.State() == service.NodeInSync {
			up++
		} else {
			bad = append(bad, xrayconf.CountryFlag(ns.Node.Country)+" "+esc(ns.Node.Name))
		}
	}
	fmt.Fprintf(&sb, "\n🛰 Ноды: %d из %d в порядке", up, all)
	if len(bad) > 0 {
		sb.WriteString(" · проблемы: " + strings.Join(bad, ", "))
	}
	return sb.String(), nil
}

func exp(u *store.User) int64 {
	if u.ExpireAt == nil {
		return 1 << 62
	}
	return *u.ExpireAt
}
