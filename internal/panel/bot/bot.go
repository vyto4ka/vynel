package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// Config configures the bot.
type Config struct {
	Service   *service.Service
	DataDir   string           // panel data directory (backups)
	Version   string           // goes into backups
	Connected func(int64) bool // live node sessions (reconciler)
	Log       *slog.Logger
	API       string        // Bot API endpoint; tests use a fake one
	Poll      time.Duration // long poll timeout (default 25 s)
	Tick      time.Duration // alert/backup check interval (default 20 s)
	Now       func() time.Time
}

// Status is what the web panel shows about the bot.
type Status struct {
	Running  bool   `json:"running"`
	Username string `json:"username,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Bot is the Telegram bot.
type Bot struct {
	cfg Config
	svc *service.Service
	log *slog.Logger

	mu       sync.Mutex
	status   Status
	api      *client
	pending  map[int64]pendingInput // chat -> what the next text message means
	menuSet  map[int64]string       // chat -> Mini App URL its menu button opens
	failures int                    // wrong bind codes since the last good one

	alerts  alertState
	backup  backupState
	watchSt watchState
}

type pendingInput struct {
	kind string // "newuser" | "search"
	msg  int64  // the prompt message, edited when done
}

// New creates the bot. It does nothing until Run.
func New(cfg Config) *Bot {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.API == "" {
		cfg.API = DefaultAPI
	}
	if cfg.Poll == 0 {
		cfg.Poll = 25 * time.Second
	}
	if cfg.Tick == 0 {
		cfg.Tick = 20 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Bot{cfg: cfg, svc: cfg.Service, log: cfg.Log, pending: map[int64]pendingInput{}}
}

// Status reports whether the bot is connected to Telegram.
func (b *Bot) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status
}

func (b *Bot) setStatus(s Status) {
	b.mu.Lock()
	b.status = s
	b.mu.Unlock()
}

func (b *Bot) client() *client {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.api
}

// Run runs the bot until ctx ends. It follows the token setting: no token = idle, a new token =
// restart with it.
func (b *Bot) Run(ctx context.Context) error {
	start := func(tok string) func() {
		rctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			b.runWith(rctx, tok)
		}()
		return func() { cancel(); <-done }
	}
	cur, stop := "", func() {}
	defer func() { stop() }()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		if tok := b.svc.BotToken(ctx); tok != cur {
			stop()
			stop, cur = func() {}, tok
			b.setStatus(Status{})
			if tok != "" {
				stop = start(tok)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// runWith runs one bot session with a token until ctx ends, retrying on errors.
func (b *Bot) runWith(ctx context.Context, token string) {
	api := newClient(b.cfg.API, token)
	b.mu.Lock()
	b.api = api
	b.mu.Unlock()
	var me *User
	for {
		var err error
		if me, err = api.getMe(ctx); err == nil {
			break
		}
		b.setStatus(Status{Error: err.Error()})
		b.log.Warn("telegram bot: cannot start", "err", err)
		if !sleep(ctx, 30*time.Second) {
			return
		}
	}
	b.setStatus(Status{Running: true, Username: me.Username})
	b.log.Info("telegram bot started", "username", me.Username)
	_ = api.setCommands(ctx, [][2]string{
		{"menu", "Главное меню"}, {"users", "Пользователи"}, {"new", "Новый пользователь: /new имя"},
		{"find", "Найти пользователя: /find имя"}, {"nodes", "Ноды"}, {"login", "Ссылка входа в веб-панель"},
		{"sessions", "Кто вошёл в веб-панель"}, {"summary", "Сводка за сутки"},
		{"backup", "Бэкап сейчас"}, {"help", "Что умеет бот"},
	})
	b.loadAlerts(ctx)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.watch(ctx)
	}()
	defer wg.Wait()

	var offset int64
	for ctx.Err() == nil {
		ups, err := api.getUpdates(ctx, offset, int(b.cfg.Poll/time.Second))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var ae *apiError
			if errors.As(err, &ae) && ae.Code == 401 {
				b.setStatus(Status{Error: "Telegram не принял токен (401): проверьте его в @BotFather"})
				<-ctx.Done()
				return
			}
			b.log.Warn("telegram getUpdates", "err", err)
			if !sleep(ctx, 5*time.Second) {
				return
			}
			continue
		}
		b.setStatus(Status{Running: true, Username: me.Username})
		for _, u := range ups {
			offset = u.UpdateID + 1
			b.handle(ctx, api, u)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// watch runs the periodic jobs (node alerts, apply errors, the panel address, the nightly
// backup, the daily summary) and passes sign-ins on as they happen.
func (b *Bot) watch(ctx context.Context) {
	t := time.NewTicker(b.cfg.Tick)
	defer t.Stop()
	for {
		b.checkNodes(ctx)
		b.checkApply(ctx)
		b.checkWebURL(ctx)
		b.maybeBackup(ctx)
		b.maybeSummary(ctx)
		b.setMenuButtons(ctx)
		if !b.nextTick(ctx, t.C) {
			return
		}
	}
}

// nextTick waits for the next tick, passing sign-ins on to the admins meanwhile.
func (b *Bot) nextTick(ctx context.Context, tick <-chan time.Time) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case n := <-b.svc.Notices():
			if n.Kind == "login" && n.Session != nil {
				b.onLogin(ctx, n.Session)
			}
		case <-tick:
			return true
		}
	}
}

// setMenuButtons gives every admin's chat a «Панель» button that opens it as a Mini App, and
// keeps it in step with the panel address.
func (b *Bot) setMenuButtons(ctx context.Context) {
	api := b.client()
	url, err := b.svc.WebURL(ctx)
	if api == nil || err != nil || url == "" {
		return
	}
	admins, _ := b.svc.BotAdmins(ctx)
	b.mu.Lock()
	if b.menuSet == nil {
		b.menuSet = map[int64]string{}
	}
	var todo []int64
	for _, a := range admins {
		if b.menuSet[a.ID] != url {
			todo = append(todo, a.ID)
		}
	}
	b.mu.Unlock()
	for _, id := range todo {
		if err := api.setMenuButton(ctx, id, "Панель", url); err == nil {
			b.mu.Lock()
			b.menuSet[id] = url
			b.mu.Unlock()
		}
	}
}

func (b *Bot) handle(ctx context.Context, api *client, u Update) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("telegram bot: handler panic", "panic", r)
		}
	}()
	switch {
	case u.CallbackQuery != nil:
		q := u.CallbackQuery
		if q.Message == nil || q.Message.Chat.Type != "private" || !b.svc.IsBotAdmin(ctx, q.From.ID) {
			api.answer(ctx, q.ID, "", false)
			return
		}
		b.onCallback(ctx, api, q)
	case u.Message != nil && u.Message.From != nil:
		m := u.Message
		if m.Chat.Type != "private" {
			return // groups are ignored: the bot is for admins in private chats only
		}
		if !b.svc.IsBotAdmin(ctx, m.From.ID) {
			b.onStranger(ctx, api, m)
			return
		}
		b.onMessage(ctx, api, m)
	}
}

// onStranger handles a message from someone who is not an admin: only "/start CODE" binds;
// everything else is ignored silently, so the bot does not reveal what it is.
func (b *Bot) onStranger(ctx context.Context, api *client, m *Message) {
	code, ok := strings.CutPrefix(strings.TrimSpace(m.Text), "/start")
	code = strings.TrimSpace(code)
	if !ok || code == "" {
		return
	}
	b.mu.Lock()
	tooMany := b.failures >= 10
	b.mu.Unlock()
	if tooMany {
		return
	}
	name := strings.TrimSpace(m.From.FirstName + " " + m.From.LastName)
	bound, err := b.svc.BindBotAdmin(ctx, code, service.BotAdmin{ID: m.From.ID, Name: name, Username: m.From.Username})
	if err != nil {
		b.log.Error("telegram bind", "err", err)
		return
	}
	if !bound {
		b.mu.Lock()
		b.failures++
		if b.failures >= 10 {
			// Someone is guessing: burn the code, a new one is needed.
			_, _ = b.svc.NewBindCode(ctx, service.Actor{Kind: "bot"})
			b.log.Warn("telegram bot: 10 wrong bind codes, the code was replaced; create a new one")
		}
		b.mu.Unlock()
		return
	}
	b.mu.Lock()
	b.failures = 0
	b.mu.Unlock()
	b.log.Info("telegram admin bound", "id", m.From.ID, "username", m.From.Username)
	_, _ = api.send(ctx, m.Chat.ID, "✅ Готово, <b>"+esc(name)+"</b>: вы администратор панели.\nУведомления о нодах и ночные бэкапы будут приходить сюда.", nil)
	b.sendMenu(ctx, api, m.Chat.ID)
}

func esc(s string) string { return html.EscapeString(s) }

func actorOf(id int64) service.Actor { return service.Actor{Kind: "bot", ID: fmt.Sprint(id)} }
