// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Favicon is the cached icon of a site host. Data is empty when the last
// download failed: the failure is cached too, so that it is not retried at once.
type Favicon struct {
	Host      string
	Data      []byte
	Type      string
	FetchedAt time.Time
}

// FaviconByHost returns the cached icon of a host, or ErrNotFound.
func (s *Store) FaviconByHost(ctx context.Context, host string) (Favicon, error) {
	f := Favicon{Host: host}
	var fetched int64
	err := s.db.QueryRowContext(ctx,
		"SELECT data, content_type, fetched_at FROM favicons WHERE host = ?", host).Scan(&f.Data, &f.Type, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return Favicon{}, ErrNotFound
	}
	if err != nil {
		return Favicon{}, fmt.Errorf("read favicon: %w", err)
	}
	f.FetchedAt = time.Unix(fetched, 0)
	return f, nil
}

// SaveFavicon stores the icon of a host, replacing the previous one.
func (s *Store) SaveFavicon(ctx context.Context, f Favicon) error {
	if f.Data == nil {
		f.Data = []byte{} // a nil slice would be stored as NULL
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO favicons (host, data, content_type, fetched_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(host) DO UPDATE SET data = excluded.data, content_type = excluded.content_type,
		 fetched_at = excluded.fetched_at`,
		f.Host, f.Data, f.Type, f.FetchedAt.Unix())
	if err != nil {
		return fmt.Errorf("save favicon: %w", err)
	}
	return nil
}
