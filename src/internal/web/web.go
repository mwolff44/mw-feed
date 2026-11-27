// SPDX-License-Identifier: GPL-3.0-or-later

// Package web serves the HTTP interface.
package web

import (
	"bytes"
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
	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/i18n"
	"github.com/mwolff44/mw-feed/internal/store"
)

//go:embed templates/*.html
var templateFiles embed.FS

var templates = parseTemplates()

func parseTemplates() *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{
		"actions":     newActionsView,
		"sidebarPage": newSidebarData,
		"itemIn":      newItemContext,
	}).ParseFS(templateFiles, "templates/*.html"))
}

const (
	sessionCookieName = "mw_feed_session"
	// persistentCookieMaxAge is 30 days, in seconds ("stay logged in").
	persistentCookieMaxAge = 30 * 24 * 60 * 60
)

// Options are the runtime options of the web server.
type Options struct {
	// TrustProxy makes the server trust X-Forwarded-For and X-Forwarded-Proto.
	TrustProxy bool
	// Version is shown on the help page; empty means "dev".
	Version string
	// Icons provides the feed icons downloaded from the third-party service.
	// It is only asked while the addFavicon setting is on; nil means never.
	Icons IconSource
}

type server struct {
	opts  Options
	store *store.Store
	auth  *auth.Service
	feeds *feed.Service
}

// NewHandler returns the HTTP handler of the application.
func NewHandler(opts Options, st *store.Store, svc *auth.Service, feeds *feed.Service) http.Handler {
	s := &server{opts: opts, store: st, auth: svc, feeds: feeds}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /install", s.installForm)
	mux.HandleFunc("POST /install", s.install)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /{$}", s.protected(s.reading("all", false)))
	mux.HandleFunc("GET /folder/{id}", s.protected(s.reading("folder", false)))
	mux.HandleFunc("GET /feed/{id}", s.protected(s.reading("feed", false)))
	mux.HandleFunc("GET /password", s.protected(s.passwordForm))
	mux.HandleFunc("POST /password", s.changePassword)
	mux.HandleFunc("GET /starred", s.protected(s.reading("all", true)))
	mux.HandleFunc("GET /starred/folder/{id}", s.protected(s.reading("folder", true)))
	mux.HandleFunc("GET /starred/feed/{id}", s.protected(s.reading("feed", true)))
	mux.HandleFunc("GET /add", s.protected(s.addForm))
	mux.HandleFunc("POST /add", s.add)
	mux.HandleFunc("GET /edit/folder/{id}", s.protected(s.editFolderForm))
	mux.HandleFunc("GET /help", s.protected(s.help))
	mux.HandleFunc("GET /config", s.protected(s.configForm))
	mux.HandleFunc("POST /config", s.config)
	mux.HandleFunc("GET /export", s.protected(s.exportOPML))
	mux.HandleFunc("GET /import", s.protected(s.importForm))
	mux.HandleFunc("POST /import", s.importOPML)
	mux.HandleFunc("GET /edit", s.protected(s.globalEditForm))
	mux.HandleFunc("POST /edit/feeds", s.editFeeds)
	mux.HandleFunc("POST /edit/delete", s.deleteFeeds)
	mux.HandleFunc("GET /edit/feed/{id}", s.protected(s.editFeedForm))
	mux.HandleFunc("POST /edit/feed/{id}", s.editFeed)
	mux.HandleFunc("GET /edit/feed/{id}/delete", s.protected(s.deleteFeedForm))
	mux.HandleFunc("POST /edit/feed/{id}/delete", s.deleteFeed)
	mux.HandleFunc("POST /edit/folder/{id}", s.editFolder)
	mux.HandleFunc("POST /edit/folder/{id}/{move}", s.moveFolder)
	mux.HandleFunc("POST /folder/{id}/toggle", s.toggleFolder)
	mux.HandleFunc("POST /display", s.display)
	mux.HandleFunc("POST /mark", s.markRead)
	mux.HandleFunc("POST /update", s.update)
	mux.HandleFunc("POST /item/{id}/{action}", s.itemAction)
	mux.HandleFunc("GET /item/{id}", s.protected(s.itemFragment))
	mux.HandleFunc("GET /favicon/{id}", s.protected(s.favicon))
	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("/", s.fallback)
	return s.secure(mux)
}

// page is the data handed to every template.
type page struct {
	Locale    string
	Theme     string
	Menu      bool // show the header and its menu (logged-in pages)
	PageTitle string
	Title     string // reader title, from the settings
	CSRFToken string
	Error     string // translated message, filled by render from ErrorKey
	ErrorKey  string
	ErrorArgs []any
	Reading   *readingView // set on reading pages
	Add       *addView     // set on the add-feed page
	Folder    *folderEdit  // set on the folder edit page
	EditFeed  *feedEdit    // set on the feed edit and delete pages
	Global    *globalView  // set on the global edit pages
	Import    *importView  // set on the import pages
	Config    *configView  // set on the configuration page
	Help      *helpView    // set on the help page
	Flash     *flashView   // one-shot message of the session, if any
}

// flashView is a translated one-shot message.
type flashView struct {
	Text  string
	Error bool
}

// T translates a key in the page locale.
func (p page) T(key string) string { return i18n.T(p.Locale, key) }

// Format translates a key whose text has placeholders and fills them.
func (p page) Format(key string, args ...any) string {
	return fmt.Sprintf(i18n.T(p.Locale, key), args...)
}

func (s *server) render(w http.ResponseWriter, r *http.Request, name string, status int, p page) {
	p.Locale, p.Theme = "en", "auto"
	if !errorPages[name] {
		p.Menu = name != "install" && name != "login"
	}
	p.PageTitle = i18n.T(p.Locale, name+".title")
	if st, err := s.store.Settings(r.Context()); err == nil {
		p.Locale, p.Theme, p.Title = st.Locale, st.Theme, st.Title
		p.PageTitle = i18n.T(p.Locale, name+".title")
		if name == "reading" {
			p.PageTitle = p.Reading.Heading + ", " + st.Title
		}
	}
	if p.ErrorKey != "" {
		p.Error = fmt.Sprintf(i18n.T(p.Locale, p.ErrorKey), p.ErrorArgs...)
	}
	p.Flash = s.takeFlash(r, p.Locale)
	// Render first, send after: a failing template must not give a truncated page.
	var out bytes.Buffer
	if err := templates.ExecuteTemplate(&out, name, p); err != nil {
		slog.Error("render template", "template", name, "error", err)
		if name != "internal" {
			s.errorPage(w, r, http.StatusInternalServerError, "internal")
			return
		}
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(out.Bytes())
}

// takeFlash returns the pending message of the caller's session, translated.
func (s *server) takeFlash(r *http.Request, locale string) *flashView {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil
	}
	f, ok, err := s.store.TakeFlash(r.Context(), c.Value)
	if err != nil {
		slog.Error("take flash", "error", err)
		return nil
	}
	if !ok {
		return nil
	}
	counted := make([]string, len(f.Parts))
	for i, part := range f.Parts {
		counted[i] = i18n.N(locale, part.Key, part.N)
	}
	if f.Key == "" { // only counted parts: one sentence
		return &flashView{Text: strings.Join(counted, ", ") + ".", Error: f.Error}
	}
	args := make([]any, len(f.Args), len(f.Args)+1)
	for i, a := range f.Args {
		args[i] = a
	}
	if len(counted) > 0 { // the counted parts fill the last placeholder
		args = append(args, strings.Join(counted, ", "))
	}
	return &flashView{Text: fmt.Sprintf(i18n.T(locale, f.Key), args...), Error: f.Error}
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

// serverError logs the failure and shows a generic page: no detail is given
// to the visitor.
func (s *server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "error", err)
	s.errorPage(w, r, http.StatusInternalServerError, "internal")
}

func (s *server) notFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "notfound")
}

func (s *server) badRequest(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusBadRequest, "badrequest")
}

func (s *server) forbidden(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusForbidden, "forbidden")
}

// errorPages are the templates of error pages: their menu depends on the
// session, not on the page.
var errorPages = map[string]bool{"notfound": true, "forbidden": true, "internal": true, "badrequest": true}

// errorPage renders an error page, with the menu if the caller is logged in.
func (s *server) errorPage(w http.ResponseWriter, r *http.Request, status int, name string) {
	p := page{}
	if sess, ok := s.currentSession(r); ok && sess.Authenticated {
		p.Menu, p.CSRFToken = true, sess.CSRFToken
	}
	s.render(w, r, name, status, p)
}

func (s *server) installForm(w http.ResponseWriter, r *http.Request) {
	installed, err := s.auth.Installed(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if installed {
		s.notFound(w, r)
		return
	}
	sess, err := s.anonymousSession(w, r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "install", http.StatusOK, page{CSRFToken: sess.CSRFToken})
}

// fallback handles every unknown path: an instance not installed yet goes to
// the installation, otherwise the page does not exist.
func (s *server) fallback(w http.ResponseWriter, r *http.Request) {
	installed, err := s.auth.Installed(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !installed {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return
	}
	s.notFound(w, r)
}

// protected wraps a handler that needs an authenticated session: not installed
// instances go to /install, visitors without a session go to /login.
func (s *server) protected(h func(http.ResponseWriter, *http.Request, auth.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		installed, err := s.auth.Installed(r.Context())
		if err != nil {
			s.serverError(w, r, err)
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
		s.serverError(w, r, err)
		return
	}
	s.setSessionCookie(w, r, renewed)
	if err := s.store.SetFlash(r.Context(), renewed.ID, store.Flash{Key: "flash.password_changed"}); err != nil {
		slog.Error("set flash", "error", err)
	}
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
		s.forbidden(w, r)
		return auth.Session{}, false
	}
	return sess, true
}

// authenticated guards the actions of a logged-in user: the request must carry
// the CSRF token of its session, and the session must be authenticated. An
// anonymous session (the visitor of the login form has one) is not enough: it
// is sent to the login page.
func (s *server) authenticated(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	sess, ok := s.checkCSRF(w, r)
	if !ok {
		return auth.Session{}, false
	}
	if !sess.Authenticated {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return auth.Session{}, false
	}
	return sess, true
}

func (s *server) install(w http.ResponseWriter, r *http.Request) {
	installed, err := s.auth.Installed(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if installed {
		s.notFound(w, r)
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
		s.serverError(w, r, err)
		return
	}
	sess, err := s.auth.Login(r.Context(), auth.LoginRequest{
		IP: s.clientIP(r), Login: login, Password: password, AnonymousSessionID: anon.ID,
	})
	if err != nil {
		s.serverError(w, r, err)
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
		s.serverError(w, r, err)
		return
	}
	if !installed {
		http.Redirect(w, r, "/install", http.StatusSeeOther)
		return
	}
	sess, err := s.anonymousSession(w, r)
	if err != nil {
		s.serverError(w, r, err)
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
		s.serverError(w, r, err)
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
		s.serverError(w, r, err)
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
		s.serverError(w, r, err)
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
