// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/feed"
)

// manualWorld has five feeds, none of them due:
//
//	"feed 10" and "Feed 2" in folder X; "alpha" in folders X and Y; "beta" and "gamma" in no folder.
type manualWorld struct {
	*world
	x, y                              int64
	feed10, feed2, alpha, beta, gamma feed.Feed
}

func newManualWorld(t *testing.T) *manualWorld {
	t.Helper()
	w := newWorld(t)
	var ids []int64
	for _, title := range []string{"X", "Y"} {
		f, err := w.svc.CreateFolder(ctx, title)
		if err != nil {
			t.Fatalf("CreateFolder: %v", err)
		}
		ids = append(ids, f.ID)
	}
	m := &manualWorld{world: w, x: ids[0], y: ids[1]}
	m.feed10 = w.addFeedIn("/feed10.xml", rss("feed 10", "t1"), ids[0])
	m.feed2 = w.addFeedIn("/feed2.xml", rss("Feed 2", "t1"), ids[0])
	m.alpha = w.addFeedIn("/alpha.xml", rss("alpha", "t1"), ids[0], ids[1])
	m.beta = w.addFeedIn("/beta.xml", rss("beta", "t1"))
	m.gamma = w.addFeedIn("/gamma.xml", rss("gamma", "t1"))
	return m
}

func (w *world) addFeedIn(path, doc string, folderIDs ...int64) feed.Feed {
	w.t.Helper()
	w.setBody(path, doc)
	f, err := w.svc.AddFeed(ctx, feed.AddFeedRequest{URL: w.server.URL + path, FolderIDs: folderIDs})
	if err != nil {
		w.t.Fatalf("AddFeed(%s): %v", path, err)
	}
	return f
}

// hitsSince returns, per path, the requests made since the snapshot.
func (w *world) snapshot() map[string]int {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string]int{}
	for k, v := range w.hits {
		out[k] = v
	}
	return out
}

func (w *world) downloadedSince(before map[string]int) map[string]int {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string]int{}
	for k, v := range w.hits {
		if v > before[k] {
			out[k] = v - before[k]
		}
	}
	return out
}

func titlesOfUpdates(updates []feed.FeedUpdate) []string {
	var out []string
	for _, u := range updates {
		out = append(out, u.Title)
	}
	return out
}

func TestUpdateSelectionDownloadsEachFeedOfTheSelectionOnceWhateverTheDelay(t *testing.T) {
	m := newManualWorld(t) // nothing is due: a manual update ignores the delay
	cases := []struct {
		name      string
		sel       feed.Selection
		wantPaths []string
		wantOrder []string // titles, natural order ignoring case
	}{
		{"one feed", feed.InFeed(m.beta.ID), []string{"/beta.xml"}, []string{"beta"}},
		{"a folder", feed.InFolder(m.x), []string{"/feed10.xml", "/feed2.xml", "/alpha.xml"}, []string{"alpha", "Feed 2", "feed 10"}},
		{"another folder: alpha alone", feed.InFolder(m.y), []string{"/alpha.xml"}, []string{"alpha"}},
		{"everything, each feed once even when in two folders", feed.AllItems(),
			[]string{"/feed10.xml", "/feed2.xml", "/alpha.xml", "/beta.xml", "/gamma.xml"},
			[]string{"alpha", "beta", "Feed 2", "feed 10", "gamma"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := m.snapshot()

			updates, err := m.svc.UpdateSelection(ctx, tc.sel)

			if err != nil {
				t.Fatalf("UpdateSelection: %v", err)
			}
			got := m.downloadedSince(before)
			if len(got) != len(tc.wantPaths) {
				t.Errorf("downloaded %v, want exactly %v, once each", got, tc.wantPaths)
			}
			for _, p := range tc.wantPaths {
				if got[p] != 1 {
					t.Errorf("%s was downloaded %d time(s), want 1", p, got[p])
				}
			}
			if titles := titlesOfUpdates(updates); len(titles) != len(tc.wantOrder) || !equalStrings(titles, tc.wantOrder) {
				t.Errorf("results for %v, want %v (feed list order)", titles, tc.wantOrder)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestUpdateSelectionReportsEachFeedAndNeverStopsAtAFailure(t *testing.T) {
	m := newManualWorld(t)
	m.setBody("/alpha.xml", rss("alpha", "t1", "t2", "t3")) // two new items
	m.mu.Lock()
	m.status["/beta.xml"] = http.StatusInternalServerError
	m.mu.Unlock()

	updates, err := m.svc.UpdateSelection(ctx, feed.AllItems())

	if err != nil {
		t.Fatalf("UpdateSelection: %v", err)
	}
	if len(updates) != 5 {
		t.Fatalf("%d results, want one per feed (5)", len(updates))
	}
	byTitle := map[string]feed.FeedUpdate{}
	for _, u := range updates {
		byTitle[u.Title] = u
	}
	if a := byTitle["alpha"]; a.FeedID != m.alpha.ID || a.Err != nil || a.Result.NewItems != 2 {
		t.Errorf("alpha = %+v, want its id, no error and 2 new items", a)
	}
	if b := byTitle["beta"]; b.FeedID != m.beta.ID || b.Err == nil {
		t.Errorf("beta = %+v, want its error reported", b)
	}
	for _, name := range []string{"Feed 2", "feed 10", "gamma"} {
		if u := byTitle[name]; u.Err != nil {
			t.Errorf("%s failed (%v) because of another feed", name, u.Err)
		}
	}
}

func TestUpdateSelectionOfAnUnknownSelectionDownloadsNothing(t *testing.T) {
	m := newManualWorld(t)
	before := m.snapshot()

	for name, sel := range map[string]feed.Selection{"folder": feed.InFolder(9999), "feed": feed.InFeed(9999)} {
		if _, err := m.svc.UpdateSelection(ctx, sel); !errors.Is(err, feed.ErrNotFound) {
			t.Errorf("unknown %s: error = %v, want feed.ErrNotFound", name, err)
		}
	}
	if got := m.downloadedSince(before); len(got) != 0 {
		t.Errorf("downloaded %v, want nothing", got)
	}
}

func TestUpdateSelectionStopsLaunchingFeedsWhenTheContextIsCanceled(t *testing.T) {
	m := newManualWorld(t)
	before := m.snapshot()
	g := newGate()
	t.Cleanup(func() {
		select {
		case <-g.release:
		default:
			close(g.release)
		}
	})
	m.mu.Lock()
	m.onServe = g.hold
	m.mu.Unlock()
	runCtx, cancel := context.WithCancel(ctx)
	type outcome struct {
		updates []feed.FeedUpdate
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		u, err := m.svc.UpdateSelection(runCtx, feed.AllItems())
		done <- outcome{u, err}
	}()
	waitFor(t, "four downloads in flight", func() bool { return g.inFlight.Load() == 4 })

	cancel()

	select {
	case o := <-done:
		if !errors.Is(o.err, context.Canceled) {
			t.Errorf("error = %v, want the context error", o.err)
		}
		for _, u := range o.updates {
			if u.Err == nil {
				t.Errorf("%s has no error although the update was canceled", u.Title)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("UpdateSelection did not return after the cancellation")
	}
	total := 0
	for _, n := range m.downloadedSince(before) {
		total += n
	}
	if total != 4 {
		t.Errorf("%d download(s) started, want only the 4 that fit the limit", total)
	}
	for _, f := range []feed.Feed{m.feed10, m.alpha, m.beta, m.gamma, m.feed2} {
		if got, _ := m.store.FeedByID(ctx, f.ID); got.LastError != "" {
			t.Errorf("%s recorded %q: a canceled update is not a feed failure", f.Title, got.LastError)
		}
	}
}
