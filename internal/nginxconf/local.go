package nginxconf

import (
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Local is what the agent knows about its own host.
type Local struct {
	Modules []string // full paths of dynamic modules to load (the stream module); empty = built in
	User    string   // worker user; "" = nginx's default
	Run     string   // runtime dir: the workers' temp files (they own it)
	State   string   // the master's pid and error log; "" = the run dir. Must not be writable by the workers
	Certs   string   // certificate store
	Decoy   string   // decoy sites
	ACME    string   // HTTP-01 webroot
	IPv6    bool     // the host can listen on IPv6
	// RejectHandshake: nginx 1.19.4+ can cut a handshake (ssl_reject_handshake). Older nginx
	// gets DummyCert/DummyKey, a self-signed certificate for no real name, and closes (444).
	RejectHandshake     bool
	DummyCert, DummyKey string
}

// Finalize fills the agent's tokens in a config built by Build.
func Finalize(conf string, l Local) string {
	if conf == "" {
		return ""
	}
	var mods []string
	for _, m := range l.Modules {
		mods = append(mods, "load_module "+m+";")
	}
	user := ""
	if l.User != "" {
		user = "user " + l.User + ";"
	}
	ipv6 := "# "
	if l.IPv6 {
		ipv6 = ""
	}
	reject := "ssl_reject_handshake on;"
	if !l.RejectHandshake {
		reject = "ssl_certificate " + l.DummyCert + ";\n        ssl_certificate_key " + l.DummyKey + ";"
	}
	if l.State != "" {
		// The master (root) writes these: keep them out of the run dir, which the workers own.
		conf = strings.Replace(conf, "pid "+TokRun+"/nginx.pid;", "pid "+l.State+"/nginx.pid;", 1)
		conf = strings.Replace(conf, "error_log "+TokRun+"/error.log ", "error_log "+l.State+"/error.log ", 1)
	}
	return strings.NewReplacer(
		TokModules, strings.Join(mods, "\n"),
		TokUser, user,
		TokRun, l.Run,
		TokCerts, l.Certs,
		TokDecoy, l.Decoy,
		TokACME, l.ACME,
		TokIPv6, ipv6,
		TokReject, reject,
	).Replace(conf)
}

var metaRe = regexp.MustCompile(`(?m)^# vynel-acme-(email|issuer): (.*)$`)

// ACME returns the certificate settings written into a config by Build.
func ACME(conf string) (email, issuer string) {
	issuer = "acme"
	for _, m := range metaRe.FindAllStringSubmatch(conf, -1) {
		switch m[1] {
		case "email":
			email = strings.TrimSpace(m[2])
		case "issuer":
			issuer = strings.TrimSpace(m[2])
		}
	}
	return email, issuer
}

var certRe = regexp.MustCompile(`(?m)^\s*ssl_certificate ` + regexp.QuoteMeta(TokCerts) + `/([^/\s]+)/fullchain\.pem;`)

// CertDomains lists the domains a config (before Finalize) needs certificates for.
func CertDomains(conf string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range certRe.FindAllStringSubmatch(conf, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

var listenRe = regexp.MustCompile(`(?m)^\s*(?:` + regexp.QuoteMeta(TokIPv6) + `)?listen (\S+?)[ ;]`)

// Listener is a public TCP port of a config.
type Listener struct {
	IP   string // "" = all addresses
	Port int
}

// PublicListeners lists the TCP ports a config (before or after Finalize) opens to the world.
func PublicListeners(conf string) []Listener {
	seen := map[Listener]bool{}
	var out []Listener
	for _, m := range listenRe.FindAllStringSubmatch(conf, -1) {
		addr := m[1]
		var l Listener
		if n, err := strconv.Atoi(addr); err == nil {
			l = Listener{Port: n}
		} else {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				continue
			}
			n, _ := strconv.Atoi(port)
			if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
				continue
			}
			if host == "::" || host == "0.0.0.0" {
				host = ""
			}
			l = Listener{IP: host, Port: n}
		}
		if l.Port > 0 && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}
