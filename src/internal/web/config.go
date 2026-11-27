// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mwolff44/mw-feed/internal/auth"
	"github.com/mwolff44/mw-feed/internal/store"
)

// configView is the data of the configuration page: the settings as the form
// shows them. Numbers are kept as text so that what was typed comes back.
type configView struct {
	store.Settings
	ByPageText, MaxItemsText, MaxUpdateText string
}

func newConfigView(st store.Settings) *configView {
	return &configView{
		Settings: st, ByPageText: strconv.Itoa(st.ByPage),
		MaxItemsText: strconv.Itoa(st.MaxItems), MaxUpdateText: strconv.Itoa(st.MaxUpdate),
	}
}

func (s *server) configForm(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	st, err := s.store.Settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "config", http.StatusOK, page{CSRFToken: sess.CSRFToken, Config: newConfigView(st)})
}

// validRedirector reports whether v is allowed as the link anonymisation
// prefix: empty, "noreferrer", or an absolute http(s) URL.
func validRedirector(v string) bool {
	if v == "" || v == "noreferrer" {
		return true
	}
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// config handles POST /config.
func (s *server) config(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	form := r.PostForm
	checked := func(name string) bool { return form.Get(name) != "" } // an unchecked box is not sent
	st := store.Settings{
		Title: strings.TrimSpace(form.Get("title")), Locale: form.Get("locale"), Theme: form.Get("theme"),
		Redirector: strings.TrimSpace(form.Get("redirector")), View: form.Get("view"), Filter: form.Get("filter"),
		Order: form.Get("order"), ListFeeds: checked("list_feeds"), Autohide: checked("autohide"),
		AddFavicon: checked("add_favicon"), Blank: checked("blank"), AutoreadItem: checked("autoread_item"),
		Autofocus: checked("autofocus"), Preload: checked("preload"), Swipe: checked("swipe"),
	}
	view := &configView{Settings: st, ByPageText: form.Get("by_page"), MaxItemsText: form.Get("max_items"), MaxUpdateText: form.Get("max_update")}
	refuse := func(key string) {
		s.render(w, r, "config", http.StatusBadRequest, page{CSRFToken: sess.CSRFToken, Config: view, ErrorKey: key})
	}
	var numbersOK bool
	st.ByPage, st.MaxItems, st.MaxUpdate, numbersOK = wholeNumbers(view.ByPageText, view.MaxItemsText, view.MaxUpdateText)
	switch {
	case st.Title == "":
		refuse("config.error_title")
		return
	case !numbersOK:
		refuse("config.error_number")
		return
	case !validRedirector(st.Redirector):
		refuse("config.error_redirector")
		return
	}
	if err := s.store.SaveSettings(r.Context(), st); err != nil {
		if isInvalidSettings(err) { // a value the form does not offer: not a form of ours
			s.badRequest(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.flash(r, sess, store.Flash{Key: "flash.settings_saved"})
	http.Redirect(w, r, "/config", http.StatusSeeOther)
}

// wholeNumbers parses texts as integers of at least 1; ok is false if any is not.
func wholeNumbers(texts ...string) (a, b, c int, ok bool) {
	var n [3]int
	for i, text := range texts {
		v, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || v < 1 {
			return 0, 0, 0, false
		}
		n[i] = v
	}
	return n[0], n[1], n[2], true
}

func isInvalidSettings(err error) bool { return errors.Is(err, store.ErrInvalidSettings) }
