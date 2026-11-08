package xrayconf

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Remnawave import: turns a Remnawave-style Xray config (a full config with an "inbounds" list,
// or a single inbound object — what Remnawave calls a Config Profile) into a vynel profile
// template, together with an optional subscription host config and an optional nginx config
// (the "server"/"location" directives go into the template's nginx section).
//
// The output template is checked by ParseTemplate + SampleRender before it is returned, so a
// successful import is always a template the panel can actually use.

// RemnawaveImport is the input of the converter.
type RemnawaveImport struct {
	Xray         string // required: the Xray JSON config (full, array of inbounds, or a single inbound)
	Host         string // optional: the subscription host JSON (Remnawave Host entity)
	Nginx        string // optional: nginx directives; empty = no nginx section
	WithNginx    bool   // whether to include the nginx section (the UI checkbox)
	InboundTag   string // optional: select a specific inbound by tag when multiple inbounds exist
	InboundIndex *int   // optional: select a specific inbound by 0-based index (after filtering dokodemo-door)
	KeepKeys     bool   // preserve existing Reality privateKey, shortIds and XHTTP/WS path as defaults
}

// RemnawaveResult is the produced template: the produced YAML source plus what the UI reports.
type RemnawaveResult struct {
	Source string   // the template YAML
	ID     string   // the suggested template id
	Notes  []string // what the converter did (renames, guessed values)
}

var (
	templateIDClean = regexp.MustCompile(`[^a-z0-9-]+`)
	domainRe2       = regexp.MustCompile(`^(?i)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// ImportRemnawave converts the configs into a template. The template's id is derived from
// titleBase (the UI passes a free-form title) and made unique by the caller if needed.
func ImportRemnawave(in RemnawaveImport, titleBase string) (*RemnawaveResult, error) {
	if strings.TrimSpace(in.Xray) == "" {
		return nil, fmt.Errorf("пустой Xray-конфиг")
	}
	var root any
	if err := json.Unmarshal([]byte(in.Xray), &root); err != nil {
		return nil, fmt.Errorf("не удалось разобрать Xray-конфиг: %w", err)
	}
	notes := []string{}
	rootMap, _ := root.(map[string]any)
	rootArr, isArr := root.([]any)
	var inbound map[string]any
	switch {
	case (rootMap != nil && rootMap["inbounds"] != nil) || isArr:
		var ins []any
		if isArr {
			ins = rootArr
		} else {
			ins, _ = rootMap["inbounds"].([]any)
		}
		if len(ins) == 0 {
			return nil, fmt.Errorf("в Xray-конфиге нет inbounds")
		}
		filtered := make([]map[string]any, 0, len(ins))
		skippedDokodemo := false
		for _, raw := range ins {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if proto, _ := m["protocol"].(string); proto == "dokodemo-door" && len(ins) > 1 {
				skippedDokodemo = true
				continue
			}
			filtered = append(filtered, m)
		}
		if skippedDokodemo {
			notes = append(notes, "служебный инбаунд dokodemo-door пропущен — панель добавляет секцию API сама")
		}
		if len(filtered) == 0 {
			inbound, _ = ins[0].(map[string]any)
		} else if tag := strings.TrimSpace(in.InboundTag); tag != "" {
			for _, m := range filtered {
				if t, _ := m["tag"].(string); t == tag {
					inbound = m
					break
				}
			}
			if inbound == nil {
				return nil, fmt.Errorf("инбаунд с тегом %s не найден в Xray-конфиге", tag)
			}
		} else if in.InboundIndex != nil {
			idx := *in.InboundIndex
			if idx < 0 || idx >= len(filtered) {
				return nil, fmt.Errorf("инбаунд с индексом %d не найден в Xray-конфиге", idx)
			}
			inbound = filtered[idx]
		} else if len(filtered) > 1 {
			return nil, fmt.Errorf("в Xray-конфиге %d инбаундов; шаблон vynel описывает один — оставьте основной, остальные игнорируются панелью (системные секции добавляются сами)", len(filtered))
		} else {
			inbound = filtered[0]
		}
	case rootMap != nil && rootMap["protocol"] != nil:
		inbound = rootMap
	default:
		return nil, fmt.Errorf("это не Xray-конфиг: нет ни inbounds, ни протокола инбаунда")
	}
	if inbound == nil {
		return nil, fmt.Errorf("инбаунд в Xray-конфиге не является объектом")
	}

	variables := []Variable{}
	vars := map[string]bool{}

	addVar := func(v Variable) {
		if vars[v.Name] {
			return
		}
		vars[v.Name] = true
		variables = append(variables, v)
	}

	// The inbound is rewritten in place: known per-node or shared values become ${VARIABLES},
	// everything the panel must own (tag, clients, listen) is forced.
	norm := normalizeTree(inbound).(map[string]any)
	protocol, _ := norm["protocol"].(string)
	if protocol == "" {
		return nil, fmt.Errorf("у инбаунда нет protocol")
	}
	if protocol == "vmess" {
		return nil, fmt.Errorf("протокол vmess не поддерживается подписками vynel (vless, hysteria)")
	}

	// tag: the panel owns it.
	norm["tag"] = "${TAG}"

	// listen: keep what the config says, but 0.0.0.0 is the built-in default.
	if listen, _ := norm["listen"].(string); listen == "" || listen == "0.0.0.0" {
		norm["listen"] = "${LISTEN_IP}"
	}

	// port: a profile const so it can be changed in one place; a non-443 value is kept.
	portVar := Variable{Name: "PORT", Scope: ScopeProfile, Source: SourceConst, Default: 443, Validate: "port",
		Description: "Порт инбаунда снаружи."}
	if p, ok := norm["port"].(int); ok && p > 0 {
		portVar.Default = p
	}
	norm["port"] = "${PORT}"
	addVar(portVar)

	ss, _ := norm["streamSettings"].(map[string]any)
	if ss == nil {
		ss = map[string]any{}
	}
	security, _ := ss["security"].(string)

	// REALITY: per-node keys and SNI. The whole settings object can be replaced.
	xhttpExtra := ""
	if rs, ok := ss["realitySettings"].(map[string]any); ok {
		// serverNames -> ${NODE_DOMAIN}; extra names stay literal (fixed SNI names).
		var snDomain string
		keepNames := []any{}
		if sns, ok := rs["serverNames"].([]any); ok {
			for _, s := range sns {
				if str, ok := s.(string); ok && str != "" {
					if snDomain == "" && domainRe2.MatchString(str) {
						snDomain = str
					} else {
						keepNames = append(keepNames, str)
					}
				}
			}
		}
		if snDomain == "" {
			return nil, fmt.Errorf("в realitySettings нет домена в serverNames — шаблону нужен ${NODE_DOMAIN}")
		}
		rs["serverNames"] = append([]any{"${NODE_DOMAIN}"}, keepNames...)
		addVar(Variable{Name: "NODE_DOMAIN", Scope: ScopeNode, Source: SourceInput, DefaultFrom: "node.domain", Validate: "domain",
			Description: "Домен ноды (A-запись на IP инбаунда). Идёт в serverNames и SNI."})
		if len(keepNames) > 0 {
			notes = append(notes, fmt.Sprintf("serverNames: %s заменён доменом ноды, остальные имена оставлены как есть (%s)", snDomain, strings.Join(stringList(keepNames), ", ")))
		}

		// privateKey: generated per node (or preserved as default if KeepKeys is set); publicKey derived.
		if rawPriv, has := rs["privateKey"]; has {
			origPriv, _ := rawPriv.(string)
			origPriv = strings.TrimSpace(origPriv)
			rs["privateKey"] = "${REALITY_PRIVATE_KEY}"
			if in.KeepKeys && origPriv != "" {
				if _, err := X25519Public(origPriv); err == nil {
					addVar(Variable{Name: "REALITY_PRIVATE_KEY", Scope: ScopeNode, Source: SourceConst, Default: origPriv, Secret: true,
						Description: "Приватный ключ REALITY, свой на каждой ноде."})
					addVar(Variable{Name: "REALITY_PUBLIC_KEY", Scope: ScopeNode, Source: SourceDerived, From: "x25519_public(REALITY_PRIVATE_KEY)",
						Description: "Публичный ключ (password / pbk), уходит в подписку."})
					notes = append(notes, "оригинальный privateKey сохранён как значение по умолчанию ${REALITY_PRIVATE_KEY}")
				}
			}
			if !vars["REALITY_PRIVATE_KEY"] {
				addVar(Variable{Name: "REALITY_PRIVATE_KEY", Scope: ScopeNode, Source: SourceGenerate, Generator: "x25519", Secret: true,
					Description: "Приватный ключ REALITY, свой на каждой ноде."})
				addVar(Variable{Name: "REALITY_PUBLIC_KEY", Scope: ScopeNode, Source: SourceDerived, From: "x25519_public(REALITY_PRIVATE_KEY)",
					Description: "Публичный ключ (password / pbk), уходит в подписку."})
				notes = append(notes, "privateKey заменён на генерируемый ${REALITY_PRIVATE_KEY} — оригинальный ключ не переносится")
			}
		}
		// shortIds: generated per node (or preserved as default if KeepKeys is set).
		if rawSids, has := rs["shortIds"]; has {
			var keptSids []string
			if in.KeepKeys {
				if arr, ok := rawSids.([]any); ok {
					keptSids = stringList(arr)
				}
			}
			if len(keptSids) > 0 {
				outSids := []any{"${REALITY_SHORT_ID}"}
				for _, extra := range keptSids[1:] {
					outSids = append(outSids, extra)
				}
				rs["shortIds"] = outSids
				addVar(Variable{Name: "REALITY_SHORT_ID", Scope: ScopeNode, Source: SourceConst, Default: keptSids[0],
					Description: "shortId (8 байт hex)."})
				notes = append(notes, "оригинальный shortId сохранён как значение по умолчанию ${REALITY_SHORT_ID}")
			} else {
				rs["shortIds"] = []any{"${REALITY_SHORT_ID}"}
				addVar(Variable{Name: "REALITY_SHORT_ID", Scope: ScopeNode, Source: SourceGenerate, Generator: "shortid",
					Description: "shortId (8 байт hex)."})
				notes = append(notes, "shortIds заменён на генерируемый ${REALITY_SHORT_ID}")
			}
		}
		// target/dest with 127.0.0.1:<port> is a self-steal: expose the port as a variable.
		for _, key := range []string{"target", "dest"} {
			if str, ok := rs[key].(string); ok {
				if host, port, err := splitHostPort(str); err == nil && (host == "127.0.0.1" || host == "localhost") {
					rs[key] = "127.0.0.1:${SELFSTEAL_PORT}"
					addVar(Variable{Name: "SELFSTEAL_PORT", Scope: ScopeProfile, Source: SourceConst, Default: port, Validate: "port",
						Description: "Локальный порт nginx с сайтом за REALITY."})
					notes = append(notes, "target на 127.0.0.1 вынесен в SELFSTEAL_PORT (self-steal)")
				}
			}
		}
		// Users write vless://-compatible values; pbk/password is client-side only.
		delete(rs, "publicKey")
		delete(rs, "password")
		delete(rs, "mldsa65Seed")
		delete(rs, "mldsa65Verify")
	}

	// TLS settings: certificateReference -> per-node certificate tokens.
	if ts, ok := ss["tlsSettings"].(map[string]any); ok {
		for _, key := range []string{"certificates", "certificate"} {
			if _, has := ts[key]; has {
				notes = append(notes, "сертификаты в tlsSettings не переносятся: нода получает свой сертификат от nginx панели")
			}
		}
		delete(ts, "certificates")
		delete(ts, "certificate")
	}

	// XHTTP: path becomes a generated profile variable (or a constant default when KeepKeys is set).
	if xs, ok := ss["xhttpSettings"].(map[string]any); ok {
		if path, ok := xs["path"].(string); ok && path != "" {
			xs["path"] = "${XHTTP_PATH}"
			if in.KeepKeys {
				addVar(Variable{Name: "XHTTP_PATH", Scope: ScopeProfile, Source: SourceConst, Default: path, Validate: "path",
					Description: "Путь XHTTP без завершающего слеша. Одинаковый на сервере и в подписке."})
				notes = append(notes, "оригинальный путь XHTTP сохранён в переменной XHTTP_PATH")
			} else {
				addVar(Variable{Name: "XHTTP_PATH", Scope: ScopeProfile, Source: SourceGenerate, Generator: "path",
					Options: []string{path, "/api/stream", "/static/chunks", "/media/feed"}, Validate: "path",
					Description: "Путь XHTTP без завершающего слеша. Одинаковый на сервере и в подписке."})
				notes = append(notes, "путь XHTTP вынесен в генерируемую переменную XHTTP_PATH (исходный — в options)")
			}
		}
		if extra, ok := xs["extra"].(string); ok && strings.TrimSpace(extra) != "" {
			// The extra JSON may itself contain ${} placeholders after reality handling; keep it
			// as the xhttp_extra block so the client side mirrors the server.
			var extraObj any
			if err := json.Unmarshal([]byte(extra), &extraObj); err == nil {
				xs["extra"] = "${XHTTP_EXTRA}"
				extraSrc, _ := json.MarshalIndent(extraObj, "", "  ")
				xhttpExtra = string(extraSrc)
			}
		}
	}

	// WebSocket and other paths: same treatment, simpler generator list.
	if ws, ok := ss["wsSettings"].(map[string]any); ok {
		if path, ok := ws["path"].(string); ok && path != "" && !strings.Contains(path, "${") {
			ws["path"] = "${XHTTP_PATH}"
			if in.KeepKeys {
				addVar(Variable{Name: "XHTTP_PATH", Scope: ScopeProfile, Source: SourceConst, Default: path, Validate: "path",
					Description: "Путь WebSocket без завершающего слеша."})
				notes = append(notes, "оригинальный путь WebSocket сохранён в переменной XHTTP_PATH")
			} else {
				addVar(Variable{Name: "XHTTP_PATH", Scope: ScopeProfile, Source: SourceGenerate, Generator: "path",
					Options: []string{path, "/api/stream", "/static/chunks"}, Validate: "path",
					Description: "Путь WebSocket без завершающего слеша."})
				notes = append(notes, "путь WebSocket вынесен в переменную XHTTP_PATH")
			}
		}
	}

	// clients: the panel injects users; an empty list is forced.
	if settings, ok := norm["settings"].(map[string]any); ok {
		delete(settings, "clients")
		settings["clients"] = []any{}
	}

	// ---- the host section (subscription) ----
	var host map[string]any
	if strings.TrimSpace(in.Host) != "" {
		var h any
		if err := json.Unmarshal([]byte(in.Host), &h); err != nil {
			return nil, fmt.Errorf("конфиг подписки: %w", err)
		}
		if hm, ok := h.(map[string]any); ok {
			host = hm
		} else {
			return nil, fmt.Errorf("конфиг подписки должен быть JSON-объектом")
		}
	}
	host = hostFromInbound(host, norm, security, addVar, &notes)

	// ---- the nginx section (optional) ----
	var nginx map[string]any
	if in.WithNginx && strings.TrimSpace(in.Nginx) != "" {
		nginx = nginxFromConfig(in.Nginx, addVar, &notes)
	}

	// ---- assemble the template ----
	id := templateID(titleBase, protocol)
	title := titleBase
	if strings.TrimSpace(title) == "" {
		title = "Импорт из Remnawave (" + strings.ToUpper(protocol) + ")"
	}
	flow := flowOf(norm, protocol)

	var b strings.Builder
	b.WriteString("# Импортирован из конфига Remnawave.\n")
	b.WriteString("# ${NODE_DOMAIN} подставляется доменом ноды, ключи REALITY генерируются панелью,\n")
	b.WriteString("# пользователи добавляются в clients автоматически.\n")
	fmt.Fprintf(&b, "id: %s\n", id)
	b.WriteString("version: 1\n")
	fmt.Fprintf(&b, "title: %s\n", yamlQuote(title))
	fmt.Fprintf(&b, "summary: %s\n", yamlQuote("Импорт из Remnawave: "+strings.ToUpper(protocol)+securitySummary(security)+"."))
	fmt.Fprintf(&b, "tag_pattern: IMP_%s_${NODE_CODE}\n", strings.ToUpper(protocol))
	b.WriteString("remark_pattern: ${NODE_FLAG} ${NODE_NAME} · " + strings.ToUpper(protocol) + "\n")
	fmt.Fprintf(&b, "protocol: %s\n", protocol)
	b.WriteString("client:\n")
	fmt.Fprintf(&b, "  flow: %q\n", flow)

	b.WriteString("\nvariables:\n")
	for _, v := range variables {
		b.WriteString("  - name: " + v.Name + "\n")
		fmt.Fprintf(&b, "    scope: %s\n", v.Scope)
		fmt.Fprintf(&b, "    source: %s\n", v.Source)
		if v.Generator != "" {
			fmt.Fprintf(&b, "    generator: %s\n", v.Generator)
		}
		if len(v.Options) > 0 {
			opts := make([]string, len(v.Options))
			for i, o := range v.Options {
				opts[i] = fmt.Sprintf("%q", o)
			}
			b.WriteString("    options: [" + strings.Join(opts, ", ") + "]\n")
		}
		if v.Default != nil {
			if s, ok := v.Default.(string); ok {
				fmt.Fprintf(&b, "    default: %s\n", yamlQuote(s))
			} else {
				fmt.Fprintf(&b, "    default: %v\n", v.Default)
			}
		}
		if v.DefaultFrom != "" {
			fmt.Fprintf(&b, "    default_from: %s\n", v.DefaultFrom)
		}
		if v.From != "" {
			fmt.Fprintf(&b, "    from: %s\n", v.From)
		}
		if v.Secret {
			b.WriteString("    secret: true\n")
		}
		if v.Validate != "" {
			fmt.Fprintf(&b, "    validate: %s\n", v.Validate)
		}
		if v.Description != "" {
			fmt.Fprintf(&b, "    description: %s\n", yamlQuote(v.Description))
		}
	}

	if xhttpExtra != "" {
		b.WriteString("\nxhttp_extra: |\n")
		writeIndented(&b, xhttpExtra, 2)
	}

	src, _ := json.MarshalIndent(norm, "", "  ")
	b.WriteString("\nxray_inbound: |\n")
	writeIndented(&b, string(src), 2)

	if host != nil {
		hs, _ := json.Marshal(host)
		// Host is a map, not a string: emit it as a YAML flow mapping with placeholders intact.
		b.WriteString("\nhost: " + jsonToYamlFlow(host) + "\n")
		_ = hs
	}

	if nginx != nil {
		b.WriteString("\nnginx:\n")
		var keys []string
		for k := range nginx {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch v := nginx[k].(type) {
			case string:
				fmt.Fprintf(&b, "  %s: %s\n", k, yamlQuote(v))
			default:
				vs, _ := json.Marshal(v)
				fmt.Fprintf(&b, "  %s: %s\n", k, vs)
			}
		}
	}

	// Ports the inbound needs on the node.
	b.WriteString("\nnode_requirements:\n")
	ports := "443/tcp, 80/tcp"
	portsVar, _ := portVar.Default.(int)
	if portsVar != 443 {
		ports = fmt.Sprintf("%d/tcp, 80/tcp", portsVar)
	}
	fmt.Fprintf(&b, "  ports: [%s]\n", ports)
	if vars["NODE_DOMAIN"] {
		b.WriteString("  dns: [{ type: A, name: \"${NODE_DOMAIN}\" }]\n")
	}

	// Validate: the produced template must parse and sample-render.
	source := b.String()
	if os.Getenv("VYNEL_DEBUG_IMPORT") != "" {
		fmt.Println(source)
	}
	if _, err := ParseTemplate([]byte(source)); err != nil {
		if os.Getenv("VYNEL_DEBUG_IMPORT") != "" {
			_ = os.WriteFile("remna-debug.yml", []byte(source), 0644)
		}
		return &RemnawaveResult{Source: source, ID: id, Notes: notes}, fmt.Errorf("сгенерированный шаблон не собрался: %w", err)
	}
	if _, err := SampleRender(mustParse(source)); err != nil {
		return &RemnawaveResult{Source: source, ID: id, Notes: notes}, fmt.Errorf("сгенерированный шаблон не прошёл пробную сборку: %w", err)
	}
	return &RemnawaveResult{Source: source, ID: id, Notes: notes}, nil
}

// mustParse parses a template that ParseTemplate already accepted.
func mustParse(source string) *Template {
	t, _ := ParseTemplate([]byte(source))
	return t
}

func stringList(xs []any) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func splitHostPort(s string) (string, int, error) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("no port")
	}
	port := 0
	for _, c := range s[i+1:] {
		if c < '0' || c > '9' {
			return "", 0, fmt.Errorf("bad port")
		}
		port = port*10 + int(c-'0')
	}
	return s[:i], port, nil
}

// hostFromInbound builds the host section: the given host config wins, missing connection
// points are derived from the inbound.
func hostFromInbound(host map[string]any, inbound map[string]any, security string, addVar func(Variable), notes *[]string) map[string]any {
	if host == nil {
		host = map[string]any{}
	}
	if host["address"] == nil {
		host["address"] = "${NODE_DOMAIN}"
		addVar(Variable{Name: "NODE_DOMAIN", Scope: ScopeNode, Source: SourceInput, DefaultFrom: "node.domain", Validate: "domain",
			Description: "Домен ноды (A-запись на IP инбаунда), адрес точки подключения."})
	}
	if host["port"] == nil {
		host["port"] = "${INBOUND_PORT}"
	}
	switch security {
	case "reality":
		if host["security"] == nil {
			host["security"] = "reality"
		}
		if host["sni"] == nil {
			host["sni"] = "${NODE_DOMAIN}"
		}
		if host["public_key"] == nil {
			host["public_key"] = "${REALITY_PUBLIC_KEY}"
		}
		if host["short_id"] == nil {
			host["short_id"] = "${REALITY_SHORT_ID}"
		}
		if host["fingerprint"] == nil {
			host["fingerprint"] = "${FINGERPRINT}"
			addVar(Variable{Name: "FINGERPRINT", Scope: ScopeProfile, Source: SourceConst, Default: "chrome",
				Options:     []string{"chrome", "firefox", "safari", "ios", "android", "edge", "random", "randomized"},
				Description: "TLS-отпечаток клиента (uTLS). Уходит в подписку."})
		}
	case "tls":
		if host["security"] == nil {
			host["security"] = "tls"
		}
		if host["sni"] == nil {
			host["sni"] = "${NODE_DOMAIN}"
		}
	}
	ss, _ := inbound["streamSettings"].(map[string]any)
	if _, ok := ss["xhttpSettings"].(map[string]any); ok {
		if host["path"] == nil {
			host["path"] = "${XHTTP_PATH}/"
		}
	} else if _, ok := ss["wsSettings"].(map[string]any); ok {
		if host["path"] == nil {
			host["path"] = "${XHTTP_PATH}"
		}
	}
	if len(host) == 0 {
		return nil
	}
	return host
}

// nginxFromConfig turns the given nginx directives into the template's nginx section. Remnawave
// runs Xray behind the system nginx; vynel's nginx roles cover the common shapes:
//   - a reverse proxy over an XHTTP path (upstream + path),
//   - anything else goes in as raw location/server directives.
func nginxFromConfig(conf string, addVar func(Variable), notes *[]string) map[string]any {
	out := map[string]any{}
	text := strings.TrimSpace(conf)
	if text == "" {
		return nil
	}
	// proxy_pass <host:port><path> — the shape of a Remnawave XHTTP site.
	proxyRe := regexp.MustCompile(`(?m)^\s*proxy_pass\s+http://([^;]+);`)
	locRe := regexp.MustCompile(`(?m)^\s*location\s+(\S+)\s*{`)
	serverNameRe := regexp.MustCompile(`(?m)^\s*server_name\s+([^;]+);`)

	upstream := ""
	if m := proxyRe.FindStringSubmatch(text); m != nil {
		target := strings.TrimSpace(m[1])
		host, port, err := splitHostPort(target)
		if err == nil && (host == "127.0.0.1" || host == "localhost") {
			upstream = "127.0.0.1:${XRAY_PORT}"
			out["upstream"] = upstream
			addVar(Variable{Name: "XRAY_PORT", Scope: ScopeProfile, Source: SourceConst, Default: port, Validate: "port",
				Description: "Локальный порт Xray за nginx, наружу не открывается."})
			*notes = append(*notes, fmt.Sprintf("proxy_pass на 127.0.0.1:%d вынесен в переменную XRAY_PORT", port))
		}
	}
	if m := locRe.FindStringSubmatch(text); m != nil && upstream != "" {
		path := strings.TrimSuffix(m[1], "/")
		if strings.HasPrefix(path, "/") && !strings.Contains(path, "${") {
			out["path"] = path
		}
	}
	if m := serverNameRe.FindStringSubmatch(text); m != nil {
		name := strings.Fields(strings.TrimSpace(m[1]))[0]
		if domainRe2.MatchString(name) {
			out["domain"] = "${NODE_DOMAIN}"
			*notes = append(*notes, fmt.Sprintf("server_name %s заменён на ${NODE_DOMAIN}", name))
		}
	}
	if upstream != "" {
		out["role"] = "reverse_proxy_xhttp"
		if out["path"] == nil {
			out["path"] = "${XHTTP_PATH}"
		}
		if out["domain"] == nil {
			out["domain"] = "${NODE_DOMAIN}"
		}
		return out
	}
	// No recognizable proxy: keep the directives as the raw server block.
	out["role"] = "reverse_proxy_xhttp"
	out["domain"] = "${NODE_DOMAIN}"
	out["path"] = "${XHTTP_PATH}"
	out["server"] = text
	*notes = append(*notes, "nginx-конфиг не похож на proxy_pass на Xray: вставлен целиком в nginx.server, проверьте его руками")
	return out
}

func templateID(base, protocol string) string {
	id := strings.ToLower(strings.TrimSpace(base))
	id = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r == ' ', r == '_', r == '.':
			return '-'
		}
		return -1
	}, id)
	id = templateIDClean.ReplaceAllString(id, "-")
	id = strings.Trim(id, "-")
	if id == "" {
		id = "remna-" + protocol
	}
	return id
}

func yamlQuote(s string) string {
	if s == "" {
		return `""`
	}
	return fmt.Sprintf("%q", s)
}

func writeIndented(b *strings.Builder, text string, indent int) {
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(strings.Repeat(" ", indent) + line + "\n")
	}
}

func securitySummary(security string) string {
	switch security {
	case "reality":
		return "+ REALITY"
	case "tls":
		return "+ TLS"
	case "":
		return ""
	default:
		return "+" + security
	}
}

func flowOf(inbound map[string]any, protocol string) string {
	if protocol != "vless" {
		return ""
	}
	// Reality over raw: vision flow; otherwise no flow.
	ss, _ := inbound["streamSettings"].(map[string]any)
	security, _ := ss["security"].(string)
	method, _ := ss["network"].(string)
	if _, has := ss["method"]; has {
		method, _ = ss["method"].(string)
	}
	if security == "reality" && (method == "" || method == "raw" || method == "tcp") {
		return "xtls-rprx-vision"
	}
	return ""
}

// jsonToYamlFlow renders a JSON tree as a single-line YAML flow mapping, keeping the
// ${VAR} placeholders inside strings. Values that YAML would misread are quoted.
func jsonToYamlFlow(v any) string {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+": "+jsonToYamlFlow(x[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []any:
		parts := make([]string, 0, len(x))
		for _, i := range x {
			parts = append(parts, jsonToYamlFlow(i))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case string:
		if placeholderRe.MatchString(x) || strings.ContainsAny(x, ":,{}[]#&*!|>'\"%@`") || x == "" || strings.HasPrefix(x, " ") {
			return fmt.Sprintf("%q", x)
		}
		return x
	case bool:
		return fmt.Sprint(x)
	case int:
		return fmt.Sprint(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
