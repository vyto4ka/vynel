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
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/vyto4ka/vynel/internal/node/agent"
	"github.com/vyto4ka/vynel/internal/node/firewall"
	"github.com/vyto4ka/vynel/internal/termui"
	"github.com/vyto4ka/vynel/internal/xrayconf"
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
	State     string // file that keeps the answers between runs ("" — do not keep)
}

// Answers go back to the script as shell variables.
type Answers struct {
	Mode, Domain, SubDomain, Email, Name, Country, PublicIP, VPNIP, SubIP string
	GatewayListen, AdminLogin, BotToken, Restore, Token                   string
	SubPath, PanelPath                                                    string
	LoginMode, SessionPaths                                               string // password | link | miniapp; "yes" = random path per session
	Firewall, SSHKeysOnly, SSHPort                                        string
	Purge                                                                 bool
}

// ErrAborted is returned when the admin leaves the form.
var ErrAborted = errors.New("aborted")

var pathRe = regexp.MustCompile(`^/?[A-Za-z0-9_-]+(/[A-Za-z0-9_-]+)*/?$`)

// cleanPath turns "sub", "/sub", "sub/" into "/sub/"; empty stays empty.
func cleanPath(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return ""
	}
	return "/" + p + "/"
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
		if err == nil && sshKeyLine.Match(b) {
			keys = true
		}
	}
	if out, err := exec.Command("sshd", "-T").Output(); err == nil {
		password = sshPasswordOn.Match(out)
	}
	return keys, password, firewall.SSHPorts(context.Background())
}

func countryName(cc string) string { return or(xrayconf.CountryName(cc), "VPN") }

var installedNames = map[string]string{"aio": "панель + нода (всё на одном сервере)", "panel": "панель", "node": "нода", "": "ничего"}

var (
	sshKeyLine    = regexp.MustCompile(`(?m)^(ssh-|ecdsa-|sk-)`)
	sshPasswordOn = regexp.MustCompile(`(?m)^passwordauthentication yes`)
)

// Run shows the form and returns the answers.
func Run(o Options) (*Answers, error) {
	// Never ask the terminal for its background colour: some SSH clients do not answer and the
	// form would hang before it is drawn. Servers are administered from dark terminals anyway.
	lipgloss.SetHasDarkBackground(true)
	a := &Answers{Domain: o.Domain, Email: o.Email, Token: o.Token, PublicIP: o.PublicIP, Country: strings.ToUpper(o.Country),
		AdminLogin: "admin", GatewayListen: ":9443", LoginMode: "password"}
	nodes := true
	var sec []string
	saved := loadState(o.State)
	if saved != nil {
		a.restore(&saved.Answers, o)
		sec = saved.Security
		if saved.Nodes != nil {
			nodes = *saved.Nodes
		}
	}
	// Whatever happens next (done, cancelled, Ctrl+C, the SSH session dropped), the answers so far
	// survive a restart.
	save := func() {
		if a.Mode != "update" && a.Mode != "uninstall" {
			saveState(o.State, &state{Answers: *a, Security: sec, Nodes: &nodes})
		}
	}
	defer save()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(hup)
	go func() {
		<-hup
		save()
		os.Exit(2)
	}()
	// Look for a domain the server already has while the first question is on screen.
	detected := make(chan string, 1)
	go func() { detected <- DetectDomain(context.Background(), a.PublicIP) }()
	th := termui.Theme()
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
	if saved != nil && saved.Mode != "update" && saved.Mode != "uninstall" {
		for _, m := range modes {
			if m.Value == saved.Mode {
				a.Mode = saved.Mode
				header += "\nОтветы прошлого запуска подставлены: можно продолжить с того же места"
			}
		}
	}
	if err := run(huh.NewGroup(
		huh.NewSelect[string]().Title("vynel — установка").Description(header).Options(modes...).Value(&a.Mode),
	)); err != nil {
		return nil, err
	}

	switch a.Mode {
	case "update":
		ok := true
		if err := run(huh.NewGroup(huh.NewConfirm().Title("Обновить vynel, Xray и nginx до свежих версий?").
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

	// A domain found on the server is offered greyed out: Enter keeps it, typing replaces it.
	found := ""
	if a.Mode == "aio" || a.Mode == "panel" {
		found = <-detected
	}
	domainOf := func() string { return or(cleanDomain(a.Domain), found) }

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
		placeholder := "nl.example.com"
		if found != "" {
			placeholder = found
			domainHelp = "Найден домен сервера — Enter оставит его, или введите свой"
		}
		fields = append(fields,
			huh.NewInput().Title(domainTitle).Placeholder(placeholder).Value(&a.Domain).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" && found != "" {
						return nil
					}
					return validDomain(s)
				}).
				DescriptionFunc(func() string {
					if a.Domain == "" && found != "" {
						return domainHelp
					}
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
		fields = append(fields, huh.NewInput().Title("Email для Let's Encrypt").Value(&a.Email).
			PlaceholderFunc(func() string {
				if d := domainOf(); d != "" {
					return "admin@" + d
				}
				return "можно пусто"
			}, a).
			Description("Enter — оставить подсказку. На него Let's Encrypt пишет о проблемах с сертификатами"))
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
			huh.NewInput().Title("Путь ссылок подписки").Placeholder("/s/").Value(&a.SubPath).
				DescriptionFunc(func() string {
					d := cleanDomain(a.SubDomain)
					if d == "" {
						d = or(domainOf(), "домен")
					}
					return "Ссылки будут вида https://" + d + or(cleanPath(a.SubPath), "/s/") + "<токен>. Пусто — /s/"
				}, a).
				Validate(func(s string) error {
					if strings.TrimSpace(s) != "" && !pathRe.MatchString(strings.TrimSpace(s)) {
						return errors.New("латиница, цифры, _ - и /, например sub или api/v1/client")
					}
					return nil
				}),
			huh.NewInput().Title("Секретный путь веб-панели").Placeholder("случайный, например /k9Qm2xT7aB3c/").Value(&a.PanelPath).
				Description("Без него панель не найти. Пусто — случайный (надёжнее)").
				Validate(func(s string) error {
					p := strings.Trim(strings.TrimSpace(s), "/")
					if p == "" {
						return nil
					}
					if !pathRe.MatchString(p) || len(p) < 6 {
						return errors.New("от 6 символов: латиница, цифры, _ -")
					}
					if cleanPath(p) == or(cleanPath(a.SubPath), "/s/") {
						return errors.New("совпадает с путём подписок")
					}
					return nil
				}),
			huh.NewSelect[string]().Title("Вход в панель").Value(&a.LoginMode).
				Options(huh.NewOption("Логин и пароль", "password"),
					huh.NewOption("Без пароля: одноразовая ссылка из бота или vynel admin login-link", "link"),
					huh.NewOption("Только через Telegram (Mini App в боте)", "miniapp")).
				Description("Без пароля его нельзя подобрать; режим меняется потом в панели (Аккаунт)"),
			huh.NewInput().Title("Логин веб-панели").Value(&a.AdminLogin).
				DescriptionFunc(func() string {
					if a.LoginMode != "password" {
						return "При входе без пароля не нужен — можно оставить"
					}
					return "Пароль сгенерируется сам и будет показан в конце"
				}, &a.LoginMode).
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
					if strings.TrimSpace(s) == "" && a.LoginMode == "miniapp" {
						return errors.New("для входа только через Telegram нужен бот")
					}
					return nil
				}),
			huh.NewInput().Title("Восстановить из бэкапа").Placeholder("путь к .tar.gz или .age, обычно пусто").Value(&a.Restore).
				Validate(func(s string) error {
					if s = strings.TrimSpace(s); s != "" {
						if _, err := os.Stat(s); err != nil {
							return errors.New("файла нет")
						}
					}
					return nil
				}))
		groups = append(groups, huh.NewGroup(fields...).Title("Панель"))
		groups = append(groups, huh.NewGroup(
			huh.NewSelect[string]().Title("Случайный адрес панели на каждую сессию?").Value(&a.SessionPaths).
				Options(huh.NewOption("Нет, постоянный секретный путь", ""), huh.NewOption("Да", "yes")).
				Description("Да — у панели не будет постоянного адреса: каждая ссылка открывает свой случайный путь,\nвсё остальное на домене — сайт-заглушка"),
		).Title("Вход по ссылке").WithHideFunc(func() bool { return a.LoginMode != "link" }))
	}

	// 4. Security: check boxes.
	keys, password, sshPorts := sshFacts()
	if sec == nil {
		sec = []string{"firewall"}
		if keys && password {
			sec = append(sec, "keys")
		}
	}
	secOpts := []huh.Option[string]{huh.NewOption("Файрвол: открыты только SSH и то, что обслуживает vynel; сканеры портов блокируются", "firewall")}
	switch {
	case keys && password:
		secOpts = append(secOpts, huh.NewOption("SSH только по ключу (пароль выключить) — ключи на сервере есть", "keys"))
	case !keys && password:
		secOpts = append(secOpts, huh.NewOption("SSH только по ключу — недоступно: на сервере нет ключей (ssh-copy-id)", "nokeys"))
	}
	portNow := fmt.Sprint(sshPorts)
	secOpts = append(secOpts, huh.NewOption("SSH на случайный порт вместо "+strings.Trim(portNow, "[]")+" — старый закроется после проверки входа", "port"))
	sec = slices.DeleteFunc(sec, func(v string) bool {
		return !slices.ContainsFunc(secOpts, func(o huh.Option[string]) bool { return o.Value == v })
	})
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
		huh.NewNote().Title("Проверьте").DescriptionFunc(func() string { return summary(a, domainOf(), nodes, sec, cache, publicOf) }, a),
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

	a.Domain, a.SubDomain = domainOf(), cleanDomain(a.SubDomain)
	if a.LoginMode != "link" {
		a.SessionPaths = ""
	}
	if a.Email = strings.TrimSpace(a.Email); a.Email == "" && a.Domain != "" && a.Mode != "node" {
		a.Email = "admin@" + a.Domain
	}
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
	a.SubPath, a.PanelPath = cleanPath(a.SubPath), cleanPath(a.PanelPath)
	return a, nil
}

func summary(a *Answers, domain string, nodes bool, sec []string, cache *dnsCache, publicOf func(string) string) string {
	var b strings.Builder
	line := func(k, v string) { fmt.Fprintf(&b, "%-12s %s\n", k, v) }
	switch a.Mode {
	case "aio", "panel":
		line("Сервер", a.PublicIP)
		if a.VPNIP != "" || a.SubIP != "" {
			line("IP", "VPN "+or(a.VPNIP, "все")+", панель "+or(a.SubIP, "все"))
		}
		line("Домен", domain+"  "+cache.check(domain, publicOf(a.VPNIP)))
		if a.SubDomain != "" {
			line("Домен ссылок", cleanDomain(a.SubDomain)+"  "+cache.check(a.SubDomain, publicOf(a.SubIP)))
		}
		if a.Mode == "aio" {
			line("В клиентах", or(a.Name, countryName(a.Country))+" "+a.Country)
			line("Ноды", map[bool]string{true: "можно подключать (порт 9443)", false: "только этот сервер"}[nodes])
		}
		line("Подписки", "https://"+or(cleanDomain(a.SubDomain), domain)+or(cleanPath(a.SubPath), "/s/")+"<токен>")
		switch a.LoginMode {
		case "link":
			line("Панель", "вход по одноразовой ссылке (бот, vynel admin login-link)"+map[bool]string{true: ", адрес случайный на каждую сессию", false: ""}[a.SessionPaths == "yes"])
		case "miniapp":
			line("Панель", "вход только через Telegram (кнопка «Панель» в боте)")
		default:
			line("Панель", "путь "+or(cleanPath(a.PanelPath), "случайный")+", логин "+a.AdminLogin+", пароль покажем в конце")
		}
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
		"SUB_PATH": a.SubPath, "PANEL_PATH": a.PanelPath, "LOGIN_MODE": a.LoginMode, "SESSION_PATHS": a.SessionPaths,
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
