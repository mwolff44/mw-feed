// SPDX-License-Identifier: GPL-3.0-or-later

// Package store persists mw-feed data in SQLite. All SQL lives here.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure Go SQLite driver
)

// Store gives typed access to the SQLite database.
type Store struct {
	db *sql.DB
}

// Settings are the user-configurable options of the reader.
type Settings struct {
	Title        string
	Locale       string
	Redirector   string
	MaxItems     int
	MaxUpdate    int
	Order        string
	View         string
	Filter       string
	ListFeeds    bool
	ByPage       int
	AutoreadItem bool
	Autohide     bool
	Autofocus    bool
	AddFavicon   bool
	Preload      bool
	Blank        bool
	Swipe        bool
	Theme        string
}

// Open creates the data directory if needed and opens the database in it.
func Open(ctx context.Context, dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := url.URL{
		Scheme:   "file",
		Opaque:   url.PathEscape(filepath.Join(dataDir, "mw-feed.db")),
		RawQuery: "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)",
	}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// A single connection serialises all access: no SQLITE_BUSY, KISS.
	db.SetMaxOpenConns(1)
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Settings returns the current settings.
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var st Settings
	err := s.db.QueryRowContext(ctx, `SELECT title, locale, redirector, max_items, max_update,
		"order", view, filter, list_feeds, by_page, autoread_item, autohide, autofocus,
		add_favicon, preload, blank, swipe, theme FROM settings WHERE id = 1`).Scan(
		&st.Title, &st.Locale, &st.Redirector, &st.MaxItems, &st.MaxUpdate,
		&st.Order, &st.View, &st.Filter, &st.ListFeeds, &st.ByPage, &st.AutoreadItem,
		&st.Autohide, &st.Autofocus, &st.AddFavicon, &st.Preload, &st.Blank, &st.Swipe, &st.Theme)
	if err != nil {
		return Settings{}, fmt.Errorf("read settings: %w", err)
	}
	return st, nil
}

// SaveSettings replaces the current settings.
func (s *Store) SaveSettings(ctx context.Context, st Settings) error {
	if err := st.validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE settings SET title = ?, locale = ?, redirector = ?,
		max_items = ?, max_update = ?, "order" = ?, view = ?, filter = ?, list_feeds = ?,
		by_page = ?, autoread_item = ?, autohide = ?, autofocus = ?, add_favicon = ?,
		preload = ?, blank = ?, swipe = ?, theme = ? WHERE id = 1`,
		st.Title, st.Locale, st.Redirector, st.MaxItems, st.MaxUpdate, st.Order, st.View,
		st.Filter, st.ListFeeds, st.ByPage, st.AutoreadItem, st.Autohide, st.Autofocus,
		st.AddFavicon, st.Preload, st.Blank, st.Swipe, st.Theme)
	if err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}
