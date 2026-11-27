// SPDX-License-Identifier: GPL-3.0-or-later

package web_test

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/auth"
	"github.com/mwolff44/mw-feed/internal/store"
	"github.com/mwolff44/mw-feed/internal/web"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

// app is a running server plus a cookie-keeping client that does not follow redirects.
type app struct {
	server *httptest.Server
	client *http.Client
	clock  *fakeClock
}

func newApp(t *testing.T, opts web.Options) *app {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	srv := httptest.NewServer(web.NewHandler(opts, st, auth.NewService(st, clock)))
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &app{server: srv, client: client, clock: clock}
}

func (a *app) get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := a.client.Get(a.server.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestNotInstalledRedirectsEveryPageToInstall(t *testing.T) {
	a := newApp(t, web.Options{})

	for _, path := range []string{"/", "/login", "/password"} {
		resp := a.get(t, path)
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/install" {
			t.Errorf("GET %s = %d → %q, want 303 → /install", path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

var csrfField = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// csrfToken extracts the CSRF token of the first form of an HTML page.
func csrfToken(t *testing.T, page string) string {
	t.Helper()
	m := csrfField.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no csrf_token field in page:\n%s", page)
	}
	return m[1]
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == "mw_feed_session" {
			return c
		}
	}
	return nil
}

func TestInstallPageShowsFormWithCSRFTokenAndAnonymousSessionCookie(t *testing.T) {
	a := newApp(t, web.Options{})

	resp := a.get(t, "/install")
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{"Install mw-feed", `name="login"`, `name="password"`, `name="password_confirm"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
	_ = csrfToken(t, page)
	c := sessionCookie(resp)
	if c == nil {
		t.Fatal("no mw_feed_session cookie")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Secure || c.MaxAge != 0 {
		t.Errorf("cookie = %+v, want HttpOnly, SameSite=Lax, Path=/, not Secure, session cookie", c)
	}
}

func (a *app) post(t *testing.T, path string, form url.Values) *http.Response {
	t.Helper()
	resp, err := a.client.PostForm(a.server.URL+path, form)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

const (
	testLogin    = "alice"
	testPassword = "a sufficiently long passphrase"
)

func installForm(csrf string) url.Values {
	return url.Values{
		"csrf_token":       {csrf},
		"login":            {testLogin},
		"password":         {testPassword},
		"password_confirm": {testPassword},
	}
}

func TestInstallRefusedWithoutAValidCSRFToken(t *testing.T) {
	a := newApp(t, web.Options{})
	_ = csrfToken(t, body(t, a.get(t, "/install"))) // gets the anonymous session cookie

	for name, form := range map[string]url.Values{
		"missing token": installForm(""),
		"wrong token":   installForm("not-the-token"),
	} {
		if resp := a.post(t, "/install", form); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, resp.StatusCode)
		}
	}
	if resp := a.get(t, "/install"); resp.StatusCode != http.StatusOK {
		t.Errorf("the instance got installed despite the refusals: GET /install = %d", resp.StatusCode)
	}
}

// install runs the whole installation and leaves the client logged in.
func (a *app) install(t *testing.T) {
	t.Helper()
	csrf := csrfToken(t, body(t, a.get(t, "/install")))
	if resp := a.post(t, "/install", installForm(csrf)); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("install: status = %d, want 303", resp.StatusCode)
	}
}

func TestInstallLogsInAndCannotBeRepeated(t *testing.T) {
	a := newApp(t, web.Options{})
	page := a.get(t, "/install")
	anon := sessionCookie(page)
	csrf := csrfToken(t, body(t, page))

	resp := a.post(t, "/install", installForm(csrf))

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("install = %d → %q, want 303 → /", resp.StatusCode, resp.Header.Get("Location"))
	}
	logged := sessionCookie(resp)
	if logged == nil || logged.Value == anon.Value {
		t.Errorf("session cookie after install = %+v, want a new one (ID regenerated)", logged)
	}
	if resp := a.get(t, "/install"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /install after install = %d, want 404", resp.StatusCode)
	}
	if resp := a.post(t, "/install", installForm(csrf)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST /install after install = %d, want 404", resp.StatusCode)
	}
}

func TestInstallShowsAnErrorAndCreatesNothingOnInvalidInput(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(url.Values)
		message string
	}{
		{"passwords differ", func(f url.Values) { f.Set("password_confirm", "something else entirely") }, "The passwords do not match."},
		{"password too short", func(f url.Values) { f.Set("password", "short"); f.Set("password_confirm", "short") }, "The password must have at least 15 characters."},
		{"password too long", func(f url.Values) {
			long := strings.Repeat("a", 129)
			f.Set("password", long)
			f.Set("password_confirm", long)
		}, "The password must have at most 128 characters."},
		{"login empty", func(f url.Values) { f.Set("login", "  ") }, "The login must not be empty."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(t, web.Options{})
			csrf := csrfToken(t, body(t, a.get(t, "/install")))
			form := installForm(csrf)
			tc.mutate(form)

			resp := a.post(t, "/install", form)
			page := body(t, resp)

			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
			if !strings.Contains(page, tc.message) {
				t.Errorf("page does not contain %q", tc.message)
			}
			_ = csrfToken(t, page) // the form can be submitted again
			if resp := a.get(t, "/install"); resp.StatusCode != http.StatusOK {
				t.Errorf("an invalid install created the user: GET /install = %d", resp.StatusCode)
			}
		})
	}
}

// anotherBrowser returns a client of the same server with its own, empty cookie jar.
func (a *app) anotherBrowser(t *testing.T) *app {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	other := *a
	other.client = &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &other
}

func TestProtectedPagesRedirectToLoginWithoutASession(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	visitor := a.anotherBrowser(t)

	for _, path := range []string{"/", "/password"} {
		resp := visitor.get(t, path)
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
			t.Errorf("GET %s = %d → %q, want 303 → /login", path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	if resp := a.get(t, "/"); resp.StatusCode != http.StatusOK {
		t.Errorf("GET / when logged in = %d, want 200", resp.StatusCode)
	}
}

func TestHomePageShowsTitleAndAccountActions(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)

	resp := a.get(t, "/")
	page := body(t, resp)

	for _, want := range []string{"<h1>mw-feed</h1>", `href="/password"`, `action="/logout"`} {
		if !strings.Contains(page, want) {
			t.Errorf("home page does not contain %q", want)
		}
	}
	_ = csrfToken(t, page)
}

func TestLoginPageShowsFormWithCSRFTokenAndAnonymousSession(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	visitor := a.anotherBrowser(t)

	resp := visitor.get(t, "/login")
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{`name="login"`, `name="password"`, `name="remember"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
	_ = csrfToken(t, page)
	if c := sessionCookie(resp); c == nil || !c.HttpOnly {
		t.Errorf("session cookie = %+v, want an HttpOnly anonymous session", c)
	}
}

func loginForm(csrf, login, password string) url.Values {
	return url.Values{"csrf_token": {csrf}, "login": {login}, "password": {password}}
}

// loginPage returns a visitor's client with its anonymous session and the CSRF token of the login form.
func (a *app) loginPage(t *testing.T) (visitor *app, csrf string) {
	t.Helper()
	visitor = a.anotherBrowser(t)
	return visitor, csrfToken(t, body(t, visitor.get(t, "/login")))
}

func TestLoginRefusedWithoutCSRFAndWithBadCredentials(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	visitor, csrf := a.loginPage(t)

	if resp := visitor.post(t, "/login", loginForm("wrong", testLogin, testPassword)); resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong CSRF token: status = %d, want 403", resp.StatusCode)
	}
	for name, form := range map[string]url.Values{
		"wrong password": loginForm(csrf, testLogin, "not the right passphrase"),
		"unknown login":  loginForm(csrf, "mallory", testPassword),
	} {
		resp := visitor.post(t, "/login", form)
		page := body(t, resp)
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(page, "Invalid login or password.") {
			t.Errorf("%s: status = %d, want 401 with the generic message", name, resp.StatusCode)
		}
		_ = csrfToken(t, page)
	}
	if resp := visitor.get(t, "/"); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("a failed login gave access: GET / = %d", resp.StatusCode)
	}
}

func TestLoginSucceedsWithASessionCookieThatEndsWithTheBrowser(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	visitor, csrf := a.loginPage(t)
	anon := visitor.client.Jar.Cookies(mustURL(t, visitor.server.URL))

	resp := visitor.post(t, "/login", loginForm(csrf, testLogin, testPassword))

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("login = %d → %q, want 303 → /", resp.StatusCode, resp.Header.Get("Location"))
	}
	c := sessionCookie(resp)
	if c == nil || c.Value == anon[0].Value || c.MaxAge != 0 {
		t.Errorf("cookie = %+v, want a new session cookie without Max-Age", c)
	}
	if resp := visitor.get(t, "/"); resp.StatusCode != http.StatusOK {
		t.Errorf("GET / after login = %d, want 200", resp.StatusCode)
	}
}

func TestRememberMeCookieLastsThirtyDaysAndIsRenewedOnEachVisit(t *testing.T) {
	const thirtyDays = 30 * 24 * 60 * 60
	a := newApp(t, web.Options{})
	a.install(t)
	visitor, csrf := a.loginPage(t)
	form := loginForm(csrf, testLogin, testPassword)
	form.Set("remember", "1")

	resp := visitor.post(t, "/login", form)

	if c := sessionCookie(resp); c == nil || c.MaxAge != thirtyDays {
		t.Fatalf("login cookie = %+v, want Max-Age %d", c, thirtyDays)
	}
	visit := visitor.get(t, "/")
	if c := sessionCookie(visit); c == nil || c.MaxAge != thirtyDays {
		t.Errorf("cookie on a later visit = %+v, want it renewed with Max-Age %d", c, thirtyDays)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestFourFailedLoginsBanTheIPWithAnExplicitMessage(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	visitor, csrf := a.loginPage(t)
	for range 4 {
		visitor.post(t, "/login", loginForm(csrf, testLogin, "not the right passphrase"))
	}

	resp := visitor.post(t, "/login", loginForm(csrf, testLogin, testPassword))
	page := body(t, resp)

	if resp.StatusCode != http.StatusTooManyRequests || !strings.Contains(page, "Too many failed attempts. Try again in 30 minutes.") {
		t.Errorf("banned login = %d, want 429 with the explicit message", resp.StatusCode)
	}
	a.clock.now = a.clock.now.Add(10 * time.Minute)
	page = body(t, visitor.post(t, "/login", loginForm(csrf, testLogin, testPassword)))
	if !strings.Contains(page, "Try again in 20 minutes.") {
		t.Errorf("the remaining time is not updated: %q", page)
	}
}

// postFrom posts a form with an X-Forwarded-For header.
func (a *app) postFrom(t *testing.T, forwardedFor, path string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, a.server.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", forwardedFor)
	resp, err := a.client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestForwardedForIsUsedForBansOnlyWhenTheProxyIsTrusted(t *testing.T) {
	cases := []struct {
		name         string
		trustProxy   bool
		wantOtherIPs int // status for a login from another forwarded address
	}{
		{"proxy not trusted: the header is ignored, all callers share one address", false, http.StatusTooManyRequests},
		{"proxy trusted: each forwarded address is counted on its own", true, http.StatusSeeOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(t, web.Options{TrustProxy: tc.trustProxy})
			a.install(t)
			visitor, csrf := a.loginPage(t)
			for range 4 {
				visitor.postFrom(t, "203.0.113.5, 10.0.0.1", "/login", loginForm(csrf, testLogin, "not the right passphrase"))
			}

			// The banned address stays banned in both cases.
			resp := visitor.postFrom(t, "203.0.113.5", "/login", loginForm(csrf, testLogin, testPassword))
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Errorf("login from the banned address = %d, want 429", resp.StatusCode)
			}
			resp = visitor.postFrom(t, "198.51.100.9", "/login", loginForm(csrf, testLogin, testPassword))
			if resp.StatusCode != tc.wantOtherIPs {
				t.Errorf("login from another forwarded address = %d, want %d", resp.StatusCode, tc.wantOtherIPs)
			}
		})
	}
}

func TestSessionCookieIsSecureOnlyOverHTTPS(t *testing.T) {
	cases := []struct {
		name       string
		trustProxy bool
		tls        bool
		proto      string
		want       bool
	}{
		{"plain HTTP", false, false, "", false},
		{"TLS terminated here", false, true, "", true},
		{"forwarded https, proxy trusted", true, false, "https", true},
		{"forwarded https, proxy not trusted", false, false, "https", false},
		{"forwarded http, proxy trusted", true, false, "http", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(context.Background(), t.TempDir())
			if err != nil {
				t.Fatalf("store.Open: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			clock := &fakeClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
			h := web.NewHandler(web.Options{TrustProxy: tc.trustProxy}, st, auth.NewService(st, clock))
			req := httptest.NewRequest(http.MethodGet, "/install", nil)
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			c := sessionCookie(rec.Result())
			if c == nil {
				t.Fatal("no session cookie")
			}
			if c.Secure != tc.want {
				t.Errorf("Secure = %v, want %v", c.Secure, tc.want)
			}
		})
	}
}

func TestLogoutNeedsCSRFThenEndsTheSession(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	csrf := csrfToken(t, body(t, a.get(t, "/")))

	if resp := a.post(t, "/logout", url.Values{"csrf_token": {"wrong"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("logout with a wrong token = %d, want 403", resp.StatusCode)
	}
	if resp := a.get(t, "/"); resp.StatusCode != http.StatusOK {
		t.Fatalf("a refused logout ended the session: GET / = %d", resp.StatusCode)
	}

	resp := a.post(t, "/logout", url.Values{"csrf_token": {csrf}})

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Errorf("logout = %d → %q, want 303 → /login", resp.StatusCode, resp.Header.Get("Location"))
	}
	if c := sessionCookie(resp); c == nil || c.MaxAge >= 0 {
		t.Errorf("cookie after logout = %+v, want it deleted (negative Max-Age)", c)
	}
	if resp := a.get(t, "/"); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Errorf("GET / after logout = %d → %q, want 303 → /login", resp.StatusCode, resp.Header.Get("Location"))
	}
}

const newPassword = "an even better passphrase 42"

func passwordForm(csrf, current, next, confirm string) url.Values {
	return url.Values{
		"csrf_token":           {csrf},
		"current_password":     {current},
		"new_password":         {next},
		"new_password_confirm": {confirm},
	}
}

func TestChangePasswordRenewsTheSessionAndEndsTheOthers(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	// A second logged-in browser.
	other, csrfLogin := a.loginPage(t)
	if resp := other.post(t, "/login", loginForm(csrfLogin, testLogin, testPassword)); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("second login: %d", resp.StatusCode)
	}

	form := a.get(t, "/password")
	page := body(t, form)
	if form.StatusCode != http.StatusOK {
		t.Fatalf("GET /password = %d, want 200", form.StatusCode)
	}
	for _, want := range []string{`name="current_password"`, `name="new_password"`, `name="new_password_confirm"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
	before := a.client.Jar.Cookies(mustURL(t, a.server.URL))[0].Value

	resp := a.post(t, "/password", passwordForm(csrfToken(t, page), testPassword, newPassword, newPassword))

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("change = %d → %q, want 303 → /", resp.StatusCode, resp.Header.Get("Location"))
	}
	if c := sessionCookie(resp); c == nil || c.Value == before {
		t.Errorf("cookie after change = %+v, want a new session ID", c)
	}
	if r := a.get(t, "/"); r.StatusCode != http.StatusOK {
		t.Errorf("the caller was logged out: GET / = %d", r.StatusCode)
	}
	if r := other.get(t, "/"); r.StatusCode != http.StatusSeeOther {
		t.Errorf("the other session survived: GET / = %d, want 303", r.StatusCode)
	}
	fresh, csrf := a.loginPage(t)
	if r := fresh.post(t, "/login", loginForm(csrf, testLogin, testPassword)); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("old password still works: %d", r.StatusCode)
	}
	if r := fresh.post(t, "/login", loginForm(csrf, testLogin, newPassword)); r.StatusCode != http.StatusSeeOther {
		t.Errorf("new password does not work: %d", r.StatusCode)
	}
}

func TestChangePasswordRefusals(t *testing.T) {
	cases := []struct {
		name       string
		form       func(csrf string) url.Values
		wantStatus int
		message    string
	}{
		{"wrong CSRF token", func(string) url.Values { return passwordForm("wrong", testPassword, newPassword, newPassword) }, http.StatusForbidden, ""},
		{"confirmation differs", func(c string) url.Values { return passwordForm(c, testPassword, newPassword, "something else") }, http.StatusBadRequest, "The passwords do not match."},
		{"wrong current password", func(c string) url.Values {
			return passwordForm(c, "not the right passphrase", newPassword, newPassword)
		}, http.StatusBadRequest, "The current password is incorrect."},
		{"new password too short", func(c string) url.Values { return passwordForm(c, testPassword, "short", "short") }, http.StatusBadRequest, "The password must have at least 15 characters."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(t, web.Options{})
			a.install(t)
			csrf := csrfToken(t, body(t, a.get(t, "/password")))

			resp := a.post(t, "/password", tc.form(csrf))
			page := body(t, resp)

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if tc.message != "" && !strings.Contains(page, tc.message) {
				t.Errorf("page does not contain %q", tc.message)
			}
			if r := a.get(t, "/"); r.StatusCode != http.StatusOK {
				t.Errorf("the caller was logged out by a refused change: GET / = %d", r.StatusCode)
			}
			fresh, c := a.loginPage(t)
			if r := fresh.post(t, "/login", loginForm(c, testLogin, testPassword)); r.StatusCode != http.StatusSeeOther {
				t.Errorf("the password changed despite the refusal: login = %d", r.StatusCode)
			}
		})
	}
}

func TestChangePasswordRedirectsAVisitorWhoIsNotLoggedIn(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	visitor, csrf := a.loginPage(t) // an anonymous session with a valid CSRF token

	resp := visitor.post(t, "/password", passwordForm(csrf, testPassword, newPassword, newPassword))

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Errorf("POST /password as a visitor = %d → %q, want 303 → /login", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestLoginPageRedirectsAnAlreadyLoggedInUserToHome(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t) // leaves the client logged in

	resp := a.get(t, "/login")

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Errorf("GET /login when logged in = %d → %q, want 303 → /", resp.StatusCode, resp.Header.Get("Location"))
	}
}
