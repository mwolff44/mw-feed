// SPDX-License-Identifier: GPL-3.0-or-later

package web_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/mwolff44/mw-feed/internal/favicon"
	"github.com/mwolff44/mw-feed/internal/store"
	"github.com/mwolff44/mw-feed/internal/web"
)

// fakeIcons answers like the favicon service and counts the questions.
type fakeIcons struct {
	mu          sync.Mutex
	icon        favicon.Icon
	ok          bool
	calls       int
	site, xmlTo string
}

func (f *fakeIcons) Icon(_ context.Context, siteURL, xmlURL string) (favicon.Icon, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.site, f.xmlTo = siteURL, xmlURL
	return f.icon, f.ok
}

var fakePNG = append([]byte("\x89PNG\r\n\x1a\n"), []byte("fake icon")...)

func faviconApp(t *testing.T, icons *fakeIcons) (*app, int64) {
	t.Helper()
	utc(t)
	a := newApp(t, web.Options{Icons: icons})
	a.install(t)
	f := a.subscribe(t, rss("Go Blog", entry{title: "One", link: "https://site.example/1", date: "Mon, 05 Jan 2026 10:00:00 GMT"}))
	return a, f.ID
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}

func TestFaviconSettingOffServesTheDefaultIconAndAsksNobody(t *testing.T) {
	icons := &fakeIcons{icon: favicon.Icon{Data: fakePNG, Type: "image/png"}, ok: true}
	a, id := faviconApp(t, icons)
	want := readBody(t, a.get(t, "/static/default-icon.png"))

	resp := a.get(t, fmt.Sprintf("/favicon/%d", id))

	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("status = %d, type = %q, want 200 image/png", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if got := readBody(t, resp); !bytes.Equal(got, want) || len(want) == 0 {
		t.Errorf("body is not the embedded default icon")
	}
	if icons.calls != 0 {
		t.Errorf("icon service asked %d times, want 0 while the setting is off", icons.calls)
	}
}

func TestFaviconSettingOnServesTheDownloadedIconWithItsHeaders(t *testing.T) {
	icons := &fakeIcons{icon: favicon.Icon{Data: fakePNG, Type: "image/png"}, ok: true}
	a, id := faviconApp(t, icons)
	a.setSettings(t, func(s *store.Settings) { s.AddFavicon = true })

	resp := a.get(t, fmt.Sprintf("/favicon/%d", id))

	if got := readBody(t, resp); !bytes.Equal(got, fakePNG) {
		t.Errorf("body = %q, want the downloaded icon", got)
	}
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", resp.Header.Get("Content-Type"))
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "private, max-age=86400" {
		t.Errorf("Cache-Control = %q, want private, max-age=86400", cc)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", resp.Header.Get("X-Content-Type-Options"))
	}
	if icons.calls != 1 || icons.xmlTo == "" {
		t.Errorf("service asked %d times with xml %q, want once with the feed URLs", icons.calls, icons.xmlTo)
	}
}

func TestFaviconFallsBackToTheDefaultIconWhenThereIsNoIcon(t *testing.T) {
	icons := &fakeIcons{ok: false}
	a, id := faviconApp(t, icons)
	a.setSettings(t, func(s *store.Settings) { s.AddFavicon = true })
	want := readBody(t, a.get(t, "/static/default-icon.png"))

	resp := a.get(t, fmt.Sprintf("/favicon/%d", id))

	if resp.StatusCode != http.StatusOK || !bytes.Equal(readBody(t, resp), want) {
		t.Errorf("status = %d, want 200 with the default icon", resp.StatusCode)
	}
}

func TestFaviconRouteIsProtectedAndChecksItsArgument(t *testing.T) {
	icons := &fakeIcons{icon: favicon.Icon{Data: fakePNG, Type: "image/png"}, ok: true}
	a, id := faviconApp(t, icons)
	a.setSettings(t, func(s *store.Settings) { s.AddFavicon = true })

	visitor := a.anotherBrowser(t)
	if resp := visitor.get(t, fmt.Sprintf("/favicon/%d", id)); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Errorf("visitor: %d → %q, want 303 → /login", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp := a.get(t, "/favicon/abc"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("/favicon/abc = %d, want 404", resp.StatusCode)
	}
	if icons.calls != 0 {
		t.Errorf("service asked %d times, want 0 for refused requests", icons.calls)
	}
	if resp := a.get(t, "/favicon/9999"); resp.StatusCode != http.StatusOK || icons.calls != 0 {
		t.Errorf("unknown feed: %d, calls %d, want 200 with the default icon and no download", resp.StatusCode, icons.calls)
	}
}

func TestReadingPagesShowTheFeedIconInTheSidebarAndOnEachItem(t *testing.T) {
	a, id := faviconApp(t, &fakeIcons{})
	img := fmt.Sprintf(`<img class="favicon" src="/favicon/%d" width="16" height="16" alt="">`, id)

	page := body(t, a.get(t, "/"))

	if n := strings.Count(page, img); n < 2 {
		t.Errorf("page has %d icons of the feed, want one in the sidebar and one on the item:\n%s", n, page)
	}
}
