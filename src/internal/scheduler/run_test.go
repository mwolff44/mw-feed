// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/scheduler"
)

// fakeUpdater is a FeedUpdater whose behaviour the tests script.
type fakeUpdater struct {
	mu         sync.Mutex
	passes     int
	dueErr     error
	due        []feed.Feed
	updateFn   func(ctx context.Context, id int64) error
	inUpdate   atomic.Int32
	overlapped atomic.Bool
	returned   atomic.Int32 // updates that have returned
}

func (f *fakeUpdater) DueFeeds(ctx context.Context) ([]feed.Feed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.passes++
	if f.inUpdate.Load() > 0 {
		f.overlapped.Store(true) // a pass began while the previous one was still updating
	}
	return f.due, f.dueErr
}

func (f *fakeUpdater) UpdateFeed(ctx context.Context, id int64, _ feed.UpdateOptions) (feed.UpdateResult, error) {
	f.inUpdate.Add(1)
	defer func() { f.inUpdate.Add(-1); f.returned.Add(1) }()
	if f.updateFn != nil {
		return feed.UpdateResult{}, f.updateFn(ctx, id)
	}
	return feed.UpdateResult{}, nil
}

func (f *fakeUpdater) passCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.passes
}

func oneDueFeed() []feed.Feed { return []feed.Feed{{ID: 1, Title: "one"}} }

// startRun runs the scheduler and returns a function that cancels it and waits for Run to return.
func startRun(t *testing.T, s *scheduler.Scheduler) (stop func()) {
	t.Helper()
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Run(runCtx); close(done) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("Run did not return after the cancellation")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func TestRunMakesAFirstPassAtOnceThenOneEveryInterval(t *testing.T) {
	fake := &fakeUpdater{}
	// The interval is an hour: only the immediate first pass can happen.
	stop := startRun(t, scheduler.New(fake, scheduler.WithInterval(time.Hour)))
	waitFor(t, "the first pass", func() bool { return fake.passCount() == 1 })
	time.Sleep(100 * time.Millisecond)
	if got := fake.passCount(); got != 1 {
		t.Errorf("%d passes with an interval of one hour, want just the first one", got)
	}
	stop()

	periodic := &fakeUpdater{}
	startRun(t, scheduler.New(periodic, scheduler.WithInterval(20*time.Millisecond)))
	waitFor(t, "several passes", func() bool { return periodic.passCount() >= 4 })
}

func TestRunNeverOverlapsPasses(t *testing.T) {
	fake := &fakeUpdater{due: oneDueFeed(), updateFn: func(ctx context.Context, _ int64) error {
		time.Sleep(60 * time.Millisecond) // much longer than the interval
		return nil
	}}
	stop := startRun(t, scheduler.New(fake, scheduler.WithInterval(5*time.Millisecond)))

	waitFor(t, "several passes", func() bool { return fake.passCount() >= 3 })
	stop()

	if fake.overlapped.Load() {
		t.Error("a pass began while the previous one was still running")
	}
}

func TestRunStopsOnCancellationAfterTheInterruptedUpdatesReturned(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	fake := &fakeUpdater{due: oneDueFeed(), updateFn: func(ctx context.Context, _ int64) error {
		once.Do(func() { close(started) })
		<-ctx.Done() // a download that only ends when canceled
		return ctx.Err()
	}}
	stop := startRun(t, scheduler.New(fake, scheduler.WithInterval(time.Hour)))
	<-started

	stop() // fails the test itself if Run does not return in time

	if fake.returned.Load() != 1 || fake.inUpdate.Load() != 0 {
		t.Errorf("updates returned = %d, still running = %d; Run must wait for the updates it started",
			fake.returned.Load(), fake.inUpdate.Load())
	}
}

func TestRunSurvivesAPassThatFails(t *testing.T) {
	fake := &fakeUpdater{dueErr: errors.New("database is busy")}

	startRun(t, scheduler.New(fake, scheduler.WithInterval(10*time.Millisecond)))

	waitFor(t, "passes after the failures", func() bool { return fake.passCount() >= 4 })
}

// recordLogger returns a logger writing, at debug level, to a buffer.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestRunLogsFailuresAsWarningsAndStaysQuietWhenIdle(t *testing.T) {
	// Idle: no due feed. At info level the log stays empty.
	idleLog := &syncBuffer{}
	idle := &fakeUpdater{}
	startRun(t, scheduler.New(idle, scheduler.WithInterval(10*time.Millisecond),
		scheduler.WithLogger(slog.New(slog.NewTextHandler(idleLog, &slog.HandlerOptions{Level: slog.LevelInfo})))))
	waitFor(t, "idle passes", func() bool { return idle.passCount() >= 3 })
	if got := idleLog.String(); got != "" {
		t.Errorf("an idle scheduler logged at info level:\n%s", got)
	}

	// A failing update is a warning naming the feed; the pass summary is debug.
	log := &syncBuffer{}
	failing := &fakeUpdater{due: oneDueFeed(), updateFn: func(context.Context, int64) error { return errors.New("unexpected HTTP status 500") }}
	startRun(t, scheduler.New(failing, scheduler.WithInterval(time.Hour),
		scheduler.WithLogger(slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})))))
	waitFor(t, "the warning", func() bool { return strings.Contains(log.String(), "level=WARN") })
	out := log.String()
	for _, want := range []string{"level=WARN", "feed=1", "unexpected HTTP status 500", "level=DEBUG"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}
