package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// Checklist step states.
const (
	StepOK   = "ok"
	StepWait = "wait" // not yet: an earlier step is not done or the node is still working on it
	StepFail = "fail"
	StepSkip = "skip" // does not apply to this node
)

// Step is one line of the "add a server" checklist.
type Step struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// SiteCheck opens https://domain/ and reports whether it answers with a valid certificate;
// tests replace it.
var SiteCheck = func(ctx context.Context, domain string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	c := &http.Client{
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/", nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// siteError explains a failed SiteCheck in plain words.
func siteError(err error) string {
	var cert *tls.CertificateVerificationError
	var dns *net.DNSError
	var op *net.OpError
	switch {
	case errors.As(err, &cert):
		return "сертификат не подходит: Caddy ещё получает его (до пары минут) или порт 80 закрыт"
	case errors.As(err, &dns):
		return "домен не находится в DNS"
	case errors.Is(err, context.DeadlineExceeded):
		return "сервер не ответил за 8 секунд: порт 443 закрыт фаерволом или у хостера"
	case errors.As(err, &op):
		return "не удалось подключиться: " + op.Err.Error()
	}
	return err.Error()
}

// NodeChecklist is the live checklist of a node being added: from the issued command to a
// site that opens with a valid certificate (docs/ROADMAP.md, stage 7).
func (s *Service) NodeChecklist(ctx context.Context, id int64, connected func(int64) bool) ([]Step, error) {
	sts, err := s.NodeStatuses(ctx, connected)
	if err != nil {
		return nil, err
	}
	var ns *NodeStatus
	for i := range sts {
		if sts[i].Node.ID == id {
			ns = &sts[i]
		}
	}
	if ns == nil {
		return nil, ErrNotFound
	}
	n := ns.Node
	steps := []Step{}
	add := func(key, title, state, detail string) bool {
		steps = append(steps, Step{Key: key, Title: title, State: state, Detail: detail})
		return state == StepOK || state == StepSkip
	}
	waitRest := func(rest ...[2]string) []Step {
		for _, r := range rest {
			steps = append(steps, Step{Key: r[0], Title: r[1], State: StepWait})
		}
		return steps
	}
	rest := [][2]string{{"online", "Нода на связи с панелью"}, {"applied", "Конфигурация применена"}, {"xray", "Xray запущен"},
		{"caddy", "Caddy запущен"}, {"dns", "Домен указывает на сервер"}, {"site", "Сайт ноды открывается с настоящим сертификатом"}, {"inbounds", "Есть профиль для пользователей"}}

	// 1. The command was run: the node registered with its token.
	if n.Local {
		add("joined", "Нода зарегистрирована", StepOK, "сервер панели")
	} else if n.CertSerial != "" {
		add("joined", "Команда выполнена, нода зарегистрирована", StepOK, "")
	} else {
		detail, state := "выполните команду на новом сервере", StepWait
		if t, err := store.LatestInstallToken(ctx, s.st.DB, n.ID); err == nil && t.UsedAt == nil && t.ExpiresAt < s.now().Unix() {
			detail, state = "токен истёк — выдайте новую команду", StepFail
		} else if errors.Is(err, store.ErrNotFound) {
			detail = "команды ещё нет — выдайте её кнопкой «Команда подключения»"
		}
		add("joined", "Команда выполнена, нода зарегистрирована", state, detail)
		return waitRest(rest...), nil
	}
	// 2. Connected now.
	if !n.Enabled {
		add("online", rest[0][1], StepFail, "нода выключена в панели")
		return waitRest(rest[1:]...), nil
	}
	if !ns.Connected {
		detail := "агент не подключён: на ноде journalctl -u vynel-node -n 50"
		if n.LastSeenAt != nil {
			detail = fmt.Sprintf("последний раз на связи %s назад; на ноде journalctl -u vynel-node -n 50", time.Since(time.Unix(*n.LastSeenAt, 0)).Round(time.Second))
		}
		add("online", rest[0][1], StepWait, detail)
		return waitRest(rest[1:]...), nil
	}
	add("online", rest[0][1], StepOK, n.AgentVersion)
	// 3. The desired config is applied.
	switch {
	case n.LastError != "":
		add("applied", rest[1][1], StepFail, n.LastError)
		return waitRest(rest[2:]...), nil
	case n.AppliedHash == "" || n.AppliedHash != n.DesiredHash:
		add("applied", rest[1][1], StepWait, "нода применяет конфигурацию")
		return waitRest(rest[2:]...), nil
	}
	add("applied", rest[1][1], StepOK, strings.Join(n.Warnings, "; "))
	// 4–5. Xray and Caddy report their versions once they run.
	if n.XrayVersion != "" {
		add("xray", rest[2][1], StepOK, n.XrayVersion)
	} else {
		add("xray", rest[2][1], StepWait, "Xray ещё не сообщил версию")
	}
	needCaddy := n.Domain != ""
	switch {
	case !needCaddy:
		add("caddy", rest[3][1], StepSkip, "у ноды нет домена")
	case n.CaddyVersion != "":
		add("caddy", rest[3][1], StepOK, n.CaddyVersion)
	default:
		add("caddy", rest[3][1], StepWait, "Caddy ещё не запущен")
	}
	// 6. DNS of the node's domain.
	dnsOK := true
	if n.Domain == "" {
		add("dns", rest[4][1], StepSkip, "у ноды нет домена")
	} else {
		addrs, err := store.ListAddresses(ctx, s.st.DB, n.ID)
		if err != nil {
			return nil, err
		}
		var want []string
		for _, a := range addrs {
			want = append(want, a.IP)
		}
		lctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		ips, err := Resolver.LookupHost(lctx, n.Domain)
		cancel()
		switch {
		case err != nil:
			dnsOK = add("dns", rest[4][1], StepFail, n.Domain+" не находится в DNS: добавьте A-запись на IP сервера")
		case len(want) > 0 && !slices.ContainsFunc(ips, func(ip string) bool { return slices.Contains(want, ip) }):
			dnsOK = add("dns", rest[4][1], StepFail, fmt.Sprintf("%s → %s, а у сервера %s (в Cloudflare нужно серое облако)", n.Domain, strings.Join(ips, ", "), strings.Join(want, ", ")))
		default:
			add("dns", rest[4][1], StepOK, n.Domain+" → "+strings.Join(ips, ", "))
		}
	}
	// 7. The site opens from outside with a real certificate.
	switch {
	case n.Domain == "":
		add("site", rest[5][1], StepSkip, "у ноды нет домена")
	case !dnsOK || n.CaddyVersion == "":
		add("site", rest[5][1], StepWait, "")
	default:
		if err := SiteCheck(ctx, n.Domain); err != nil {
			add("site", rest[5][1], StepFail, siteError(err))
		} else {
			add("site", rest[5][1], StepOK, "https://"+n.Domain+"/")
		}
	}
	// 8. Users get something from this node.
	nis, err := store.ListNodeInbounds(ctx, s.st.DB, n.ID, 0)
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, ni := range nis {
		if ni.Enabled {
			tags = append(tags, ni.Tag)
		}
	}
	if len(tags) == 0 {
		add("inbounds", rest[6][1], StepFail, "добавьте профиль: «Ноды» → «+ Профиль»")
	} else {
		add("inbounds", rest[6][1], StepOK, strings.Join(tags, ", "))
	}
	return steps, nil
}
