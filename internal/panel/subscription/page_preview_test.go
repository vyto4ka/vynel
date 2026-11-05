package subscription

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
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
