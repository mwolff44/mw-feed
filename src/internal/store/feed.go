// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Feed is a subscribed feed.
type Feed struct {
	ID          int64
	XMLURL      string
	SiteURL     string
	Title       string
	Description string
	// UpdateInterval is the minimum delay in minutes between updates; nil means automatic.
	UpdateInterval *int
	LastUpdateAt   time.Time // zero if never updated
	LastError      string
	LastWarning    string // translation key
	ETag           string
	LastModified   string
	LastItemTime   time.Time // date of the most recent item ever imported; zero if none
	FolderIDs      []int64   // sorted
}

// NewFeed holds the fields of a feed to create.
type NewFeed struct {
	XMLURL       string
	SiteURL      string
	Title        string
	Description  string
	ETag         string
	LastModified string
	LastUpdateAt time.Time
	LastItemTime time.Time
}

const feedColumns = `id, xml_url, html_url, title, description, update_interval, last_update_at,
	last_error, last_warning, http_etag, http_last_modified, last_item_time`

func scanFeed(row interface{ Scan(...any) error }) (Feed, error) {
	var f Feed
	var interval, updated, lastItem sql.NullInt64
	err := row.Scan(&f.ID, &f.XMLURL, &f.SiteURL, &f.Title, &f.Description, &interval, &updated,
		&f.LastError, &f.LastWarning, &f.ETag, &f.LastModified, &lastItem)
	if err != nil {
		return Feed{}, err
	}
	if interval.Valid {
		v := int(interval.Int64)
		f.UpdateInterval = &v
	}
	f.LastUpdateAt, f.LastItemTime = timeFromNull(updated), timeFromNull(lastItem)
	return f, nil
}

// InsertFeed creates a feed and returns its ID.
func (q *queries) InsertFeed(ctx context.Context, f NewFeed) (int64, error) {
	res, err := q.db.ExecContext(ctx,
		`INSERT INTO feeds (xml_url, html_url, title, description, http_etag, http_last_modified,
			last_update_at, last_item_time) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		f.XMLURL, f.SiteURL, f.Title, f.Description, f.ETag, f.LastModified,
		unixOrNull(f.LastUpdateAt), unixOrNull(f.LastItemTime))
	if err != nil {
		return 0, fmt.Errorf("insert feed: %w", err)
	}
	return res.LastInsertId()
}

// FeedByID returns a feed, or ErrNotFound.
func (q *queries) FeedByID(ctx context.Context, id int64) (Feed, error) {
	f, err := scanFeed(q.db.QueryRowContext(ctx, "SELECT "+feedColumns+" FROM feeds WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Feed{}, ErrNotFound
	}
	if err != nil {
		return Feed{}, fmt.Errorf("read feed: %w", err)
	}
	folders, err := q.feedFolders(ctx)
	if err != nil {
		return Feed{}, err
	}
	f.FolderIDs = folders[f.ID]
	return f, nil
}

// FeedByXMLURL returns the feed with that XML URL, or ErrNotFound.
func (q *queries) FeedByXMLURL(ctx context.Context, xmlURL string) (Feed, error) {
	f, err := scanFeed(q.db.QueryRowContext(ctx, "SELECT "+feedColumns+" FROM feeds WHERE xml_url = ?", xmlURL))
	if errors.Is(err, sql.ErrNoRows) {
		return Feed{}, ErrNotFound
	}
	if err != nil {
		return Feed{}, fmt.Errorf("read feed: %w", err)
	}
	folders, err := q.feedFolders(ctx)
	if err != nil {
		return Feed{}, err
	}
	f.FolderIDs = folders[f.ID]
	return f, nil
}

// Feeds returns all feeds, by ID, with their folders.
func (q *queries) Feeds(ctx context.Context) ([]Feed, error) {
	rows, err := q.db.QueryContext(ctx, "SELECT "+feedColumns+" FROM feeds ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list feeds: %w", err)
	}
	var out []Feed
	for rows.Next() {
		f, err := scanFeed(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("list feeds: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	folders, err := q.feedFolders(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].FolderIDs = folders[out[i].ID]
	}
	return out, nil
}

// feedFolders returns the folder IDs of every feed, sorted.
func (q *queries) feedFolders(ctx context.Context) (map[int64][]int64, error) {
	rows, err := q.db.QueryContext(ctx, "SELECT feed_id, folder_id FROM feed_folders ORDER BY folder_id")
	if err != nil {
		return nil, fmt.Errorf("list feed folders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64][]int64{}
	for rows.Next() {
		var feedID, folderID int64
		if err := rows.Scan(&feedID, &folderID); err != nil {
			return nil, fmt.Errorf("list feed folders: %w", err)
		}
		out[feedID] = append(out[feedID], folderID)
	}
	return out, rows.Err()
}

// SetFeedFolders replaces the folders of a feed.
func (q *queries) SetFeedFolders(ctx context.Context, feedID int64, folderIDs []int64) error {
	if _, err := q.db.ExecContext(ctx, "DELETE FROM feed_folders WHERE feed_id = ?", feedID); err != nil {
		return fmt.Errorf("set feed folders: %w", err)
	}
	for _, id := range folderIDs {
		if _, err := q.db.ExecContext(ctx, "INSERT OR IGNORE INTO feed_folders (feed_id, folder_id) VALUES (?, ?)", feedID, id); err != nil {
			return fmt.Errorf("set feed folders: %w", err)
		}
	}
	return nil
}

// FeedEdit holds the user-editable fields of a feed.
type FeedEdit struct {
	Title          string
	SiteURL        string
	Description    string
	XMLURL         string
	UpdateInterval *int // nil = automatic
	// ClearValidators forgets the HTTP validators (the URL changed).
	ClearValidators bool
}

// UpdateFeedFields saves the user-editable fields of a feed and, when the
// title changes, renames the feed on all its items.
func (q *queries) UpdateFeedFields(ctx context.Context, id int64, e FeedEdit) error {
	var interval any
	if e.UpdateInterval != nil {
		interval = *e.UpdateInterval
	}
	query := `UPDATE feeds SET title = ?, html_url = ?, description = ?, xml_url = ?, update_interval = ?`
	if e.ClearValidators {
		query += `, http_etag = '', http_last_modified = ''`
	}
	if _, err := q.db.ExecContext(ctx, query+` WHERE id = ?`, e.Title, e.SiteURL, e.Description, e.XMLURL, interval, id); err != nil {
		return fmt.Errorf("update feed: %w", err)
	}
	if _, err := q.db.ExecContext(ctx, `UPDATE items SET feed_title = ? WHERE feed_id = ?`, e.Title, id); err != nil {
		return fmt.Errorf("rename feed on items: %w", err)
	}
	return nil
}

// DeleteFeed removes a feed with its folder associations and its items, except
// the starred ones, which are kept without a feed (they keep their feed title).
// It reports whether the feed existed.
func (q *queries) DeleteFeed(ctx context.Context, id int64) (bool, error) {
	if _, err := q.db.ExecContext(ctx, "DELETE FROM items WHERE feed_id = ? AND starred_at IS NULL", id); err != nil {
		return false, fmt.Errorf("delete items: %w", err)
	}
	res, err := q.db.ExecContext(ctx, "DELETE FROM feeds WHERE id = ?", id)
	if err != nil {
		return false, fmt.Errorf("delete feed: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// FeedUpdate records the outcome of an update attempt.
type FeedUpdate struct {
	At    time.Time
	Error string // empty after a success
	// LastItemTime moves the "newest known item" date forward; zero leaves it unchanged.
	LastItemTime time.Time
	// Warning is the translation key of the new warning ("" clears it); nil leaves it unchanged.
	Warning *string
	// SiteURL and Description fill the stored values only if those are empty.
	SiteURL     string
	Description string
	// Validators replaces the stored HTTP validators when not nil.
	Validators *Validators
}

// Validators are the HTTP cache validators of the last successful download.
type Validators struct {
	ETag         string
	LastModified string
}

// RecordFeedUpdate saves the outcome of an update attempt.
func (q *queries) RecordFeedUpdate(ctx context.Context, id int64, u FeedUpdate) error {
	var etag, lastModified any
	if u.Validators != nil {
		etag, lastModified = u.Validators.ETag, u.Validators.LastModified
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE feeds SET last_update_at = ?1, last_error = ?2,
			last_item_time = CASE WHEN ?3 IS NOT NULL AND (last_item_time IS NULL OR ?3 > last_item_time) THEN ?3 ELSE last_item_time END,
			last_warning = COALESCE(?4, last_warning),
			html_url = CASE WHEN html_url = '' THEN ?5 ELSE html_url END,
			description = CASE WHEN description = '' THEN ?6 ELSE description END,
			http_etag = COALESCE(?7, http_etag),
			http_last_modified = COALESCE(?8, http_last_modified)
		 WHERE id = ?9`,
		u.At.Unix(), u.Error, unixOrNull(u.LastItemTime), u.Warning, u.SiteURL, u.Description,
		etag, lastModified, id)
	if err != nil {
		return fmt.Errorf("record feed update: %w", err)
	}
	return nil
}
