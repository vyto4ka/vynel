package service

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// KaringOutboundDNS normalizes the explicitly configured node-hostname resolvers.
// This is not the DNS for user traffic and is never inferred from DNSProxy.
func KaringOutboundDNS(servers []string) ([]string, error) {
	out := []string{}
	for _, server := range servers {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}
		if ip := net.ParseIP(server); ip != nil {
			host := server
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
			server = "udp://" + host
		}
		u, err := url.Parse(server)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(server, ",\r\n\t ") {
			return nil, invalid("Karing DNS: неверный резолвер %q", server)
		}
		switch u.Scheme {
		case "udp", "tcp", "tls", "https":
		default:
			return nil, invalid("Karing DNS: укажите IP или udp://, tcp://, tls://, https://")
		}
		if u.Scheme != "https" && (u.Path != "" || u.RawQuery != "") {
			return nil, invalid("Karing DNS: неверный резолвер %q", server)
		}
		if u.Port() != "" {
			port, err := strconv.Atoi(u.Port())
			if err != nil || port < 1 || port > 65535 {
				return nil, invalid("Karing DNS: неверный порт")
			}
		}
		out = append(out, server)
	}
	if len(out) > 4 {
		return nil, invalid("Karing DNS: не больше 4 резолверов")
	}
	return out, nil
}

const DefaultKaringLink = "karing://install-config?url={url_enc}&name={title_query}&x-hwid=true&outbound-dns={karing_dns}"

func upgradeKaringLink(link string) string {
	switch link {
	case "karing://install-config?url={url_enc}&name={title_url}",
		"karing://install-config?url={url_enc}&name={title_url}&x-hwid=true",
		"karing://install-config?url={url_enc}&name={title_query}&x-hwid=true":
		return DefaultKaringLink
	}
	return link
}
