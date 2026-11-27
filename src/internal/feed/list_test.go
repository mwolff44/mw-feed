// SPDX-License-Identifier: GPL-3.0-or-later

package feed_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/feed"
)

// fixture is a reader with three feeds and two folders:
//
//	feed A in folders X and Y: a1 (5 Jan), a2 (7 Jan), a3 (9 Jan)
//	feed B in folder Y:        b1 (6 Jan), b2 (8 Jan)
//	feed C in no folder:       c1 (10 Jan)
//
// Newest first: c1 a3 b2 a2 b1 a1.
type fixture struct {
	*env
	x, y        int64 // folders
	a, b, c     feed.Feed
	itemByTitle map[string]feed.Item
}

func (e *env) addFeedAt(t *testing.T, path, doc string, req feed.AddFeedRequest) feed.Feed {
	t.Helper()
	e.server.set(doc)
	req.URL = e.server.URL + path
	added, err := e.svc.AddFeed(ctx, req)
	if err != nil {
		t.Fatalf("AddFeed(%s): %v", path, err)
	}
	return added
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	e := newEnv(t)
	f := &fixture{env: e}
	ids := e.folders(t, "X", "Y")
	f.x, f.y = ids["X"], ids["Y"]
	item := func(title, date string) entry { return entry{title, "https://s.example/" + title, date, ""} }
	f.a = e.addFeedAt(t, "/a.xml", rss("Feed A", "https://a.example/", "d", item("a1", jan5), item("a2", jan7), item("a3", jan9)),
		feed.AddFeedRequest{FolderIDs: []int64{f.x, f.y}})
	f.b = e.addFeedAt(t, "/b.xml", rss("Feed B", "https://b.example/", "d", item("b1", jan6), item("b2", jan8)),
		feed.AddFeedRequest{FolderIDs: []int64{f.y}})
	f.c = e.addFeedAt(t, "/c.xml", rss("Feed C", "https://c.example/", "d", item("c1", jan10)), feed.AddFeedRequest{})
	f.itemByTitle = map[string]feed.Item{}
	for _, id := range []int64{f.a.ID, f.b.ID, f.c.ID} {
		items, err := e.svc.Items(ctx, id)
		if err != nil {
			t.Fatalf("Items: %v", err)
		}
		for _, it := range items {
			f.itemByTitle[it.Title] = it
		}
	}
	return f
}

func (f *fixture) id(title string) int64 { return f.itemByTitle[title].ID }

// titlesOf lists the titles of a page, in order.
func titlesOf(p feed.ItemPage) string { return itemTitles(p.Items) }

func TestListItemsAllShowsEveryItemOfExistingFeedsExactlyOnce(t *testing.T) {
	f := newFixture(t)
	// A starred item of a deleted feed is orphaned: it must not appear in a normal list.
	gone := f.addFeedAt(t, "/gone.xml", rss("Gone", "https://g.example/", "d", entry{"orphan", "https://g.example/1", jan10, ""}), feed.AddFeedRequest{})
	items, _ := f.svc.Items(ctx, gone.ID)
	if err := f.svc.SetStarred(ctx, items[0].ID, true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	if err := f.svc.DeleteFeed(ctx, gone.ID); err != nil {
		t.Fatalf("DeleteFeed: %v", err)
	}

	page, err := f.svc.ListItems(ctx, feed.ItemQuery{})

	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	// Feed A is in two folders, yet its items appear once; the orphan is absent.
	if got := titlesOf(page); got != "c1,a3,b2,a2,b1,a1" {
		t.Errorf("items = %s, want c1,a3,b2,a2,b1,a1", got)
	}
	if page.Total != 6 {
		t.Errorf("Total = %d, want 6", page.Total)
	}
}

func TestListItemsBySelection(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		sel  feed.Selection
		want string
	}{
		{"all", feed.AllItems(), "c1,a3,b2,a2,b1,a1"},
		{"folder X: feed A only", feed.InFolder(f.x), "a3,a2,a1"},
		{"folder Y: feeds A and B, A once", feed.InFolder(f.y), "a3,b2,a2,b1,a1"},
		{"feed A", feed.InFeed(f.a.ID), "a3,a2,a1"},
		{"feed B", feed.InFeed(f.b.ID), "b2,b1"},
		{"feed C, which is in no folder", feed.InFeed(f.c.ID), "c1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.svc.ListItems(ctx, feed.ItemQuery{Selection: tc.sel})

			if err != nil {
				t.Fatalf("ListItems: %v", err)
			}
			if got := titlesOf(page); got != tc.want {
				t.Errorf("items = %s, want %s", got, tc.want)
			}
			if page.Total != len(strings.Split(tc.want, ",")) {
				t.Errorf("Total = %d, want %d", page.Total, len(strings.Split(tc.want, ",")))
			}
		})
	}
}

func TestListItemsOfAnUnknownSelectionIsNotFound(t *testing.T) {
	f := newFixture(t)

	for name, sel := range map[string]feed.Selection{"folder": feed.InFolder(9999), "feed": feed.InFeed(9999)} {
		if _, err := f.svc.ListItems(ctx, feed.ItemQuery{Selection: sel}); !errors.Is(err, feed.ErrNotFound) {
			t.Errorf("unknown %s: error = %v, want feed.ErrNotFound", name, err)
		}
	}
}

// readSome marks a1, b2 and c1 as read; a2, a3 and b1 stay unread.
func (f *fixture) readSome(t *testing.T) {
	t.Helper()
	for _, title := range []string{"a1", "b2", "c1"} {
		if err := f.svc.SetRead(ctx, f.id(title), true); err != nil {
			t.Fatalf("SetRead: %v", err)
		}
	}
}

func TestListItemsFilterAndOrder(t *testing.T) {
	f := newFixture(t)
	f.readSome(t)
	cases := []struct {
		name   string
		sel    feed.Selection
		filter string
		order  string
		want   string
	}{
		{"all, unread, newer first", feed.AllItems(), feed.FilterUnread, feed.OrderNewerFirst, "a3,a2,b1"},
		{"all, unread, older first", feed.AllItems(), feed.FilterUnread, feed.OrderOlderFirst, "b1,a2,a3"},
		{"all, all, newer first", feed.AllItems(), feed.FilterAll, feed.OrderNewerFirst, "c1,a3,b2,a2,b1,a1"},
		{"all, all, older first", feed.AllItems(), feed.FilterAll, feed.OrderOlderFirst, "a1,b1,a2,b2,a3,c1"},
		{"folder Y, unread, newer first", feed.InFolder(f.y), feed.FilterUnread, feed.OrderNewerFirst, "a3,a2,b1"},
		{"folder Y, all, older first", feed.InFolder(f.y), feed.FilterAll, feed.OrderOlderFirst, "a1,b1,a2,b2,a3"},
		{"folder X, unread, older first", feed.InFolder(f.x), feed.FilterUnread, feed.OrderOlderFirst, "a2,a3"},
		{"feed B, unread, newer first", feed.InFeed(f.b.ID), feed.FilterUnread, feed.OrderNewerFirst, "b1"},
		{"feed C, unread: everything read", feed.InFeed(f.c.ID), feed.FilterUnread, feed.OrderNewerFirst, ""},
		{"feed C, all", feed.InFeed(f.c.ID), feed.FilterAll, feed.OrderNewerFirst, "c1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.svc.ListItems(ctx, feed.ItemQuery{Selection: tc.sel, Filter: tc.filter, Order: tc.order})

			if err != nil {
				t.Fatalf("ListItems: %v", err)
			}
			if got := titlesOf(page); got != tc.want {
				t.Errorf("items = %q, want %q", got, tc.want)
			}
			wantTotal := 0
			if tc.want != "" {
				wantTotal = len(strings.Split(tc.want, ","))
			}
			if page.Total != wantTotal {
				t.Errorf("Total = %d, want %d", page.Total, wantTotal)
			}
		})
	}
}

func TestListItemsUsesTheSettingsWhenFilterOrOrderAreAbsent(t *testing.T) {
	f := newFixture(t)
	f.readSome(t)

	// Defaults: unread items, newer first.
	page, _ := f.svc.ListItems(ctx, feed.ItemQuery{})
	if got := titlesOf(page); got != "a3,a2,b1" {
		t.Errorf("default settings: items = %s, want a3,a2,b1", got)
	}

	st, _ := f.store.Settings(ctx)
	st.Filter, st.Order = "all", "olderFirst"
	if err := f.store.SaveSettings(ctx, st); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	page, _ = f.svc.ListItems(ctx, feed.ItemQuery{})
	if got := titlesOf(page); got != "a1,b1,a2,b2,a3,c1" {
		t.Errorf("settings all/olderFirst: items = %s, want a1,b1,a2,b2,a3,c1", got)
	}
	// An explicit value wins over the settings.
	page, _ = f.svc.ListItems(ctx, feed.ItemQuery{Filter: feed.FilterUnread, Order: feed.OrderNewerFirst})
	if got := titlesOf(page); got != "a3,a2,b1" {
		t.Errorf("explicit values: items = %s, want a3,a2,b1", got)
	}
}

func TestListItemsRejectsUnknownFilterAndOrder(t *testing.T) {
	f := newFixture(t)

	for name, q := range map[string]feed.ItemQuery{
		"filter": {Filter: "foo"},
		"order":  {Order: "sideways"},
	} {
		if _, err := f.svc.ListItems(ctx, q); !errors.Is(err, feed.ErrInvalidQuery) {
			t.Errorf("invalid %s: error = %v, want feed.ErrInvalidQuery", name, err)
		}
	}
}

func TestListItemsOrdersEqualDatesByImportOrderInTheSameDirection(t *testing.T) {
	e := newEnv(t)
	same := func(title string) entry { return entry{title, "https://s.example/" + title, jan5, ""} }
	d := e.addFeedAt(t, "/d.xml", rss("Feed D", "https://d.example/", "d", same("d1"), same("d2"), same("d3")), feed.AddFeedRequest{})

	newer, _ := e.svc.ListItems(ctx, feed.ItemQuery{Selection: feed.InFeed(d.ID), Filter: feed.FilterAll, Order: feed.OrderNewerFirst})
	older, _ := e.svc.ListItems(ctx, feed.ItemQuery{Selection: feed.InFeed(d.ID), Filter: feed.FilterAll, Order: feed.OrderOlderFirst})

	if got := titlesOf(newer); got != "d3,d2,d1" {
		t.Errorf("newer first = %s, want d3,d2,d1 (latest import first)", got)
	}
	if got := titlesOf(older); got != "d1,d2,d3" {
		t.Errorf("older first = %s, want d1,d2,d3", got)
	}
}

func TestListItemsPagination(t *testing.T) {
	f := newFixture(t) // all items, newest first: c1 a3 b2 a2 b1 a1
	all := feed.ItemQuery{Filter: feed.FilterAll, Order: feed.OrderNewerFirst}
	cases := []struct {
		name          string
		perPage, page int
		wantItems     string
		wantPage      int
		wantPageCount int
	}{
		{"first page of three", 2, 1, "c1,a3", 1, 3},
		{"second page", 2, 2, "b2,a2", 2, 3},
		{"last page", 2, 3, "b1,a1", 3, 3},
		{"page 0 becomes page 1", 2, 0, "c1,a3", 1, 3},
		{"negative page becomes page 1", 2, -5, "c1,a3", 1, 3},
		{"page past the end becomes the last page", 2, 4, "b1,a1", 3, 3},
		{"page far past the end", 2, 99, "b1,a1", 3, 3},
		{"page size that does not divide the total", 4, 2, "b1,a1", 2, 2},
		{"page size equal to the total", 6, 1, "c1,a3,b2,a2,b1,a1", 1, 1},
		{"page size above the total", 10, 1, "c1,a3,b2,a2,b1,a1", 1, 1},
		{"page size of 1", 1, 6, "a1", 6, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := all
			q.PerPage, q.Page = tc.perPage, tc.page

			page, err := f.svc.ListItems(ctx, q)

			if err != nil {
				t.Fatalf("ListItems: %v", err)
			}
			if got := titlesOf(page); got != tc.wantItems {
				t.Errorf("items = %s, want %s", got, tc.wantItems)
			}
			if page.Page != tc.wantPage || page.PageCount != tc.wantPageCount || page.Total != 6 {
				t.Errorf("page %d of %d, total %d; want page %d of %d, total 6", page.Page, page.PageCount, page.Total, tc.wantPage, tc.wantPageCount)
			}
		})
	}
}

func TestListItemsPaginationOfAnEmptyListIsPageOneOfOne(t *testing.T) {
	f := newFixture(t)
	f.readSome(t) // feed C has only c1, now read

	page, err := f.svc.ListItems(ctx, feed.ItemQuery{Selection: feed.InFeed(f.c.ID), Filter: feed.FilterUnread, Page: 7})

	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(page.Items) != 0 || page.Page != 1 || page.PageCount != 1 || page.Total != 0 {
		t.Errorf("page = %+v, want an empty page 1 of 1", page)
	}
}

func TestListItemsPageSizeComesFromTheSettingsUnlessTheQueryGivesOne(t *testing.T) {
	f := newFixture(t)
	all := feed.ItemQuery{Filter: feed.FilterAll}

	page, _ := f.svc.ListItems(ctx, all) // by_page is 10 by default
	if len(page.Items) != 6 || page.PageCount != 1 {
		t.Errorf("default: %d items on %d page(s), want 6 on 1", len(page.Items), page.PageCount)
	}
	st, _ := f.store.Settings(ctx)
	st.ByPage = 4
	if err := f.store.SaveSettings(ctx, st); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	page, _ = f.svc.ListItems(ctx, all)
	if len(page.Items) != 4 || page.PageCount != 2 {
		t.Errorf("by_page=4: %d items on %d page(s), want 4 on 2", len(page.Items), page.PageCount)
	}
	all.PerPage = 2
	page, _ = f.svc.ListItems(ctx, all)
	if len(page.Items) != 2 || page.PageCount != 3 {
		t.Errorf("PerPage=2: %d items on %d page(s), want 2 on 3", len(page.Items), page.PageCount)
	}
	all.PerPage = -1
	if _, err := f.svc.ListItems(ctx, all); !errors.Is(err, feed.ErrInvalidQuery) {
		t.Errorf("PerPage=-1: error = %v, want feed.ErrInvalidQuery", err)
	}
}

func TestItemReturnsOneCompleteItem(t *testing.T) {
	f := newFixture(t)

	it, err := f.svc.Item(ctx, f.id("a2"))

	if err != nil {
		t.Fatalf("Item: %v", err)
	}
	if it.Title != "a2" || it.FeedID != f.a.ID || it.FeedTitle != "Feed A" || it.Link != "https://s.example/a2" ||
		!strings.Contains(it.Content, "About a2") || it.Read || !it.StarredAt.IsZero() {
		t.Errorf("item = %+v", it)
	}
	if _, err := f.svc.Item(ctx, 9999); !errors.Is(err, feed.ErrNotFound) {
		t.Errorf("unknown item: error = %v, want feed.ErrNotFound", err)
	}
}

func TestCountsPerFeedPerFolderAndGlobally(t *testing.T) {
	f := newFixture(t)
	emptyFolder := f.folders(t, "Empty")["Empty"]
	bare := f.addFeedAt(t, "/bare.xml", rss("Bare", "https://bare.example/", "d"), feed.AddFeedRequest{})

	counts, err := f.svc.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	// Nothing read yet. Folder Y holds feeds A and B; feed A is also in X.
	want := feed.Counts{
		All:     feed.Count{Unread: 6, Total: 6},
		Feeds:   map[int64]feed.Count{f.a.ID: {3, 3}, f.b.ID: {2, 2}, f.c.ID: {1, 1}, bare.ID: {0, 0}},
		Folders: map[int64]feed.Count{f.x: {3, 3}, f.y: {5, 5}, emptyFolder: {0, 0}},
	}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("counts = %+v\nwant     %+v", counts, want)
	}

	f.readSome(t) // a1, b2, c1
	counts, _ = f.svc.Counts(ctx)
	want = feed.Counts{
		All:     feed.Count{Unread: 3, Total: 6}, // not the sum of the folders: A is in two of them
		Feeds:   map[int64]feed.Count{f.a.ID: {2, 3}, f.b.ID: {1, 2}, f.c.ID: {0, 1}, bare.ID: {0, 0}},
		Folders: map[int64]feed.Count{f.x: {2, 3}, f.y: {3, 5}, emptyFolder: {0, 0}},
	}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("after reading: counts = %+v\nwant %+v", counts, want)
	}
}

func TestCountsIgnoreOrphanedItemsAndFollowDeletions(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.SetStarred(ctx, f.id("b1"), true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	if err := f.svc.DeleteFeed(ctx, f.b.ID); err != nil { // b1 survives as an orphan; b2 is deleted
		t.Fatalf("DeleteFeed: %v", err)
	}

	counts, err := f.svc.Counts(ctx)

	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.All != (feed.Count{Unread: 4, Total: 4}) { // a1 a2 a3 c1; the orphan b1 is not counted
		t.Errorf("All = %+v, want 4 unread of 4", counts.All)
	}
	if _, ok := counts.Feeds[f.b.ID]; ok {
		t.Error("the deleted feed still has counters")
	}
	if counts.Folders[f.y] != (feed.Count{Unread: 3, Total: 3}) {
		t.Errorf("folder Y = %+v, want 3 of 3 (feed A only)", counts.Folders[f.y])
	}
}

// unreadTitles lists, newest first, the unread items of all existing feeds.
func (f *fixture) unreadTitles(t *testing.T) string {
	t.Helper()
	page, err := f.svc.ListItems(ctx, feed.ItemQuery{Filter: feed.FilterUnread, Order: feed.OrderNewerFirst, PerPage: 100})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	return titlesOf(page)
}

func TestMarkReadBySelection(t *testing.T) {
	cases := []struct {
		name          string
		sel           func(f *fixture) feed.Selection
		read          bool
		prepare       func(t *testing.T, f *fixture) // sets up some state first
		wantChanged   int
		wantUnreadNow string // unread items afterwards
	}{
		{"a feed", func(f *fixture) feed.Selection { return feed.InFeed(f.a.ID) }, true, nil, 3, "c1,b2,b1"},
		{"a folder, its feeds only (A is in two folders)", func(f *fixture) feed.Selection { return feed.InFolder(f.x) }, true, nil, 3, "c1,b2,b1"},
		{"a folder with two feeds", func(f *fixture) feed.Selection { return feed.InFolder(f.y) }, true, nil, 5, "c1"},
		{"everything", func(f *fixture) feed.Selection { return feed.AllItems() }, true, nil, 6, ""},
		{"only the items that change are counted", func(f *fixture) feed.Selection { return feed.AllItems() }, true,
			func(t *testing.T, f *fixture) { f.readSome(t) }, 3, ""},
		{"marking unread", func(f *fixture) feed.Selection { return feed.InFolder(f.y) }, false,
			func(t *testing.T, f *fixture) { markAll(t, f, true) }, 5, "a3,b2,a2,b1,a1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.prepare != nil {
				tc.prepare(t, f)
			}

			changed, err := f.svc.MarkRead(ctx, tc.sel(f), tc.read)

			if err != nil {
				t.Fatalf("MarkRead: %v", err)
			}
			if changed != tc.wantChanged {
				t.Errorf("changed = %d, want %d", changed, tc.wantChanged)
			}
			if got := f.unreadTitles(t); got != tc.wantUnreadNow {
				t.Errorf("unread afterwards = %q, want %q", got, tc.wantUnreadNow)
			}
		})
	}
}

func markAll(t *testing.T, f *fixture, read bool) {
	t.Helper()
	if _, err := f.svc.MarkRead(ctx, feed.AllItems(), read); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
}

func TestMarkReadCoversTheWholeSelectionNotJustThePage(t *testing.T) {
	f := newFixture(t)
	first, _ := f.svc.ListItems(ctx, feed.ItemQuery{PerPage: 2}) // only 2 of the 6 items are on the page

	changed, err := f.svc.MarkRead(ctx, feed.AllItems(), true)

	if err != nil || changed != 6 || len(first.Items) != 2 {
		t.Errorf("changed = %d (error %v), page size %d; want all 6 items marked", changed, err, len(first.Items))
	}
	if got := f.unreadTitles(t); got != "" {
		t.Errorf("unread afterwards = %q, want none", got)
	}
}

func TestMarkReadLeavesStarsAndOrphansAlone(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.SetStarred(ctx, f.id("a1"), true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	if err := f.svc.SetStarred(ctx, f.id("b1"), true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	if err := f.svc.DeleteFeed(ctx, f.b.ID); err != nil { // b1 becomes an orphan
		t.Fatalf("DeleteFeed: %v", err)
	}

	changed, err := f.svc.MarkRead(ctx, feed.AllItems(), true)

	if err != nil || changed != 4 { // a1 a2 a3 c1; not the orphan
		t.Fatalf("changed = %d, error = %v; want 4", changed, err)
	}
	starred, _ := f.svc.StarredItems(ctx)
	byTitle := map[string]feed.Item{}
	for _, it := range starred {
		byTitle[it.Title] = it
	}
	if a1 := byTitle["a1"]; !a1.Read || a1.StarredAt.IsZero() {
		t.Errorf("a1 = read %v, starred %v; want read and still starred", a1.Read, !a1.StarredAt.IsZero())
	}
	if b1 := byTitle["b1"]; b1.Read || b1.StarredAt.IsZero() {
		t.Errorf("orphan b1 = read %v, starred %v; want untouched: unread and starred", b1.Read, !b1.StarredAt.IsZero())
	}
}

func TestMarkReadOfAnUnknownSelectionChangesNothing(t *testing.T) {
	f := newFixture(t)

	for name, sel := range map[string]feed.Selection{"folder": feed.InFolder(9999), "feed": feed.InFeed(9999)} {
		if _, err := f.svc.MarkRead(ctx, sel, true); !errors.Is(err, feed.ErrNotFound) {
			t.Errorf("unknown %s: error = %v, want feed.ErrNotFound", name, err)
		}
	}
	if got := f.unreadTitles(t); got != "c1,a3,b2,a2,b1,a1" {
		t.Errorf("unread = %q, want nothing changed", got)
	}
}

// starInOrder stars the given items one minute apart, the first title first.
func (f *fixture) starInOrder(t *testing.T, titles ...string) {
	t.Helper()
	for _, title := range titles {
		if err := f.svc.SetStarred(ctx, f.id(title), true); err != nil {
			t.Fatalf("SetStarred(%s): %v", title, err)
		}
		f.clock.now = f.clock.now.Add(time.Minute)
	}
}

func TestListStarredBySelectionNewestStarFirst(t *testing.T) {
	f := newFixture(t)
	f.starInOrder(t, "a2", "c1", "b1", "a1") // starred in this order: a1 is the latest star
	// A starred item whose feed is later deleted is an orphan: it only shows in "all".
	gone := f.addFeedAt(t, "/gone.xml", rss("Gone", "https://g.example/", "d", entry{"orphan", "https://g.example/1", jan10, ""}), feed.AddFeedRequest{FolderIDs: []int64{f.x}})
	items, _ := f.svc.Items(ctx, gone.ID)
	if err := f.svc.SetStarred(ctx, items[0].ID, true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	if err := f.svc.DeleteFeed(ctx, gone.ID); err != nil {
		t.Fatalf("DeleteFeed: %v", err)
	}
	cases := []struct {
		name string
		sel  feed.Selection
		want string
	}{
		{"all, orphans included, the earliest star last", feed.AllItems(), "orphan,a1,b1,c1,a2"},
		{"folder X", feed.InFolder(f.x), "a1,a2"},
		{"folder Y", feed.InFolder(f.y), "a1,b1,a2"},
		{"feed C", feed.InFeed(f.c.ID), "c1"},
		{"feed B", feed.InFeed(f.b.ID), "b1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.svc.ListStarred(ctx, feed.StarredQuery{Selection: tc.sel})

			if err != nil {
				t.Fatalf("ListStarred: %v", err)
			}
			if got := titlesOf(page); got != tc.want {
				t.Errorf("starred = %s, want %s", got, tc.want)
			}
			if page.Total != len(strings.Split(tc.want, ",")) {
				t.Errorf("Total = %d, want %d", page.Total, len(strings.Split(tc.want, ",")))
			}
		})
	}
	for name, sel := range map[string]feed.Selection{"folder": feed.InFolder(9999), "feed": feed.InFeed(9999)} {
		if _, err := f.svc.ListStarred(ctx, feed.StarredQuery{Selection: sel}); !errors.Is(err, feed.ErrNotFound) {
			t.Errorf("unknown %s: error = %v, want feed.ErrNotFound", name, err)
		}
	}
}

func TestListStarredIgnoresTheReadStateAndTheOrderSetting(t *testing.T) {
	f := newFixture(t)
	f.starInOrder(t, "a2", "c1", "b1")
	f.readSome(t) // c1 is read
	st, _ := f.store.Settings(ctx)
	st.Filter, st.Order = "unread", "olderFirst"
	if err := f.store.SaveSettings(ctx, st); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	page, err := f.svc.ListStarred(ctx, feed.StarredQuery{})

	if err != nil {
		t.Fatalf("ListStarred: %v", err)
	}
	if got := titlesOf(page); got != "b1,c1,a2" { // read items included; newest star first whatever the order setting
		t.Errorf("starred = %s, want b1,c1,a2", got)
	}
}

func TestListStarredPagination(t *testing.T) {
	f := newFixture(t)
	f.starInOrder(t, "a1", "a2", "a3", "b1", "b2") // newest star first: b2 b1 a3 a2 a1
	cases := []struct {
		perPage, page int
		want          string
		wantPage      int
	}{
		{2, 1, "b2,b1", 1}, {2, 2, "a3,a2", 2}, {2, 3, "a1", 3}, {2, 0, "b2,b1", 1}, {2, 9, "a1", 3},
	}
	for _, tc := range cases {
		page, err := f.svc.ListStarred(ctx, feed.StarredQuery{PerPage: tc.perPage, Page: tc.page})

		if err != nil {
			t.Fatalf("ListStarred: %v", err)
		}
		if titlesOf(page) != tc.want || page.Page != tc.wantPage || page.PageCount != 3 || page.Total != 5 {
			t.Errorf("per page %d, page %d: %s (page %d of %d, total %d); want %s (page %d of 3, total 5)",
				tc.perPage, tc.page, titlesOf(page), page.Page, page.PageCount, page.Total, tc.want, tc.wantPage)
		}
	}
	empty, err := f.svc.ListStarred(ctx, feed.StarredQuery{Selection: feed.InFeed(f.c.ID), Page: 3})
	if err != nil || len(empty.Items) != 0 || empty.Page != 1 || empty.PageCount != 1 || empty.Total != 0 {
		t.Errorf("empty = %+v, error %v; want an empty page 1 of 1", empty, err)
	}
	if _, err := f.svc.ListStarred(ctx, feed.StarredQuery{PerPage: -1}); !errors.Is(err, feed.ErrInvalidQuery) {
		t.Errorf("negative page size: error = %v, want feed.ErrInvalidQuery", err)
	}
}

func TestStarredItemsSurviveRetentionAndFeedDeletion(t *testing.T) {
	f := newFixture(t)
	f.starInOrder(t, "a1", "b1")
	f.readSome(t) // a1 is read and starred
	// The feed shows only a new item: a1 (read, absent) would be deleted if it were not starred.
	f.server.set(rss("Feed A", "https://a.example/", "d", entry{"a4", "https://s.example/a4", jan10, ""}))
	if _, err := f.svc.UpdateFeed(ctx, f.a.ID, feed.UpdateOptions{}); err != nil {
		t.Fatalf("UpdateFeed: %v", err)
	}
	if err := f.svc.DeleteFeed(ctx, f.b.ID); err != nil {
		t.Fatalf("DeleteFeed: %v", err)
	}

	page, err := f.svc.ListStarred(ctx, feed.StarredQuery{})

	if err != nil {
		t.Fatalf("ListStarred: %v", err)
	}
	if got := titlesOf(page); got != "b1,a1" {
		t.Errorf("starred = %s, want b1,a1 (kept after retention and after their feed's deletion)", got)
	}
	if page.Items[0].FeedTitle != "Feed B" || page.Items[0].FeedID != 0 {
		t.Errorf("orphan = feed %q (id %d), want the former feed title and no feed", page.Items[0].FeedTitle, page.Items[0].FeedID)
	}
}
