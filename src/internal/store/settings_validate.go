// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"slices"
)

// ErrInvalidSettings is returned when a setting has a value that is not allowed.
var ErrInvalidSettings = errors.New("invalid settings")

func (s Settings) validate() error {
	enums := []struct {
		field   string
		value   string
		allowed []string
	}{
		{"order", s.Order, []string{"newerFirst", "olderFirst"}},
		{"view", s.View, []string{"list", "expanded"}},
		{"filter", s.Filter, []string{"unread", "all"}},
		{"theme", s.Theme, []string{"auto", "light", "dark"}},
		{"locale", s.Locale, []string{"en", "fr", "de"}},
	}
	for _, e := range enums {
		if !slices.Contains(e.allowed, e.value) {
			return fmt.Errorf("%w: %s %q", ErrInvalidSettings, e.field, e.value)
		}
	}
	minimums := []struct {
		field string
		value int
	}{
		{"by_page", s.ByPage},
		{"max_items", s.MaxItems},
		{"max_update", s.MaxUpdate},
	}
	for _, m := range minimums {
		if m.value < 1 {
			return fmt.Errorf("%w: %s must be at least 1, got %d", ErrInvalidSettings, m.field, m.value)
		}
	}
	return nil
}
