// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/fetch"
	"github.com/mwolff44/mw-feed/internal/scheduler"
	"github.com/mwolff44/mw-feed/internal/store"
)

var ctx = context.Background()

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// world is a feed service over a real store, with a fake feed server whose
// answer and speed the tests control. Each feed lives at its own path.
type world struct {
	t      *testing.T
	store  *store.Store
	clock  *fakeClock
	svc    *feed.Service
	server *httptest.Server

	mu      sync.Mutex
	hits    map[string]int
	bodies  map[string]string
	status  map[string]int
	onServe func(path string) // called, without the lock, for every request
}

func newWorld(t *testing.T) *world {
	t.Helper()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	w := &world{
		t: t, store: st,
		clock:  &fakeClock{now: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)},
		hits:   map[string]int{},
		bodies: map[string]string{},
		status: map[string]int{},
	}
	w.server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		w.hits[r.URL.Path]++
		body, status, hook := w.bodies[r.URL.Path], w.status[r.URL.Path], w.onServe
		w.mu.Unlock()
		if hook != nil {
			hook(r.URL.Path)
		}
		if status != 0 {
			rw.WriteHeader(status)
		}
		_, _ = rw.Write([]byte(body))
	}))
	t.Cleanup(w.server.Close)
	w.svc = feed.NewService(st, fetch.New(fetch.Options{AllowPrivate: true}), w.clock)
	return w
}

// rss builds a feed with the given item titles; item i is dated i+1 days after 1 Jan 2026.
func rss(title string, items ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0"?><rss version="2.0"><channel><title>%s</title><link>https://%s.example/</link><description>d</description>`, title, title)
	for i, it := range items {
		fmt.Fprintf(&b, `<item><title>%s</title><link>https://%s.example/%s</link><pubDate>%s</pubDate></item>`,
			it, title, it, time.Date(2026, 1, 1+i, 10, 0, 0, 0, time.UTC).Format(time.RFC1123))
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

// addFeed subscribes to a feed served at /path.
func (w *world) addFeed(path, doc string) feed.Feed {
	w.t.Helper()
	w.setBody(path, doc)
	f, err := w.svc.AddFeed(ctx, feed.AddFeedRequest{URL: w.server.URL + path})
	if err != nil {
		w.t.Fatalf("AddFeed(%s): %v", path, err)
	}
	return f
}

func (w *world) setBody(path, doc string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bodies[path], w.status[path] = doc, 0
}

func (w *world) hitCount(path string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hits[path]
}

func TestTickUpdatesOnlyFeedsThatAreStrictlyDue(t *testing.T) {
	w := newWorld(t) // maxUpdate is 60 minutes by default
	w.addFeed("/a.xml", rss("a", "a1"))
	sched := scheduler.New(w.svc)
	base := w.hitCount("/a.xml") // the download of AddFeed

	w.clock.Advance(60 * time.Minute) // exactly the delay: not due yet
	if _, err := sched.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := w.hitCount("/a.xml"); got != base {
		t.Errorf("a feed exactly at its delay was updated (%d requests, want %d)", got, base)
	}

	w.clock.Advance(time.Second) // now strictly past the delay
	res, err := sched.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := w.hitCount("/a.xml"); got != base+1 {
		t.Errorf("a due feed was not updated (%d requests, want %d)", got, base+1)
	}
	if res.Due != 1 || res.Updated != 1 {
		t.Errorf("result = %+v, want 1 due and 1 updated", res)
	}
}

func (w *world) setInterval(f feed.Feed, minutes *int) {
	w.t.Helper()
	err := w.store.InTx(ctx, func(tx *store.Tx) error {
		cur, err := tx.FeedByID(ctx, f.ID)
		if err != nil {
			return err
		}
		// Written straight to the store, so that values EditFeed would refuse can be tested too.
		return tx.UpdateFeedFields(ctx, f.ID, store.FeedEdit{
			Title: cur.Title, SiteURL: cur.SiteURL, Description: cur.Description, XMLURL: cur.XMLURL, UpdateInterval: minutes,
		})
	})
	if err != nil {
		w.t.Fatalf("setInterval: %v", err)
	}
}

func (w *world) setMaxUpdate(minutes int) {
	w.t.Helper()
	st, err := w.store.Settings(ctx)
	if err != nil {
		w.t.Fatalf("Settings: %v", err)
	}
	st.MaxUpdate = minutes
	if err := w.store.SaveSettings(ctx, st); err != nil {
		w.t.Fatalf("SaveSettings: %v", err)
	}
}

func intp(n int) *int { return &n }

// wantUpdatedAfter checks that a Tick made after waiting for `wait` updates the
// feed (updated=true) or leaves it alone.
func (w *world) wantUpdatedAfter(sched *scheduler.Scheduler, path string, wait time.Duration, updated bool, why string) {
	w.t.Helper()
	w.clock.Advance(wait)
	before := w.hitCount(path)
	if _, err := sched.Tick(ctx); err != nil {
		w.t.Fatalf("Tick: %v", err)
	}
	if got := w.hitCount(path) > before; got != updated {
		w.t.Errorf("%s: updated = %v, want %v", why, got, updated)
	}
}

func TestDueDateFollowsTheEffectiveDelayOfEachFeed(t *testing.T) {
	cases := []struct {
		name  string
		setup func(w *world, f feed.Feed)
		// the delay in minutes the feed should effectively have
		wantMinutes int
	}{
		{"automatic: the maxUpdate setting", func(w *world, f feed.Feed) {}, 60},
		{"custom delay", func(w *world, f feed.Feed) { w.setInterval(f, intp(10)) }, 10},
		{"custom delay at the minimum", func(w *world, f feed.Feed) { w.setInterval(f, intp(5)) }, 5},
		{"custom delay lowered to the new maxUpdate", func(w *world, f feed.Feed) {
			w.setInterval(f, intp(45))
			w.setMaxUpdate(30)
		}, 30},
		{"stored delay under 5 minutes falls back to maxUpdate", func(w *world, f feed.Feed) { w.setInterval(f, intp(3)) }, 60},
		{"stored delay over maxUpdate falls back to maxUpdate", func(w *world, f feed.Feed) { w.setInterval(f, intp(90)) }, 60},
		{"custom delay with a larger maxUpdate", func(w *world, f feed.Feed) {
			w.setMaxUpdate(240)
			w.setInterval(f, intp(120))
		}, 120},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			f := w.addFeed("/f.xml", rss("f", "f1"))
			tc.setup(w, f)
			sched := scheduler.New(w.svc)
			d := time.Duration(tc.wantMinutes) * time.Minute

			w.wantUpdatedAfter(sched, "/f.xml", d-time.Minute, false, "one minute before the delay")
			w.wantUpdatedAfter(sched, "/f.xml", time.Minute, false, "exactly at the delay")
			w.wantUpdatedAfter(sched, "/f.xml", time.Second, true, "just after the delay")
			w.wantUpdatedAfter(sched, "/f.xml", time.Minute, false, "right after an update")
		})
	}
}

func TestAFeedThatWasNeverUpdatedIsDueAtOnce(t *testing.T) {
	w := newWorld(t)
	w.setBody("/new.xml", rss("new", "n1"))
	// Created without a download, as an OPML import will do.
	if _, err := w.store.InsertFeed(ctx, store.NewFeed{XMLURL: w.server.URL + "/new.xml", Title: "New"}); err != nil {
		t.Fatalf("InsertFeed: %v", err)
	}

	res, err := scheduler.New(w.svc).Tick(ctx)

	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Due != 1 || res.Updated != 1 || w.hitCount("/new.xml") != 1 {
		t.Errorf("result = %+v, %d request(s); want the feed updated at once", res, w.hitCount("/new.xml"))
	}
	feeds, _ := w.svc.Feeds(ctx)
	if feeds[0].LastUpdateAt.IsZero() {
		t.Error("the update date was not recorded")
	}
}

func TestAFailingFeedIsRetriedAtTheSameRhythmAsTheOthers(t *testing.T) {
	w := newWorld(t)
	w.addFeed("/bad.xml", rss("bad", "b1"))
	sched := scheduler.New(w.svc)
	w.mu.Lock()
	w.status["/bad.xml"] = http.StatusInternalServerError
	w.mu.Unlock()

	w.wantUpdatedAfter(sched, "/bad.xml", 61*time.Minute, true, "the feed is due and tried")
	feeds, _ := w.svc.Feeds(ctx)
	if feeds[0].LastError == "" {
		t.Error("the failure was not recorded")
	}
	w.wantUpdatedAfter(sched, "/bad.xml", time.Minute, false, "a minute after a failure: no tight retry loop")
	w.wantUpdatedAfter(sched, "/bad.xml", 60*time.Minute, true, "an hour after the failure: tried again")
}

// gate makes the fake server hold every request until released, and counts how
// many are held at once.
type gate struct {
	release  chan struct{}
	inFlight atomic.Int32
	maxSeen  atomic.Int32
}

func newGate() *gate { return &gate{release: make(chan struct{})} }

func (g *gate) hold(string) {
	n := g.inFlight.Add(1)
	for {
		old := g.maxSeen.Load()
		if n <= old || g.maxSeen.CompareAndSwap(old, n) {
			break
		}
	}
	<-g.release
	g.inFlight.Add(-1)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (w *world) addManyFeeds(n int) []feed.Feed {
	w.t.Helper()
	var feeds []feed.Feed
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("/f%d.xml", i)
		feeds = append(feeds, w.addFeed(path, rss(fmt.Sprintf("f%d", i), "x1")))
	}
	return feeds
}

func TestAtMostFourFeedsAreUpdatedAtTheSameTime(t *testing.T) {
	w := newWorld(t)
	w.addManyFeeds(10)
	g := newGate()
	t.Cleanup(func() {
		select {
		case <-g.release:
		default:
			close(g.release)
		}
	})
	w.mu.Lock()
	w.onServe = g.hold
	w.mu.Unlock()
	w.clock.Advance(2 * time.Hour) // all ten are due
	done := make(chan scheduler.TickResult, 1)

	go func() {
		res, _ := scheduler.New(w.svc).Tick(ctx)
		done <- res
	}()

	waitFor(t, "four requests in flight", func() bool { return g.inFlight.Load() == 4 })
	time.Sleep(100 * time.Millisecond) // give a fifth one the chance to wrongly start
	if got := g.maxSeen.Load(); got != 4 {
		t.Errorf("at most %d requests were in flight, want exactly 4", got)
	}
	close(g.release)
	res := <-done
	if res.Due != 10 || res.Updated != 10 {
		t.Errorf("result = %+v, want 10 due and 10 updated", res)
	}
	if got := g.maxSeen.Load(); got != 4 {
		t.Errorf("maximum in flight = %d after the whole pass, want 4", got)
	}
}

func TestAFailingFeedDoesNotStopTheOthers(t *testing.T) {
	w := newWorld(t)
	feeds := w.addManyFeeds(5)
	w.mu.Lock()
	w.status["/f0.xml"], w.status["/f1.xml"] = http.StatusInternalServerError, http.StatusBadGateway
	w.bodies["/f2.xml"] = "<html>not a feed</html>"
	w.bodies["/f3.xml"] = rss("f3", "x1", "x2") // has a new item
	w.mu.Unlock()
	w.clock.Advance(2 * time.Hour)

	res, err := scheduler.New(w.svc).Tick(ctx)

	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Due != 5 || res.Updated != 2 || res.Failed != 3 {
		t.Errorf("result = %+v, want 5 due, 2 updated, 3 failed", res)
	}
	for i, wantErr := range []bool{true, true, true, false, false} {
		got, _ := w.store.FeedByID(ctx, feeds[i].ID)
		if (got.LastError != "") != wantErr {
			t.Errorf("feed %d: last error = %q, want an error: %v", i, got.LastError, wantErr)
		}
	}
	page, err := w.svc.ListItems(ctx, feed.ItemQuery{Selection: feed.InFeed(feeds[3].ID), Filter: feed.FilterAll})
	if err != nil || page.Total != 2 {
		t.Errorf("the healthy feed has %d item(s) (error %v), want its new one imported", page.Total, err)
	}
}

func TestCancellationAbortsDownloadsWithoutRecordingErrors(t *testing.T) {
	w := newWorld(t)
	feeds := w.addManyFeeds(6)
	var before int
	for i := range feeds {
		before += w.hitCount(fmt.Sprintf("/f%d.xml", i))
	}
	g := newGate()
	t.Cleanup(func() {
		select {
		case <-g.release:
		default:
			close(g.release)
		}
	})
	w.mu.Lock()
	w.onServe = g.hold
	w.mu.Unlock()
	w.clock.Advance(2 * time.Hour)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan scheduler.TickResult, 1)
	go func() {
		res, _ := scheduler.New(w.svc).Tick(runCtx)
		done <- res
	}()
	waitFor(t, "four requests in flight", func() bool { return g.inFlight.Load() == 4 })

	cancel() // as on SIGTERM

	select {
	case res := <-done:
		if res.Updated != 0 || res.Failed != 0 || res.Canceled != 6 {
			t.Errorf("result = %+v, want all 6 updates canceled", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Tick did not return after the cancellation")
	}
	var after int
	for i, f := range feeds {
		after += w.hitCount(fmt.Sprintf("/f%d.xml", i))
		got, _ := w.store.FeedByID(ctx, f.ID)
		if got.LastError != "" {
			t.Errorf("feed %d recorded %q: an interrupted update is not a feed failure", i, got.LastError)
		}
	}
	if after-before != 4 {
		t.Errorf("%d request(s) were started, want only the 4 allowed in flight", after-before)
	}
}

// hold makes the server hold the requests for one path only.
func (g *gate) holdPath(path string) func(string) {
	return func(p string) {
		if p == path {
			g.hold(p)
		}
	}
}

func TestTwoUpdatesOfTheSameFeedNeverRunInParallelNorImportTwice(t *testing.T) {
	w := newWorld(t)
	f := w.addFeed("/a.xml", rss("a", "a1"))
	w.setBody("/a.xml", rss("a", "a1", "a2")) // one new item
	g := newGate()
	t.Cleanup(func() {
		select {
		case <-g.release:
		default:
			close(g.release)
		}
	})
	w.mu.Lock()
	w.onServe = g.holdPath("/a.xml")
	w.mu.Unlock()
	base := w.hitCount("/a.xml")
	results := make(chan feed.UpdateResult, 2)
	update := func() {
		res, err := w.svc.UpdateFeed(ctx, f.ID, feed.UpdateOptions{Force: true})
		if err != nil {
			t.Errorf("UpdateFeed: %v", err)
		}
		results <- res
	}

	go update()
	waitFor(t, "the first download", func() bool { return g.inFlight.Load() == 1 })
	go update() // the same feed, while the first update is downloading
	time.Sleep(150 * time.Millisecond)

	if got := w.hitCount("/a.xml") - base; got != 1 {
		t.Errorf("%d download(s) started while the first was running, want the second to wait", got)
	}
	close(g.release)
	first, second := <-results, <-results

	if first.NewItems+second.NewItems != 1 {
		t.Errorf("new items imported: %d and %d, want exactly one in total", first.NewItems, second.NewItems)
	}
	page, _ := w.svc.ListItems(ctx, feed.ItemQuery{Selection: feed.InFeed(f.ID), Filter: feed.FilterAll})
	if page.Total != 2 {
		t.Errorf("the feed has %d items, want 2 (no duplicate)", page.Total)
	}
	if got := w.hitCount("/a.xml") - base; got != 2 {
		t.Errorf("%d downloads in total, want 2: the second update ran after the first", got)
	}
}

func TestAScheduledUpdateSkipsAFeedThatAManualUpdateJustRefreshed(t *testing.T) {
	w := newWorld(t)
	f := w.addFeed("/a.xml", rss("a", "a1"))
	g := newGate()
	t.Cleanup(func() {
		select {
		case <-g.release:
		default:
			close(g.release)
		}
	})
	w.mu.Lock()
	w.onServe = g.holdPath("/a.xml")
	w.mu.Unlock()
	w.clock.Advance(2 * time.Hour) // the feed is due
	base := w.hitCount("/a.xml")
	manual := make(chan error, 1)
	go func() {
		_, err := w.svc.UpdateFeed(ctx, f.ID, feed.UpdateOptions{Force: true})
		manual <- err
	}()
	waitFor(t, "the manual download", func() bool { return g.inFlight.Load() == 1 })
	tick := make(chan scheduler.TickResult, 1)
	go func() {
		res, _ := scheduler.New(w.svc).Tick(ctx) // sees the feed as due, then waits for the lock
		tick <- res
	}()
	time.Sleep(150 * time.Millisecond)

	close(g.release)

	if err := <-manual; err != nil {
		t.Fatalf("manual update: %v", err)
	}
	res := <-tick
	if res.Due != 1 || res.Skipped != 1 || res.Updated != 0 {
		t.Errorf("tick = %+v, want the feed seen as due but skipped once refreshed", res)
	}
	if got := w.hitCount("/a.xml") - base; got != 1 {
		t.Errorf("%d download(s), want only the manual one", got)
	}
}

func TestWaitingForAFeedLockRespectsTheContext(t *testing.T) {
	w := newWorld(t)
	f := w.addFeed("/a.xml", rss("a", "a1"))
	g := newGate()
	t.Cleanup(func() {
		select {
		case <-g.release:
		default:
			close(g.release)
		}
	})
	w.mu.Lock()
	w.onServe = g.holdPath("/a.xml")
	w.mu.Unlock()
	go func() { _, _ = w.svc.UpdateFeed(ctx, f.ID, feed.UpdateOptions{Force: true}) }()
	waitFor(t, "the first download", func() bool { return g.inFlight.Load() == 1 })
	waiting, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	_, err := w.svc.UpdateFeed(waiting, f.ID, feed.UpdateOptions{Force: true})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want the context deadline: the waiting call must give up", err)
	}
	got, _ := w.store.FeedByID(ctx, f.ID)
	if got.LastError != "" {
		t.Errorf("a call that gave up waiting recorded %q on the feed", got.LastError)
	}
}
