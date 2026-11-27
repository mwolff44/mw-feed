// SPDX-License-Identifier: GPL-3.0-or-later

package web_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/auth"
	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/fetch"
	"github.com/mwolff44/mw-feed/internal/store"
	"github.com/mwolff44/mw-feed/internal/web"
)

const wantCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src *; media-src *; " +
	"frame-src https://www.youtube-nocookie.com; connect-src 'self'; form-action 'self'; " +
	"base-uri 'none'; frame-ancestors 'none'"

func TestSecurityHeadersOnEveryKindOfResponse(t *testing.T) {
	a := newApp(t, web.Options{})
	notInstalled := a.get(t, "/install")
	a.install(t)
	visitor := a.anotherBrowser(t)

	post := func(path string) *http.Response {
		resp, err := a.client.PostForm(a.server.URL+path, url.Values{})
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	responses := map[string]*http.Response{
		"install page":          notInstalled,
		"login page (visitor)":  visitor.get(t, "/login"),
		"redirect to login":     visitor.get(t, "/"),
		"protected page":        a.get(t, "/"),
		"unknown page (404)":    a.get(t, "/no-such-page"),
		"POST without CSRF":     post("/login"),
		"static stylesheet":     a.get(t, "/static/style.css"),
		"unknown static (404)":  a.get(t, "/static/no-such-file"),
		"static from a visitor": visitor.get(t, "/static/style.css"),
	}

	for name, resp := range responses {
		want := map[string]string{
			"Content-Security-Policy": wantCSP,
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "no-referrer",
		}
		for h, v := range want {
			if got := resp.Header.Get(h); got != v {
				t.Errorf("%s: %s = %q, want %q", name, h, got, v)
			}
		}
	}
}

func TestStaticFilesArePublicAndCacheableWhilePagesAreNotStored(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	visitor := a.anotherBrowser(t)

	css := visitor.get(t, "/static/style.css")
	if css.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/style.css as a visitor = %d, want 200", css.StatusCode)
	}
	if ct := css.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("stylesheet Content-Type = %q, want text/css", ct)
	}
	if cc := css.Header.Get("Cache-Control"); cc == "" || strings.Contains(cc, "no-store") {
		t.Errorf("stylesheet Cache-Control = %q, want a cacheable policy", cc)
	}

	for name, resp := range map[string]*http.Response{
		"protected page": a.get(t, "/"),
		"login page":     visitor.get(t, "/login"),
		"install page":   newApp(t, web.Options{}).get(t, "/install"),
		"redirect":       visitor.get(t, "/"),
	} {
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", name, cc)
		}
	}
}

func TestStrictTransportSecurityOnlyOverHTTPS(t *testing.T) {
	cases := []struct {
		name       string
		trustProxy bool
		tls        bool
		proto      string
		want       string
	}{
		{"plain HTTP", false, false, "", ""},
		{"TLS terminated here", false, true, "", "max-age=31536000"},
		{"forwarded https, proxy trusted", true, false, "https", "max-age=31536000"},
		{"forwarded https, proxy not trusted", false, false, "https", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(context.Background(), t.TempDir())
			if err != nil {
				t.Fatalf("store.Open: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
			h := web.NewHandler(web.Options{TrustProxy: tc.trustProxy}, st, auth.NewService(st, clock), feed.NewService(st, fetch.New(fetch.Options{}), clock))
			req := httptest.NewRequest(http.MethodGet, "/install", nil)
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if got := rec.Result().Header.Get("Strict-Transport-Security"); got != tc.want {
				t.Errorf("Strict-Transport-Security = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPagesCarryLanguageThemeAndStylesheet(t *testing.T) {
	a := newApp(t, web.Options{})
	installPage := body(t, a.get(t, "/install"))
	a.install(t)
	visitor := a.anotherBrowser(t)

	pages := map[string]string{
		"install": installPage,
		"login":   body(t, visitor.get(t, "/login")),
		"home":    body(t, a.get(t, "/")),
	}
	for name, page := range pages {
		for _, want := range []string{`<html lang="en" data-theme="auto">`, `<link rel="stylesheet" href="/static/style.css">`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s page does not contain %q", name, want)
			}
		}
	}

	st, err := a.store.Settings(context.Background())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	st.Locale, st.Theme = "fr", "dark"
	if err := a.store.SaveSettings(context.Background(), st); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	for name, page := range map[string]string{
		"login": body(t, visitor.get(t, "/login")),
		"home":  body(t, a.get(t, "/")),
	} {
		if want := `<html lang="fr" data-theme="dark">`; !strings.Contains(page, want) {
			t.Errorf("%s page after changing the settings does not contain %q", name, want)
		}
	}
}

func TestMenuAppearsOnlyOnPagesOfALoggedInUser(t *testing.T) {
	a := newApp(t, web.Options{})
	installPage := body(t, a.get(t, "/install"))
	a.install(t)
	visitor := a.anotherBrowser(t)

	for name, page := range map[string]string{
		"install": installPage,
		"login":   body(t, visitor.get(t, "/login")),
	} {
		if strings.Contains(page, "<header") || strings.Contains(page, `action="/logout"`) {
			t.Errorf("%s page must have no menu", name)
		}
	}

	for _, path := range []string{"/", "/password"} {
		page := body(t, a.get(t, path))
		for _, want := range []string{
			`<header class="top">`,
			`<a class="brand" href="/">mw-feed</a>`,
			`<form method="post" action="/logout">`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("GET %s does not contain %q", path, want)
			}
		}
		_ = csrfToken(t, page)
	}
}

func TestUnknownPageIsAStyledNotFoundWithTheMenuOnlyWhenLoggedIn(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	visitor := a.anotherBrowser(t)

	resp := a.get(t, "/no-such-page")
	page := body(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /no-such-page = %d, want 404", resp.StatusCode)
	}
	for _, want := range []string{"Page not found", `<header class="top">`, `href="/static/style.css"`} {
		if !strings.Contains(page, want) {
			t.Errorf("logged-in 404 does not contain %q", want)
		}
	}

	resp = visitor.get(t, "/no-such-page")
	page = body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(page, "Page not found") {
		t.Errorf("visitor 404 = %d, want 404 with the message", resp.StatusCode)
	}
	if strings.Contains(page, "<header") {
		t.Error("a visitor's 404 must have no menu")
	}
}

func TestUnknownPageOfAnInstanceNotYetInstalledGoesToInstall(t *testing.T) {
	a := newApp(t, web.Options{})

	resp := a.get(t, "/no-such-page")

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/install" {
		t.Errorf("GET /no-such-page = %d → %q, want 303 → /install", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestMissingCSRFTokenGivesAStyledForbiddenPage(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)

	resp := a.post(t, "/password", url.Values{})
	page := body(t, resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without token = %d, want 403", resp.StatusCode)
	}
	if !strings.Contains(page, "Forbidden") || !strings.Contains(page, `href="/static/style.css"`) {
		t.Errorf("403 page is not the styled page:\n%s", page)
	}
}

func TestInternalErrorShowsAGenericPageWithoutDetails(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	if err := a.store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	resp := a.get(t, "/")
	page := body(t, resp)

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("GET / with a broken store = %d, want 500", resp.StatusCode)
	}
	if !strings.Contains(page, "Internal error") {
		t.Errorf("500 page has no generic message:\n%s", page)
	}
	for _, leak := range []string{"sql", "closed", "database"} {
		if strings.Contains(strings.ToLower(page), leak) {
			t.Errorf("500 page leaks %q:\n%s", leak, page)
		}
	}
}

func TestPagesAreTranslatedInTheConfiguredLanguage(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	visitor := a.anotherBrowser(t)
	for locale, want := range map[string]string{"fr": "Se connecter", "de": "Anmelden", "en": "Log in"} {
		st, err := a.store.Settings(context.Background())
		if err != nil {
			t.Fatalf("Settings: %v", err)
		}
		st.Locale = locale
		if err := a.store.SaveSettings(context.Background(), st); err != nil {
			t.Fatalf("SaveSettings: %v", err)
		}

		page := body(t, visitor.get(t, "/login"))

		if !strings.Contains(page, want) {
			t.Errorf("login page in %s does not contain %q", locale, want)
		}
	}
}

// A visitor who is not logged in has an anonymous session with a valid CSRF
// token (the login form needs one): it must not be enough to act.
func TestActionsRefuseAVisitorWhoIsNotLoggedInEvenWithAValidToken(t *testing.T) {
	a, list := seeded(t)
	id := list.Items[0].ID
	visitor, token := a.loginPage(t)

	for _, path := range []string{
		fmt.Sprintf("/item/%d/read", id), fmt.Sprintf("/item/%d/star", id), "/display", "/mark", "/update", "/add",
		"/edit/feeds", "/edit/delete", "/import", "/config", "/edit/feed/1", "/edit/feed/1/delete", "/edit/folder/1", "/edit/folder/1/up", "/folder/1/toggle",
	} {
		resp := visitor.post(t, path, url.Values{"csrf_token": {token}, "kind": {"all"}, "read": {"1"}, "view": {"expanded"}, "url": {"http://x.example/"}})

		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
			t.Errorf("POST %s as a visitor = %d → %q, want 303 → /login", path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	if it, _ := a.feeds.Item(ctx, id); it.Read || !it.StarredAt.IsZero() {
		t.Error("a visitor changed an item")
	}
	if st, _ := a.store.Settings(ctx); st.View != "list" {
		t.Error("a visitor changed the settings")
	}
}
