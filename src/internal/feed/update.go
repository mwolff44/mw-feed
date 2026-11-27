// SPDX-License-Identifier: GPL-3.0-or-later

package feed

import (
	"context"
	"fmt"
	"slices"
	"sync"
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
	// OnlyIfDue makes the update a scheduled one: once the feed is locked, it is
	// skipped if another update has made it not due any more.
	OnlyIfDue bool
}

// UpdateResult tells what an update did.
type UpdateResult struct {
	// NewItems is the number of items imported.
	NewItems int
	// NotModified is true when the server answered 304: there was nothing to read.
	NotModified bool
	// Skipped is true when OnlyIfDue found the feed not due any more.
	Skipped bool
}

// UpdateFeed downloads a feed again and imports its new items.
func (s *Service) UpdateFeed(ctx context.Context, id int64, opts UpdateOptions) (UpdateResult, error) {
	// One update per feed at a time; the others wait for it.
	unlock, err := s.locks.lock(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	defer unlock()
	select { // at most maxConcurrentUpdates updates at the same time
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return UpdateResult{}, ctx.Err()
	}
	current, err := s.store.FeedByID(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return UpdateResult{}, err
	}
	now := s.clock.Now()
	if opts.OnlyIfDue && !isDue(current, now, settings.MaxUpdate) {
		return UpdateResult{Skipped: true}, nil
	}
	// A failure is recorded on the feed and returned; the stored items are kept.
	fail := func(err error) (UpdateResult, error) {
		if ctx.Err() != nil {
			return UpdateResult{}, ctx.Err() // canceled: not a failure of the feed, record nothing
		}
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

// DueFeeds returns the feeds whose update is due: never updated, or last
// updated more than their delay ago.
func (s *Service) DueFeeds(ctx context.Context) ([]Feed, error) {
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	feeds, err := s.store.Feeds(ctx)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	var due []Feed
	for _, f := range feeds {
		if isDue(f, now, settings.MaxUpdate) {
			due = append(due, f)
		}
	}
	return due, nil
}

// effectiveDelay is the time between two updates of a feed: its own delay if it
// has one within [minUpdateInterval, maxUpdate] minutes, else maxUpdate. It is
// evaluated each time, so lowering maxUpdate also lowers longer custom delays.
func effectiveDelay(f Feed, maxUpdate int) time.Duration {
	minutes := maxUpdate
	if f.UpdateInterval != nil && *f.UpdateInterval >= minUpdateInterval && *f.UpdateInterval <= maxUpdate {
		minutes = *f.UpdateInterval
	}
	return time.Duration(minutes) * time.Minute
}

// isDue reports whether a feed must be updated: never updated, or updated more
// than its effective delay ago.
func isDue(f Feed, now time.Time, maxUpdate int) bool {
	return f.LastUpdateAt.IsZero() || now.Sub(f.LastUpdateAt) > effectiveDelay(f, maxUpdate)
}

// FeedUpdate is the outcome of the manual update of one feed.
type FeedUpdate struct {
	FeedID int64
	Title  string
	Result UpdateResult
	Err    error
}

// UpdateSelection updates by hand every feed of a selection, once each, in the
// order of the feed list. It downloads each feed in full, whatever its delay,
// and waits for all of them. A feed that fails does not stop the others: its
// error is in its FeedUpdate. If the context is canceled, feeds not finished
// carry the context error, nothing more is started, and the error is returned too.
func (s *Service) UpdateSelection(ctx context.Context, sel Selection) ([]FeedUpdate, error) {
	if err := s.checkSelection(ctx, sel); err != nil {
		return nil, err
	}
	all, err := s.Feeds(ctx)
	if err != nil {
		return nil, err
	}
	var feeds []Feed
	for _, f := range all {
		switch {
		case sel.Kind == store.SelectFeed && f.ID != sel.ID:
		case sel.Kind == store.SelectFolder && !slices.Contains(f.FolderIDs, sel.ID):
		default:
			feeds = append(feeds, f)
		}
	}
	updates := make([]FeedUpdate, len(feeds))
	var wg sync.WaitGroup
	for i, f := range feeds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.UpdateFeed(ctx, f.ID, UpdateOptions{Force: true})
			updates[i] = FeedUpdate{FeedID: f.ID, Title: f.Title, Result: res, Err: err}
		}()
	}
	wg.Wait()
	return updates, ctx.Err()
}
