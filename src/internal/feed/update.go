// SPDX-License-Identifier: GPL-3.0-or-later

package feed

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/mwolff44/mw-feed/internal/fetch"
	"github.com/mwolff44/mw-feed/internal/store"
)

// WarningItemsMissed is the translation key of the warning shown when every
// item of a fetched feed is new although the feed already had items.
const WarningItemsMissed = "items_missed"

// UpdateOptions tune UpdateFeed.
type UpdateOptions struct {
	// Force is a manual update: the whole feed is downloaded again, without
	// conditional request headers.
	Force bool
}

// UpdateResult tells what an update did.
type UpdateResult struct {
	// NewItems is the number of items imported.
	NewItems int
	// NotModified is true when the server answered 304: there was nothing to read.
	NotModified bool
}

// UpdateFeed downloads a feed again and imports its new items.
func (s *Service) UpdateFeed(ctx context.Context, id int64, opts UpdateOptions) (UpdateResult, error) {
	current, err := s.store.FeedByID(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return UpdateResult{}, err
	}
	now := s.clock.Now()
	// A failure is recorded on the feed and returned; the stored items are kept.
	fail := func(err error) (UpdateResult, error) {
		if rerr := s.store.RecordFeedUpdate(ctx, id, store.FeedUpdate{At: now, Error: err.Error()}); rerr != nil {
			return UpdateResult{}, fmt.Errorf("%w (and recording it failed: %v)", err, rerr)
		}
		return UpdateResult{}, err
	}
	req := fetch.Request{URL: current.XMLURL}
	if !opts.Force { // scheduled updates are conditional; manual ones download everything
		req.ETag, req.LastModified = current.ETag, current.LastModified
	}
	resp, err := s.fetcher.Fetch(ctx, req)
	if err != nil {
		return fail(fmt.Errorf("download feed: %w", err))
	}
	if resp.NotModified {
		// Nothing to read: only note the attempt and clear any previous error.
		if err := s.store.RecordFeedUpdate(ctx, id, store.FeedUpdate{At: now}); err != nil {
			return UpdateResult{}, err
		}
		return UpdateResult{NotModified: true}, nil
	}
	parsed, err := Parse(resp.Body, now)
	if err != nil {
		return fail(err)
	}
	entries := newest(parsed.Items, settings.MaxItems)

	var result UpdateResult
	err = s.store.InTx(ctx, func(tx *store.Tx) error {
		known, err := tx.ItemLinks(ctx, id)
		if err != nil {
			return err
		}
		var newestNew time.Time
		for _, e := range entries {
			if known[e.Link] {
				continue
			}
			// A new item must also be more recent than the newest item ever known.
			if !current.LastItemTime.IsZero() && !e.PublishedAt.After(current.LastItemTime) {
				continue
			}
			added, err := tx.InsertItem(ctx, newItem(id, current.Title, current.SiteURL, e))
			if err != nil {
				return err
			}
			if added {
				result.NewItems++
				if e.PublishedAt.After(newestNew) {
					newestNew = e.PublishedAt
				}
			}
		}
		// A feed that shows nothing may be a transient glitch: keep everything.
		if len(entries) > 0 {
			if err := applyRetention(ctx, tx, id, entries, settings.MaxItems); err != nil {
				return err
			}
		}
		// Items may have been missed if the feed had items and all the fetched ones are new.
		warning := ""
		if len(known) > 0 && len(entries) > 0 && result.NewItems == len(entries) {
			warning = WarningItemsMissed
		}
		return tx.RecordFeedUpdate(ctx, id, store.FeedUpdate{
			At: now, LastItemTime: newestNew, Warning: &warning,
			SiteURL: parsed.SiteURL, Description: parsed.Description,
			Validators: &store.Validators{ETag: resp.ETag, LastModified: resp.LastModified},
		})
	})
	return result, err
}

// applyRetention deletes, among the items of a feed, those that are read, no
// longer in the feed and not starred; then, if there are still more than
// maxItems, the oldest ones that are not starred. Starred items are never deleted.
func applyRetention(ctx context.Context, tx *store.Tx, feedID int64, entries []Entry, maxItems int) error {
	inFeed := make(map[string]bool, len(entries))
	for _, e := range entries {
		inFeed[e.Link] = true
	}
	states, err := tx.ItemStates(ctx, feedID)
	if err != nil {
		return err
	}
	var doomed []int64
	var kept []store.ItemState
	for _, st := range states {
		if st.Read && !st.Starred && !inFeed[st.Link] {
			doomed = append(doomed, st.ID)
		} else {
			kept = append(kept, st)
		}
	}
	if excess := len(kept) - maxItems; excess > 0 {
		var candidates []store.ItemState
		for _, st := range kept {
			if !st.Starred {
				candidates = append(candidates, st)
			}
		}
		slices.SortFunc(candidates, func(a, b store.ItemState) int {
			if c := a.PublishedAt.Compare(b.PublishedAt); c != 0 {
				return c
			}
			return int(a.ID - b.ID)
		})
		for i := 0; i < excess && i < len(candidates); i++ {
			doomed = append(doomed, candidates[i].ID)
		}
	}
	return tx.DeleteItems(ctx, doomed)
}
