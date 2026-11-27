// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrUserExists is returned when the single user has already been created.
var ErrUserExists = errors.New("user already exists")

// HasUser reports whether the single user has been created.
func (s *Store) HasUser(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user").Scan(&n); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return n > 0, nil
}

// CreateUser creates the single user. It fails with ErrUserExists if there is one.
func (s *Store) CreateUser(ctx context.Context, login, passwordHash string) error {
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO user (id, login, password_hash) VALUES (1, ?, ?) ON CONFLICT DO NOTHING",
		login, passwordHash)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("create user: %w", err)
	} else if n == 0 {
		return ErrUserExists
	}
	return nil
}

// User is the single account.
type User struct {
	Login        string
	PasswordHash string
}

// User returns the single user, or ErrNotFound if not installed yet.
func (s *Store) User(ctx context.Context) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, "SELECT login, password_hash FROM user WHERE id = 1").
		Scan(&u.Login, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	return u, nil
}

// SetPasswordAndEndSessions replaces the password hash and deletes every
// session, in one transaction.
func (s *Store) SetPasswordAndEndSessions(ctx context.Context, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "UPDATE user SET password_hash = ? WHERE id = 1", passwordHash); err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return fmt.Errorf("change password: end sessions: %w", err)
	}
	return tx.Commit()
}
