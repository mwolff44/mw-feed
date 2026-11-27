// SPDX-License-Identifier: GPL-3.0-or-later

package feed

import (
	"context"

	"github.com/mwolff44/mw-feed/internal/store"
)

// SetRead marks one item read or unread.
func (s *Service) SetRead(ctx context.Context, itemID int64, read bool) error {
	ok, err := s.store.SetItemRead(ctx, itemID, read)
	if err == nil && !ok {
		err = ErrNotFound
	}
	return err
}

// SetStarred stars or un-stars one item. Starring an already starred item keeps
// its first star date. Un-starring an item whose feed was deleted deletes it.
func (s *Service) SetStarred(ctx context.Context, itemID int64, starred bool) error {
	var ok bool
	var err error
	if starred {
		ok, err = s.store.StarItem(ctx, itemID, s.clock.Now())
	} else {
		ok, err = s.store.UnstarItem(ctx, itemID)
	}
	if err == nil && !ok {
		err = ErrNotFound
	}
	return err
}

// StarredItems returns every starred item, most recently starred first.
func (s *Service) StarredItems(ctx context.Context) ([]Item, error) {
	return s.store.StarredItems(ctx)
}

// DeleteFeed removes a feed and its items, except the starred ones, which stay
// available with the title of their former feed.
func (s *Service) DeleteFeed(ctx context.Context, id int64) error {
	return s.store.InTx(ctx, func(tx *store.Tx) error {
		ok, err := tx.DeleteFeed(ctx, id)
		if err == nil && !ok {
			err = ErrNotFound
		}
		return err
	})
}
