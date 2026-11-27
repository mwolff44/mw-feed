// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mwolff44/mw-feed/internal/anonymize"
	"github.com/mwolff44/mw-feed/internal/auth"
	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/i18n"
	"github.com/mwolff44/mw-feed/internal/store"
)

// itemView is an item ready to be displayed.
type itemView struct {
	ID          int64
	Title       string
	FeedTitle   string
	Icon        string // URL of the icon of the feed
	Description string
	Date        string
	Link        anonymize.Attrs // the item link, rewritten by the link settings
	Via         anonymize.Attrs // the source link; empty Href when there is none
	Source      string          // the translated label of the source link
	Author      string
	Content     template.HTML // sanitised when the feed was fetched, then its links rewritten
	Read        bool
	Open        bool
	Starred     bool
}

// readingView is the data of a reading page.
type readingView struct {
	Heading string
	Count   string       // "2 unread" or "12 items"
	Sidebar *sidebarView // nil when the feed list is hidden
	Items   []itemView
	// Menu data: what the display choices of the menu currently are.
	Kind                string // "all", "folder" or "feed": the selection
	SelectionID         int64
	View, Order, Filter string
	ListFeeds           bool
	// SwitchURL leads to the same selection among starred items, or back.
	SwitchURL string
	// EditURL leads to the edit page of the selection.
	EditURL  string
	Expanded bool   // the view setting: every item in full, no <details>
	Pager    *pager // nil when there is a single page
	Empty    string // message shown instead of an empty list
	// Starred marks the pages of starred items, which have no read buttons.
	Starred bool
	// Return is the local URL the action forms send the visitor back to.
	Return string
}

// pager holds the links to the neighbour pages; an empty URL means there is none.
type pager struct {
	Prev, Next  string
	Page, Count int
}

func pagerOf(path string, list feed.ItemPage) *pager {
	if list.PageCount < 2 {
		return nil
	}
	p := &pager{Page: list.Page, Count: list.PageCount}
	if list.Page > 1 {
		p.Prev = path + "?page=" + strconv.Itoa(list.Page-1)
	}
	if list.Page < list.PageCount {
		p.Next = path + "?page=" + strconv.Itoa(list.Page+1)
	}
	return p
}

// selectionFromPath builds the selection of a reading page from the
// {id} of its URL; kind is "all", "folder" or "feed". ok is false when the
// identifier is not a positive number.
func selectionFromPath(kind string, r *http.Request) (sel feed.Selection, ok bool) {
	switch kind {
	case "folder", "feed":
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			return feed.Selection{}, false
		}
		if kind == "folder" {
			return feed.InFolder(id), true
		}
		return feed.InFeed(id), true
	}
	return feed.AllItems(), true
}

// reading serves the item list of a selection: its items, or only its starred
// items.
func (s *server) reading(kind string, starred bool) func(http.ResponseWriter, *http.Request, auth.Session) {
	return func(w http.ResponseWriter, r *http.Request, sess auth.Session) {
		sel, ok := selectionFromPath(kind, r)
		if !ok {
			s.notFound(w, r)
			return
		}
		ctx := r.Context()
		var list feed.ItemPage
		var err error
		pageNumber, convErr := strconv.Atoi(r.URL.Query().Get("page"))
		if convErr != nil {
			pageNumber = 1 // the service clamps out of range pages
		}
		if starred {
			list, err = s.feeds.ListStarred(ctx, feed.StarredQuery{Selection: sel, Page: pageNumber})
		} else {
			list, err = s.feeds.ListItems(ctx, feed.ItemQuery{Selection: sel, Page: pageNumber})
		}
		if errors.Is(err, feed.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		st, err2 := s.store.Settings(ctx)
		if err == nil {
			err = err2
		}
		var heading string
		if err == nil {
			heading, err = s.selectionTitle(r, st.Locale, sel, starred)
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		open, _ := strconv.ParseInt(r.URL.Query().Get("open"), 10, 64)
		side, err := s.sidebar(ctx, st, sel, starred)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		view := &readingView{Heading: heading, Return: r.URL.RequestURI(), Starred: starred, Sidebar: side,
			Kind: kind, SelectionID: sel.ID, View: st.View, Order: st.Order, Filter: st.Filter, ListFeeds: st.ListFeeds,
			SwitchURL: switchURL(kind, sel.ID, starred), EditURL: editURL(kind, sel.ID)}
		if starred || st.Filter == feed.FilterAll {
			view.Count = i18n.N(st.Locale, "reading.items", list.Total)
		} else {
			view.Count = i18n.N(st.Locale, "reading.unread", list.Total)
		}
		view.Expanded = st.View == "expanded"
		view.Pager = pagerOf(r.URL.Path, list)
		switch {
		case len(list.Items) > 0:
		case starred:
			view.Empty = i18n.T(st.Locale, "empty.starred")
		case st.Filter == feed.FilterAll:
			view.Empty = i18n.T(st.Locale, "empty.all")
		default:
			view.Empty = i18n.T(st.Locale, "empty.unread")
		}
		links := anonymize.Options{Redirector: st.Redirector, Blank: st.Blank}
		for _, it := range list.Items {
			via := anonymize.Attrs{}
			if it.Via != "" {
				via = anonymize.Link(it.Via, links)
			}
			view.Items = append(view.Items, itemView{
				ID: it.ID, Title: it.Title, FeedTitle: it.FeedTitle, Icon: iconURL(it.FeedID), Description: it.Description,
				Date: i18n.FormatTime(st.Locale, it.PublishedAt), Link: anonymize.Link(it.Link, links), Via: via, Source: i18n.T(st.Locale, "item.source"), Author: it.Author,
				Content: template.HTML(anonymize.Links(it.Content, links)), //nolint:gosec // sanitised once, before storage; the rewrite only touches link attributes
				Read:    it.Read, Open: it.ID == open, Starred: !it.StarredAt.IsZero(),
			})
		}
		s.render(w, r, "reading", http.StatusOK, page{CSRFToken: sess.CSRFToken, Reading: view})
	}
}

// selectionTitle is the name of a selection: "All items" ("Starred" on the
// starred pages), or the title of its folder or feed.
func (s *server) selectionTitle(r *http.Request, locale string, sel feed.Selection, starred bool) (string, error) {
	switch sel.Kind {
	case store.SelectFolder:
		f, err := s.store.FolderByID(r.Context(), sel.ID)
		return f.Title, err
	case store.SelectFeed:
		f, err := s.store.FeedByID(r.Context(), sel.ID)
		return f.Title, err
	}
	if starred {
		return i18n.T(locale, "reading.starred"), nil
	}
	return i18n.T(locale, "reading.all_items"), nil
}

// safeReturn returns target if it is a local path ("/..." but not "//...",
// no backslash, no control character, no scheme or host), else "/": a form
// field is never trusted as a redirect target.
func safeReturn(target string) string {
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || strings.ContainsAny(target, "\\") {
		return "/"
	}
	for _, c := range target {
		if c < 0x20 || c == 0x7f {
			return "/"
		}
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return target
}

// itemAction handles POST /item/{id}/{read|unread|star|unstar}.
func (s *server) itemAction(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticated(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r)
		return
	}
	switch r.PathValue("action") {
	case "read":
		err = s.feeds.SetRead(r.Context(), id, true)
	case "unread":
		err = s.feeds.SetRead(r.Context(), id, false)
	case "star":
		err = s.feeds.SetStarred(r.Context(), id, true)
	case "unstar":
		err = s.feeds.SetStarred(r.Context(), id, false)
	default:
		s.notFound(w, r)
		return
	}
	if errors.Is(err, feed.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, safeReturn(r.PostFormValue("return")), http.StatusSeeOther)
}

// actionsView is what the read and star forms of an item need: the item and the page.
type actionsView struct {
	page
	ID      int64
	Read    bool
	Starred bool
}

func newActionsView(it itemView, p page) actionsView {
	return actionsView{page: p, ID: it.ID, Read: it.Read, Starred: it.Starred}
}

// switchURL is the URL of the same selection among the starred items, or among
// all items when starred is true.
func switchURL(kind string, id int64, starred bool) string {
	path := ""
	if kind != "all" {
		path = "/" + kind + "/" + strconv.FormatInt(id, 10)
	}
	if starred {
		if path == "" {
			return "/"
		}
		return path
	}
	return "/starred" + path
}

// display handles POST /display: it changes the view, filter, order and
// feed-list settings (any subset of them) and goes back to the page.
func (s *server) display(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticated(w, r); !ok {
		return
	}
	st, err := s.store.Settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	form := r.PostForm
	if v := form.Get("view"); v != "" {
		st.View = v
	}
	if v := form.Get("order"); v != "" {
		st.Order = v
	}
	if v := form.Get("filter"); v != "" {
		st.Filter = v
	}
	switch form.Get("listFeeds") {
	case "":
	case "show":
		st.ListFeeds = true
	case "hide":
		st.ListFeeds = false
	default:
		s.badRequest(w, r)
		return
	}
	if err := s.store.SaveSettings(r.Context(), st); errors.Is(err, store.ErrInvalidSettings) {
		s.badRequest(w, r)
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, safeReturn(r.PostFormValue("return")), http.StatusSeeOther)
}

// selectionFromForm reads the selection of an action form (kind, id). The
// error is a bad request: the form is not one this server produced.
func selectionFromForm(r *http.Request) (feed.Selection, error) {
	switch r.PostFormValue("kind") {
	case "all":
		return feed.AllItems(), nil
	case "folder", "feed":
		id, err := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
		if err != nil || id <= 0 {
			return feed.Selection{}, errors.New("invalid id")
		}
		if r.PostFormValue("kind") == "folder" {
			return feed.InFolder(id), nil
		}
		return feed.InFeed(id), nil
	}
	return feed.Selection{}, errors.New("invalid kind")
}

// flash stores a one-shot message for the session, translated on display. A
// failure to store it is logged, not shown: the action itself succeeded.
func (s *server) flash(r *http.Request, sess auth.Session, f store.Flash) {
	if err := s.store.SetFlash(r.Context(), sess.ID, f); err != nil {
		slog.Error("set flash", "error", err)
	}
}

// markRead handles POST /mark: the whole selection becomes read or unread.
func (s *server) markRead(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	sel, err := selectionFromForm(r)
	read := r.PostFormValue("read")
	if err != nil || (read != "0" && read != "1") {
		s.badRequest(w, r)
		return
	}
	n, err := s.feeds.MarkRead(r.Context(), sel, read == "1")
	if errors.Is(err, feed.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	key := "flash.marked_unread"
	if read == "1" {
		key = "flash.marked_read"
	}
	s.flash(r, sess, store.Flash{Parts: []store.Counted{{Key: key, N: n}}})
	http.Redirect(w, r, safeReturn(r.PostFormValue("return")), http.StatusSeeOther)
}

// update handles POST /update: it downloads every feed of the selection, waits
// for all of them, and reports how it went.
func (s *server) update(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	sel, err := selectionFromForm(r)
	if err != nil {
		s.badRequest(w, r)
		return
	}
	results, err := s.feeds.UpdateSelection(r.Context(), sel)
	if errors.Is(err, feed.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		// The caller went away (context canceled): nobody to answer.
		slog.Warn("manual update interrupted", "error", err)
		return
	}
	updated, failed, added := 0, 0, 0
	for _, res := range results {
		if res.Err != nil {
			failed++
			continue
		}
		updated++
		added += res.Result.NewItems
	}
	parts := []store.Counted{{Key: "flash.feeds_updated", N: updated}, {Key: "flash.new_items", N: added}}
	if failed > 0 {
		parts = append(parts, store.Counted{Key: "flash.feeds_failed", N: failed})
	}
	s.flash(r, sess, store.Flash{Error: failed > 0, Parts: parts})
	http.Redirect(w, r, safeReturn(r.PostFormValue("return")), http.StatusSeeOther)
}

// editURL is the edit page of a selection: everything, one folder or one feed.
func editURL(kind string, id int64) string {
	if kind == "all" {
		return "/edit"
	}
	return "/edit/" + kind + "/" + strconv.FormatInt(id, 10)
}
