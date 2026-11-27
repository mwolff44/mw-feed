// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Item is an entry of a feed.
type Item struct {
	ID          int64
	FeedID      int64 // 0 once the feed was deleted (starred items only)
	FeedTitle   string
	Title       string
	Link        string
	Via         string
	Author      string
	Description string
	Content     string
	PublishedAt time.Time
	Read        bool
	StarredAt   time.Time // zero if not starred
}

// NewItem holds the fields of an item to create. New items are unread and not starred.
type NewItem struct {
	FeedID      int64
	FeedTitle   string
	Title       string
	Link        string
	Via         string
	Author      string
	Description string
	Content     string
	PublishedAt time.Time
}

// InsertItem adds an item unless the feed already has one with that link; it
// reports whether the item was added.
func (q *queries) InsertItem(ctx context.Context, it NewItem) (bool, error) {
	res, err := q.db.ExecContext(ctx,
		`INSERT INTO items (feed_id, feed_title, link, title, via, author, description, content, published_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (feed_id, link) DO NOTHING`,
		it.FeedID, it.FeedTitle, it.Link, it.Title, it.Via, it.Author, it.Description, it.Content, it.PublishedAt.Unix())
	if err != nil {
		return false, fmt.Errorf("insert item: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

const itemColumns = `id, COALESCE(feed_id, 0), feed_title, title, link, via, author, description, content,
	published_at, is_read, starred_at`

func (q *queries) queryItems(ctx context.Context, where, order string, args ...any) ([]Item, error) {
	rows, err := q.db.QueryContext(ctx, "SELECT "+itemColumns+" FROM items WHERE "+where+" ORDER BY "+order, args...)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Item
	for rows.Next() {
		var it Item
		var published int64
		var starred sql.NullInt64
		if err := rows.Scan(&it.ID, &it.FeedID, &it.FeedTitle, &it.Title, &it.Link, &it.Via, &it.Author,
			&it.Description, &it.Content, &published, &it.Read, &starred); err != nil {
			return nil, fmt.Errorf("list items: %w", err)
		}
		it.PublishedAt, it.StarredAt = time.Unix(published, 0), timeFromNull(starred)
		out = append(out, it)
	}
	return out, rows.Err()
}

// SetItemRead marks an item read or unread; it reports whether the item exists.
func (q *queries) SetItemRead(ctx context.Context, id int64, read bool) (bool, error) {
	res, err := q.db.ExecContext(ctx, "UPDATE items SET is_read = ? WHERE id = ?", read, id)
	if err != nil {
		return false, fmt.Errorf("mark item: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// StarItem stars an item at the given time, keeping the date of an earlier
// star; it reports whether the item exists.
func (q *queries) StarItem(ctx context.Context, id int64, at time.Time) (bool, error) {
	res, err := q.db.ExecContext(ctx, "UPDATE items SET starred_at = COALESCE(starred_at, ?) WHERE id = ?", at.Unix(), id)
	if err != nil {
		return false, fmt.Errorf("star item: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// UnstarItem removes the star of an item. An item whose feed was deleted is
// deleted too, since nothing refers to it any more. It reports whether the item existed.
func (q *queries) UnstarItem(ctx context.Context, id int64) (bool, error) {
	res, err := q.db.ExecContext(ctx, "DELETE FROM items WHERE id = ? AND feed_id IS NULL", id)
	if err != nil {
		return false, fmt.Errorf("unstar item: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		return n > 0, err
	}
	res, err = q.db.ExecContext(ctx, "UPDATE items SET starred_at = NULL WHERE id = ?", id)
	if err != nil {
		return false, fmt.Errorf("unstar item: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ItemLinks returns the set of links of the items of a feed.
func (q *queries) ItemLinks(ctx context.Context, feedID int64) (map[string]bool, error) {
	rows, err := q.db.QueryContext(ctx, "SELECT link FROM items WHERE feed_id = ?", feedID)
	if err != nil {
		return nil, fmt.Errorf("list item links: %w", err)
	}
	defer func() { _ = rows.Close() }()
	links := map[string]bool{}
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, fmt.Errorf("list item links: %w", err)
		}
		links[l] = true
	}
	return links, rows.Err()
}

// ItemState is the light view of an item that retention needs.
type ItemState struct {
	ID          int64
	Link        string
	Read        bool
	Starred     bool
	PublishedAt time.Time
}

// ItemStates returns the retention-relevant state of every item of a feed.
func (q *queries) ItemStates(ctx context.Context, feedID int64) ([]ItemState, error) {
	rows, err := q.db.QueryContext(ctx,
		"SELECT id, link, is_read, starred_at IS NOT NULL, published_at FROM items WHERE feed_id = ?", feedID)
	if err != nil {
		return nil, fmt.Errorf("list item states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ItemState
	for rows.Next() {
		var st ItemState
		var published int64
		if err := rows.Scan(&st.ID, &st.Link, &st.Read, &st.Starred, &published); err != nil {
			return nil, fmt.Errorf("list item states: %w", err)
		}
		st.PublishedAt = time.Unix(published, 0)
		out = append(out, st)
	}
	return out, rows.Err()
}

// DeleteItems removes items by ID.
func (q *queries) DeleteItems(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if _, err := q.db.ExecContext(ctx, "DELETE FROM items WHERE id = ?", id); err != nil {
			return fmt.Errorf("delete item: %w", err)
		}
	}
	return nil
}

// SelectionKind tells which items a Selection covers.
type SelectionKind int

// The kinds of selection.
const (
	SelectAll    SelectionKind = iota // every item of every existing feed
	SelectFolder                      // the items of the feeds of a folder
	SelectFeed                        // the items of one feed
)

// Selection designates a set of items: all, a folder, or a feed.
type Selection struct {
	Kind SelectionKind
	ID   int64 // folder or feed ID; unused for SelectAll
}

// ItemList describes a page of items to read.
type ItemList struct {
	Selection   Selection
	UnreadOnly  bool
	OldestFirst bool
	Limit       int
	Offset      int
}

// where returns the SQL condition selecting the items of s. Items of deleted
// feeds (feed_id NULL) belong to no selection.
func (s Selection) where() (string, []any) {
	switch s.Kind {
	case SelectFeed:
		return "feed_id = ?", []any{s.ID}
	case SelectFolder:
		return "feed_id IN (SELECT feed_id FROM feed_folders WHERE folder_id = ?)", []any{s.ID}
	default:
		return "feed_id IS NOT NULL", nil
	}
}

// condition returns the SQL condition of a selection, optionally limited to unread items.
func condition(sel Selection, unreadOnly bool) (string, []any) {
	where, args := sel.where()
	if unreadOnly {
		where += " AND is_read = 0"
	}
	return where, args
}

// CountItems returns the number of items of a selection.
func (q *queries) CountItems(ctx context.Context, sel Selection, unreadOnly bool) (int, error) {
	where, args := condition(sel, unreadOnly)
	var total int
	if err := q.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM items WHERE "+where, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count items: %w", err)
	}
	return total, nil
}

// ListItems returns a page of the selected items.
func (q *queries) ListItems(ctx context.Context, l ItemList) ([]Item, error) {
	where, args := condition(l.Selection, l.UnreadOnly)
	order := "published_at DESC, id DESC"
	if l.OldestFirst {
		order = "published_at ASC, id ASC"
	}
	return q.queryItems(ctx, where, order+" LIMIT ? OFFSET ?", append(args, l.Limit, l.Offset)...)
}

// ItemByID returns an item, or ErrNotFound.
func (q *queries) ItemByID(ctx context.Context, id int64) (Item, error) {
	items, err := q.queryItems(ctx, "id = ?", "id", id)
	if err != nil {
		return Item{}, err
	}
	if len(items) == 0 {
		return Item{}, ErrNotFound
	}
	return items[0], nil
}

// ItemCount is a number of unread items and a total number of items.
type ItemCount struct {
	Unread int
	Total  int
}

// ItemCounts holds the counters of the items of existing feeds, by feed and by
// folder. Feeds and folders without any item are absent.
type ItemCounts struct {
	ByFeed   map[int64]ItemCount
	ByFolder map[int64]ItemCount
}

// CountItemsByFeedAndFolder computes the counters with two grouped queries.
func (q *queries) CountItemsByFeedAndFolder(ctx context.Context) (ItemCounts, error) {
	byFeed, err := q.groupedCounts(ctx,
		`SELECT feed_id, COUNT(*), SUM(is_read = 0) FROM items WHERE feed_id IS NOT NULL GROUP BY feed_id`)
	if err != nil {
		return ItemCounts{}, err
	}
	byFolder, err := q.groupedCounts(ctx,
		`SELECT ff.folder_id, COUNT(*), SUM(i.is_read = 0)
		 FROM feed_folders ff JOIN items i ON i.feed_id = ff.feed_id GROUP BY ff.folder_id`)
	if err != nil {
		return ItemCounts{}, err
	}
	return ItemCounts{ByFeed: byFeed, ByFolder: byFolder}, nil
}

func (q *queries) groupedCounts(ctx context.Context, query string) (map[int64]ItemCount, error) {
	rows, err := q.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("count items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]ItemCount{}
	for rows.Next() {
		var id int64
		var c ItemCount
		if err := rows.Scan(&id, &c.Total, &c.Unread); err != nil {
			return nil, fmt.Errorf("count items: %w", err)
		}
		out[id] = c
	}
	return out, rows.Err()
}

// SetReadForSelection marks every item of a selection read or unread, and
// returns the number of items whose state changed.
func (q *queries) SetReadForSelection(ctx context.Context, sel Selection, read bool) (int, error) {
	where, args := sel.where()
	res, err := q.db.ExecContext(ctx, "UPDATE items SET is_read = ? WHERE "+where+" AND is_read != ?",
		append([]any{read}, append(args, read)...)...)
	if err != nil {
		return 0, fmt.Errorf("mark items: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// starredCondition returns the SQL condition of the starred items of a
// selection. "All" includes the starred items of deleted feeds.
func starredCondition(sel Selection) (string, []any) {
	if sel.Kind == SelectAll {
		return "starred_at IS NOT NULL", nil
	}
	where, args := sel.where()
	return where + " AND starred_at IS NOT NULL", args
}

// CountStarred returns the number of starred items of a selection.
func (q *queries) CountStarred(ctx context.Context, sel Selection) (int, error) {
	where, args := starredCondition(sel)
	var total int
	if err := q.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM items WHERE "+where, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count starred items: %w", err)
	}
	return total, nil
}

// ListStarred returns a page of the starred items of a selection, most
// recently starred first.
func (q *queries) ListStarred(ctx context.Context, sel Selection, limit, offset int) ([]Item, error) {
	where, args := starredCondition(sel)
	return q.queryItems(ctx, where, "starred_at DESC, id DESC LIMIT ? OFFSET ?", append(args, limit, offset)...)
}
