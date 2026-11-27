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

// ItemsOfFeed returns the items of a feed, newest first.
func (q *queries) ItemsOfFeed(ctx context.Context, feedID int64) ([]Item, error) {
	return q.queryItems(ctx, "feed_id = ?", "published_at DESC, id DESC", feedID)
}

// StarredItems returns every starred item, most recently starred first.
func (q *queries) StarredItems(ctx context.Context) ([]Item, error) {
	return q.queryItems(ctx, "starred_at IS NOT NULL", "starred_at DESC, id DESC")
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
