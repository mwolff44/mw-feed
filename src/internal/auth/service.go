// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mwolff44/mw-feed/internal/store"
)

// ErrAlreadyInstalled is returned when installing an already installed instance.
var ErrAlreadyInstalled = errors.New("already installed")

// Clock provides the current time; tests inject a fake one.
type Clock interface{ Now() time.Time }

// Service holds the authentication rules (install, login, sessions, bans).
type Service struct {
	store *store.Store
	clock Clock
}

// NewService returns a Service backed by st.
func NewService(st *store.Store, clock Clock) *Service {
	return &Service{store: st, clock: clock}
}

// Installed reports whether the single user exists.
func (s *Service) Installed(ctx context.Context) (bool, error) {
	return s.store.HasUser(ctx)
}

// Install creates the single user. It can only succeed once.
func (s *Service) Install(ctx context.Context, login, password string) error {
	login, err := normalizeLogin(login)
	if err != nil {
		return err
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := s.store.CreateUser(ctx, login, hash); err != nil {
		if errors.Is(err, store.ErrUserExists) {
			return ErrAlreadyInstalled
		}
		return err
	}
	return nil
}

// ChangePassword sets a new password, ends every session and returns a fresh
// session replacing the caller's one.
func (s *Service) ChangePassword(ctx context.Context, sessionID, current, next string) (Session, error) {
	old, err := s.Authenticate(ctx, sessionID)
	if err != nil {
		return Session{}, err
	}
	if !old.Authenticated {
		return Session{}, ErrNoSession
	}
	user, err := s.store.User(ctx)
	if err != nil {
		return Session{}, err
	}
	// A wrong current password is not counted towards the login ban: the
	// caller is already authenticated.
	if err := s.checkCredentials(ctx, user.Login, current); err != nil {
		return Session{}, err
	}
	if err := checkPassword(next); err != nil {
		return Session{}, err
	}
	hash, err := HashPassword(next)
	if err != nil {
		return Session{}, fmt.Errorf("hash password: %w", err)
	}
	if err := s.store.SetPasswordAndEndSessions(ctx, hash); err != nil {
		return Session{}, err
	}
	return s.newSession(ctx, true, old.Persistent)
}
