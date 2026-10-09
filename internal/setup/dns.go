package setup

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// PublicDNS are asked directly: the system resolver also answers from /etc/hosts, where Debian
// maps the server's own name to 127.0.1.1, and a domain named like the server then "resolves"
// to loopback.
var PublicDNS = []string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:53"}

// ErrNoRecord means the domain has no A record.
var ErrNoRecord = errors.New("no A record")

// LookupA returns the A records of a domain as public DNS sees them.
func LookupA(ctx context.Context, domain string) ([]string, error) {
	name, err := dnsmessage.NewName(strings.TrimSuffix(domain, ".") + ".")
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, server := range PublicDNS {
		ips, err := queryA(ctx, server, name)
		if err == nil || errors.Is(err, ErrNoRecord) {
			return ips, err
		}
		lastErr = err
	}
	// No direct DNS (blocked port 53): the system resolver, without loopback answers.
	addrs, err := net.DefaultResolver.LookupHost(ctx, domain)
	if err != nil {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, err
	}
	var out []string
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil && !ip.IsLoopback() {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, ErrNoRecord
	}
	return out, nil
}

func queryA(ctx context.Context, server string, name dnsmessage.Name) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	id := uint16(time.Now().UnixNano())
	msg := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}
	q, err := msg.Pack()
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "udp", server)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	if _, err := c.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, 1500)
	n, err := c.Read(buf)
	if err != nil {
		return nil, err
	}
	var resp dnsmessage.Message
	if err := resp.Unpack(buf[:n]); err != nil {
		return nil, err
	}
	if resp.ID != id {
		return nil, errors.New("dns: mismatched reply")
	}
	if resp.RCode == dnsmessage.RCodeNameError {
		return nil, ErrNoRecord
	}
	if resp.RCode != dnsmessage.RCodeSuccess {
		return nil, errors.New("dns: " + resp.RCode.String())
	}
	var out []string
	for _, a := range resp.Answers {
		if r, ok := a.Body.(*dnsmessage.AResource); ok {
			out = append(out, net.IP(r.A[:]).String())
		}
	}
	if len(out) == 0 {
		return nil, ErrNoRecord
	}
	return out, nil
}

// cloudflare lists Cloudflare's proxy ranges (orange cloud): such a domain hides the server.
var cloudflare = []string{"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
	"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15",
	"104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22"}

// IsCloudflare reports whether an address belongs to Cloudflare's proxy.
func IsCloudflare(ip string) bool {
	p := net.ParseIP(ip)
	for _, c := range cloudflare {
		if _, n, err := net.ParseCIDR(c); err == nil && n.Contains(p) {
			return true
		}
	}
	return false
}

// DNSStatus is a domain checked against the address it must point at.
type DNSStatus struct {
	OK       bool
	Resolved []string
	Message  string // Russian, for the installer
}

// CheckDomain tells whether domain points at want, in words for the installer.
func CheckDomain(ctx context.Context, domain, want string) DNSStatus {
	ips, err := LookupA(ctx, domain)
	st := DNSStatus{Resolved: ips}
	switch {
	case errors.Is(err, ErrNoRecord):
		st.Message = "✗ у " + domain + " нет A-записи — создайте её на " + want
	case err != nil:
		st.Message = "? DNS не ответил: " + err.Error()
	default:
		for _, ip := range ips {
			if ip == want {
				st.OK = true
			}
		}
		switch {
		case st.OK:
			st.Message = "✓ " + domain + " → " + want
		case IsCloudflare(ips[0]):
			st.Message = "✗ " + domain + " за прокси Cloudflare (оранжевое облако): включите «DNS only» (серое облако), запись на " + want
		default:
			st.Message = "✗ " + domain + " → " + strings.Join(ips, ", ") + ", а нужно " + want
		}
	}
	return st
}
