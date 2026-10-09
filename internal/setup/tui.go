// Package setup is the installer's interactive part: a terminal form (fields, choices, check
// boxes) that scripts/install.sh opens before it installs anything. The form only collects
// answers; the script does the work with them, so a run without a terminal (flags only) does
// exactly the same.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/vyto4ka/vynel/internal/node/agent"
	"github.com/vyto4ka/vynel/internal/node/firewall"
)

// Options is what the script knows before asking.
type Options struct {
	Installed string // aio | panel | node | "" (nothing)
	Version   string // installed vynel version
	PublicIP  string
	Country   string
	Domain    string // prefilled from flags
	Email     string
	Token     string
}

// Answers go back to the script as shell variables.
type Answers struct {
	Mode, Domain, SubDomain, Email, Name, Country, PublicIP, VPNIP, SubIP string
	GatewayListen, AdminLogin, BotToken, Restore, Token                   string
	Firewall, SSHKeysOnly, SSHPort                                        string
	Purge                                                                 bool
}

// ErrAborted is returned when the admin leaves the form.
var ErrAborted = errors.New("aborted")

var (
	miku  = lipgloss.Color("#39c5bb")
	pink  = lipgloss.Color("#ff5fa2")
	muted = lipgloss.AdaptiveColor{Light: "243", Dark: "245"}
	text  = lipgloss.AdaptiveColor{Light: "235", Dark: "252"}
	red   = lipgloss.Color("#ff6b81")
)

// theme: the panel's colours (Miku teal, pink accents).
func theme() *huh.Theme {
	t := huh.ThemeBase()
	t.Focused.Base = t.Focused.Base.BorderForeground(miku)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(miku).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(pink).Bold(true).MarginBottom(1)
	t.Focused.Description = t.Focused.Description.Foreground(muted)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(red)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(red)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(pink)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(pink)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(pink)
	t.Focused.Option = t.Focused.Option.Foreground(text)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(pink)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(miku)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(miku).SetString("[✓] ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(muted).SetString("[ ] ")
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(text)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("#0b1215")).Background(miku).Bold(true)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(text).Background(lipgloss.AdaptiveColor{Light: "252", Dark: "237"})
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(pink)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(lipgloss.AdaptiveColor{Light: "248", Dark: "240"})
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(pink)
	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()
	t.Group.Title = t.Focused.Title.Foreground(pink)
	t.Group.Description = t.Focused.Description
	return t
}

var (
	domainRe  = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)
	loginRe   = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,64}$`)
	countryRe = regexp.MustCompile(`^[A-Za-z]{2}$`)
	botRe     = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]{20,}$`)
)

func cleanDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

func validDomain(s string) error {
	if !domainRe.MatchString(cleanDomain(s)) {
		return errors.New("это не похоже на домен, пример: nl.example.com")
	}
	return nil
}

// dnsCache keeps lookups out of the typing path: each domain is asked once.
type dnsCache struct {
	mu sync.Mutex
	m  map[string]DNSStatus
}

func (c *dnsCache) check(domain, want string) string {
	domain = cleanDomain(domain)
	if !domainRe.MatchString(domain) || want == "" {
		return ""
	}
	key := domain + "@" + want
	c.mu.Lock()
	st, ok := c.m[key]
	c.mu.Unlock()
	if !ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		st = CheckDomain(ctx, domain, want)
		cancel()
		c.mu.Lock()
		c.m[key] = st
		c.mu.Unlock()
	}
	return st.Message
}

// localIPs are the interface addresses, the primary first.
func localIPs() []string {
	var ips []string
	for _, a := range agent.LocalAddresses() {
		if net.ParseIP(a.Ip).To4() == nil {
			continue
		}
		if a.Primary {
			ips = append([]string{a.Ip}, ips...)
		} else {
			ips = append(ips, a.Ip)
		}
	}
	return ips
}

func isPrivate(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && (p.IsPrivate() || (p.To4() != nil && p.To4()[0] == 100 && p.To4()[1]&0xc0 == 64))
}

// sshFacts: whether keys are installed, whether passwords are accepted, the ports.
func sshFacts() (keys, password bool, ports []int) {
	files, _ := filepath.Glob("/home/*/.ssh/authorized_keys")
	for _, f := range append([]string{"/root/.ssh/authorized_keys"}, files...) {
		b, err := os.ReadFile(f)
		if err == nil && regexp.MustCompile(`(?m)^(ssh-|ecdsa-|sk-)`).Match(b) {
			keys = true
		}
	}
	if out, err := exec.Command("sshd", "-T").Output(); err == nil {
		password = regexp.MustCompile(`(?m)^passwordauthentication yes`).Match(out)
	}
	return keys, password, firewall.SSHPorts(context.Background())
}

var countries = map[string]string{"NL": "Нидерланды", "DE": "Германия", "FI": "Финляндия", "SE": "Швеция", "FR": "Франция",
	"GB": "Великобритания", "US": "США", "CA": "Канада", "PL": "Польша", "LV": "Латвия", "LT": "Литва", "EE": "Эстония",
	"AT": "Австрия", "CH": "Швейцария", "CZ": "Чехия", "ES": "Испания", "IT": "Италия", "RO": "Румыния", "BG": "Болгария",
	"HU": "Венгрия", "RS": "Сербия", "MD": "Молдова", "TR": "Турция", "KZ": "Казахстан", "AM": "Армения", "GE": "Грузия",
	"AE": "ОАЭ", "JP": "Япония", "SG": "Сингапур", "HK": "Гонконг", "RU": "Россия", "UA": "Украина"}

func countryName(cc string) string {
	if n, ok := countries[strings.ToUpper(cc)]; ok {
		return n
	}
	return "VPN"
}

var installedNames = map[string]string{"aio": "панель + нода (всё на одном сервере)", "panel": "панель", "node": "нода", "": "ничего"}

// Run shows the form and returns the answers.
func Run(o Options) (*Answers, error) {
	// Never ask the terminal for its background colour: some SSH clients do not answer and the
	// form would hang before it is drawn. Servers are administered from dark terminals anyway.
	lipgloss.SetHasDarkBackground(true)
	a := &Answers{Domain: o.Domain, Email: o.Email, Token: o.Token, PublicIP: o.PublicIP, Country: strings.ToUpper(o.Country),
		AdminLogin: "admin", GatewayListen: ":9443"}
	th := theme()
	run := func(groups ...*huh.Group) error {
		err := huh.NewForm(groups...).WithTheme(th).WithShowHelp(true).Run()
		if errors.Is(err, huh.ErrUserAborted) {
			return ErrAborted
		}
		return err
	}

	// 1. What to do.
	installed := o.Installed != ""
	modes := []huh.Option[string]{}
	if !installed {
		modes = append(modes,
			huh.NewOption("Всё на этом сервере: панель + VPN   · проще всего начать с этого", "aio"),
			huh.NewOption("Только панель                       · VPN-ноды на других серверах", "panel"),
			huh.NewOption("Только VPN-нода                     · нужен токен из панели", "node"))
	} else {
		modes = append(modes,
			huh.NewOption("Обновить                            · данные сохранятся", "update"),
			huh.NewOption("Защитить сервер                     · файрвол, SSH по ключу, порт SSH", "harden"))
		if o.Installed == "aio" {
			modes = append(modes, huh.NewOption("Перенастроить                       · домены, IP, бот (данные сохранятся)", "aio"))
		}
	}
	modes = append(modes, huh.NewOption("Удалить", "uninstall"))
	header := "Сейчас на сервере: " + installedNames[o.Installed]
	if o.Version != "" {
		header += " · " + o.Version
	}
	if err := run(huh.NewGroup(
		huh.NewSelect[string]().Title("vynel — установка").Description(header).Options(modes...).Value(&a.Mode),
	)); err != nil {
		return nil, err
	}

	switch a.Mode {
	case "update":
		ok := true
		if err := run(huh.NewGroup(huh.NewConfirm().Title("Обновить vynel, Xray и Caddy до свежих версий?").
			Description("Пользователи, ключи и настройки сохраняются. Если новая версия не запустится, вернётся прежняя.").
			Affirmative("Обновить").Negative("Отмена").Value(&ok))); err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrAborted
		}
		return a, nil
	case "uninstall":
		ok := false
		if err := run(huh.NewGroup(
			huh.NewConfirm().Title("Удалить и все данные?").Description("Пользователи, ключи, сертификаты. «Нет» — удалить только службы, данные останутся.").
				Affirmative("Да, всё").Negative("Нет, только службы").Value(&a.Purge),
			huh.NewConfirm().Title("Точно удалить vynel с этого сервера?").Affirmative("Удалить").Negative("Отмена").Value(&ok),
		)); err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrAborted
		}
		return a, nil
	}

	var groups []*huh.Group
	cache := &dnsCache{m: map[string]DNSStatus{}}
	ips := localIPs()
	publicOf := func(ip string) string {
		if ip == "" || isPrivate(ip) {
			return a.PublicIP
		}
		return ip
	}

	// 2. Server and domains.
	switch a.Mode {
	case "aio", "panel":
		fields := []huh.Field{
			huh.NewInput().Title("Публичный IP сервера").Value(&a.PublicIP).Validate(func(s string) error {
				if net.ParseIP(strings.TrimSpace(s)).To4() == nil {
					return errors.New("нужен IPv4-адрес")
				}
				return nil
			}),
		}
		if a.Mode == "aio" && len(ips) > 1 {
			opts := []huh.Option[string]{huh.NewOption("все адреса", "")}
			for i, ip := range ips {
				label := ip
				if i == 0 {
					label += "  · основной"
				}
				if isPrivate(ip) {
					label += "  · за NAT"
				}
				opts = append(opts, huh.NewOption(label, ip))
			}
			fields = append(fields,
				huh.NewSelect[string]().Title("IP для VPN").Description("На сервере несколько адресов: VPN и панель можно развести — заблокируют один, второй продолжит работать").
					Options(opts...).Value(&a.VPNIP),
				huh.NewSelect[string]().Title("IP для подписок и веб-панели").Options(opts...).Value(&a.SubIP))
		}
		domainTitle := "Домен сервера"
		domainHelp := "A-запись на этот сервер. На нём VPN, сайт-заглушка, подписки и панель"
		if a.Mode == "panel" {
			domainTitle, domainHelp = "Домен панели и подписок", "A-запись на этот сервер"
		}
		fields = append(fields,
			huh.NewInput().Title(domainTitle).Placeholder("nl.example.com").Value(&a.Domain).Validate(validDomain).
				DescriptionFunc(func() string {
					if m := cache.check(a.Domain, publicOf(a.VPNIP)); m != "" {
						return m
					}
					return domainHelp
				}, &a.Domain))
		if a.Mode == "aio" {
			fields = append(fields,
				huh.NewInput().Title("Отдельный домен подписок и панели").Placeholder("пусто — тот же домен").Value(&a.SubDomain).
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							if a.VPNIP != a.SubIP {
								return errors.New("VPN и панель на разных IP: нужен второй домен (A-запись на IP панели)")
							}
							return nil
						}
						return validDomain(s)
					}).
					DescriptionFunc(func() string {
						if m := cache.check(a.SubDomain, publicOf(a.SubIP)); m != "" {
							return m
						}
						return "Не обязательно: одного домена достаточно"
					}, &a.SubDomain))
		}
		fields = append(fields, huh.NewInput().Title("Email для Let's Encrypt").Placeholder("можно пусто").Value(&a.Email))
		groups = append(groups, huh.NewGroup(fields...).Title("Сервер и домены").
			Description("Tab / ↓ — следующее поле, Shift+Tab — назад, Enter — дальше"))
	case "node":
		groups = append(groups, huh.NewGroup(
			huh.NewInput().Title("Токен подключения").Placeholder("vyn1.…").Value(&a.Token).
				Description("Панель → «Ноды» → «Добавить сервер». Одноразовый, действует 24 часа").
				Validate(func(s string) error {
					if !strings.HasPrefix(strings.TrimSpace(s), "vyn1.") {
						return errors.New("токен должен начинаться с vyn1")
					}
					return nil
				}),
		).Title("VPN-нода"))
	}

	// 3. Panel, VPN name, bot.
	nodes := true
	if a.Mode == "aio" || a.Mode == "panel" {
		var fields []huh.Field
		if a.Mode == "aio" {
			fields = append(fields,
				huh.NewInput().Title("Код страны (флаг в приложениях)").Placeholder("NL").Value(&a.Country).CharLimit(2).
					Validate(func(s string) error {
						if s != "" && !countryRe.MatchString(s) {
							return errors.New("две латинские буквы, например NL")
						}
						return nil
					}),
				huh.NewInput().Title("Название сервера в приложениях").Value(&a.Name).
					PlaceholderFunc(func() string { return countryName(a.Country) }, &a.Country),
				huh.NewConfirm().Title("Подключать другие серверы (ноды) в будущем?").Description("Откроет порт 9443 для нод").
					Affirmative("Да").Negative("Нет").Value(&nodes))
		}
		fields = append(fields,
			huh.NewInput().Title("Логин веб-панели").Description("Пароль сгенерируется сам и будет показан в конце").Value(&a.AdminLogin).
				Validate(func(s string) error {
					if !loginRe.MatchString(s) {
						return errors.New("латиница, цифры и _ . - @")
					}
					return nil
				}),
			huh.NewInput().Title("Токен Telegram-бота").Placeholder("от @BotFather, можно потом в панели").Value(&a.BotToken).
				Description("Пользователи из чата, вход без пароля, ночные бэкапы, уведомления").
				Validate(func(s string) error {
					if s != "" && !botRe.MatchString(strings.TrimSpace(s)) {
						return errors.New("токен вида 123456789:AA…")
					}
					return nil
				}),
			huh.NewInput().Title("Восстановить из бэкапа").Placeholder("путь к .tar.gz, обычно пусто").Value(&a.Restore).
				Validate(func(s string) error {
					if s = strings.TrimSpace(s); s != "" {
						if _, err := os.Stat(s); err != nil {
							return errors.New("файла нет")
						}
					}
					return nil
				}))
		groups = append(groups, huh.NewGroup(fields...).Title("Панель"))
	}

	// 4. Security: check boxes.
	keys, password, sshPorts := sshFacts()
	sec := []string{"firewall"}
	if keys && password {
		sec = append(sec, "keys")
	}
	secOpts := []huh.Option[string]{huh.NewOption("Файрвол: открыты только SSH и то, что обслуживает vynel; сканеры портов блокируются", "firewall").Selected(true)}
	switch {
	case keys && password:
		secOpts = append(secOpts, huh.NewOption("SSH только по ключу (пароль выключить) — ключи на сервере есть", "keys").Selected(true))
	case !keys && password:
		secOpts = append(secOpts, huh.NewOption("SSH только по ключу — недоступно: на сервере нет ключей (ssh-copy-id)", "nokeys"))
	}
	portNow := fmt.Sprint(sshPorts)
	secOpts = append(secOpts, huh.NewOption("SSH на случайный порт вместо "+strings.Trim(portNow, "[]")+" — старый закроется после проверки входа", "port"))
	groups = append(groups, huh.NewGroup(
		huh.NewMultiSelect[string]().Title("Защита сервера").Description("Пробел или x — отметить, Enter — дальше").
			Options(secOpts...).Value(&sec).Validate(func(v []string) error {
			for _, x := range v {
				if x == "nokeys" {
					return errors.New("сначала добавьте SSH-ключ на сервер (ssh-copy-id root@сервер)")
				}
			}
			return nil
		}),
	).Title("Безопасность"))

	// 5. Summary.
	ok := true
	groups = append(groups, huh.NewGroup(
		huh.NewNote().Title("Проверьте").DescriptionFunc(func() string { return summary(a, nodes, sec, cache, publicOf) }, a),
		huh.NewConfirm().Title("Устанавливаем?").Affirmative("Установить").Negative("Отмена").Value(&ok),
	))
	if a.Mode == "harden" {
		groups[len(groups)-1] = huh.NewGroup(huh.NewConfirm().Title("Применить?").Affirmative("Применить").Negative("Отмена").Value(&ok))
	}
	if err := run(groups...); err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrAborted
	}

	a.Domain, a.SubDomain = cleanDomain(a.Domain), cleanDomain(a.SubDomain)
	a.Country = strings.ToUpper(a.Country)
	if a.Name == "" {
		a.Name = countryName(a.Country)
	}
	if !nodes {
		a.GatewayListen = "127.0.0.1:9443"
	}
	a.Firewall = "off"
	for _, s := range sec {
		switch s {
		case "firewall":
			a.Firewall = "on"
		case "keys":
			a.SSHKeysOnly = "yes"
		case "port":
			a.SSHPort = "random"
		}
	}
	a.BotToken, a.Restore, a.Token = strings.TrimSpace(a.BotToken), strings.TrimSpace(a.Restore), strings.TrimSpace(a.Token)
	return a, nil
}

func summary(a *Answers, nodes bool, sec []string, cache *dnsCache, publicOf func(string) string) string {
	var b strings.Builder
	line := func(k, v string) { fmt.Fprintf(&b, "%-12s %s\n", k, v) }
	switch a.Mode {
	case "aio", "panel":
		line("Сервер", a.PublicIP)
		if a.VPNIP != "" || a.SubIP != "" {
			line("IP", "VPN "+or(a.VPNIP, "все")+", панель "+or(a.SubIP, "все"))
		}
		line("Домен", cleanDomain(a.Domain)+"  "+cache.check(a.Domain, publicOf(a.VPNIP)))
		if a.SubDomain != "" {
			line("Подписки", cleanDomain(a.SubDomain)+"  "+cache.check(a.SubDomain, publicOf(a.SubIP)))
		}
		if a.Mode == "aio" {
			line("В клиентах", or(a.Name, countryName(a.Country))+" "+a.Country)
			line("Ноды", map[bool]string{true: "можно подключать (порт 9443)", false: "только этот сервер"}[nodes])
		}
		line("Панель", "логин "+a.AdminLogin+", пароль покажем в конце")
		line("Бот", map[bool]string{true: "настроен", false: "потом, в панели"}[a.BotToken != ""])
		if a.Restore != "" {
			line("Бэкап", a.Restore)
		}
	case "node":
		line("Токен", trim(a.Token, 24))
	}
	var s []string
	for _, x := range sec {
		s = append(s, map[string]string{"firewall": "файрвол", "keys": "SSH по ключу", "port": "SSH на другой порт"}[x])
	}
	line("Защита", or(strings.Join(s, ", "), "ничего"))
	return b.String()
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Shell writes the answers as shell assignments the script sources. Values are single-quoted.
func (a *Answers) Shell(w io.Writer) error {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	vals := map[string]string{
		"MODE": a.Mode, "DOMAIN": a.Domain, "SUB_DOMAIN": a.SubDomain, "EMAIL": a.Email, "NAME": a.Name, "COUNTRY": a.Country,
		"PUBLIC_IP": a.PublicIP, "VPN_IP": a.VPNIP, "SUB_IP": a.SubIP, "GATEWAY_LISTEN": a.GatewayListen,
		"ADMIN_LOGIN": a.AdminLogin, "BOT_TOKEN": a.BotToken, "RESTORE": a.Restore, "TOKEN": a.Token,
		"FIREWALL": a.Firewall, "SSH_KEYS_ONLY": a.SSHKeysOnly, "SSH_PORT": a.SSHPort, "PURGE": "0",
	}
	if a.Purge {
		vals["PURGE"] = "1"
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := fmt.Fprintf(w, "%s=%s\n", k, q(vals[k])); err != nil {
			return err
		}
	}
	return nil
}
