// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Ban policy: fixed by design.
const (
	maxLoginFailures = 4
	banDuration      = 1800 * time.Second
)

// ErrBanned is matched (errors.Is) by every *BannedError.
var ErrBanned = errors.New("too many failed logins")

// BannedError tells until when an IP is banned.
type BannedError struct {
	Until     time.Time
	Remaining time.Duration
}

func (e *BannedError) Error() string {
	return fmt.Sprintf("%v: banned until %s", ErrBanned, e.Until.Format(time.RFC3339))
}

// Is lets errors.Is(err, ErrBanned) match.
func (e *BannedError) Is(target error) bool { return target == ErrBanned }

// checkBan returns a *BannedError if ip is currently banned.
func (s *Service) checkBan(ctx context.Context, ip string) error {
	f, err := s.store.LoginFailures(ctx, ip)
	if err != nil {
		return err
	}
	if f.BannedUntil.IsZero() {
		return nil
	}
	if now := s.clock.Now(); now.Before(f.BannedUntil) {
		return &BannedError{Until: f.BannedUntil, Remaining: f.BannedUntil.Sub(now)}
	}
	// The ban is over: the counter restarts at 0.
	return s.store.ClearLoginFailures(ctx, ip)
}
