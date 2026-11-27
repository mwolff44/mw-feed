// SPDX-License-Identifier: GPL-3.0-or-later

package feed

import "context"

// Observation helpers for the tests: they list everything without pagination.
// They are not part of the service.

const everything = 1 << 30

// Items returns all the items of a feed, newest first, read or not.
func (s *Service) Items(ctx context.Context, feedID int64) ([]Item, error) {
	page, err := s.ListItems(ctx, ItemQuery{Selection: InFeed(feedID), Filter: FilterAll, Order: OrderNewerFirst, PerPage: everything})
	return page.Items, err
}

// StarredItems returns every starred item, orphaned ones included, most
// recently starred first.
func (s *Service) StarredItems(ctx context.Context) ([]Item, error) {
	page, err := s.ListStarred(ctx, StarredQuery{PerPage: everything})
	return page.Items, err
}
