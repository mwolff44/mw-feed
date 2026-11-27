// SPDX-License-Identifier: GPL-3.0-or-later

package fetch_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/fetch"
)

// allowLocal lets tests reach httptest servers, which listen on loopback.
func allowLocal() fetch.Options { return fetch.Options{AllowPrivate: true} }

func TestFetchReturnsTheBodyOfASuccessfulResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello feed"))
	}))
	t.Cleanup(srv.Close)

	resp, err := fetch.New(allowLocal()).Fetch(context.Background(), fetch.Request{URL: srv.URL})

	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := string(resp.Body); got != "hello feed" {
		t.Errorf("body = %q, want %q", got, "hello feed")
	}
}

func TestFetchRefusesEverythingButHTTPAndHTTPS(t *testing.T) {
	for _, url := range []string{
		"ftp://example.org/feed.xml",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"gopher://example.org/",
		"example.org/feed.xml",
		"//example.org/feed.xml",
		"",
	} {
		_, err := fetch.New(allowLocal()).Fetch(context.Background(), fetch.Request{URL: url})
		if !errors.Is(err, fetch.ErrUnsupportedScheme) {
			t.Errorf("Fetch(%q) error = %v, want fetch.ErrUnsupportedScheme", url, err)
		}
	}
}

func TestFetchBlocksLoopbackByDefaultWithoutReachingTheServer(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(srv.Close)

	_, err := fetch.New(fetch.Options{}).Fetch(context.Background(), fetch.Request{URL: srv.URL})

	if !errors.Is(err, fetch.ErrBlockedAddress) {
		t.Errorf("error = %v, want fetch.ErrBlockedAddress", err)
	}
	if hits.Load() != 0 {
		t.Errorf("the server received %d request(s), want 0", hits.Load())
	}
}

// recordingDial records the addresses it is asked to connect to and refuses
// the connection: no real network is used.
type recordingDial struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingDial) dial(_ context.Context, _, address string) (net.Conn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, address)
	return nil, errors.New("recordingDial: connection refused on purpose")
}

func (r *recordingDial) called() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func TestFetchBlocksNonPublicAddressRanges(t *testing.T) {
	blockedHosts := []string{
		"127.0.0.1", "127.1.2.3", "[::1]", // loopback
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1", "[fd00::1]", "[fc00::1]", // private
		"169.254.169.254", "[fe80::1]", // link-local, cloud metadata
		"0.0.0.0", "[::]", // unspecified
		"224.0.0.1", "[ff02::1]", // multicast
		"100.64.0.1", "100.127.255.254", // CGNAT
		"[::ffff:127.0.0.1]", "[::ffff:10.0.0.1]", // IPv4 inside IPv6
	}
	publicHosts := []string{
		"93.184.216.34", "8.8.8.8", "11.0.0.1", "172.15.0.1", "172.32.0.1",
		"100.63.255.255", "100.128.0.1", "192.169.0.1", "[2606:2800:220:1:248:1893:25c8:1946]",
	}

	for _, host := range blockedHosts {
		rec := &recordingDial{}
		_, err := fetch.New(fetch.Options{Dial: rec.dial}).Fetch(context.Background(), fetch.Request{URL: "http://" + host + "/feed"})
		if !errors.Is(err, fetch.ErrBlockedAddress) {
			t.Errorf("%s: error = %v, want fetch.ErrBlockedAddress", host, err)
		}
		if len(rec.called()) != 0 {
			t.Errorf("%s: a connection was attempted: %v", host, rec.called())
		}
	}
	for _, host := range publicHosts {
		rec := &recordingDial{}
		_, err := fetch.New(fetch.Options{Dial: rec.dial}).Fetch(context.Background(), fetch.Request{URL: "http://" + host + "/feed"})
		if errors.Is(err, fetch.ErrBlockedAddress) {
			t.Errorf("%s is public but was blocked", host)
		}
		if len(rec.called()) != 1 {
			t.Errorf("%s: connection attempts = %v, want exactly one", host, rec.called())
		}
	}
}

// fakeResolver answers from a table.
type fakeResolver map[string][]string

func (r fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	ips, ok := r[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	var out []netip.Addr
	for _, ip := range ips {
		out = append(out, netip.MustParseAddr(ip))
	}
	return out, nil
}

func TestFetchChecksTheAddressesAHostNameResolvesTo(t *testing.T) {
	resolver := fakeResolver{
		"public.example":   {"93.184.216.34"},
		"internal.example": {"10.0.0.5"},
		"mixed.example":    {"93.184.216.34", "10.0.0.5"},
	}
	cases := []struct {
		host         string
		allowPrivate bool
		wantBlocked  bool
		wantDialed   string // address actually connected to, if any
	}{
		{"public.example", false, false, "93.184.216.34:80"},
		{"internal.example", false, true, ""},
		{"mixed.example", false, true, ""},
		{"internal.example", true, false, "10.0.0.5:80"},
	}
	for _, tc := range cases {
		rec := &recordingDial{}
		f := fetch.New(fetch.Options{AllowPrivate: tc.allowPrivate, Resolver: resolver, Dial: rec.dial})

		_, err := f.Fetch(context.Background(), fetch.Request{URL: "http://" + tc.host + "/feed"})

		if got := errors.Is(err, fetch.ErrBlockedAddress); got != tc.wantBlocked {
			t.Errorf("%s (allowPrivate=%v): blocked = %v, want %v (error: %v)", tc.host, tc.allowPrivate, got, tc.wantBlocked, err)
		}
		calls := rec.called()
		switch {
		case tc.wantDialed == "" && len(calls) != 0:
			t.Errorf("%s: connection attempted to %v, want none", tc.host, calls)
		case tc.wantDialed != "" && (len(calls) != 1 || calls[0] != tc.wantDialed):
			t.Errorf("%s: connected to %v, want the resolved IP %s, not the name", tc.host, calls, tc.wantDialed)
		}
	}
}

func TestFetchReportsAHostThatDoesNotResolve(t *testing.T) {
	f := fetch.New(fetch.Options{Resolver: fakeResolver{}})

	_, err := f.Fetch(context.Background(), fetch.Request{URL: "http://nowhere.example/feed"})

	if err == nil || errors.Is(err, fetch.ErrBlockedAddress) {
		t.Errorf("error = %v, want a resolution error that is not ErrBlockedAddress", err)
	}
}

// redirectChain serves /r/0 → /r/1 → … → /r/n, which answers "end".
func redirectChain(t *testing.T, redirects int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for i := 0; i < redirects; i++ {
		next := fmt.Sprintf("/r/%d", i+1)
		mux.HandleFunc(fmt.Sprintf("/r/%d", i), func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, next, http.StatusFound)
		})
	}
	mux.HandleFunc(fmt.Sprintf("/r/%d", redirects), func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("end"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchFollowsAtMostTenRedirects(t *testing.T) {
	ten := redirectChain(t, 10)
	resp, err := fetch.New(allowLocal()).Fetch(context.Background(), fetch.Request{URL: ten.URL + "/r/0"})
	if err != nil || string(resp.Body) != "end" {
		t.Errorf("10 redirects: body = %q, error = %v; want \"end\", nil", resp.Body, err)
	}

	eleven := redirectChain(t, 11)
	_, err = fetch.New(allowLocal()).Fetch(context.Background(), fetch.Request{URL: eleven.URL + "/r/0"})
	if !errors.Is(err, fetch.ErrTooManyRedirects) {
		t.Errorf("11 redirects: error = %v, want fetch.ErrTooManyRedirects", err)
	}
}

func TestFetchRefusesARedirectToAnotherScheme(t *testing.T) {
	for _, target := range []string{"ftp://example.org/feed", "file:///etc/passwd"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusFound)
		}))
		_, err := fetch.New(allowLocal()).Fetch(context.Background(), fetch.Request{URL: srv.URL})
		srv.Close()
		if !errors.Is(err, fetch.ErrUnsupportedScheme) {
			t.Errorf("redirect to %s: error = %v, want fetch.ErrUnsupportedScheme", target, err)
		}
	}
}

func TestFetchChecksTheAddressOfEveryRedirectHop(t *testing.T) {
	// The first hop is public, so it is allowed (its "connection" goes to the
	// test server); the redirect then points to a host resolving to a private IP.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://internal.example/secret", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	var dialed []string
	f := fetch.New(fetch.Options{
		Resolver: fakeResolver{"public.example": {"93.184.216.34"}, "internal.example": {"10.0.0.5"}},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return (&net.Dialer{}).DialContext(ctx, "tcp", srv.Listener.Addr().String())
		},
	})

	_, err := f.Fetch(context.Background(), fetch.Request{URL: "http://public.example/feed"})

	if !errors.Is(err, fetch.ErrBlockedAddress) {
		t.Errorf("error = %v, want fetch.ErrBlockedAddress", err)
	}
	if len(dialed) != 1 || dialed[0] != "93.184.216.34:80" {
		t.Errorf("connections = %v, want only the first public hop", dialed)
	}
}

func TestFetchRefusesABodyOverTheSizeLimitInsteadOfTruncatingIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 11)))
	}))
	t.Cleanup(srv.Close)

	atLimit := fetch.New(fetch.Options{AllowPrivate: true, MaxBytes: 11})
	if resp, err := atLimit.Fetch(context.Background(), fetch.Request{URL: srv.URL}); err != nil || len(resp.Body) != 11 {
		t.Errorf("body exactly at the limit: len = %d, error = %v; want 11, nil", len(resp.Body), err)
	}
	overLimit := fetch.New(fetch.Options{AllowPrivate: true, MaxBytes: 10})
	if _, err := overLimit.Fetch(context.Background(), fetch.Request{URL: srv.URL}); !errors.Is(err, fetch.ErrTooLarge) {
		t.Errorf("body over the limit: error = %v, want fetch.ErrTooLarge", err)
	}
}

func TestFetchRejectsNon2xxStatuses(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusInternalServerError, http.StatusTeapot} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte("<html>error page</html>"))
		}))
		_, err := fetch.New(allowLocal()).Fetch(context.Background(), fetch.Request{URL: srv.URL})
		srv.Close()

		var statusErr *fetch.StatusError
		if !errors.As(err, &statusErr) || statusErr.Code != code {
			t.Errorf("status %d: error = %v, want a *fetch.StatusError with that code", code, err)
		}
		if err != nil && !strings.Contains(err.Error(), fmt.Sprintf("unexpected HTTP status %d", code)) {
			t.Errorf("status %d: message = %q", code, err)
		}
	}
}

func TestFetchGivesUpAfterTheTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	f := fetch.New(fetch.Options{AllowPrivate: true, Timeout: 50 * time.Millisecond})

	start := time.Now()
	_, err := f.Fetch(context.Background(), fetch.Request{URL: srv.URL})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want a timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v, the timeout was 50 ms", elapsed)
	}
}

func TestFetchSendsAGenericUserAgentAndNoOtherCustomHeader(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	t.Cleanup(srv.Close)

	if _, err := fetch.New(allowLocal()).Fetch(context.Background(), fetch.Request{URL: srv.URL}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if ua := got.Get("User-Agent"); ua != "mw-feed" {
		t.Errorf("User-Agent = %q, want %q", ua, "mw-feed")
	}
	for _, h := range []string{"Accept", "Referer", "Cookie", "Authorization", "If-None-Match", "If-Modified-Since"} {
		if v := got.Get(h); v != "" {
			t.Errorf("header %s = %q, want none", h, v)
		}
	}
}

func TestFetchConditionalRequests(t *testing.T) {
	const etag, modified = `"v42"`, "Wed, 21 Oct 2026 07:28:00 GMT"
	var gotNoneMatch, gotModifiedSince string
	notModified := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotNoneMatch, gotModifiedSince = r.Header.Get("If-None-Match"), r.Header.Get("If-Modified-Since")
		if notModified {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", modified)
		_, _ = w.Write([]byte("fresh"))
	}))
	t.Cleanup(srv.Close)
	f := fetch.New(allowLocal())

	// A first download reports the validators; no conditional header is sent.
	resp, err := f.Fetch(context.Background(), fetch.Request{URL: srv.URL})
	if err != nil || resp.NotModified || resp.ETag != etag || resp.LastModified != modified {
		t.Fatalf("first fetch = %+v, %v; want a 200 with the validators", resp, err)
	}
	if gotNoneMatch != "" || gotModifiedSince != "" {
		t.Errorf("conditional headers sent without validators: %q %q", gotNoneMatch, gotModifiedSince)
	}

	// With validators, they are sent back, and a 304 is reported as such.
	notModified = true
	resp, err = f.Fetch(context.Background(), fetch.Request{URL: srv.URL, ETag: etag, LastModified: modified})
	if err != nil || !resp.NotModified || len(resp.Body) != 0 {
		t.Errorf("conditional fetch = %+v, %v; want NotModified with no body", resp, err)
	}
	if gotNoneMatch != etag || gotModifiedSince != modified {
		t.Errorf("headers sent = %q %q, want %q %q", gotNoneMatch, gotModifiedSince, etag, modified)
	}

	// A 304 nobody asked for is a server error, not "nothing to do".
	_, err = f.Fetch(context.Background(), fetch.Request{URL: srv.URL})
	var statusErr *fetch.StatusError
	if !errors.As(err, &statusErr) || statusErr.Code != http.StatusNotModified {
		t.Errorf("unsolicited 304: error = %v, want a *fetch.StatusError 304", err)
	}
}
