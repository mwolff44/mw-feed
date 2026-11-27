// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Flash is a one-shot message shown to a session on its next page: a message
// key and its arguments (never text taken from the URL).
//
// Either Key and Args give one text, or Parts give counted texts that are
// translated with their plural form and joined into one sentence.
type Flash struct {
	Error bool      `json:"error,omitempty"`
	Key   string    `json:"key,omitempty"`
	Args  []string  `json:"args,omitempty"`
	Parts []Counted `json:"parts,omitempty"`
}

// Counted is a text with a number, whose key has "_one" and "_other" forms.
type Counted struct {
	Key string `json:"key"`
	N   int    `json:"n"`
}

// SetFlash stores the message of a session, replacing any pending one.
// An unknown session is not an error: there is nobody to show it to.
func (s *Store) SetFlash(ctx context.Context, sessionID string, f Flash) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode flash: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE sessions SET flash = ? WHERE id = ?", string(raw), sessionID); err != nil {
		return fmt.Errorf("set flash: %w", err)
	}
	return nil
}

// TakeFlash returns the pending message of a session and clears it. The bool
// is false when there is none.
func (s *Store) TakeFlash(ctx context.Context, sessionID string) (Flash, bool, error) {
	var f Flash
	found := false
	err := s.InTx(ctx, func(tx *Tx) error {
		var raw string
		err := tx.db.QueryRowContext(ctx, "SELECT flash FROM sessions WHERE id = ?", sessionID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && raw == "") {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.db.ExecContext(ctx, "UPDATE sessions SET flash = '' WHERE id = ?", sessionID); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			return nil // unreadable message: dropped
		}
		found = true
		return nil
	})
	if err != nil {
		return Flash{}, false, fmt.Errorf("take flash: %w", err)
	}
	return f, found, nil
}
