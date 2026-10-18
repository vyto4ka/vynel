package jointoken

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	in := Token{Addr: "panel.example.com:9443", SNI: "gw-abc.internal", CAFingerprint: strings.Repeat("a", 64), Secret: "s3cret"}
	out, err := Parse(in.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("got %+v", out)
	}
}

func TestRejects(t *testing.T) {
	for _, s := range []string{"", "garbage", "vpn1.!!!", Token{Addr: "nohost", SNI: "x", CAFingerprint: strings.Repeat("a", 64), Secret: "s"}.Encode(),
		Token{Addr: "h:1", SNI: "x", CAFingerprint: "short", Secret: "s"}.Encode()} {
		if _, err := Parse(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
