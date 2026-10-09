package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

const pageSize = 8

var statusIcon = map[string]string{
	store.StatusActive: "🟢", store.StatusLimited: "🟣", store.StatusExpired: "🟠", store.StatusDisabled: "⚪️",
}

var statusText = map[string]string{
	store.StatusActive: "активен", store.StatusLimited: "трафик исчерпан", store.StatusExpired: "истёк", store.StatusDisabled: "отключён",
}

var resetText = map[string]string{"no": "не сбрасывается", "day": "сброс каждый день", "week": "сброс каждую неделю", "month": "сброс каждый месяц"}

func gb(b int64) string {
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.2f ТБ", float64(b)/(1<<40))
	case b >= 1<<30:
		return fmt.Sprintf("%.1f ГБ", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f МБ", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%d КБ", b>>10)
}

func (b *Bot) loc(ctx context.Context) *time.Location { return b.svc.BotLocation(ctx) }

func (b *Bot) fmtTime(ctx context.Context, ts int64, layout string) string {
	return time.Unix(ts, 0).In(b.loc(ctx)).Format(layout)
}

func ago(now time.Time, ts *int64) string {
	if ts == nil {
		return "никогда"
	}
	d := now.Sub(time.Unix(*ts, 0))
	switch {
	case d < 3*time.Minute:
		return "сейчас"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	}
	return fmt.Sprintf("%d дн назад", int(d.Hours()/24))
}

func duration(d time.Duration) string {
	m := int(d.Round(time.Minute).Minutes())
	switch {
	case m < 1:
		return "меньше минуты"
	case m < 60:
		return fmt.Sprintf("%d мин", m)
	case m < 48*60:
		return fmt.Sprintf("%d ч %d мин", m/60, m%60)
	}
	return fmt.Sprintf("%d дн %d ч", m/(24*60), m/60%24)
}

// ---- messages ----

func (b *Bot) onMessage(ctx context.Context, api *client, m *Message) {
	text := strings.TrimSpace(m.Text)
	chat := m.Chat.ID
	if strings.HasPrefix(text, "/") {
		b.mu.Lock()
		delete(b.pending, chat)
		b.mu.Unlock()
		cmd, arg, _ := strings.Cut(text, " ")
		cmd, _, _ = strings.Cut(cmd, "@")
		arg = strings.TrimSpace(arg)
		switch cmd {
		case "/start", "/menu":
			b.sendMenu(ctx, api, chat)
		case "/users":
			b.showUsers(ctx, api, chat, 0, 0, "")
		case "/find":
			if arg == "" {
				b.ask(ctx, api, chat, "search", "🔎 Кого найти? Напишите часть имени или заметки.")
				return
			}
			b.showUsers(ctx, api, chat, 0, 0, arg)
		case "/new":
			if arg == "" {
				b.ask(ctx, api, chat, "newuser", "➕ Имя нового пользователя? Латиница, цифры и _ . - @, например <code>vasya</code>.")
				return
			}
			b.newUser(ctx, api, chat, 0, arg)
		case "/nodes":
			b.showNodes(ctx, api, chat, 0)
		case "/stats":
			b.sendMenu(ctx, api, chat)
		case "/login":
			b.sendLoginLink(ctx, api, m.From.ID, chat)
		case "/backup":
			b.backupNow(ctx, api, chat)
		case "/sessions":
			b.showSessions(ctx, api, chat, 0, "")
		case "/firewall":
			b.showFirewall(ctx, api, chat, 0, "")
		case "/summary":
			text, err := b.summaryText(ctx)
			if err != nil {
				text = "Не получилось: " + esc(errText(err))
			}
			_, _ = api.send(ctx, chat, text, Keyboard{row(btn("☰ Меню", "menu"))})
		default:
			b.help(ctx, api, chat)
		}
		return
	}
	b.mu.Lock()
	p, ok := b.pending[chat]
	delete(b.pending, chat)
	b.mu.Unlock()
	switch {
	case ok && p.kind == "newuser":
		b.newUser(ctx, api, chat, p.msg, text)
	case ok && p.kind == "search":
		b.showUsers(ctx, api, chat, p.msg, 0, text)
	default:
		// Plain text is a search: the quickest way to open someone's card.
		b.showUsers(ctx, api, chat, 0, 0, text)
	}
}

func (b *Bot) ask(ctx context.Context, api *client, chat int64, kind, prompt string) {
	m, err := api.send(ctx, chat, prompt, Keyboard{row(btn("Отмена", "menu"))})
	if err != nil {
		return
	}
	b.mu.Lock()
	b.pending[chat] = pendingInput{kind: kind, msg: m.MessageID}
	b.mu.Unlock()
}

func (b *Bot) help(ctx context.Context, api *client, chat int64) {
	_, _ = api.send(ctx, chat, `<b>Что умеет бот</b>
/menu — сводка и меню
/users — пользователи; просто напишите имя, чтобы найти
/new vasya — новый пользователь по шаблону
/nodes — ноды и их состояние
/login — ссылка входа в веб-панель (1 минута, один раз)
/sessions — кто вошёл в веб-панель, завершить сессию
/summary — сводка за сутки
/firewall — файрвол: кто заблокирован, разблокировать, выключить
/backup — бэкап сейчас
Кнопка «Панель» у поля ввода открывает панель прямо в Telegram.

Сам бот присылает: бэкап каждый вечер, сводку каждое утро, сообщение о каждом входе в панель, о недоступной ноде (оно обновляется, пока нода не вернётся) и об ошибке применения конфигурации.`, Keyboard{row(btn("☰ Меню", "menu"))})
}

// ---- callbacks ----

// show sends a new message or edits the one the button was pressed on.
func (b *Bot) show(ctx context.Context, api *client, chat, msg int64, text string, kb Keyboard) {
	if msg != 0 {
		if err := api.edit(ctx, chat, msg, text, kb); err == nil {
			return
		}
	}
	_, _ = api.send(ctx, chat, text, kb)
}

func (b *Bot) onCallback(ctx context.Context, api *client, q *CallbackQuery) {
	chat, msg := q.Message.Chat.ID, q.Message.MessageID
	parts := strings.Split(q.Data, ":")
	arg := func(i int) int64 {
		if i >= len(parts) {
			return 0
		}
		n, _ := strconv.ParseInt(parts[i], 10, 64)
		return n
	}
	actor := actorOf(q.From.ID)
	toast := ""
	defer func() { api.answer(ctx, q.ID, toast, false) }()
	b.mu.Lock()
	delete(b.pending, chat)
	b.mu.Unlock()

	switch parts[0] {
	case "menu":
		b.showMenu(ctx, api, chat, msg)
	case "ul": // list: ul:<page>
		b.showUsers(ctx, api, chat, msg, int(arg(1)), "")
	case "find":
		b.ask(ctx, api, chat, "search", "🔎 Кого найти? Напишите часть имени или заметки.")
	case "nu":
		b.ask(ctx, api, chat, "newuser", "➕ Имя нового пользователя? Латиница, цифры и _ . - @, например <code>vasya</code>.")
	case "nt": // create with template: nt:<template id>:<name>
		name := strings.Join(parts[2:], ":")
		b.createUser(ctx, api, chat, msg, name, arg(1))
	case "u":
		b.showUser(ctx, api, chat, msg, arg(1), "")
	case "ux": // extend: ux:<id>:<months>
		if _, err := b.svc.ExtendUser(ctx, actor, arg(1), int(arg(2)), 0); err != nil {
			toast = errText(err)
		} else {
			toast = fmt.Sprintf("Продлено на %d мес", arg(2))
		}
		b.showUser(ctx, api, chat, msg, arg(1), "")
	case "ue": // enable/disable: ue:<id>:<0|1>
		if _, err := b.svc.SetUserEnabled(ctx, actor, arg(1), arg(2) == 1); err != nil {
			toast = errText(err)
		}
		b.showUser(ctx, api, chat, msg, arg(1), "")
	case "ur":
		if _, err := b.svc.ResetUserTraffic(ctx, actor, arg(1)); err != nil {
			toast = errText(err)
		} else {
			toast = "Трафик сброшен"
		}
		b.showUser(ctx, api, chat, msg, arg(1), "")
	case "lim": // limit menu
		b.showLimits(ctx, api, chat, msg, arg(1))
	case "ul2": // set limit: ul2:<id>:<GiB, 0 = unlimited>
		lim := arg(2) << 30
		if _, err := b.svc.SetUserTrafficLimit(ctx, actor, arg(1), &lim); err != nil {
			toast = errText(err)
		}
		b.showUser(ctx, api, chat, msg, arg(1), "")
	case "dev":
		b.showDevices(ctx, api, chat, msg, arg(1))
	case "dd": // delete device: dd:<user>:<device>
		if err := b.svc.DeleteDevice(ctx, actor, arg(1), arg(2)); err != nil {
			toast = errText(err)
		} else {
			toast = "Устройство отвязано"
		}
		b.showDevices(ctx, api, chat, msg, arg(1))
	case "rl": // new link: confirm first
		b.confirm(ctx, api, chat, msg, "🔗 Выпустить новую ссылку подписки? Старая перестанет работать, пользователю нужно будет добавить новую.", "rl!:"+parts[1], "u:"+parts[1])
	case "rl!":
		if _, err := b.svc.ReissueUser(ctx, actor, arg(1), true, false); err != nil {
			toast = errText(err)
		} else {
			toast = "Новая ссылка выпущена"
		}
		b.showUser(ctx, api, chat, msg, arg(1), "")
	case "qr":
		b.sendQR(ctx, api, chat, arg(1))
	case "del":
		u, err := b.svc.User(ctx, arg(1))
		if err != nil {
			toast = errText(err)
			return
		}
		b.confirm(ctx, api, chat, msg, "🗑 Удалить <b>"+esc(u.Username)+"</b>? Это нельзя отменить.", "del!:"+parts[1], "u:"+parts[1])
	case "del!":
		if err := b.svc.DeleteUser(ctx, actor, arg(1)); err != nil {
			toast = errText(err)
			return
		}
		toast = "Пользователь удалён"
		b.showUsers(ctx, api, chat, msg, 0, "")
	case "nodes":
		b.showNodes(ctx, api, chat, msg)
	case "login":
		b.sendLoginLink(ctx, api, q.From.ID, chat)
	case "ss":
		b.showSessions(ctx, api, chat, msg, "")
	case "fw":
		b.showFirewall(ctx, api, chat, msg, "")
	case "fwu", "fwoff", "fwoff!", "fwon":
		toast = b.firewallAction(ctx, api, chat, msg, parts[0])
	case "sx": // end one session: sx:<id>
		n, err := b.svc.EndWebSessions(ctx, actor, parts[1], "")
		switch {
		case err != nil:
			toast = errText(err)
		case n == 0:
			toast = "Сессия уже завершена"
		default:
			toast = "Сессия завершена"
		}
		b.showSessions(ctx, api, chat, msg, "")
	case "sxa":
		b.confirm(ctx, api, chat, msg, "⛔ Завершить все сессии веб-панели? Войти снова можно будет кнопкой «Войти в панель».", "sxa!", "ss")
	case "sxa!":
		n, err := b.svc.EndWebSessions(ctx, actor, "", "")
		if err != nil {
			toast = errText(err)
		}
		b.showSessions(ctx, api, chat, msg, fmt.Sprintf("Завершено сессий: %d", n))
	case "bk":
		toast = "Готовлю бэкап…"
		go b.backupNow(context.WithoutCancel(ctx), api, chat)
	}
}

func errText(err error) string {
	s := err.Error()
	s = strings.TrimPrefix(s, service.ErrInvalid.Error()+": ")
	if errors.Is(err, service.ErrNotFound) {
		return "Не найдено — возможно, уже удалено"
	}
	if len(s) > 180 {
		s = s[:180]
	}
	return s
}

func (b *Bot) confirm(ctx context.Context, api *client, chat, msg int64, text, yes, no string) {
	b.show(ctx, api, chat, msg, text, Keyboard{row(btn("Да", yes), btn("Нет", no))})
}

// ---- menu ----

func (b *Bot) menuText(ctx context.Context) string {
	o, err := b.svc.Overview(ctx)
	if err != nil {
		return "vynel"
	}
	total := 0
	for _, n := range o.UsersByStatus {
		total += n
	}
	nodes, _ := b.svc.NodeStatuses(ctx, b.cfg.Connected)
	up, all := 0, 0
	for _, ns := range nodes {
		if !ns.Node.Enabled {
			continue
		}
		all++
		if ns.State() == service.NodeInSync {
			up++
		}
	}
	nodeLine := fmt.Sprintf("🛰 Ноды: %d из %d работают", up, all)
	if up < all {
		nodeLine = "⚠️ " + nodeLine[len("🛰 "):]
	}
	return fmt.Sprintf("<b>vynel</b>\n\n👥 Пользователи: %d (%d активных) · онлайн %d\n📶 Сегодня %s · за 30 дней %s\n%s",
		total, o.UsersByStatus[store.StatusActive], o.OnlineNow, gb(o.TodayBytes), gb(o.MonthBytes), nodeLine)
}

var menuKeyboard = Keyboard{
	row(btn("👥 Пользователи", "ul:0"), btn("➕ Новый", "nu")),
	row(btn("🔎 Найти", "find"), btn("🛰 Ноды", "nodes")),
	row(btn("🔑 Войти в панель", "login"), btn("🔐 Сессии", "ss")),
	row(btn("💾 Бэкап сейчас", "bk"), btn("🧱 Файрвол", "fw")),
	row(btn("🔄 Обновить", "menu")),
}

func (b *Bot) sendMenu(ctx context.Context, api *client, chat int64) {
	_, _ = api.send(ctx, chat, b.menuText(ctx), menuKeyboard)
}

func (b *Bot) showMenu(ctx context.Context, api *client, chat, msg int64) {
	b.show(ctx, api, chat, msg, b.menuText(ctx), menuKeyboard)
}

// ---- users ----

func (b *Bot) showUsers(ctx context.Context, api *client, chat, msg int64, page int, search string) {
	all, err := b.svc.Users(ctx, store.UserFilter{Search: search})
	if err != nil {
		b.show(ctx, api, chat, msg, "Ошибка: "+esc(err.Error()), Keyboard{row(btn("☰ Меню", "menu"))})
		return
	}
	if search != "" && len(all) == 1 {
		b.showUser(ctx, api, chat, msg, all[0].ID, "")
		return
	}
	pages := (len(all) + pageSize - 1) / pageSize
	if page >= pages {
		page = max(0, pages-1)
	}
	title := fmt.Sprintf("👥 <b>Пользователи</b>: %d", len(all))
	if search != "" {
		title = fmt.Sprintf("🔎 «%s»: найдено %d", esc(search), len(all))
	}
	if pages > 1 {
		title += fmt.Sprintf(" · стр. %d/%d", page+1, pages)
	}
	if len(all) == 0 {
		title += "\n\nНикого нет."
	}
	var kb Keyboard
	now := b.cfg.Now()
	for _, u := range all[min(page*pageSize, len(all)):min((page+1)*pageSize, len(all))] {
		label := statusIcon[u.Status] + " " + u.Username
		if u.ExpireAt != nil {
			d := int(time.Unix(*u.ExpireAt, 0).Sub(now).Hours() / 24)
			if d >= 0 {
				label += fmt.Sprintf(" · %d дн", d)
			}
		}
		kb = append(kb, row(btn(label, "u:"+strconv.FormatInt(u.ID, 10))))
	}
	var nav []Button
	if search == "" && page > 0 {
		nav = append(nav, btn("‹ Назад", "ul:"+strconv.Itoa(page-1)))
	}
	if search == "" && page+1 < pages {
		nav = append(nav, btn("Дальше ›", "ul:"+strconv.Itoa(page+1)))
	}
	if len(nav) > 0 {
		kb = append(kb, nav)
	}
	kb = append(kb, row(btn("➕ Новый", "nu"), btn("🔎 Найти", "find"), btn("☰ Меню", "menu")))
	b.show(ctx, api, chat, msg, title, kb)
}

func (b *Bot) userText(ctx context.Context, u *store.User) string {
	now := b.cfg.Now()
	var sb strings.Builder
	fmt.Fprintf(&sb, "👤 <b>%s</b> — %s %s\n", esc(u.Username), statusIcon[u.Status], statusText[u.Status])
	if u.ExpireAt != nil {
		d := time.Unix(*u.ExpireAt, 0).Sub(now)
		left := "истёк"
		if d > 0 {
			left = fmt.Sprintf("ещё %d дн", int(d.Hours()/24))
		}
		fmt.Fprintf(&sb, "📅 до %s (%s)\n", b.fmtTime(ctx, *u.ExpireAt, "02.01.2006"), left)
	} else {
		sb.WriteString("📅 бессрочно\n")
	}
	limit := "∞"
	if u.TrafficLimitBytes != nil {
		limit = gb(*u.TrafficLimitBytes)
	}
	fmt.Fprintf(&sb, "📶 %s из %s · %s\n", gb(u.TrafficUsedBytes), limit, resetText[u.ResetStrategy])
	devs, _ := b.svc.Devices(ctx, u.ID)
	lim := "по умолчанию"
	if u.HWIDLimit != nil {
		lim = strconv.FormatInt(*u.HWIDLimit, 10)
		if *u.HWIDLimit == 0 {
			lim = "∞"
		}
	} else if def, _ := b.svc.Setting(ctx, service.SettingHWIDLimit, "3"); def != "" {
		lim = def
	}
	if u.HWIDOff {
		sb.WriteString("📱 HWID не проверяется\n")
	} else {
		fmt.Fprintf(&sb, "📱 устройства: %d из %s\n", len(devs), lim)
	}
	if gs, _ := b.svc.UserGroupIDs(ctx, u.ID); len(gs) > 0 {
		names := []string{}
		all, _ := b.svc.Groups(ctx)
		for _, g := range all {
			for _, id := range gs {
				if g.ID == id {
					names = append(names, g.Name)
				}
			}
		}
		fmt.Fprintf(&sb, "👥 %s\n", esc(strings.Join(names, ", ")))
	} else {
		sb.WriteString("👥 без групп — серверов в подписке не будет\n")
	}
	fmt.Fprintf(&sb, "🕒 онлайн: %s\n", ago(now, u.OnlineAt))
	if u.Note != "" {
		fmt.Fprintf(&sb, "📝 %s\n", esc(u.Note))
	}
	if url, err := b.svc.SubscriptionURL(ctx, u); err == nil {
		fmt.Fprintf(&sb, "\n🔗 <code>%s</code>", esc(url))
	}
	return sb.String()
}

func (b *Bot) showUser(ctx context.Context, api *client, chat, msg, id int64, notice string) {
	u, err := b.svc.User(ctx, id)
	if err != nil {
		b.show(ctx, api, chat, msg, "Пользователь не найден — возможно, удалён.", Keyboard{row(btn("👥 К списку", "ul:0"))})
		return
	}
	s := strconv.FormatInt(id, 10)
	toggle := btn("⏸ Отключить", "ue:"+s+":0")
	if u.Disabled {
		toggle = btn("▶️ Включить", "ue:"+s+":1")
	}
	text := b.userText(ctx, u)
	if notice != "" {
		text = notice + "\n\n" + text
	}
	b.show(ctx, api, chat, msg, text, Keyboard{
		row(btn("+1 мес", "ux:"+s+":1"), btn("+3 мес", "ux:"+s+":3"), btn("+6 мес", "ux:"+s+":6"), btn("+12 мес", "ux:"+s+":12")),
		row(toggle, btn("🔄 Сбросить трафик", "ur:"+s)),
		row(btn("📱 Устройства", "dev:"+s), btn("📶 Лимит", "lim:"+s)),
		row(btn("🖼 QR-код", "qr:"+s), btn("🔗 Новая ссылка", "rl:"+s)),
		row(btn("🗑 Удалить", "del:"+s), btn("👥 К списку", "ul:0")),
	})
}

func (b *Bot) showLimits(ctx context.Context, api *client, chat, msg, id int64) {
	s := strconv.FormatInt(id, 10)
	var opts []Button
	for _, g := range []int64{50, 100, 200, 500, 1000} {
		opts = append(opts, btn(fmt.Sprintf("%d ГБ", g), fmt.Sprintf("ul2:%s:%d", s, g)))
	}
	b.show(ctx, api, chat, msg, "📶 Лимит трафика за период:", Keyboard{opts[:3], opts[3:], row(btn("∞ без лимита", "ul2:"+s+":0"), btn("« Назад", "u:"+s))})
}

func (b *Bot) showDevices(ctx context.Context, api *client, chat, msg, id int64) {
	u, err := b.svc.User(ctx, id)
	if err != nil {
		b.show(ctx, api, chat, msg, "Пользователь не найден.", Keyboard{row(btn("👥 К списку", "ul:0"))})
		return
	}
	ds, _ := b.svc.Devices(ctx, id)
	s := strconv.FormatInt(id, 10)
	text := fmt.Sprintf("📱 <b>Устройства %s</b>: %d", esc(u.Username), len(ds))
	if len(ds) == 0 {
		text += "\n\nПоявятся после первого обновления подписки в приложении."
	}
	var kb Keyboard
	for _, d := range ds {
		name := strings.TrimSpace(d.Model + " " + d.Platform + " " + d.OSVersion)
		if name == "" {
			name = "устройство"
		}
		last := d.LastSeen
		text += fmt.Sprintf("\n• %s — %s", esc(name), ago(b.cfg.Now(), &last))
		kb = append(kb, row(btn("✖️ Отвязать "+truncate(name, 30), fmt.Sprintf("dd:%s:%d", s, d.ID))))
	}
	kb = append(kb, row(btn("« Назад", "u:"+s)))
	b.show(ctx, api, chat, msg, text, kb)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (b *Bot) sendQR(ctx context.Context, api *client, chat, id int64) {
	u, err := b.svc.User(ctx, id)
	if err != nil {
		return
	}
	url, err := b.svc.SubscriptionURL(ctx, u)
	if err != nil {
		_, _ = api.send(ctx, chat, "Нет ссылки: "+esc(err.Error()), nil)
		return
	}
	png, err := qrcode.Encode(url, qrcode.Medium, 512)
	if err != nil {
		return
	}
	_ = api.sendPhoto(ctx, chat, "qr.png", png, "Подписка <b>"+esc(u.Username)+"</b>\n<code>"+esc(url)+"</code>")
}

// newUser starts creating a user: with one template it is created at once, otherwise the
// template is chosen with buttons.
func (b *Bot) newUser(ctx context.Context, api *client, chat, msg int64, name string) {
	name = strings.TrimSpace(name)
	tpls, err := b.svc.UserTemplates(ctx)
	if err != nil || len(tpls) <= 1 || len(name) > 50 {
		b.createUser(ctx, api, chat, msg, name, 0)
		return
	}
	var kb Keyboard
	for _, t := range tpls {
		label := t.Name
		if t.IsDefault {
			label += " ✓"
		}
		kb = append(kb, row(btn(label, fmt.Sprintf("nt:%d:%s", t.ID, name))))
	}
	kb = append(kb, row(btn("Отмена", "menu")))
	b.show(ctx, api, chat, msg, "➕ <b>"+esc(name)+"</b>: выберите шаблон", kb)
}

func (b *Bot) createUser(ctx context.Context, api *client, chat, msg int64, name string, tplID int64) {
	u, err := b.svc.CreateUser(ctx, actorOf(chat), service.CreateUserInput{Username: name, TemplateID: tplID})
	if err != nil {
		text := "Не получилось: " + esc(errText(err))
		if errors.Is(err, service.ErrConflict) {
			text = "Пользователь <b>" + esc(name) + "</b> уже есть."
		}
		b.show(ctx, api, chat, msg, text, Keyboard{row(btn("➕ Другое имя", "nu"), btn("☰ Меню", "menu"))})
		return
	}
	b.showUser(ctx, api, chat, msg, u.ID, "✅ Создан. Отправьте пользователю ссылку ниже или QR-код.")
}

// ---- nodes ----

var nodeIcon = map[string]string{
	service.NodeInSync: "🟢", service.NodeSyncing: "🟡", service.NodeOffline: "🔴", service.NodePending: "⏳", service.NodeDisabled: "⚪️",
}

var nodeText = map[string]string{
	service.NodeInSync: "работает", service.NodeSyncing: "применяет настройки", service.NodeOffline: "нет связи",
	service.NodePending: "ждёт подключения", service.NodeDisabled: "выключена",
}

func (b *Bot) showNodes(ctx context.Context, api *client, chat, msg int64) {
	ns, err := b.svc.NodeStatuses(ctx, b.cfg.Connected)
	if err != nil {
		b.show(ctx, api, chat, msg, "Ошибка: "+esc(err.Error()), nil)
		return
	}
	var sb strings.Builder
	sb.WriteString("🛰 <b>Ноды</b>\n")
	now := b.cfg.Now()
	for _, s := range ns {
		n := s.Node
		st := s.State()
		fmt.Fprintf(&sb, "\n%s <b>%s %s</b> (%s) — %s", nodeIcon[st], xrayconf.CountryFlag(n.Country), esc(n.Name), n.Code, nodeText[st])
		if m := s.Metrics; m != nil && st != service.NodeOffline {
			mem := 0.0
			if m.MemTotal > 0 {
				mem = 100 * float64(m.MemUsed) / float64(m.MemTotal)
			}
			fmt.Fprintf(&sb, "\n    онлайн %d · CPU %.0f%% · RAM %.0f%% · сегодня %s", m.Online, m.CPU, mem, gb(s.TodayBytes))
		}
		if st == service.NodeOffline {
			fmt.Fprintf(&sb, "\n    последняя связь: %s", ago(now, n.LastSeenAt))
		}
		if len(n.Warnings) > 0 || n.LastError != "" {
			fmt.Fprintf(&sb, "\n    ⚠️ %s", esc(truncate(strings.TrimSpace(n.LastError+" "+strings.Join(n.Warnings, "; ")), 160)))
		}
	}
	if len(ns) == 0 {
		sb.WriteString("\nНод нет.")
	}
	b.show(ctx, api, chat, msg, sb.String(), Keyboard{row(btn("🔄 Обновить", "nodes"), btn("☰ Меню", "menu"))})
}

// ---- web login ----

// sendLoginLink sends a one-time link into the web panel and blanks it out when it expires.
func (b *Bot) sendLoginLink(ctx context.Context, api *client, from, chat int64) {
	link, exp, err := b.svc.LoginLink(ctx, actorOf(from))
	if err != nil {
		_, _ = api.send(ctx, chat, "Не получилось: "+esc(errText(err)), nil)
		return
	}
	text := fmt.Sprintf("🔑 <b>Вход в панель</b>\nСсылка работает до %s и только один раз.", b.fmtTime(ctx, exp.Unix(), "15:04:05"))
	kb := Keyboard{{{Text: "Открыть в браузере", URL: link}}}
	if base, err := b.svc.WebURL(ctx); err == nil && base != "" {
		kb = append(kb, row(Button{Text: "📱 Открыть в Telegram", WebApp: &WebApp{URL: base}}))
	}
	m, err := api.send(ctx, chat, text, kb)
	if err != nil {
		return
	}
	b.log.Info("web login link issued via telegram", "admin", from)
	go func() {
		wait := time.Until(exp) + time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		_ = api.edit(context.WithoutCancel(ctx), chat, m.MessageID, "🔑 Ссылка входа истекла. Новая: /login, или кнопка «Панель» у поля ввода", nil)
	}()
}
