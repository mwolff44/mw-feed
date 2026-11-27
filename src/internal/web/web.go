// SPDX-License-Identifier: GPL-3.0-or-later

// Package web serves the HTTP interface.
package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strings"

	"github.com/mwolff44/mw-feed/internal/auth"
	"github.com/mwolff44/mw-feed/internal/i18n"
	"github.com/mwolff44/mw-feed/internal/store"
)

//go:embed templates/*.html
var templateFiles embed.FS

var templates = template.Must(template.ParseFS(templateFiles, "templates/*.html"))

const (
	sessionCookieName = "mw_feed_session"
	// persistentCookieMaxAge is 30 days, in seconds ("stay logged in").
	persistentCookieMaxAge = 30 * 24 * 60 * 60
)

// Options are the runtime options of the web server.
type Options struct {
	// TrustProxy makes the server trust X-Forwarded-For and X-Forwarded-Proto.
	TrustProxy bool
}

type server struct {
	opts  Options
	store *store.Store
	auth  *auth.Service
}

// NewHandler returns the HTTP handler of the application.
func NewHandler(opts Options, st *store.Store, svc *auth.Service) http.Handler {
	s := &server{opts: opts, store: st, auth: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /install", s.installForm)
	mux.HandleFunc("POST /install", s.install)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /{$}", s.protected(s.home))
	mux.HandleFunc("GET /password", s.protected(s.passwordForm))
	mux.HandleFunc("POST /password", s.changePassword)
	mux.HandleFunc("/", s.notInstalledRedirect)
	return mux
}

// page is the data handed to every template.
type page struct {
	Locale    string
	PageTitle string
	Title     string // reader title, from the settings
	CSRFToken string
	Error     string // translated message, filled by render from ErrorKey
	ErrorKey  string
	ErrorArgs []any
}

// T translates a key in the page locale.
func (p page) T(key string) string { return i18n.T(p.Locale, key) }

func (s *server) render(w http.ResponseWriter, r *http.Request, name string, status int, p page) {
	p.Locale = "en"
	p.PageTitle = i18n.T(p.Locale, name+".title")
	if st, err := s.store.Settings(r.Context()); err == nil {
		p.Locale, p.Title = st.Locale, st.Title
		p.PageTitle = i18n.T(p.Locale, name+".title")
		if name == "home" {
			p.PageTitle = st.Title
		}
	}
	if p.ErrorKey != "" {
		p.Error = fmt.Sprintf(i18n.T(p.Locale, p.ErrorKey), p.ErrorArgs...)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := templates.ExecuteTemplate(w, name, p); err != nil {
		slog.Error("render template", "template", name, "error", err)
	}
}

func (s *server) isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (s.opts.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
}

func (s *server) setSessionCookie(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	c := &http.Cookie{
		Name:     sessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.isHTTPS(r),
	}
	if sess.Persistent {
		c.MaxAge = persistentCookieMaxAge
	}
	http.SetCookie(w, c)
}

// anonymousSession returns the caller's session, creating an anonymous one
// (and its cookie) if there is none that is valid.
func (s *server) anonymousSession(w http.ResponseWriter, r *http.Request) (auth.Session, error) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		if sess, err := s.auth.Authenticate(r.Context(), c.Value); err == nil {
			return sess, nil
		}
	}
	sess, err := s.auth.NewAnonymousSession(r.Context())
	if err != nil {
		return auth.Session{}, err
	}
	s.setSessionCookie(w, r, sess)
	return sess, nil
}

func (s *server) serverError(w http.ResponseWriter, err error) {
	slog.Error("request failed", "error", err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

func (s *server) installForm(w http.ResponseWriter, r *http.Request) {
	installed, err := s.auth.Installed(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	if installed {
		http.NotFound(w, r)
		return
	}
	sess, err := s.anonymousSession(w, r)
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.render(w, r, "install", http.StatusOK, page{CSRFToken: sess.CSRFToken})
}

func (s *server) notInstalledRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/install", http.StatusSeeOther)
}

// protected wraps a handler that needs an authenticated session: not installed
// instances go to /install, visitors without a session go to /login.
func (s *server) protected(h func(http.ResponseWriter, *http.Request, auth.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		installed, err := s.auth.Installed(r.Context())
		if err != nil {
			s.serverError(w, err)
			return
		}
		if !installed {
			http.Redirect(w, r, "/install", http.StatusSeeOther)
			return
		}
		sess, ok := s.currentSession(r)
		if !ok || !sess.Authenticated {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if sess.Persistent {
			s.setSessionCookie(w, r, sess) // renewed on each visit
		}
		h(w, r, sess)
	}
}

func (s *server) home(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	s.render(w, r, "home", http.StatusOK, page{CSRFToken: sess.CSRFToken})
}

func (s *server) passwordForm(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	s.render(w, r, "password", http.StatusOK, page{CSRFToken: sess.CSRFToken})
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.checkCSRF(w, r)
	if !ok {
		return
	}
	if !sess.Authenticated {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	next := r.PostFormValue("new_password")
	if next != r.PostFormValue("new_password_confirm") {
		s.render(w, r, "password", http.StatusBadRequest, page{CSRFToken: sess.CSRFToken, ErrorKey: "error.password_mismatch"})
		return
	}
	renewed, err := s.auth.ChangePassword(r.Context(), sess.ID, r.PostFormValue("current_password"), next)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		s.render(w, r, "password", http.StatusBadRequest, page{CSRFToken: sess.CSRFToken, ErrorKey: "error.current_password_wrong"})
		return
	}
	if key, ok := credentialErrorKey(err); ok {
		s.render(w, r, "password", http.StatusBadRequest, page{CSRFToken: sess.CSRFToken, ErrorKey: key})
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.setSessionCookie(w, r, renewed)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// currentSession returns the caller's valid session, if any.
func (s *server) currentSession(r *http.Request) (auth.Session, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return auth.Session{}, false
	}
	sess, err := s.auth.Authenticate(r.Context(), c.Value)
	if err != nil {
		return auth.Session{}, false
	}
	return sess, true
}

// checkCSRF returns the caller's session if the request carries its CSRF token.
// Otherwise it answers 403 and reports false.
func (s *server) checkCSRF(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	sess, ok := s.currentSession(r)
	if !ok || !auth.VerifyCSRF(sess, r.PostFormValue("csrf_token")) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return auth.Session{}, false
	}
	return sess, true
}

func (s *server) install(w http.ResponseWriter, r *http.Request) {
	installed, err := s.auth.Installed(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	if installed {
		http.NotFound(w, r)
		return
	}
	anon, ok := s.checkCSRF(w, r)
	if !ok {
		return
	}
	login, password := r.PostFormValue("login"), r.PostFormValue("password")
	if password != r.PostFormValue("password_confirm") {
		s.render(w, r, "install", http.StatusBadRequest, page{CSRFToken: anon.CSRFToken, ErrorKey: "error.password_mismatch"})
		return
	}
	if err := s.auth.Install(r.Context(), login, password); err != nil {
		if key, ok := credentialErrorKey(err); ok {
			s.render(w, r, "install", http.StatusBadRequest, page{CSRFToken: anon.CSRFToken, ErrorKey: key})
			return
		}
		s.serverError(w, err)
		return
	}
	sess, err := s.auth.Login(r.Context(), auth.LoginRequest{
		IP: s.clientIP(r), Login: login, Password: password, AnonymousSessionID: anon.ID,
	})
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.setSessionCookie(w, r, sess)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// credentialErrorKey maps a login or password rule violation to a message key.
func credentialErrorKey(err error) (string, bool) {
	switch {
	case errors.Is(err, auth.ErrInvalidLogin):
		return "error.login_empty", true
	case errors.Is(err, auth.ErrPasswordTooShort):
		return "error.password_too_short", true
	case errors.Is(err, auth.ErrPasswordTooLong):
		return "error.password_too_long", true
	}
	return "", false
}

// clientIP returns the address of the caller: the first X-Forwarded-For
// address when the proxy is trusted and the value is a valid IP, otherwise the
// address of the TCP peer.
func (s *server) clientIP(r *http.Request) string {
	if s.opts.TrustProxy {
		first, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ",")
		if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *server) loginForm(w http.ResponseWriter, r *http.Request) {
	installed, err := s.auth.Installed(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	if !installed {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return
	}
	sess, err := s.anonymousSession(w, r)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if sess.Authenticated {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", http.StatusOK, page{CSRFToken: sess.CSRFToken})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	installed, err := s.auth.Installed(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	if !installed {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return
	}
	anon, ok := s.checkCSRF(w, r)
	if !ok {
		return
	}
	sess, err := s.auth.Login(r.Context(), auth.LoginRequest{
		IP:                 s.clientIP(r),
		Login:              r.PostFormValue("login"),
		Password:           r.PostFormValue("password"),
		Remember:           r.PostFormValue("remember") != "",
		AnonymousSessionID: anon.ID,
	})
	var banned *auth.BannedError
	if errors.As(err, &banned) {
		minutes := int(math.Ceil(banned.Remaining.Minutes()))
		s.render(w, r, "login", http.StatusTooManyRequests,
			page{CSRFToken: anon.CSRFToken, ErrorKey: "error.banned", ErrorArgs: []any{minutes}})
		return
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		s.render(w, r, "login", http.StatusUnauthorized, page{CSRFToken: anon.CSRFToken, ErrorKey: "error.invalid_credentials"})
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.setSessionCookie(w, r, sess)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.checkCSRF(w, r)
	if !ok {
		return
	}
	if err := s.auth.Logout(r.Context(), sess.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.clearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.isHTTPS(r),
	})
}
