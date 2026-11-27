// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// Session is a browser session, anonymous or authenticated.
type Session struct {
	ID            string
	CSRFToken     string
	Authenticated bool
	Persistent    bool
	CreatedAt     time.Time
	LastSeenAt    time.Time
}

// CreateSession stores a new session.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, csrf_token, authenticated, persistent, created_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.CSRFToken, sess.Authenticated, sess.Persistent,
		sess.CreatedAt.Unix(), sess.LastSeenAt.Unix())
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// Session returns the session with the given ID, or ErrNotFound.
func (s *Store) Session(ctx context.Context, id string) (Session, error) {
	var sess Session
	var created, seen int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, csrf_token, authenticated, persistent, created_at, last_seen_at
		 FROM sessions WHERE id = ?`, id).
		Scan(&sess.ID, &sess.CSRFToken, &sess.Authenticated, &sess.Persistent, &created, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("read session: %w", err)
	}
	sess.CreatedAt, sess.LastSeenAt = time.Unix(created, 0), time.Unix(seen, 0)
	return sess, nil
}

// TouchSession records activity on a session.
func (s *Store) TouchSession(ctx context.Context, id string, at time.Time) error {
	if _, err := s.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ? WHERE id = ?", at.Unix(), id); err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// DeleteSession removes a session. Deleting an unknown session is not an error.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// PurgeExpired deletes sessions idle for longer than their timeout, and the
// login-failure rows whose ban is over.
func (s *Store) PurgeExpired(ctx context.Context, now time.Time, idle, persistentIdle time.Duration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM sessions
		 WHERE (persistent = 0 AND last_seen_at < ?) OR (persistent = 1 AND last_seen_at < ?)`,
		now.Add(-idle).Unix(), now.Add(-persistentIdle).Unix()); err != nil {
		return fmt.Errorf("purge sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM login_failures WHERE banned_until IS NOT NULL AND banned_until <= ?",
		now.Unix()); err != nil {
		return fmt.Errorf("purge login failures: %w", err)
	}
	return tx.Commit()
}
