// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mwolff44/mw-feed/internal/auth"
	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/i18n"
	"github.com/mwolff44/mw-feed/internal/store"
)

// folderChoice is a folder offered as a checkbox.
type folderChoice struct {
	ID      int64
	Title   string
	Checked bool
}

// addView is the data of the add-feed page.
type addView struct {
	URL       string
	NewFolder string
	Folders   []folderChoice
}

// folderChoices lists every folder as a checkbox, checked when its ID is in checked.
func (s *server) folderChoices(ctx context.Context, checked []int64) ([]folderChoice, error) {
	folders, err := s.feeds.Folders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]folderChoice, len(folders))
	for i, f := range folders {
		out[i] = folderChoice{ID: f.ID, Title: f.Title, Checked: slices.Contains(checked, f.ID)}
	}
	return out, nil
}

// idsFromForm reads every positive integer given under a form field name;
// anything else is ignored.
func idsFromForm(r *http.Request, name string) []int64 {
	var ids []int64
	for _, v := range r.PostForm[name] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *server) addForm(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	folders, err := s.folderChoices(r.Context(), nil)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "add", http.StatusOK, page{CSRFToken: sess.CSRFToken, Add: &addView{Folders: folders}})
}

// add handles POST /add: it subscribes to a feed.
func (s *server) add(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	checked := idsFromForm(r, "folder")
	req := feed.AddFeedRequest{URL: r.PostFormValue("url"), FolderIDs: checked, NewFolder: r.PostFormValue("new_folder")}
	added, err := s.feeds.AddFeed(ctx, req)
	if err == nil {
		list, lerr := s.feeds.ListItems(ctx, feed.ItemQuery{Selection: feed.InFeed(added.ID), Filter: feed.FilterAll, PerPage: 1})
		if lerr != nil {
			s.serverError(w, r, lerr)
			return
		}
		s.flash(r, sess, store.Flash{
			Key: "flash.feed_added", Args: []string{added.Title},
			Parts: []store.Counted{{Key: "flash.items_imported", N: list.Total}},
		})
		http.Redirect(w, r, "/feed/"+strconv.FormatInt(added.ID, 10), http.StatusSeeOther)
		return
	}
	if errors.Is(err, context.Canceled) {
		return // the browser went away
	}
	if errors.Is(err, store.ErrNotFound) { // a folder that does not exist: not a form of ours
		s.badRequest(w, r)
		return
	}
	folders, ferr := s.folderChoices(ctx, checked)
	if ferr != nil {
		s.serverError(w, r, ferr)
		return
	}
	p := page{CSRFToken: sess.CSRFToken, Add: &addView{URL: req.URL, NewFolder: req.NewFolder, Folders: folders}}
	switch {
	case errors.Is(err, feed.ErrInvalidURL):
		p.ErrorKey = "add.error_url"
	case errors.Is(err, feed.ErrDuplicateFeed):
		p.ErrorKey = "add.error_duplicate"
	default:
		p.ErrorKey, p.ErrorArgs = "add.error_failed", []any{err.Error()}
	}
	s.render(w, r, "add", http.StatusBadRequest, p)
}

// folderEdit is the data of the folder edit page.
type folderEdit struct {
	ID    int64
	Title string
}

// folderFromPath reads the {id} of the URL and loads the folder; ok is false
// after the 404 page was served.
func (s *server) folderFromPath(w http.ResponseWriter, r *http.Request) (store.Folder, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r)
		return store.Folder{}, false
	}
	f, err := s.store.FolderByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return store.Folder{}, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return store.Folder{}, false
	}
	return f, true
}

func (s *server) editFolderForm(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	f, ok := s.folderFromPath(w, r)
	if !ok {
		return
	}
	s.render(w, r, "folder", http.StatusOK, page{CSRFToken: sess.CSRFToken, Folder: &folderEdit{ID: f.ID, Title: f.Title}})
}

// editFolder handles POST /edit/folder/{id}: rename, or delete when the title is empty.
func (s *server) editFolder(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	f, ok := s.folderFromPath(w, r)
	if !ok {
		return
	}
	title := strings.TrimSpace(r.PostFormValue("title"))
	err := s.feeds.RenameFolder(r.Context(), f.ID, title)
	if errors.Is(err, feed.ErrFolderExists) {
		s.render(w, r, "folder", http.StatusBadRequest, page{
			CSRFToken: sess.CSRFToken, ErrorKey: "folder.error_duplicate", Folder: &folderEdit{ID: f.ID, Title: title},
		})
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if title == "" {
		s.flash(r, sess, store.Flash{Key: "flash.folder_deleted"})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.flash(r, sess, store.Flash{Key: "flash.folder_saved"})
	http.Redirect(w, r, "/folder/"+strconv.FormatInt(f.ID, 10), http.StatusSeeOther)
}

// moveFolder handles POST /edit/folder/{id}/{up|down}: it swaps the folder with
// its neighbour; at the end of the list nothing changes.
func (s *server) moveFolder(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticated(w, r); !ok {
		return
	}
	f, ok := s.folderFromPath(w, r)
	if !ok {
		return
	}
	direction := r.PathValue("move")
	if direction != "up" && direction != "down" {
		s.notFound(w, r)
		return
	}
	folders, err := s.feeds.Folders(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	ids := make([]int64, len(folders))
	for i, fo := range folders {
		ids[i] = fo.ID
	}
	i := slices.Index(ids, f.ID)
	j := i - 1
	if direction == "down" {
		j = i + 1
	}
	if j >= 0 && j < len(ids) {
		ids[i], ids[j] = ids[j], ids[i]
		if err := s.feeds.ReorderFolders(r.Context(), ids); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	http.Redirect(w, r, safeReturn(r.PostFormValue("return")), http.StatusSeeOther)
}

// toggleFolder handles POST /folder/{id}/toggle.
func (s *server) toggleFolder(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticated(w, r); !ok {
		return
	}
	f, ok := s.folderFromPath(w, r)
	if !ok {
		return
	}
	if err := s.feeds.ToggleFolder(r.Context(), f.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, safeReturn(r.PostFormValue("return")), http.StatusSeeOther)
}

// feedEdit is the data of the feed edit page.
type feedEdit struct {
	ID                                            int64
	Title, XMLURL, SiteURL, Description, Interval string
	NewFolder                                     string
	Folders                                       []folderChoice
	IntervalHelp, LastUpdate, LastError, Warning  string
}

// feedFromPath reads the {id} of the URL and loads the feed; ok is false after
// the 404 page was served.
func (s *server) feedFromPath(w http.ResponseWriter, r *http.Request) (store.Feed, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r)
		return store.Feed{}, false
	}
	f, err := s.store.FeedByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return store.Feed{}, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return store.Feed{}, false
	}
	return f, true
}

// feedEditView builds the page of a feed; the form fields are those given.
func (s *server) feedEditView(ctx context.Context, f store.Feed, st store.Settings, fields feedEdit, checked []int64) (*feedEdit, error) {
	folders, err := s.folderChoices(ctx, checked)
	if err != nil {
		return nil, err
	}
	fields.ID, fields.Folders = f.ID, folders
	fields.IntervalHelp = fmt.Sprintf(i18n.T(st.Locale, "edit_feed.interval_help"), 5, st.MaxUpdate)
	fields.LastUpdate = i18n.T(st.Locale, "edit_feed.never_updated")
	if !f.LastUpdateAt.IsZero() {
		fields.LastUpdate = i18n.FormatTime(st.Locale, f.LastUpdateAt)
	}
	fields.LastError = f.LastError
	if f.LastWarning != "" {
		fields.Warning = i18n.T(st.Locale, "warning."+f.LastWarning)
	}
	return &fields, nil
}

func (s *server) editFeedForm(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	f, ok := s.feedFromPath(w, r)
	if !ok {
		return
	}
	st, err := s.store.Settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	fields := feedEdit{Title: f.Title, XMLURL: f.XMLURL, SiteURL: f.SiteURL, Description: f.Description}
	if f.UpdateInterval != nil {
		fields.Interval = strconv.Itoa(*f.UpdateInterval)
	}
	view, err := s.feedEditView(r.Context(), f, st, fields, f.FolderIDs)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "edit_feed", http.StatusOK, page{CSRFToken: sess.CSRFToken, EditFeed: view})
}

// editFeed handles POST /edit/feed/{id}.
func (s *server) editFeed(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	f, ok := s.feedFromPath(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	fields := feedEdit{
		Title: r.PostFormValue("title"), XMLURL: r.PostFormValue("xml_url"), SiteURL: r.PostFormValue("site_url"),
		Description: r.PostFormValue("description"), Interval: strings.TrimSpace(r.PostFormValue("interval")),
		NewFolder: r.PostFormValue("new_folder"),
	}
	checked := idsFromForm(r, "folder")
	req := feed.EditFeedRequest{
		Title: fields.Title, XMLURL: fields.XMLURL, SiteURL: fields.SiteURL, Description: fields.Description,
		FolderIDs: checked, NewFolder: fields.NewFolder,
	}
	var errorKey string
	if fields.Interval != "" {
		minutes, convErr := strconv.Atoi(fields.Interval)
		if convErr != nil {
			errorKey = "edit_feed.error_interval"
		}
		req.UpdateInterval = &minutes
	}
	if errorKey == "" {
		_, err := s.feeds.EditFeed(ctx, f.ID, req)
		switch {
		case err == nil:
			s.flash(r, sess, store.Flash{Key: "flash.feed_saved"})
			http.Redirect(w, r, "/feed/"+strconv.FormatInt(f.ID, 10), http.StatusSeeOther)
			return
		case errors.Is(err, feed.ErrInvalidFeedTitle):
			errorKey = "edit_feed.error_title"
		case errors.Is(err, feed.ErrInvalidURL):
			errorKey = "add.error_url"
		case errors.Is(err, feed.ErrDuplicateFeed):
			errorKey = "add.error_duplicate"
		case errors.Is(err, store.ErrNotFound): // a folder that does not exist: not a form of ours
			s.badRequest(w, r)
			return
		default:
			s.serverError(w, r, err)
			return
		}
	}
	st, err := s.store.Settings(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view, err := s.feedEditView(ctx, f, st, fields, checked)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "edit_feed", http.StatusBadRequest, page{CSRFToken: sess.CSRFToken, EditFeed: view, ErrorKey: errorKey})
}

func (s *server) deleteFeedForm(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	f, ok := s.feedFromPath(w, r)
	if !ok {
		return
	}
	s.render(w, r, "delete_feed", http.StatusOK, page{CSRFToken: sess.CSRFToken, EditFeed: &feedEdit{ID: f.ID, Title: f.Title}})
}

// deleteFeed handles POST /edit/feed/{id}/delete, after the confirmation page.
func (s *server) deleteFeed(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	f, ok := s.feedFromPath(w, r)
	if !ok {
		return
	}
	if err := s.feeds.DeleteFeed(r.Context(), f.ID); err != nil && !errors.Is(err, feed.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	s.flash(r, sess, store.Flash{Key: "flash.feed_deleted"})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// globalFolder is a folder line of the global edit page.
type globalFolder struct {
	ID      int64
	Title   string
	CanUp   bool
	CanDown bool
}

// globalView is the data of the global edit page.
type globalView struct {
	Folders []globalFolder
	Feeds   []store.Feed
}

func (s *server) globalEditForm(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	ctx := r.Context()
	folders, err := s.feeds.Folders(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	feeds, err := s.feeds.Feeds(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view := &globalView{Feeds: feeds}
	for i, f := range folders {
		view.Folders = append(view.Folders, globalFolder{ID: f.ID, Title: f.Title, CanUp: i > 0, CanDown: i < len(folders)-1})
	}
	s.render(w, r, "global", http.StatusOK, page{CSRFToken: sess.CSRFToken, Global: view})
}

// editFeeds handles POST /edit/feeds: folders added to and removed from the checked feeds.
func (s *server) editFeeds(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	feedIDs := idsFromForm(r, "feed")
	if len(feedIDs) == 0 {
		s.flash(r, sess, store.Flash{Error: true, Key: "flash.no_feed_selected"})
		http.Redirect(w, r, "/edit", http.StatusSeeOther)
		return
	}
	n, err := s.feeds.EditFeedsFolders(r.Context(), feed.EditFoldersRequest{
		FeedIDs: feedIDs, AddFolderIDs: idsFromForm(r, "add_folder"), NewFolder: r.PostFormValue("new_folder"),
		RemoveFolderIDs: idsFromForm(r, "remove_folder"),
	})
	switch {
	case errors.Is(err, feed.ErrInvalidRequest):
		s.flash(r, sess, store.Flash{Error: true, Key: "flash.folder_conflict"})
	case errors.Is(err, feed.ErrNotFound): // a stale or forged form
		s.badRequest(w, r)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	default:
		s.flash(r, sess, store.Flash{Parts: []store.Counted{{Key: "flash.folders_updated", N: n}}})
	}
	http.Redirect(w, r, "/edit", http.StatusSeeOther)
}

// deleteFeeds handles POST /edit/delete. Without confirm=1 it only shows the
// confirmation page; with it, it deletes the checked feeds.
func (s *server) deleteFeeds(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	ids := idsFromForm(r, "feed")
	if len(ids) == 0 {
		s.flash(r, sess, store.Flash{Error: true, Key: "flash.no_feed_selected"})
		http.Redirect(w, r, "/edit", http.StatusSeeOther)
		return
	}
	if r.PostFormValue("confirm") != "1" {
		var feeds []store.Feed
		for _, id := range ids {
			if f, err := s.store.FeedByID(r.Context(), id); err == nil {
				feeds = append(feeds, f)
			} else if !errors.Is(err, store.ErrNotFound) {
				s.serverError(w, r, err)
				return
			}
		}
		s.render(w, r, "delete_feeds", http.StatusOK, page{CSRFToken: sess.CSRFToken, Global: &globalView{Feeds: feeds}})
		return
	}
	n, err := s.feeds.DeleteFeeds(r.Context(), ids)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(r, sess, store.Flash{Parts: []store.Counted{{Key: "flash.feeds_deleted", N: n}}})
	http.Redirect(w, r, "/edit", http.StatusSeeOther)
}
