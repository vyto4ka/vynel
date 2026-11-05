package service

import (
	"slices"
	"testing"
)

func TestKaringOutboundDNS(t *testing.T) {
	got, err := KaringOutboundDNS([]string{" 8.8.8.8 ", "", "2001:4860:4860::8888", "https://dns.google/dns-query", "tls://dns.example:853"})
	want := []string{"udp://8.8.8.8", "udp://[2001:4860:4860::8888]", "https://dns.google/dns-query", "tls://dns.example:853"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range [][]string{
		{"localhost"}, {"file:///etc/passwd"}, {"https://user:pass@dns.example/query"},
		{"udp://8.8.8.8:0"}, {"udp://8.8.8.8:65536"}, {"https://dns.example/query#fragment"},
		{"udp://8.8.8.8,udp://1.1.1.1"}, {"udp://8.8.8.8/path"}, {"https://dns.example/\nquery"},
		{"1.1.1.1", "8.8.8.8", "9.9.9.9", "8.8.4.4", "1.0.0.1"},
	} {
		if _, err := KaringOutboundDNS(bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}
