// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"context"
	"slices"
	"strconv"

	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/i18n"
	"github.com/mwolff44/mw-feed/internal/store"
)

// linkView is a line of the feed list: a link to a selection and its counter.
type linkView struct {
	Title     string
	URL       string
	Tooltip   string
	Icon      string // URL of the feed icon; empty for lines that are not a feed
	Error     bool
	Current   bool
	ShowCount bool
	Count     int
}

type folderView struct {
	linkView
	ID    int64
	Open  bool
	Feeds []linkView
}

// sidebarView is the feed list: all items, the feeds without a folder, then
// the folders with their feeds.
type sidebarView struct {
	All     linkView
	Feeds   []linkView
	Folders []folderView
}

// sidebar builds the feed list for the selection being read. On the starred
// pages the links point to the starred pages and there are no counters
// (plan decision 74); autohide only applies to the other pages (decision 75).
func (s *server) sidebar(ctx context.Context, st store.Settings, sel feed.Selection, starred bool) (*sidebarView, error) {
	if !st.ListFeeds {
		return nil, nil
	}
	feeds, err := s.feeds.Feeds(ctx)
	if err != nil {
		return nil, err
	}
	folders, err := s.feeds.Folders(ctx)
	if err != nil {
		return nil, err
	}
	var counts feed.Counts
	if !starred {
		if counts, err = s.feeds.Counts(ctx); err != nil {
			return nil, err
		}
	}
	prefix := ""
	if starred {
		prefix = "/starred"
	}
	hide := st.Autohide && !starred
	isCurrentFeed := func(id int64) bool { return sel.Kind == store.SelectFeed && sel.ID == id }
	isCurrentFolder := func(id int64) bool { return sel.Kind == store.SelectFolder && sel.ID == id }

	feedLink := func(f feed.Feed) linkView {
		tooltip := f.Description
		if tooltip == "" {
			tooltip = f.Title
		}
		if f.LastError != "" {
			tooltip = f.LastError
		}
		return linkView{
			Title: f.Title, URL: prefix + "/feed/" + strconv.FormatInt(f.ID, 10), Tooltip: tooltip, Icon: iconURL(f.ID),
			Error: f.LastError != "", Current: isCurrentFeed(f.ID), ShowCount: !starred, Count: counts.Feeds[f.ID].Unread,
		}
	}
	rootURL := prefix
	if rootURL == "" {
		rootURL = "/"
	}
	view := &sidebarView{All: linkView{
		Title: i18n.T(st.Locale, "reading.all_items"), URL: rootURL, Current: sel.Kind == store.SelectAll,
		ShowCount: !starred, Count: counts.All.Unread,
	}}
	for _, f := range feeds {
		if len(f.FolderIDs) > 0 {
			continue
		}
		if l := feedLink(f); !hide || l.Count > 0 || l.Current {
			view.Feeds = append(view.Feeds, l)
		}
	}
	for _, fo := range folders {
		fv := folderView{ID: fo.ID, Open: !fo.Collapsed, linkView: linkView{
			Title: fo.Title, URL: prefix + "/folder/" + strconv.FormatInt(fo.ID, 10), Current: isCurrentFolder(fo.ID),
			ShowCount: !starred, Count: counts.Folders[fo.ID].Unread,
		}}
		holdsCurrentFeed := false
		for _, f := range feeds {
			if !slices.Contains(f.FolderIDs, fo.ID) {
				continue
			}
			holdsCurrentFeed = holdsCurrentFeed || isCurrentFeed(f.ID)
			if l := feedLink(f); !hide || l.Count > 0 || l.Current || isCurrentFolder(fo.ID) {
				fv.Feeds = append(fv.Feeds, l)
			}
		}
		if !hide || fv.Count > 0 || fv.Current || holdsCurrentFeed {
			view.Folders = append(view.Folders, fv)
		}
	}
	return view, nil
}

// sidebarData is what the feed list template needs: the list and the page (for texts).
type sidebarData struct {
	page
	*sidebarView
}

func newSidebarData(v *sidebarView, p page) sidebarData { return sidebarData{page: p, sidebarView: v} }
