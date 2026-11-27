// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mwolff44/mw-feed/internal/store"
)

// Session is what callers know about a browser session.
type Session struct {
	ID            string
	CSRFToken     string
	Authenticated bool
	Persistent    bool
}

// LoginRequest carries a login attempt.
type LoginRequest struct {
	IP       string
	Login    string
	Password string
	Remember bool

	// AnonymousSessionID is the pre-login session, which login replaces.
	AnonymousSessionID string
}

// ErrInvalidCredentials is returned for an unknown login or a wrong password,
// without telling which one.
var ErrInvalidCredentials = errors.New("invalid credentials")

// dummyHash is verified when the login is unknown, so that the response time
// does not reveal whether the login exists.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashPassword("dummy password used for timing equalisation")
	if err != nil {
		panic(err)
	}
	return h
})

// randomToken returns 256 bits of randomness, base64url encoded.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// newSession creates and stores a session.
func (s *Service) newSession(ctx context.Context, authenticated, persistent bool) (Session, error) {
	// Opportunistic housekeeping: no background task needed.
	if err := s.store.PurgeExpired(ctx, s.clock.Now(), inactivityTimeout, persistentInactivityTimeout); err != nil {
		return Session{}, err
	}
	id, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	now := s.clock.Now()
	rec := store.Session{ID: id, CSRFToken: csrf, Authenticated: authenticated, Persistent: persistent, CreatedAt: now, LastSeenAt: now}
	if err := s.store.CreateSession(ctx, rec); err != nil {
		return Session{}, err
	}
	return toSession(rec), nil
}

// NewAnonymousSession creates an unauthenticated session, used to carry the
// CSRF token of the install and login forms.
func (s *Service) NewAnonymousSession(ctx context.Context) (Session, error) {
	return s.newSession(ctx, false, false)
}

// Login creates an authenticated session.
func (s *Service) Login(ctx context.Context, req LoginRequest) (Session, error) {
	if err := s.checkBan(ctx, req.IP); err != nil {
		return Session{}, err
	}
	if err := s.checkCredentials(ctx, req.Login, req.Password); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			if ferr := s.store.RecordLoginFailure(ctx, req.IP, s.clock.Now(), maxLoginFailures, banDuration); ferr != nil {
				return Session{}, ferr
			}
		}
		return Session{}, err
	}
	if err := s.store.ClearLoginFailures(ctx, req.IP); err != nil {
		return Session{}, err
	}
	if req.AnonymousSessionID != "" {
		if err := s.store.DeleteSession(ctx, req.AnonymousSessionID); err != nil {
			return Session{}, err
		}
	}
	return s.newSession(ctx, true, req.Remember)
}

// Inactivity timeouts: a session survives that long without activity.
const (
	inactivityTimeout           = 3600 * time.Second
	persistentInactivityTimeout = 30 * 24 * time.Hour
)

// timeout returns how long rec may stay idle before expiring.
func timeout(rec store.Session) time.Duration {
	if rec.Persistent {
		return persistentInactivityTimeout
	}
	return inactivityTimeout
}

// ErrNoSession means the session is unknown or has expired.
var ErrNoSession = errors.New("no valid session")

// Authenticate returns the session with the given ID and renews its activity.
func (s *Service) Authenticate(ctx context.Context, id string) (Session, error) {
	rec, err := s.store.Session(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	now := s.clock.Now()
	if now.Sub(rec.LastSeenAt) > timeout(rec) {
		if err := s.store.DeleteSession(ctx, id); err != nil {
			return Session{}, err
		}
		return Session{}, ErrNoSession
	}
	if err := s.store.TouchSession(ctx, id, now); err != nil {
		return Session{}, err
	}
	return toSession(rec), nil
}

func toSession(r store.Session) Session {
	return Session{ID: r.ID, CSRFToken: r.CSRFToken, Authenticated: r.Authenticated, Persistent: r.Persistent}
}

// checkCredentials verifies the login and password in a way that takes the
// same time whether or not the login exists.
func (s *Service) checkCredentials(ctx context.Context, login, password string) error {
	user, err := s.store.User(ctx)
	switch {
	case errors.Is(err, store.ErrNotFound):
		user = store.User{PasswordHash: dummyHash()}
	case err != nil:
		return err
	}
	passwordOK, err := VerifyPassword(user.PasswordHash, password)
	if err != nil {
		return err
	}
	loginOK := subtle.ConstantTimeCompare([]byte(user.Login), []byte(strings.TrimSpace(login))) == 1
	if !passwordOK || !loginOK || user.Login == "" {
		return ErrInvalidCredentials
	}
	return nil
}

// Logout ends a session. Ending an unknown session is not an error.
func (s *Service) Logout(ctx context.Context, id string) error {
	return s.store.DeleteSession(ctx, id)
}

// VerifyCSRF reports whether token is the CSRF token of sess, in constant time.
func VerifyCSRF(sess Session, token string) bool {
	if token == "" || sess.CSRFToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(sess.CSRFToken), []byte(token)) == 1
}
