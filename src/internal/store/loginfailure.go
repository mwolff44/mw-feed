// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LoginFailure is the failed-login state of one IP address.
type LoginFailure struct {
	Count       int
	BannedUntil time.Time // zero if the IP is not banned
}

// LoginFailures returns the state of ip; the zero value if it has no failures.
func (s *Store) LoginFailures(ctx context.Context, ip string) (LoginFailure, error) {
	var f LoginFailure
	var banned sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT count, banned_until FROM login_failures WHERE ip = ?", ip).
		Scan(&f.Count, &banned)
	if errors.Is(err, sql.ErrNoRows) {
		return LoginFailure{}, nil
	}
	if err != nil {
		return LoginFailure{}, fmt.Errorf("read login failures: %w", err)
	}
	if banned.Valid {
		f.BannedUntil = time.Unix(banned.Int64, 0)
	}
	return f, nil
}

// RecordLoginFailure counts one more failure for ip, atomically. When the count
// reaches maxFailures, ip is banned until at+banFor.
func (s *Store) RecordLoginFailure(ctx context.Context, ip string, at time.Time, maxFailures int, banFor time.Duration) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO login_failures (ip, count, last_failure_at, banned_until)
		VALUES (?1, 1, ?2, CASE WHEN 1 >= ?3 THEN ?4 END)
		ON CONFLICT(ip) DO UPDATE SET
			count = count + 1,
			last_failure_at = ?2,
			banned_until = CASE WHEN count + 1 >= ?3 THEN ?4 END`,
		ip, at.Unix(), maxFailures, at.Add(banFor).Unix())
	if err != nil {
		return fmt.Errorf("record login failure: %w", err)
	}
	return nil
}

// ClearLoginFailures forgets all failures of ip.
func (s *Store) ClearLoginFailures(ctx context.Context, ip string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM login_failures WHERE ip = ?", ip); err != nil {
		return fmt.Errorf("clear login failures: %w", err)
	}
	return nil
}
