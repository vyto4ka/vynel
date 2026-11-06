package update

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestXrayReleaseSelection(t *testing.T) {
	for input, want := range map[string]string{"": "", "latest": "", " 26.10.31 ": "v26.10.31", "v1.8.24": "v1.8.24"} {
		got, err := NormalizeXrayRelease(input)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"../../x", "v26.10", "v26.10.31;reboot", "v26.10.31\nVYNEL_REF=evil", "https://example.com/xray", "v26.10.31-beta"} {
		if _, err := NormalizeXrayRelease(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestXrayPinSurvivesChannelChanges(t *testing.T) {
	old := EnvFile
	t.Cleanup(func() { EnvFile = old })
	EnvFile = filepath.Join(t.TempDir(), "install.env")
	s := Source{Ref: "main", Release: ReleaseLatest, XrayRelease: "v26.9.30"}
	if err := SaveSource(s); err != nil {
		t.Fatal(err)
	}
	if got := LoadSource(); got != s {
		t.Fatalf("round trip: %+v", got)
	}
	dev, err := s.WithChannel(ChannelDev, "test")
	if err != nil {
		t.Fatal(err)
	}
	next, err := dev.Follow("latest", "main")
	if err != nil || next.XrayRelease != s.XrayRelease {
		t.Fatalf("lost pin: %+v, %v", next, err)
	}
	if !slices.Contains(next.env(), "VYNEL_XRAY_VERSION=v26.9.30") {
		t.Fatalf("pin not passed to installer: %v", next.env())
	}
	s.XrayRelease = "latest"
	if err := SaveSource(s); err != nil {
		t.Fatal(err)
	}
	if got := LoadSource(); got.XrayRelease != "" {
		t.Fatalf("pin not removed: %+v", got)
	}
	before, _ := os.ReadFile(EnvFile)
	s.XrayRelease = "v26.10.31\nVYNEL_REF=evil"
	if err := SaveSource(s); err == nil {
		t.Fatal("invalid pin saved")
	}
	after, _ := os.ReadFile(EnvFile)
	if string(before) != string(after) {
		t.Fatal("invalid pin changed install.env")
	}
}
