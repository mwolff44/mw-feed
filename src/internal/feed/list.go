// SPDX-License-Identifier: GPL-3.0-or-later

package feed

import (
	"context"
	"errors"
	"fmt"

	"github.com/mwolff44/mw-feed/internal/store"
)

// Selection designates a set of items: all, a folder, or a feed.
type Selection = store.Selection

// AllItems selects the items of every existing feed.
func AllItems() Selection { return Selection{Kind: store.SelectAll} }

// InFolder selects the items of the feeds of a folder.
func InFolder(id int64) Selection { return Selection{Kind: store.SelectFolder, ID: id} }

// InFeed selects the items of one feed.
func InFeed(id int64) Selection { return Selection{Kind: store.SelectFeed, ID: id} }

// The filters and orders of a list, as stored in the settings.
const (
	FilterUnread    = "unread"
	FilterAll       = "all"
	OrderNewerFirst = "newerFirst"
	OrderOlderFirst = "olderFirst"
)

// ErrInvalidQuery means a list query has an unknown filter or order.
var ErrInvalidQuery = errors.New("invalid list query")

// ItemQuery asks for a page of items. The zero Selection is "all". An empty
// Filter or Order means the one of the settings.
type ItemQuery struct {
	Selection Selection
	Filter    string
	Order     string
	// Page starts at 1. PerPage is the page size: 0 means the byPage setting.
	Page    int
	PerPage int
}

// ItemPage is one page of items.
type ItemPage struct {
	Items []Item
	// Page is the page served (1-based); PageCount is at least 1; Total counts
	// the items of the whole selection.
	Page      int
	PageCount int
	Total     int
}

// checkSelection returns ErrNotFound if the folder or feed of s does not exist.
func (s *Service) checkSelection(ctx context.Context, sel Selection) error {
	var err error
	switch sel.Kind {
	case store.SelectFeed:
		_, err = s.store.FeedByID(ctx, sel.ID)
	case store.SelectFolder:
		_, err = s.store.FolderByID(ctx, sel.ID)
	}
	if err != nil {
		return fmt.Errorf("selection: %w", err)
	}
	return nil
}

// ListItems returns a page of items.
func (s *Service) ListItems(ctx context.Context, q ItemQuery) (ItemPage, error) {
	if err := s.checkSelection(ctx, q.Selection); err != nil {
		return ItemPage{}, err
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return ItemPage{}, err
	}
	filter, order := q.Filter, q.Order
	if filter == "" {
		filter = settings.Filter
	}
	if order == "" {
		order = settings.Order
	}
	if filter != FilterUnread && filter != FilterAll {
		return ItemPage{}, fmt.Errorf("%w: filter %q", ErrInvalidQuery, filter)
	}
	if order != OrderNewerFirst && order != OrderOlderFirst {
		return ItemPage{}, fmt.Errorf("%w: order %q", ErrInvalidQuery, order)
	}
	perPage, err := s.pageSize(ctx, q.PerPage)
	if err != nil {
		return ItemPage{}, err
	}
	unreadOnly := filter == FilterUnread
	total, err := s.store.CountItems(ctx, q.Selection, unreadOnly)
	if err != nil {
		return ItemPage{}, err
	}
	page, pageCount := pageOf(q.Page, perPage, total)
	items, err := s.store.ListItems(ctx, store.ItemList{
		Selection: q.Selection, UnreadOnly: unreadOnly, OldestFirst: order == OrderOlderFirst,
		Limit: perPage, Offset: (page - 1) * perPage,
	})
	if err != nil {
		return ItemPage{}, err
	}
	return ItemPage{Items: items, Page: page, PageCount: pageCount, Total: total}, nil
}

// pageSize resolves the requested page size: 0 means the byPage setting.
func (s *Service) pageSize(ctx context.Context, requested int) (int, error) {
	if requested < 0 {
		return 0, fmt.Errorf("%w: page size %d", ErrInvalidQuery, requested)
	}
	if requested > 0 {
		return requested, nil
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return 0, err
	}
	return settings.ByPage, nil
}

// pageOf returns the page actually served and the number of pages: pages start
// at 1, out of range requests become the first or the last page, and an empty
// list has one page.
func pageOf(requested, perPage, total int) (page, pageCount int) {
	pageCount = max(1, (total+perPage-1)/perPage)
	return min(max(requested, 1), pageCount), pageCount
}

// StarredQuery asks for a page of starred items. They are always listed most
// recently starred first, whatever the read state or the order setting.
type StarredQuery struct {
	Selection Selection
	Page      int
	PerPage   int
}

// ListStarred returns a page of starred items. "All" includes the starred
// items of deleted feeds; a folder or a feed only gives those of its feeds.
func (s *Service) ListStarred(ctx context.Context, q StarredQuery) (ItemPage, error) {
	if err := s.checkSelection(ctx, q.Selection); err != nil {
		return ItemPage{}, err
	}
	perPage, err := s.pageSize(ctx, q.PerPage)
	if err != nil {
		return ItemPage{}, err
	}
	total, err := s.store.CountStarred(ctx, q.Selection)
	if err != nil {
		return ItemPage{}, err
	}
	page, pageCount := pageOf(q.Page, perPage, total)
	items, err := s.store.ListStarred(ctx, q.Selection, perPage, (page-1)*perPage)
	if err != nil {
		return ItemPage{}, err
	}
	return ItemPage{Items: items, Page: page, PageCount: pageCount, Total: total}, nil
}

// Item returns one item, with its content.
func (s *Service) Item(ctx context.Context, id int64) (Item, error) {
	return s.store.ItemByID(ctx, id)
}

// Count is a number of unread items out of a total.
type Count struct {
	Unread int
	Total  int
}

// Counts holds the counters shown beside the feeds and folders. Every existing
// feed and folder has an entry, even without items.
type Counts struct {
	// All counts each item of each existing feed once, even if its feed is in
	// several folders: it is not the sum of the folders.
	All     Count
	Feeds   map[int64]Count
	Folders map[int64]Count
}

// Counts computes the counters, by query: nothing is stored.
func (s *Service) Counts(ctx context.Context) (Counts, error) {
	feeds, err := s.store.Feeds(ctx)
	if err != nil {
		return Counts{}, err
	}
	folders, err := s.store.Folders(ctx)
	if err != nil {
		return Counts{}, err
	}
	stored, err := s.store.CountItemsByFeedAndFolder(ctx)
	if err != nil {
		return Counts{}, err
	}
	out := Counts{Feeds: map[int64]Count{}, Folders: map[int64]Count{}}
	for _, f := range feeds {
		c := stored.ByFeed[f.ID]
		out.Feeds[f.ID] = Count{Unread: c.Unread, Total: c.Total}
		out.All.Unread += c.Unread
		out.All.Total += c.Total
	}
	for _, f := range folders {
		c := stored.ByFolder[f.ID]
		out.Folders[f.ID] = Count{Unread: c.Unread, Total: c.Total}
	}
	return out, nil
}

// MarkRead marks every item of a selection read or unread, whatever the page or
// filter the user was looking at, and returns how many items changed state.
// Stars are not affected.
func (s *Service) MarkRead(ctx context.Context, sel Selection, read bool) (int, error) {
	if err := s.checkSelection(ctx, sel); err != nil {
		return 0, err
	}
	return s.store.SetReadForSelection(ctx, sel, read)
}
