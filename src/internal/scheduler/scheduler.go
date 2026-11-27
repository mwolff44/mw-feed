// SPDX-License-Identifier: GPL-3.0-or-later

// Package scheduler keeps feeds up to date in the background.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/mwolff44/mw-feed/internal/feed"
)

// defaultInterval is the time between two passes (plan 4.2). It is not
// configurable by the user; tests shorten it with WithInterval.
const defaultInterval = time.Minute

// FeedUpdater is what the scheduler needs from the feed service.
type FeedUpdater interface {
	DueFeeds(ctx context.Context) ([]feed.Feed, error)
	UpdateFeed(ctx context.Context, id int64, opts feed.UpdateOptions) (feed.UpdateResult, error)
}

// Scheduler runs passes over the feeds that are due.
type Scheduler struct {
	updater  FeedUpdater
	interval time.Duration
	log      *slog.Logger
}

// Option tunes a Scheduler.
type Option func(*Scheduler)

// WithInterval sets the time between two passes.
func WithInterval(d time.Duration) Option { return func(s *Scheduler) { s.interval = d } }

// WithLogger sets the logger; the default is slog's default logger.
func WithLogger(l *slog.Logger) Option { return func(s *Scheduler) { s.log = l } }

// New returns a Scheduler.
func New(u FeedUpdater, opts ...Option) *Scheduler {
	s := &Scheduler{updater: u, interval: defaultInterval, log: slog.Default()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// TickResult summarises one pass.
type TickResult struct {
	Due     int // feeds that were due
	Updated int // updates that succeeded
	Failed  int // updates that failed
	// Canceled counts the updates interrupted, or never started, because the
	// context was canceled: they are not failures of the feeds.
	Canceled int
	// Skipped counts the feeds that were due when the pass began but had been
	// refreshed by another update (a manual one, say) before their turn.
	Skipped int
}

// Tick updates the feeds that are due and waits for all the updates.
func (s *Scheduler) Tick(ctx context.Context) (TickResult, error) {
	due, err := s.updater.DueFeeds(ctx)
	if err != nil {
		return TickResult{}, err
	}
	res := TickResult{Due: len(due)}
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	// One goroutine per due feed: the service itself bounds how many run at once.
	for _, f := range due {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.updater.UpdateFeed(ctx, f.ID, feed.UpdateOptions{OnlyIfDue: true})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && r.Skipped:
				res.Skipped++
			case err == nil:
				res.Updated++
			case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
				res.Canceled++
			default:
				res.Failed++
				s.log.Warn("feed update failed", "feed", f.ID, "error", err)
			}
		}()
	}
	wg.Wait()
	return res, nil
}

// Run makes a pass at once, then one every interval, until ctx is canceled. A
// pass that lasts longer than the interval delays the next one: passes never
// overlap. Run returns after the updates it started have returned.
func (s *Scheduler) Run(ctx context.Context) {
	s.pass(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pass(ctx)
		}
	}
}

// pass runs one Tick and logs its outcome.
func (s *Scheduler) pass(ctx context.Context) {
	res, err := s.Tick(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("scheduler pass failed", "error", err)
		}
		return
	}
	if res.Due > 0 {
		s.log.Debug("scheduler pass done", "due", res.Due, "updated", res.Updated,
			"failed", res.Failed, "canceled", res.Canceled, "skipped", res.Skipped)
	}
}
