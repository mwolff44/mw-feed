// SPDX-License-Identifier: GPL-3.0-or-later

package favicon_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/favicon"
	"github.com/mwolff44/mw-feed/internal/fetch"
	"github.com/mwolff44/mw-feed/internal/store"
)

// png is the signature of a PNG file followed by padding: enough for content detection.
var png = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

// ico is the header of an ICO file followed by padding.
var ico = append([]byte{0, 0, 1, 0, 1, 0, 16, 16, 0, 0, 1, 0, 32, 0}, make([]byte, 32)...)

// fakeFetcher records the URLs asked for and answers from a script.
type fakeFetcher struct {
	mu    sync.Mutex
	urls  []string
	body  []byte
	err   error
	calls int
}

func (f *fakeFetcher) Fetch(_ context.Context, req fetch.Request) (fetch.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.urls = append(f.urls, req.URL)
	if f.err != nil {
		return fetch.Response{}, f.err
	}
	return fetch.Response{Body: f.body}, nil
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func newService(t *testing.T, f *fakeFetcher) (*favicon.Service, *fakeClock) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return favicon.NewService(st, f, clock), clock
}

func TestIconIsDownloadedOnceFromTheSiteHostThenServedFromTheCache(t *testing.T) {
	f := &fakeFetcher{body: png}
	svc, _ := newService(t, f)

	first, ok := svc.Icon(context.Background(), "https://blog.example.org/some/page", "https://feeds.example.net/rss")
	second, ok2 := svc.Icon(context.Background(), "https://blog.example.org/other", "https://feeds.example.net/other.xml")

	if !ok || !ok2 {
		t.Fatalf("Icon = %v, %v, want an icon both times", ok, ok2)
	}
	if first.Type != "image/png" || string(first.Data) != string(png) || string(second.Data) != string(png) {
		t.Errorf("icon = %q %d bytes, want the PNG that was downloaded", first.Type, len(first.Data))
	}
	want := []string{"https://icons.duckduckgo.com/ip3/blog.example.org.ico"}
	if len(f.urls) != 1 || f.urls[0] != want[0] {
		t.Errorf("downloads = %v, want exactly %v (feeds of one host share the icon)", f.urls, want)
	}
}

func TestHostComesFromTheXMLURLWhenTheFeedHasNoSiteURL(t *testing.T) {
	f := &fakeFetcher{body: ico}
	svc, _ := newService(t, f)

	icon, ok := svc.Icon(context.Background(), "", "https://Feeds.Example.NET:8443/rss")

	if !ok || icon.Type != "image/x-icon" {
		t.Fatalf("Icon = %q, %v, want an ICO icon", icon.Type, ok)
	}
	if len(f.urls) != 1 || f.urls[0] != "https://icons.duckduckgo.com/ip3/feeds.example.net.ico" {
		t.Errorf("downloads = %v, want the lower-case host without port", f.urls)
	}
}

func TestFailureIsRememberedForADayThenRetried(t *testing.T) {
	f := &fakeFetcher{err: errors.New("service down")}
	svc, clock := newService(t, f)
	ask := func() bool { _, ok := svc.Icon(context.Background(), "https://example.org/", ""); return ok }

	if ask() {
		t.Fatal("Icon succeeded although the download failed")
	}
	clock.now = clock.now.Add(23 * time.Hour)
	if ask() || f.calls != 1 {
		t.Errorf("after 23 h: calls = %d, want 1 (the failure is remembered)", f.calls)
	}
	clock.now = clock.now.Add(2 * time.Hour)
	f.err, f.body = nil, png
	if !ask() || f.calls != 2 {
		t.Errorf("after 25 h: calls = %d, want a second try that succeeds", f.calls)
	}
}

func TestIconIsDownloadedAgainAfterThirtyDays(t *testing.T) {
	f := &fakeFetcher{body: png}
	svc, clock := newService(t, f)
	svc.Icon(context.Background(), "https://example.org/", "")

	clock.now = clock.now.Add(29 * 24 * time.Hour)
	svc.Icon(context.Background(), "https://example.org/", "")
	if f.calls != 1 {
		t.Fatalf("after 29 days: calls = %d, want 1", f.calls)
	}
	clock.now = clock.now.Add(2 * 24 * time.Hour)
	svc.Icon(context.Background(), "https://example.org/", "")
	if f.calls != 2 {
		t.Errorf("after 31 days: calls = %d, want 2", f.calls)
	}
}

func TestContentThatIsNotARasterIconIsRefused(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	html := []byte("<html><body>not an icon</body></html>")
	big := append(append([]byte{}, png...), make([]byte, 64*1024)...) // over 64 KiB
	for name, body := range map[string][]byte{"svg": svg, "html": html, "empty": nil, "too big": big} {
		f := &fakeFetcher{body: body}
		svc, _ := newService(t, f)

		if icon, ok := svc.Icon(context.Background(), "https://example.org/", ""); ok {
			t.Errorf("%s: Icon = %q, want it refused", name, icon.Type)
		}
	}
}

func TestInvalidHostIsNeverSentToTheService(t *testing.T) {
	for _, site := range []string{"", "not a url", "https://exa mple.org/", "https://münchen.example/", "ftp://", "https:///path"} {
		f := &fakeFetcher{body: png}
		svc, _ := newService(t, f)

		if _, ok := svc.Icon(context.Background(), site, ""); ok || f.calls != 0 {
			t.Errorf("site %q: ok = %v, calls = %d, want no icon and no download", site, ok, f.calls)
		}
	}
}
