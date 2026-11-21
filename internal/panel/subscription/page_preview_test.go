package subscription

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"golang.org/x/net/html"
)

// An external protocol without a handler must not navigate the preview iframe.
// Public subscription links retain their existing launch behavior.
func TestPreviewLinkTargets(t *testing.T) {
	e := newEnv(t)
	d, err := BuildPage(e.user, "https://sub.example.com/s/demo", service.SubBasics{
		Title: "VPN", SupportURL: "https://support.example.com", Announce: "News", AnnounceURL: "https://news.example.com",
	}, service.DefaultSubPage(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, preview := range []bool{false, true} {
		d.Preview = preview
		var buf bytes.Buffer
		if err := RenderPage(&buf, d); err != nil {
			t.Fatal(err)
		}
		doc, err := html.Parse(&buf)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "a" {
				attrs := map[string]string{}
				for _, a := range n.Attr {
					attrs[a.Key] = a.Val
				}
				href := attrs["href"]
				if strings.HasPrefix(href, "happ://") || href == "https://support.example.com" || href == "https://news.example.com" {
					found[href] = true
					want := ""
					if preview {
						want = "_blank"
					}
					if attrs["target"] != want {
						t.Errorf("preview=%v, %s target=%q", preview, href, attrs["target"])
					}
					if preview && !strings.Contains(attrs["rel"], "noopener") {
						t.Errorf("missing noopener: %s", href)
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		if len(found) != 3 {
			t.Fatalf("missing links: %v", found)
		}
	}
}

func TestPageExtrasAndDeviceDelete(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Now()
	prefix := e.svc.SubPrefix(ctx)

	// Register a HWID device by hitting the subscription endpoint as an app.
	req := httptest.NewRequest(http.MethodGet, prefix+e.user.SubToken, nil)
	req.Header.Set("User-Agent", "Happ/4.1.0")
	req.Header.Set("x-hwid", "device-hwid-001")
	req.Header.Set("x-device-model", "iPhone 16 Pro")
	req.Header.Set("x-device-os", "iOS")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sub get: %d %s", w.Code, w.Body.String())
	}

	devs, err := e.svc.Devices(ctx, e.user.ID)
	if err != nil || len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d (err=%v)", len(devs), err)
	}

	// Render page with extras and verify sections exist.
	d, err := BuildPage(e.user, "https://sub.example.com"+prefix+e.user.SubToken, service.SubBasics{Title: "Vynel"}, service.DefaultSubPage(), now)
	if err != nil {
		t.Fatal(err)
	}
	PopulatePageExtras(&d, devs, 5, false, []service.DayTraffic{
		{Day: now.AddDate(0, 0, -1).Unix(), Down: 2 << 30, Up: 512 << 20},
		{Day: now.Unix(), Down: 4 << 30, Up: 1 << 30},
	}, prefix+e.user.SubToken+"/devices/", now)
	var buf bytes.Buffer
	if err := RenderPage(&buf, d); err != nil {
		t.Fatal(err)
	}
	htmlStr := buf.String()
	for _, want := range []string{"Трафик по дням", "Устройства (HWID)", "iPhone 16 Pro", "os-tabs", "dev-del"} {
		if !strings.Contains(htmlStr, want) {
			t.Fatalf("rendered page missing %q", want)
		}
	}

	// Delete the device via POST {prefix}{token}/devices/{id}/delete.
	delReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("%s%s/devices/%d/delete", prefix, e.user.SubToken, devs[0].ID), nil)
	delW := httptest.NewRecorder()
	e.h.ServeHTTP(delW, delReq)
	if delW.Code != http.StatusOK || !strings.Contains(delW.Body.String(), `"ok":true`) {
		t.Fatalf("device delete: %d %s", delW.Code, delW.Body.String())
	}

	// Verify device is removed.
	after, err := e.svc.Devices(ctx, e.user.ID)
	if err != nil || len(after) != 0 {
		t.Fatalf("expected 0 devices after delete, got %d (err=%v)", len(after), err)
	}

	// Random POST path must return 404 (stealth).
	badReq := httptest.NewRequest(http.MethodPost, prefix+e.user.SubToken+"/unknown", nil)
	badW := httptest.NewRecorder()
	e.h.ServeHTTP(badW, badReq)
	if badW.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on bad POST, got %d", badW.Code)
	}
	_ = store.Device{}
}

func TestPreviewPageLangAndNoDuplicateHeader(t *testing.T) {
	e := newEnv(t)
	d, err := BuildPage(e.user, "https://sub.example.com/s/demo", service.SubBasics{Title: "VPN"}, service.DefaultSubPage(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d.Preview = true
	ApplyPageLang(&d, "en")
	var buf bytes.Buffer
	if err := RenderPage(&buf, d); err != nil {
		t.Fatal(err)
	}
	htmlStr := buf.String()
	if strings.Contains(htmlStr, "Предпросмотр") || strings.Contains(htmlStr, "Открыть сохранённую страницу") {
		t.Fatalf("duplicate Russian preview header must not be rendered inside iframe")
	}
	for _, want := range []string{`<html lang="en" translate="no"`, "Traffic", "Valid until", "Connect"} {
		if !strings.Contains(htmlStr, want) {
			t.Fatalf("English preview page missing %q", want)
		}
	}
}
