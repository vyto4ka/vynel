package service

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Subscription look and response headers (docs/USER_GUIDE.md §5). Stored as JSON settings so
// they are edited in the web panel and need no migrations.
const (
	SettingSubHeaders     = "sub.headers"      // JSON []SubHeader; empty = DefaultSubHeaders
	SettingSubPage        = "sub.page"         // JSON SubPage; empty = DefaultSubPage
	SettingSubAnnounce    = "sub.announce"     // text shown by apps (announce header) and on the page
	SettingSubAnnounceURL = "sub.announce_url" // link of the announcement
	SettingSubPageURL     = "sub.page_url"     // profile-web-page-url; empty = the subscription page itself
	SettingSubRules       = "sub.ua_rules"     // JSON []SubRule; empty = DefaultSubRules
)

// SubRule maps a User-Agent to a subscription format; the first enabled match wins.
type SubRule struct {
	Pattern string `json:"pattern"` // regexp on the User-Agent
	Format  string `json:"format"`  // base64 | mihomo | singbox | xray | html
	Enabled bool   `json:"enabled"`
	Note    string `json:"note,omitempty"`
}

// SubFormats are the formats a rule may pick.
var SubFormats = []string{"base64", "mihomo", "singbox", "xray", "html"}

// DefaultSubRules are the built-in rules (docs/ARCHITECTURE.md §8.3).
func DefaultSubRules() []SubRule {
	return []SubRule{
		{`(?i)keqdroid|keqdis`, "base64", true, "KeqDroid лучше всего читает ссылки vless://"},
		{`(?i)karing`, "base64", true, "Karing сам собирает sing-box из ссылок; XHTTP ему не отдаётся"},
		{`(?i)clash|mihomo|stash|flclash|koala`, "mihomo", true, "Clash-клиенты"},
		{`(?i)sing-?box|\bSF[AIMT]\b`, "singbox", true, "sing-box и его приложения"},
		{`(?i)happ|v2raytun|v2rayn|v2rayng|streisand|hiddify|incy|shadowrocket|nekobox|nekoray|v2box|foxray`, "base64", true, "Приложения на ядре Xray"},
	}
}

// SubRules returns the configured User-Agent rules (the defaults until changed).
func (s *Service) SubRules(ctx context.Context) ([]SubRule, error) {
	raw, err := s.Setting(ctx, SettingSubRules, "")
	if err != nil || strings.TrimSpace(raw) == "" {
		return DefaultSubRules(), err
	}
	var rs []SubRule
	if err := json.Unmarshal([]byte(raw), &rs); err != nil {
		return DefaultSubRules(), nil
	}
	return rs, nil
}

// CheckSubRules validates rules (also used for unsaved tests).
func CheckSubRules(rs []SubRule) error {
	for i := range rs {
		r := &rs[i]
		r.Pattern = strings.TrimSpace(r.Pattern)
		if r.Pattern == "" {
			return invalid("rule %d: the pattern is empty", i+1)
		}
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return invalid("rule %d: not a valid regexp: %v", i+1, err)
		}
		if !slices.Contains(SubFormats, r.Format) {
			return invalid("rule %d: unknown format %q", i+1, r.Format)
		}
	}
	return nil
}

// SetSubRules validates and stores the rules; nil restores the defaults.
func (s *Service) SetSubRules(ctx context.Context, actor Actor, rs []SubRule) error {
	if rs == nil {
		return s.SetSetting(ctx, actor, SettingSubRules, "")
	}
	if err := CheckSubRules(rs); err != nil {
		return err
	}
	b, _ := json.Marshal(rs)
	return s.SetSetting(ctx, actor, SettingSubRules, string(b))
}

// SubHeader is one response header of subscriptions. Value may contain {variables}
// (see SubVariables); a header whose value renders empty is not sent.
type SubHeader struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Base64  bool   `json:"base64"`  // send "base64:<value in base64>": UTF-8 text survives every client
	Clients string `json:"clients"` // regexp on the User-Agent; empty = every client
	Enabled bool   `json:"enabled"`
	Note    string `json:"note,omitempty"`
}

// SubApp is a button on the subscription page. Link may contain {url}, {url_enc}, {url_b64},
// {title_url}.
type SubApp struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Link      string   `json:"link"`
	Download  string   `json:"download"`  // where to get the app (optional)
	Platforms []string `json:"platforms"` // android, ios, windows, macos, linux
	Enabled   bool     `json:"enabled"`
	Note      string   `json:"note,omitempty"`
}

// SubPage is the look of the subscription page opened in a browser.
type SubPage struct {
	Heading      string   `json:"heading"`      // empty = sub.title
	Description  string   `json:"description"`  // under the heading
	Instructions string   `json:"instructions"` // how to connect
	Footer       string   `json:"footer"`
	Theme        string   `json:"theme"`  // auto | dark | light
	Accent       string   `json:"accent"` // #rrggbb
	ShowQR       bool     `json:"showQr"`
	ShowTraffic  bool     `json:"showTraffic"`
	Apps         []SubApp `json:"apps"`
}

// SubBasics are the plain subscription settings used by headers and the page.
type SubBasics struct {
	Title       string `json:"title"`
	UpdateHours int    `json:"updateHours"`
	SupportURL  string `json:"supportUrl"`
	Announce    string `json:"announce"`
	AnnounceURL string `json:"announceUrl"`
	PageURL     string `json:"pageUrl"`
}

// SubVariable documents a {variable} for the UI.
type SubVariable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Example     string `json:"example"`
}

// SubVariables are the variables header values and app links may use.
var SubVariables = []SubVariable{
	{"title", "Название подписки (Основное → Название)", "VPN"},
	{"username", "Имя пользователя", "vasya"},
	{"upload", "Отдано байт (всегда 0: учитывается общий трафик)", "0"},
	{"download", "Использовано байт за период", "1073741824"},
	{"total", "Лимит трафика в байтах, 0 — без лимита", "107374182400"},
	{"expire", "Окончание подписки, unix-время, 0 — бессрочно", "1767225600"},
	{"used", "Использовано, по-человечески", "1.0 ГБ"},
	{"limit", "Лимит, по-человечески", "100 ГБ"},
	{"expire_date", "Дата окончания", "01.01.2026"},
	{"days_left", "Дней до окончания", "30"},
	{"status", "Статус подписки", "активна"},
	{"update_hours", "Интервал обновления в часах", "12"},
	{"support_url", "Ссылка на поддержку", "https://t.me/support"},
	{"announce", "Объявление", "Профилактика в субботу"},
	{"announce_url", "Ссылка объявления", "https://t.me/news"},
	{"sub_url", "Ссылка подписки пользователя", "https://example.com/s/…"},
	{"page_url", "Страница для «открыть в браузере» (Основное → Страница)", "https://example.com/s/…"},
	{"url", "То же, что sub_url (для ссылок приложений)", "https://example.com/s/…"},
	{"url_enc", "Ссылка подписки, закодированная для ?url=", "https%3A%2F%2F…"},
	{"url_b64", "Ссылка подписки в base64", "aHR0cHM6Ly9…"},
	{"title_url", "Название, закодированное для URL", "VPN"},
}

// SubHeaderPreset is a header from the catalog with a hint where it works.
type SubHeaderPreset struct {
	SubHeader
	Apps        string `json:"apps"` // which clients read it
	Description string `json:"description"`
}

// Clients matching in the catalog; the User-Agents come from the apps themselves.
const (
	uaHapp  = `(?i)\bhapp\b`
	uaIncy  = `(?i)\bincy\b`
	uaHappI = `(?i)\b(happ|incy)\b`
	uaV2T   = `(?i)v2raytun`
	uaClash = `(?i)clash|mihomo|stash|flclash|koala`
)

// SubHeaderCatalog lists the headers popular clients understand. The first ones are the
// defaults (enabled); the rest are switched on when needed.
var SubHeaderCatalog = []SubHeaderPreset{
	{SubHeader{Name: "Subscription-Userinfo", Value: "upload={upload}; download={download}; total={total}; expire={expire}", Enabled: true},
		"все: Happ, v2RayTun, Hiddify, KeqDroid, Clash, Streisand, Shadowrocket, Karing…", "Трафик и дата окончания в приложении"},
	{SubHeader{Name: "Profile-Title", Value: "{title}", Base64: true, Enabled: true},
		"Happ, v2RayTun, Hiddify, KeqDroid, INCY, Streisand, Karing", "Название подписки в приложении"},
	{SubHeader{Name: "Profile-Update-Interval", Value: "{update_hours}", Enabled: true},
		"все", "Как часто приложение само обновляет подписку, часов"},
	{SubHeader{Name: "Support-Url", Value: "{support_url}", Enabled: true},
		"Happ, v2RayTun, Hiddify, KeqDroid, INCY", "Кнопка «поддержка» в приложении"},
	{SubHeader{Name: "Profile-Web-Page-Url", Value: "{page_url}", Enabled: true},
		"Happ, v2RayTun, Hiddify, KeqDroid, Clash Verge, FlClash", "Кнопка «открыть в браузере»"},
	{SubHeader{Name: "Announce", Value: "{announce}", Base64: true, Enabled: true},
		"Happ, v2RayTun, KeqDroid, INCY", "Объявление вверху приложения (Основное → Объявление)"},
	{SubHeader{Name: "Announce-Url", Value: "{announce_url}", Enabled: true},
		"Happ, v2RayTun", "Ссылка при нажатии на объявление"},
	{SubHeader{Name: "Content-Disposition", Value: "attachment; filename*=UTF-8''{title_url}", Clients: uaClash, Enabled: true},
		"Clash, Mihomo, FlClash, Stash", "Имя профиля в Clash-клиентах"},

	{SubHeader{Name: "Update-Always", Value: "true", Clients: uaV2T},
		"v2RayTun", "Обновлять подписку при каждом открытии приложения"},
	{SubHeader{Name: "Hide-Settings", Value: "1", Clients: uaHappI},
		"Happ, INCY", "Скрыть настройки серверов (адрес, ключи) от пользователя"},
	{SubHeader{Name: "Routing", Value: "happ://routing/onadd/…", Clients: uaHapp},
		"Happ", "Профиль маршрутизации (ссылка из конструктора Happ)"},
	{SubHeader{Name: "Routing-Enable", Value: "true", Clients: uaHapp},
		"Happ", "Включить присланную маршрутизацию"},
	{SubHeader{Name: "ProviderID", Value: "", Clients: uaHappI},
		"Happ, INCY", "ID провайдера из кабинета Happ: открывает расширенные заголовки"},
	{SubHeader{Name: "Notification-Subs-Expire", Value: "1", Clients: uaHapp},
		"Happ", "Напоминать об окончании подписки"},
	{SubHeader{Name: "Sub-Expire", Value: "1", Clients: uaHapp},
		"Happ", "Показывать плашку, когда подписка истекает"},
	{SubHeader{Name: "Sub-Expire-Button-Link", Value: "{support_url}", Clients: uaHapp},
		"Happ", "Куда ведёт кнопка на плашке об окончании (продление)"},
	{SubHeader{Name: "Sub-Info-Text", Value: "", Clients: uaHapp},
		"Happ", "Текст информационной плашки"},
	{SubHeader{Name: "Sub-Info-Color", Value: "blue", Clients: uaHapp},
		"Happ", "Цвет плашки: blue, green, red"},
	{SubHeader{Name: "Sub-Info-Button-Text", Value: "", Clients: uaHapp},
		"Happ", "Текст кнопки на плашке"},
	{SubHeader{Name: "Sub-Info-Button-Link", Value: "", Clients: uaHapp},
		"Happ", "Ссылка кнопки на плашке"},
	{SubHeader{Name: "Subscription-Always-Hwid-Enable", Value: "1", Clients: uaHapp},
		"Happ", "Всегда отправлять HWID (нужно для лимита устройств)"},
	{SubHeader{Name: "Ping-Type", Value: "proxy", Clients: uaHapp},
		"Happ", "Как мерить пинг: proxy, proxy-head, tcp, icmp"},
	{SubHeader{Name: "Subscription-Autoconnect", Value: "1", Clients: uaHapp},
		"Happ", "Подключаться сразу после добавления подписки"},
	{SubHeader{Name: "Subscription-Autoconnect-Type", Value: "lowestdelay", Clients: uaHapp},
		"Happ", "К какому серверу: lastused или lowestdelay"},
	{SubHeader{Name: "Color-Profile", Value: "", Clients: uaHapp},
		"Happ", "Цветовая тема приложения (JSON из документации Happ)"},
	{SubHeader{Name: "Fallback-Url", Value: "", Clients: uaHapp},
		"Happ", "Запасной адрес подписки, если основной недоступен"},
	{SubHeader{Name: "New-Url", Value: "", Clients: uaHapp},
		"Happ", "Переезд: приложение заменит адрес подписки на этот"},
	{SubHeader{Name: "Socks-Auth-Mode", Value: "", Clients: uaHapp},
		"Happ", "Пароль на локальный SOCKS (чтобы другие приложения не узнали адрес сервера)"},
	{SubHeader{Name: "Per-App-Proxy-Mode", Value: "bypass", Clients: uaHapp},
		"Happ", "Раздельное туннелирование: on (только список) или bypass (кроме списка)"},
	{SubHeader{Name: "Per-App-Proxy-List", Value: "", Clients: uaHapp},
		"Happ", "Список приложений через запятую (com.example.app)"},
	{SubHeader{Name: "Tun-Mode", Value: "", Clients: uaHapp},
		"Happ", "Режим TUN (значение из документации Happ)"},
}

// DefaultSubHeaders are the headers sent when none are configured.
func DefaultSubHeaders() []SubHeader {
	var out []SubHeader
	for _, p := range SubHeaderCatalog {
		if p.Enabled {
			out = append(out, p.SubHeader)
		}
	}
	return out
}

// SubAppCatalog lists known apps; the enabled ones form the default page.
var SubAppCatalog = []SubApp{
	{ID: "keqdroid", Name: "KeqDroid", Link: "keqdroid://install-config?url={url_enc}", Download: "https://github.com/Lemonochka/keqdroid/releases/latest",
		Platforms: []string{"android", "windows", "linux"}, Enabled: true, Note: "Рекомендуем: ссылки VLESS, HWID, объявления"},
	{ID: "happ", Name: "Happ", Link: "happ://add/{url}", Download: "https://www.happ.su/",
		Platforms: []string{"android", "ios", "windows", "macos", "linux"}, Enabled: true},
	{ID: "v2raytun", Name: "v2RayTun", Link: "v2raytun://import/{url}", Download: "https://v2raytun.com/",
		Platforms: []string{"android", "ios", "windows", "macos"}, Enabled: true},
	{ID: "hiddify", Name: "Hiddify", Link: "hiddify://import/{url}#{title_url}", Download: "https://github.com/hiddify/hiddify-app/releases/latest",
		Platforms: []string{"android", "ios", "windows", "macos", "linux"}, Enabled: true},
	{ID: "streisand", Name: "Streisand", Link: "streisand://import/{url}#{title_url}",
		Platforms: []string{"ios", "macos"}, Enabled: true},
	{ID: "clash", Name: "Clash / Mihomo", Link: "clash://install-config?url={url_enc}", Download: "https://github.com/clash-verge-rev/clash-verge-rev/releases/latest",
		Platforms: []string{"android", "windows", "macos", "linux"}, Enabled: true},
	{ID: "v2rayng", Name: "v2rayNG", Link: "v2rayng://install-config?url={url_enc}", Download: "https://github.com/2dust/v2rayNG/releases/latest",
		Platforms: []string{"android"}, Enabled: true},
	{ID: "flclashx", Name: "FlClashX", Link: "flclashx://install-config?url={url_enc}",
		Platforms: []string{"android", "windows", "macos", "linux"}},
	{ID: "karing", Name: "Karing", Link: "karing://install-config?url={url_enc}&name={title_url}", Download: "https://karing.app/",
		Platforms: []string{"android", "ios", "windows", "macos"}},
	{ID: "singbox", Name: "sing-box", Link: "sing-box://import-remote-profile?url={url_enc}#{title_url}", Download: "https://sing-box.sagernet.org/",
		Platforms: []string{"android", "ios", "macos"}},
	{ID: "shadowrocket", Name: "Shadowrocket", Link: "sub://{url_b64}",
		Platforms: []string{"ios"}},
}

// DefaultSubPage is the page used when none is configured.
func DefaultSubPage() SubPage {
	apps := make([]SubApp, len(SubAppCatalog))
	copy(apps, SubAppCatalog)
	return SubPage{
		Description:  "",
		Instructions: "1. Установите приложение — для вашего устройства оно показано первым.\n2. Нажмите кнопку с его названием: подписка добавится сама.\n3. Или скопируйте ссылку и добавьте её в приложении вручную («Добавить подписку» / «Импорт из буфера»).",
		Theme:        "dark",
		Accent:       "#39c5bb",
		ShowQR:       true,
		ShowTraffic:  true,
		Apps:         apps,
	}
}

// SubHeaders returns the configured headers (the defaults until changed).
func (s *Service) SubHeaders(ctx context.Context) ([]SubHeader, error) {
	raw, err := s.Setting(ctx, SettingSubHeaders, "")
	if err != nil || strings.TrimSpace(raw) == "" {
		return DefaultSubHeaders(), err
	}
	var hs []SubHeader
	if err := json.Unmarshal([]byte(raw), &hs); err != nil {
		return DefaultSubHeaders(), nil
	}
	return hs, nil
}

var headerNameRe = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,64}$")

// reservedHeaders are set by the server itself.
var reservedHeaders = map[string]bool{
	"content-type": true, "content-length": true, "transfer-encoding": true, "connection": true,
	"set-cookie": true, "cache-control": true, "date": true, "server": true, "keep-alive": true,
}

// SetSubHeaders validates and stores the headers; nil restores the defaults.
func (s *Service) SetSubHeaders(ctx context.Context, actor Actor, hs []SubHeader) error {
	if hs == nil {
		return s.SetSetting(ctx, actor, SettingSubHeaders, "")
	}
	seen := map[string]bool{}
	for i := range hs {
		h := &hs[i]
		h.Name = strings.TrimSpace(h.Name)
		if !headerNameRe.MatchString(h.Name) {
			return invalid("header %q: the name may contain latin letters, digits and - only", h.Name)
		}
		if reservedHeaders[strings.ToLower(h.Name)] {
			return invalid("header %s is set by the server itself", h.Name)
		}
		key := strings.ToLower(h.Name) + "|" + h.Clients
		if h.Enabled && seen[key] {
			return invalid("header %s is listed twice for the same clients", h.Name)
		}
		seen[key] = h.Enabled || seen[key]
		h.Value = strings.NewReplacer("\r", "", "\n", " ").Replace(h.Value)
		if h.Clients != "" {
			if _, err := regexp.Compile(h.Clients); err != nil {
				return invalid("header %s: clients is not a valid regexp: %v", h.Name, err)
			}
		}
	}
	b, _ := json.Marshal(hs)
	return s.SetSetting(ctx, actor, SettingSubHeaders, string(b))
}

// SubPage returns the page config (the default until changed). Apps added to the catalog
// later are appended (disabled) so they show up in the editor.
func (s *Service) SubPage(ctx context.Context) (SubPage, error) {
	raw, err := s.Setting(ctx, SettingSubPage, "")
	if err != nil || strings.TrimSpace(raw) == "" {
		return DefaultSubPage(), err
	}
	p := DefaultSubPage()
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return DefaultSubPage(), nil
	}
	have := map[string]bool{}
	for _, a := range p.Apps {
		have[a.ID] = true
	}
	for _, a := range SubAppCatalog {
		if !have[a.ID] {
			a.Enabled = false
			p.Apps = append(p.Apps, a)
		}
	}
	return p, nil
}

var accentRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// SetSubPage validates and stores the page config; nil restores the default.
func (s *Service) SetSubPage(ctx context.Context, actor Actor, p *SubPage) error {
	if p == nil {
		return s.SetSetting(ctx, actor, SettingSubPage, "")
	}
	if err := CheckSubPage(p); err != nil {
		return err
	}
	b, _ := json.Marshal(p)
	return s.SetSetting(ctx, actor, SettingSubPage, string(b))
}

// CheckSubPage validates a page config (also used for unsaved previews).
func CheckSubPage(p *SubPage) error {
	switch p.Theme {
	case "":
		p.Theme = "dark"
	case "auto", "dark", "light":
	default:
		return invalid("theme must be auto, dark or light")
	}
	if p.Accent == "" {
		p.Accent = "#39c5bb"
	}
	if !accentRe.MatchString(p.Accent) {
		return invalid("accent must be a color like #39c5bb")
	}
	ids := map[string]bool{}
	for i := range p.Apps {
		a := &p.Apps[i]
		a.Name = strings.TrimSpace(a.Name)
		if a.Name == "" {
			return invalid("an app without a name")
		}
		if a.ID == "" {
			a.ID = strings.ToLower(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(a.Name, "-"))
		}
		if ids[a.ID] {
			a.ID += "-" + strconv.Itoa(i)
		}
		ids[a.ID] = true
		link := strings.ToLower(strings.TrimSpace(a.Link))
		if strings.HasPrefix(link, "javascript:") || strings.HasPrefix(link, "data:") {
			return invalid("app %s: this kind of link is not allowed", a.Name)
		}
		if d := strings.ToLower(a.Download); d != "" && !strings.HasPrefix(d, "https://") && !strings.HasPrefix(d, "http://") {
			return invalid("app %s: the download link must start with https://", a.Name)
		}
	}
	return nil
}

// SubBasicsGet reads the plain subscription settings.
func (s *Service) SubBasicsGet(ctx context.Context) SubBasics {
	b := SubBasics{}
	b.Title, _ = s.Setting(ctx, SettingSubTitle, "VPN")
	h, _ := s.Setting(ctx, SettingSubUpdateHours, "12")
	b.UpdateHours, _ = strconv.Atoi(h)
	b.SupportURL, _ = s.Setting(ctx, SettingSubSupportURL, "")
	b.Announce, _ = s.Setting(ctx, SettingSubAnnounce, "")
	b.AnnounceURL, _ = s.Setting(ctx, SettingSubAnnounceURL, "")
	b.PageURL, _ = s.Setting(ctx, SettingSubPageURL, "")
	return b
}

// SetSubBasics stores the plain subscription settings (only the changed ones).
func (s *Service) SetSubBasics(ctx context.Context, actor Actor, b SubBasics) error {
	if b.UpdateHours < 0 || b.UpdateHours > 24*30 {
		return invalid("update interval must be 0–720 hours")
	}
	cur := s.SubBasicsGet(ctx)
	set := func(key, old, val string) error {
		if old == val {
			return nil
		}
		return s.SetSetting(ctx, actor, key, val)
	}
	for _, x := range []struct{ key, old, val string }{
		{SettingSubTitle, cur.Title, strings.TrimSpace(b.Title)},
		{SettingSubUpdateHours, strconv.Itoa(cur.UpdateHours), strconv.Itoa(b.UpdateHours)},
		{SettingSubSupportURL, cur.SupportURL, strings.TrimSpace(b.SupportURL)},
		{SettingSubAnnounce, cur.Announce, strings.TrimSpace(b.Announce)},
		{SettingSubAnnounceURL, cur.AnnounceURL, strings.TrimSpace(b.AnnounceURL)},
		{SettingSubPageURL, cur.PageURL, strings.TrimSpace(b.PageURL)},
	} {
		if err := set(x.key, x.old, x.val); err != nil {
			return err
		}
	}
	return nil
}
