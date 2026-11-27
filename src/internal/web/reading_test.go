// SPDX-License-Identifier: GPL-3.0-or-later

package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/store"
	"github.com/mwolff44/mw-feed/internal/web"
)

var ctx = context.Background()

// entry describes one RSS item of a test feed.
type entry struct{ title, link, date, author, content, guid string }

// rss builds an RSS 2.0 document.
func rss(title string, entries ...entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0"?><rss version="2.0"><channel><title>%s</title><link>https://site.example/</link><description>About %s</description>`, title, title)
	for _, e := range entries {
		fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><pubDate>%s</pubDate>`, e.title, e.link, e.date)
		if e.author != "" {
			fmt.Fprintf(&b, `<author>nobody@example.org (%s)</author>`, e.author)
		}
		if e.guid != "" {
			fmt.Fprintf(&b, `<guid isPermaLink="true">%s</guid>`, e.guid)
		}
		content := e.content
		if content == "" {
			content = "About " + e.title
		}
		fmt.Fprintf(&b, `<description><![CDATA[%s]]></description></item>`, content)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

// subscribe adds a feed served by a fake server, and returns it.
func (a *app) subscribe(t *testing.T, xml string, folders ...string) feed.Feed {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(xml))
	}))
	t.Cleanup(srv.Close)
	req := feed.AddFeedRequest{URL: srv.URL}
	if len(folders) > 0 {
		req.NewFolder = folders[0]
	}
	f, err := a.feeds.AddFeed(ctx, req)
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	return f
}

func utc(t *testing.T) {
	t.Helper()
	previous := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previous })
}

func TestHomeListsUnreadItemsNewestFirstAndHidesReadOnes(t *testing.T) {
	utc(t)
	a := newApp(t, web.Options{})
	a.install(t)
	a.subscribe(t, rss("Go Blog",
		entry{title: "One", link: "https://site.example/1", date: "Mon, 05 Jan 2026 10:00:00 GMT"},
		entry{title: "Two", link: "https://site.example/2", date: "Tue, 06 Jan 2026 10:00:00 GMT"},
		entry{title: "Three", link: "https://site.example/3", date: "Wed, 07 Jan 2026 10:00:00 GMT"},
	))
	list, err := a.feeds.ListItems(ctx, feed.ItemQuery{Filter: feed.FilterAll})
	if err != nil || len(list.Items) != 3 {
		t.Fatalf("ListItems = %+v, %v", list, err)
	}
	if err := a.feeds.SetRead(ctx, list.Items[1].ID, true); err != nil { // "Two"
		t.Fatalf("SetRead: %v", err)
	}

	resp := a.get(t, "/")
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", resp.StatusCode)
	}
	three, one := strings.Index(page, "Three"), strings.Index(page, "One")
	if three < 0 || one < 0 || three > one {
		t.Errorf("want Three before One in:\n%s", page)
	}
	if strings.Contains(page, "Two") {
		t.Error("a read item is shown with the unread filter")
	}
	for _, want := range []string{"2026-01-07 10:00", "Go Blog", "About Three"} {
		if !strings.Contains(page, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
}

// seeded installs the app and subscribes to a feed of two items, "Older" (id
// first) and "Newer".
func seeded(t *testing.T) (*app, feed.ItemPage) {
	t.Helper()
	utc(t)
	a := newApp(t, web.Options{})
	a.install(t)
	a.subscribe(t, rss("Go Blog",
		entry{title: "Older", link: "https://site.example/older", date: "Mon, 05 Jan 2026 10:00:00 GMT", author: "Ann", content: "<p>Body of older</p>"},
		entry{title: "Newer", link: "https://site.example/newer", date: "Tue, 06 Jan 2026 10:00:00 GMT", author: "Bob", content: "<p>Body of newer</p>"},
	))
	list, err := a.feeds.ListItems(ctx, feed.ItemQuery{Filter: feed.FilterAll})
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("ListItems = %+v, %v", list, err)
	}
	return a, list
}

func TestListViewShowsOneDetailsPerItemAndOpensTheRequestedOne(t *testing.T) {
	a, list := seeded(t)
	newer, older := list.Items[0], list.Items[1]

	closed := body(t, a.get(t, "/"))
	if got := strings.Count(closed, "<details"); got != 2 {
		t.Fatalf("closed page has %d <details>, want 2:\n%s", got, closed)
	}
	for _, want := range []string{fmt.Sprintf(`<details id="item-%d" class="item unread">`, newer.ID), "<summary>"} {
		if !strings.Contains(closed, want) {
			t.Errorf("closed page does not contain %q", want)
		}
	}
	if strings.Contains(closed, ` open>`) {
		t.Error("nothing should be open without ?open")
	}

	open := body(t, a.get(t, fmt.Sprintf("/?open=%d", older.ID)))

	if !strings.Contains(open, fmt.Sprintf(`<details id="item-%d" class="item unread" open>`, older.ID)) {
		t.Errorf("?open=%d did not open that item:\n%s", older.ID, open)
	}
	if strings.Contains(open, fmt.Sprintf(`<details id="item-%d" class="item unread" open>`, newer.ID)) {
		t.Error("another item was opened too")
	}
	for _, want := range []string{
		`<a class="item-link" href="https://site.example/older">Older</a>`,
		"<p>Body of older</p>", "Ann", "2026-01-05 10:00",
	} {
		if !strings.Contains(open, want) {
			t.Errorf("opened item does not contain %q", want)
		}
	}
}

// token returns the CSRF token of the logged-in session.
func (a *app) token(t *testing.T) string {
	t.Helper()
	return csrfToken(t, body(t, a.get(t, "/")))
}

func (a *app) act(t *testing.T, path string, fields url.Values) *http.Response {
	t.Helper()
	fields.Set("csrf_token", a.token(t))
	return a.post(t, path, fields)
}

func TestItemActionsChangeTheItemAndComeBackToThePage(t *testing.T) {
	a, list := seeded(t)
	newer := list.Items[0]
	state := func() (read, starred bool) {
		it, err := a.feeds.Item(ctx, newer.ID)
		if err != nil {
			t.Fatalf("Item: %v", err)
		}
		return it.Read, !it.StarredAt.IsZero()
	}
	back := fmt.Sprintf("/?page=1&open=%d", newer.ID)

	steps := []struct {
		action           string
		wantRead, wantSt bool
	}{
		{"read", true, false},
		{"star", true, true},
		{"unread", false, true},
		{"unstar", false, false},
	}
	for _, s := range steps {
		resp := a.act(t, fmt.Sprintf("/item/%d/%s", newer.ID, s.action), url.Values{"return": {back}})

		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != back {
			t.Fatalf("%s = %d → %q, want 303 → %q", s.action, resp.StatusCode, resp.Header.Get("Location"), back)
		}
		if read, starred := state(); read != s.wantRead || starred != s.wantSt {
			t.Fatalf("after %s: read=%v starred=%v, want read=%v starred=%v", s.action, read, starred, s.wantRead, s.wantSt)
		}
	}
}

func TestItemActionsAreRefusedWithoutCSRFOrForUnknownItems(t *testing.T) {
	a, list := seeded(t)
	id := list.Items[0].ID

	if resp := a.post(t, fmt.Sprintf("/item/%d/read", id), url.Values{}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST without token = %d, want 403", resp.StatusCode)
	}
	if it, _ := a.feeds.Item(ctx, id); it.Read {
		t.Error("the item was marked read without a CSRF token")
	}
	for _, path := range []string{"/item/999999/read", "/item/abc/read", "/item/0/star"} {
		if resp := a.act(t, path, url.Values{}); resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, resp.StatusCode)
		}
	}
	if resp := a.get(t, fmt.Sprintf("/item/%d/read", id)); resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSeeOther {
		t.Errorf("GET on an action = %d, want it refused", resp.StatusCode)
	}
}

func TestReturnTargetMustBeALocalPath(t *testing.T) {
	a, list := seeded(t)
	path := fmt.Sprintf("/item/%d/read", list.Items[0].ID)
	cases := map[string]string{
		"/feed/3?page=2#x":       "/feed/3?page=2#x",
		"":                       "/",
		"https://evil.example/":  "/",
		"//evil.example/":        "/",
		`/\evil.example`:         "/",
		"javascript:alert(1)":    "/",
		"evil":                   "/",
		"/ok\r\nSet-Cookie: x=1": "/",
	}
	for given, want := range cases {
		resp := a.act(t, path, url.Values{"return": {given}})

		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != want {
			t.Errorf("return=%q → %d %q, want 303 %q", given, resp.StatusCode, resp.Header.Get("Location"), want)
		}
	}
}

func TestItemsOfTheListCarryTheirActionForms(t *testing.T) {
	a, list := seeded(t)
	newer := list.Items[0]
	if err := a.feeds.SetStarred(ctx, newer.ID, true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	if err := a.feeds.SetRead(ctx, list.Items[1].ID, true); err != nil {
		t.Fatalf("SetRead: %v", err)
	}

	settings, err := a.store.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	settings.Filter = feed.FilterAll
	if err := a.store.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	page := body(t, a.get(t, "/?open=1&page=1"))

	for _, want := range []string{
		fmt.Sprintf(`action="/item/%d/read"`, newer.ID),
		fmt.Sprintf(`action="/item/%d/unstar"`, newer.ID),
		fmt.Sprintf(`action="/item/%d/unread"`, list.Items[1].ID),
		fmt.Sprintf(`action="/item/%d/star"`, list.Items[1].ID),
		`<input type="hidden" name="return" value="/?open=1&amp;page=1">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
}

// twoFeeds subscribes to "Alpha" (two items, in the folder "Tech") and "Beta"
// (one item, in no folder).
func twoFeeds(t *testing.T) (a *app, alpha, beta feed.Feed, tech int64) {
	t.Helper()
	utc(t)
	a = newApp(t, web.Options{})
	a.install(t)
	alpha = a.subscribe(t, rss("Alpha",
		entry{title: "Alpha one", link: "https://alpha.example/1", date: "Mon, 05 Jan 2026 10:00:00 GMT"},
		entry{title: "Alpha two", link: "https://alpha.example/2", date: "Tue, 06 Jan 2026 10:00:00 GMT"},
	), "Tech")
	beta = a.subscribe(t, rss("Beta",
		entry{title: "Beta one", link: "https://beta.example/1", date: "Wed, 07 Jan 2026 10:00:00 GMT"},
	))
	folders, err := a.feeds.Folders(ctx)
	if err != nil || len(folders) != 1 {
		t.Fatalf("Folders = %+v, %v", folders, err)
	}
	return a, alpha, beta, folders[0].ID
}

func TestSelectionsListOnlyTheirItemsAndTitleTheirPage(t *testing.T) {
	a, alpha, beta, tech := twoFeeds(t)

	cases := []struct {
		path        string
		heading     string
		has, hasNot []string
	}{
		{"/", "All items", []string{"Alpha one", "Alpha two", "Beta one"}, nil},
		{fmt.Sprintf("/feed/%d", alpha.ID), "Alpha", []string{"Alpha one", "Alpha two"}, []string{"Beta one"}},
		{fmt.Sprintf("/feed/%d", beta.ID), "Beta", []string{"Beta one"}, []string{"Alpha one"}},
		{fmt.Sprintf("/folder/%d", tech), "Tech", []string{"Alpha one", "Alpha two"}, []string{"Beta one"}},
	}
	for _, tc := range cases {
		resp := a.get(t, tc.path)
		page := body(t, resp)

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", tc.path, resp.StatusCode)
		}
		if !strings.Contains(page, "<h1>"+tc.heading) {
			t.Errorf("GET %s: heading is not %q in:\n%s", tc.path, tc.heading, page)
		}
		if !strings.Contains(page, "<title>"+tc.heading+", mw-feed</title>") {
			t.Errorf("GET %s: <title> is not %q", tc.path, tc.heading+", mw-feed")
		}
		for _, want := range tc.has {
			if !strings.Contains(page, want) {
				t.Errorf("GET %s does not list %q", tc.path, want)
			}
		}
		for _, not := range tc.hasNot {
			if strings.Contains(page, not) {
				t.Errorf("GET %s lists %q", tc.path, not)
			}
		}
	}
}

func TestHeadingCountsTheListedItemsAccordingToTheFilter(t *testing.T) {
	a, alpha, _, _ := twoFeeds(t)
	if err := a.feeds.SetRead(ctx, mustFirstItem(t, a, alpha.ID).ID, true); err != nil {
		t.Fatalf("SetRead: %v", err)
	}
	path := fmt.Sprintf("/feed/%d", alpha.ID)

	if page := body(t, a.get(t, path)); !strings.Contains(page, `<h1>Alpha <small>1 unread</small></h1>`) {
		t.Errorf("unread filter: heading is wrong in:\n%s", page)
	}
	settings, err := a.store.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	settings.Filter = feed.FilterAll
	if err := a.store.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if page := body(t, a.get(t, path)); !strings.Contains(page, `<h1>Alpha <small>2 items</small></h1>`) {
		t.Errorf("all filter: heading is wrong in:\n%s", page)
	}
}

func mustFirstItem(t *testing.T, a *app, feedID int64) feed.Item {
	t.Helper()
	list, err := a.feeds.ListItems(ctx, feed.ItemQuery{Selection: feed.InFeed(feedID), Filter: feed.FilterAll})
	if err != nil || len(list.Items) == 0 {
		t.Fatalf("ListItems = %+v, %v", list, err)
	}
	return list.Items[0]
}

func TestUnknownSelectionsAreNotFound(t *testing.T) {
	a, _, _, _ := twoFeeds(t)
	for _, path := range []string{"/feed/999", "/feed/abc", "/feed/0", "/feed/-1", "/folder/999", "/folder/abc", "/starred/feed/999", "/starred/folder/999"} {
		if resp := a.get(t, path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestStarredPagesListStarredItemsNewestStarFirstWithoutReadButtons(t *testing.T) {
	a, alpha, beta, tech := twoFeeds(t)
	byTitle := map[string]int64{}
	list, err := a.feeds.ListItems(ctx, feed.ItemQuery{Filter: feed.FilterAll})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, it := range list.Items {
		byTitle[it.Title] = it.ID
	}
	// Star "Alpha one" first, then "Beta one", one minute apart.
	for _, title := range []string{"Alpha one", "Beta one"} {
		if err := a.feeds.SetStarred(ctx, byTitle[title], true); err != nil {
			t.Fatalf("SetStarred: %v", err)
		}
		a.clock.now = a.clock.now.Add(time.Minute)
	}
	// Read items stay listed among the starred ones.
	if err := a.feeds.SetRead(ctx, byTitle["Beta one"], true); err != nil {
		t.Fatalf("SetRead: %v", err)
	}

	resp := a.get(t, "/starred")
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /starred = %d, want 200", resp.StatusCode)
	}
	beta1, alpha1 := strings.Index(page, "Beta one"), strings.Index(page, "Alpha one")
	if beta1 < 0 || alpha1 < 0 || beta1 > alpha1 {
		t.Errorf("want the most recently starred first (Beta one, Alpha one) in:\n%s", page)
	}
	if strings.Contains(page, "Alpha two") {
		t.Error("an item that is not starred is listed")
	}
	for _, want := range []string{"<h1>Starred <small>2 items</small></h1>", "<title>Starred, mw-feed</title>",
		fmt.Sprintf(`action="/item/%d/unstar"`, byTitle["Beta one"])} {
		if !strings.Contains(page, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
	if strings.Contains(page, "/read\"") || strings.Contains(page, "/unread\"") {
		t.Error("the starred page has read/unread buttons")
	}

	for path, want := range map[string]string{
		fmt.Sprintf("/starred/feed/%d", alpha.ID): "Alpha one",
		fmt.Sprintf("/starred/feed/%d", beta.ID):  "Beta one",
		fmt.Sprintf("/starred/folder/%d", tech):   "Alpha one",
	} {
		page := body(t, a.get(t, path))
		if !strings.Contains(page, want) || !strings.Contains(page, "<h1>") {
			t.Errorf("GET %s does not list %q", path, want)
		}
	}
	if page := body(t, a.get(t, fmt.Sprintf("/starred/folder/%d", tech))); strings.Contains(page, "Beta one") {
		t.Error("the starred page of a folder lists an item of another feed")
	}
}

func TestStarredItemOfADeletedFeedStaysListedUnderAll(t *testing.T) {
	a, alpha, _, _ := twoFeeds(t)
	it := mustFirstItem(t, a, alpha.ID)
	if err := a.feeds.SetStarred(ctx, it.ID, true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	if err := a.feeds.DeleteFeed(ctx, alpha.ID); err != nil {
		t.Fatalf("DeleteFeed: %v", err)
	}

	page := body(t, a.get(t, "/starred"))

	if !strings.Contains(page, it.Title) || !strings.Contains(page, "Alpha") {
		t.Errorf("the starred item of a deleted feed is not listed with its feed title:\n%s", page)
	}
}

func (a *app) setSettings(t *testing.T, change func(*store.Settings)) {
	t.Helper()
	settings, err := a.store.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	change(&settings)
	if err := a.store.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
}

func fiveItems(t *testing.T) (*app, feed.Feed) {
	t.Helper()
	utc(t)
	a := newApp(t, web.Options{})
	a.install(t)
	var entries []entry
	for i := 1; i <= 5; i++ {
		entries = append(entries, entry{
			title: fmt.Sprintf("Item %d", i), link: fmt.Sprintf("https://site.example/%d", i),
			date: fmt.Sprintf("Mon, 0%d Jan 2026 10:00:00 GMT", i),
		})
	}
	f := a.subscribe(t, rss("Many", entries...))
	a.setSettings(t, func(s *store.Settings) { s.ByPage = 2 })
	return a, f
}

func TestPaginationShowsPagesAndClampsInvalidOnes(t *testing.T) {
	a, f := fiveItems(t)

	// Newest first, 2 per page: page 1 = items 5, 4; page 2 = 3, 2; page 3 = 1.
	cases := []struct {
		path       string
		has, hasNo []string
		pager      string
	}{
		{"/", []string{"Item 5", "Item 4"}, []string{"Item 3"}, `<span class="disabled">Previous page</span> <span class="current">1 / 3</span> <a href="/?page=2">Next page</a>`},
		{"/?page=2", []string{"Item 3", "Item 2"}, []string{"Item 4", "Item 1"}, `<a href="/?page=1">Previous page</a> <span class="current">2 / 3</span> <a href="/?page=3">Next page</a>`},
		{"/?page=3", []string{"Item 1"}, []string{"Item 2"}, `<a href="/?page=2">Previous page</a> <span class="current">3 / 3</span> <span class="disabled">Next page</span>`},
		{"/?page=99", []string{"Item 1"}, []string{"Item 2"}, `<span class="current">3 / 3</span>`},
		{"/?page=abc", []string{"Item 5"}, []string{"Item 3"}, `<span class="current">1 / 3</span>`},
		{"/?page=0", []string{"Item 5"}, nil, `<span class="current">1 / 3</span>`},
		{fmt.Sprintf("/feed/%d?page=2", f.ID), []string{"Item 3"}, []string{"Item 5"}, fmt.Sprintf(`<a href="/feed/%d?page=3">Next page</a>`, f.ID)},
	}
	for _, tc := range cases {
		page := body(t, a.get(t, tc.path))
		for _, want := range append(tc.has, tc.pager) {
			if !strings.Contains(page, want) {
				t.Errorf("GET %s does not contain %q", tc.path, want)
			}
		}
		for _, not := range tc.hasNo {
			if strings.Contains(page, not) {
				t.Errorf("GET %s contains %q", tc.path, not)
			}
		}
	}

	a.setSettings(t, func(s *store.Settings) { s.ByPage = 10 })
	if page := body(t, a.get(t, "/")); strings.Contains(page, `class="pager"`) {
		t.Error("a single page must have no pager")
	}
}

func TestExpandedViewShowsEveryItemInFullWithoutDetails(t *testing.T) {
	a, list := seeded(t)
	a.setSettings(t, func(s *store.Settings) { s.View = "expanded" })

	page := body(t, a.get(t, "/"))

	if strings.Contains(page, "<details") {
		t.Error("the expanded view must not use <details>")
	}
	for _, want := range []string{
		fmt.Sprintf(`<article id="item-%d" class="item unread">`, list.Items[0].ID),
		"<p>Body of newer</p>", "<p>Body of older</p>",
		fmt.Sprintf(`action="/item/%d/read"`, list.Items[1].ID),
	} {
		if !strings.Contains(page, want) {
			t.Errorf("expanded page does not contain %q", want)
		}
	}
}

func TestEmptyListsSayWhy(t *testing.T) {
	utc(t)
	a := newApp(t, web.Options{})
	a.install(t)

	cases := []struct {
		path   string
		filter string
		want   string
	}{
		{"/", feed.FilterUnread, "No unread items."},
		{"/", feed.FilterAll, "No items."},
		{"/starred", feed.FilterUnread, "No starred items."},
	}
	for _, tc := range cases {
		a.setSettings(t, func(s *store.Settings) { s.Filter = tc.filter })
		page := body(t, a.get(t, tc.path))
		if !strings.Contains(page, `<p class="empty">`+tc.want+`</p>`) {
			t.Errorf("GET %s with filter %s: no %q in:\n%s", tc.path, tc.filter, tc.want, page)
		}
	}
}

func TestFeedListShowsAllFeedsFoldersAndUnreadCounters(t *testing.T) {
	a, alpha, beta, tech := twoFeeds(t)
	if err := a.feeds.SetRead(ctx, mustFirstItem(t, a, alpha.ID).ID, true); err != nil {
		t.Fatalf("SetRead: %v", err)
	}

	page := body(t, a.get(t, "/"))

	for _, want := range []string{
		`<nav class="feeds"`,
		`<a href="/" aria-current="page">All items</a> <span class="count">2</span>`,
		fmt.Sprintf(`<a href="/feed/%d" title="About Beta">Beta</a> <span class="count">1</span>`, beta.ID),
		fmt.Sprintf(`<details class="folder" open><summary><a href="/folder/%d">Tech</a> <span class="count">1</span></summary>`, tech),
		fmt.Sprintf(`<a href="/feed/%d" title="About Alpha">Alpha</a> <span class="count">1</span>`, alpha.ID),
	} {
		if !strings.Contains(page, want) {
			t.Errorf("feed list does not contain %q in:\n%s", want, page)
		}
	}
	// Feeds without a folder come before the folders.
	if strings.Index(page, "Beta</a>") > strings.Index(page, `class="folder"`) {
		t.Error("feeds without a folder must be listed before the folders")
	}
}

func TestFeedListMarksTheCurrentSelectionAndTheFeedErrors(t *testing.T) {
	a, alpha, beta, tech := twoFeeds(t)
	err := a.store.InTx(ctx, func(tx *store.Tx) error {
		return tx.RecordFeedUpdate(ctx, beta.ID, store.FeedUpdate{At: a.clock.now, Error: "download failed: timeout"})
	})
	if err != nil {
		t.Fatalf("RecordFeedUpdate: %v", err)
	}

	for path, want := range map[string]string{
		fmt.Sprintf("/feed/%d", alpha.ID): fmt.Sprintf(`<a href="/feed/%d" title="About Alpha" aria-current="page">`, alpha.ID),
		fmt.Sprintf("/folder/%d", tech):   fmt.Sprintf(`<a href="/folder/%d" aria-current="page">`, tech),
		fmt.Sprintf("/feed/%d", beta.ID):  `class="error" title="download failed: timeout"`,
	} {
		if page := body(t, a.get(t, path)); !strings.Contains(page, want) {
			t.Errorf("GET %s does not contain %q", path, want)
		}
	}
}

func TestFeedListFollowsTheFoldersCollapsedStateAndTheListFeedsSetting(t *testing.T) {
	a, _, _, tech := twoFeeds(t)
	if err := a.feeds.ToggleFolder(ctx, tech); err != nil {
		t.Fatalf("ToggleFolder: %v", err)
	}

	page := body(t, a.get(t, "/"))
	if !strings.Contains(page, `<details class="folder"><summary>`) {
		t.Errorf("a collapsed folder must not be open:\n%s", page)
	}

	a.setSettings(t, func(s *store.Settings) { s.ListFeeds = false })
	if page := body(t, a.get(t, "/")); strings.Contains(page, `<nav class="feeds"`) {
		t.Error("the feed list is shown although listFeeds is hide")
	}
}

func TestStarredPagesListFeedsWithStarredLinksAndNoCounters(t *testing.T) {
	a, alpha, _, tech := twoFeeds(t)

	page := body(t, a.get(t, "/starred"))

	for _, want := range []string{
		`<a href="/starred" aria-current="page">All items</a>`,
		fmt.Sprintf(`<a href="/starred/feed/%d" title="About Alpha">Alpha</a>`, alpha.ID),
		fmt.Sprintf(`<a href="/starred/folder/%d">Tech</a>`, tech),
	} {
		if !strings.Contains(page, want) {
			t.Errorf("starred feed list does not contain %q in:\n%s", want, page)
		}
	}
	if strings.Contains(page, `class="count"`) {
		t.Error("the starred pages must have no counters")
	}
}

func TestAutohideHidesFeedsAndFoldersWithoutUnreadItemsExceptTheCurrentOnes(t *testing.T) {
	a, alpha, beta, tech := twoFeeds(t)
	a.setSettings(t, func(s *store.Settings) { s.Autohide = true })
	list, err := a.feeds.ListItems(ctx, feed.ItemQuery{Selection: feed.InFeed(alpha.ID), Filter: feed.FilterAll})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, it := range list.Items {
		if err := a.feeds.SetRead(ctx, it.ID, true); err != nil {
			t.Fatalf("SetRead: %v", err)
		}
	}

	page := body(t, a.get(t, "/"))
	if strings.Contains(page, ">Alpha</a>") || strings.Contains(page, ">Tech</a>") {
		t.Errorf("a feed and a folder without unread items are shown:\n%s", page)
	}
	if !strings.Contains(page, ">Beta</a>") || !strings.Contains(page, `>All items</a>`) {
		t.Error("Beta and All items must stay")
	}

	if page := body(t, a.get(t, fmt.Sprintf("/feed/%d", alpha.ID))); !strings.Contains(page, ">Alpha</a>") || !strings.Contains(page, ">Tech</a>") {
		t.Error("the current feed and the folder that contains it must stay")
	}
	if page := body(t, a.get(t, fmt.Sprintf("/folder/%d", tech))); !strings.Contains(page, ">Tech</a>") {
		t.Error("the current folder must stay")
	}
	if page := body(t, a.get(t, fmt.Sprintf("/feed/%d", beta.ID))); strings.Contains(page, ">Alpha</a>") {
		t.Error("Alpha must stay hidden on another feed's page")
	}
	if page := body(t, a.get(t, "/starred")); !strings.Contains(page, ">Alpha</a>") || !strings.Contains(page, ">Tech</a>") {
		t.Error("autohide must hide nothing on the starred pages")
	}
}

func TestDisplayFormChangesTheSettingsAndComesBack(t *testing.T) {
	a, _ := seeded(t)

	resp := a.act(t, "/display", url.Values{"view": {"expanded"}, "order": {"olderFirst"}, "listFeeds": {"hide"}, "filter": {"all"}, "return": {"/?page=2"}})

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/?page=2" {
		t.Fatalf("POST /display = %d → %q, want 303 → /?page=2", resp.StatusCode, resp.Header.Get("Location"))
	}
	st, err := a.store.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if st.View != "expanded" || st.Order != "olderFirst" || st.ListFeeds || st.Filter != "all" {
		t.Errorf("settings = view %q order %q listFeeds %v filter %q", st.View, st.Order, st.ListFeeds, st.Filter)
	}

	// Only the given fields change.
	a.act(t, "/display", url.Values{"view": {"list"}})
	if st, _ := a.store.Settings(ctx); st.View != "list" || st.Order != "olderFirst" {
		t.Errorf("after changing only the view: view %q order %q", st.View, st.Order)
	}
}

func TestDisplayFormRefusesInvalidValuesAndMissingCSRF(t *testing.T) {
	a, _ := seeded(t)

	resp := a.act(t, "/display", url.Values{"view": {"sideways"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid view = %d, want 400", resp.StatusCode)
	}
	if resp := a.post(t, "/display", url.Values{"view": {"expanded"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("without CSRF = %d, want 403", resp.StatusCode)
	}
	if st, _ := a.store.Settings(ctx); st.View != "list" {
		t.Errorf("a refused request changed the view to %q", st.View)
	}
}

func TestMenuOfReadingPagesOffersTheOppositeChoicesForTheSelection(t *testing.T) {
	a, alpha, _, tech := twoFeeds(t)

	page := body(t, a.get(t, fmt.Sprintf("/feed/%d", alpha.ID)))
	for _, want := range []string{
		`<form method="post" action="/display">`,
		`<button type="submit" name="view" value="expanded">View as expanded</button>`,
		`<button type="submit" name="listFeeds" value="hide">Hide feeds list</button>`,
		`<button type="submit" name="filter" value="all">Show all items</button>`,
		`<button type="submit" name="order" value="olderFirst">Show older first</button>`,
		`<form method="post" action="/update">`,
		`<input type="hidden" name="kind" value="feed">`,
		fmt.Sprintf(`<input type="hidden" name="id" value="%d">`, alpha.ID),
		`<button type="submit">Update feed</button>`,
		`<button type="submit" name="read" value="1">Mark feed as read</button>`,
		`<button type="submit" name="read" value="0">Mark feed as unread</button>`,
		`<a href="/starred/feed/`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("feed page menu does not contain %q", want)
		}
	}

	a.setSettings(t, func(s *store.Settings) {
		s.View, s.ListFeeds, s.Filter, s.Order = "expanded", false, "all", "olderFirst"
	})
	page = body(t, a.get(t, fmt.Sprintf("/folder/%d", tech)))
	for _, want := range []string{
		`name="view" value="list">View as list`, `name="listFeeds" value="show">Show feeds list`,
		`name="filter" value="unread">Show unread items`, `name="order" value="newerFirst">Show newer first`,
		`Update folder`, `Mark folder as read`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("folder page menu does not contain %q", want)
		}
	}
	if page := body(t, a.get(t, "/")); !strings.Contains(page, "Mark all as read") || !strings.Contains(page, `<a href="/starred">`) {
		t.Error("the home menu has no mark-all or starred link")
	}
}

func TestStarredPagesOnlyOfferTheDisplayChoicesThatApplyToThem(t *testing.T) {
	a, _ := seeded(t)

	page := body(t, a.get(t, "/starred"))

	for _, want := range []string{`name="view"`, `name="listFeeds"`, `<a href="/">Items</a>`} {
		if !strings.Contains(page, want) {
			t.Errorf("starred menu does not contain %q", want)
		}
	}
	for _, not := range []string{`name="filter"`, `name="order"`, `action="/update"`, `action="/mark"`} {
		if strings.Contains(page, not) {
			t.Errorf("starred menu contains %q, which does not apply to starred items", not)
		}
	}
}

func TestMarkFormMarksTheWholeSelectionAndSaysHowMany(t *testing.T) {
	a, alpha, beta, tech := twoFeeds(t)
	a.setSettings(t, func(s *store.Settings) { s.ByPage = 1 }) // a page is not the selection
	unread := func(sel feed.Selection) int {
		list, err := a.feeds.ListItems(ctx, feed.ItemQuery{Selection: sel, Page: 1})
		if err != nil {
			t.Fatalf("ListItems: %v", err)
		}
		return list.Total
	}
	star := mustFirstItem(t, a, alpha.ID)
	if err := a.feeds.SetStarred(ctx, star.ID, true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}

	resp := a.act(t, "/mark", url.Values{"kind": {"folder"}, "id": {fmt.Sprint(tech)}, "read": {"1"}, "return": {"/"}})

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("POST /mark = %d → %q, want 303 → /", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := unread(feed.InFolder(tech)); got != 0 {
		t.Errorf("%d unread items left in the folder, want 0", got)
	}
	if got := unread(feed.InFeed(beta.ID)); got != 1 {
		t.Errorf("another feed was marked: %d unread, want 1", got)
	}
	if it, _ := a.feeds.Item(ctx, star.ID); it.StarredAt.IsZero() {
		t.Error("marking read removed a star")
	}
	if page := body(t, a.get(t, "/")); !strings.Contains(page, `<p class="flash" role="status">2 items marked as read.</p>`) {
		t.Errorf("no flash with the count in:\n%s", page)
	}

	a.act(t, "/mark", url.Values{"kind": {"all"}, "read": {"0"}})
	if got := unread(feed.AllItems()); got != 3 {
		t.Errorf("after marking everything unread: %d unread, want 3", got)
	}
	if page := body(t, a.get(t, "/")); !strings.Contains(page, "2 items marked as unread.") {
		t.Error("no flash for the unread marking")
	}
	_ = alpha
}

func TestMarkAndUpdateFormsRefuseUnknownSelectionsAndBadInput(t *testing.T) {
	a, _, _, _ := twoFeeds(t)
	bad := []url.Values{
		{"kind": {"feed"}, "id": {"999"}, "read": {"1"}},
		{"kind": {"folder"}, "id": {"999"}, "read": {"1"}},
		{"kind": {"feed"}, "id": {"abc"}, "read": {"1"}},
		{"kind": {"feed"}, "read": {"1"}},
		{"kind": {"nothing"}, "read": {"1"}},
		{"kind": {"all"}, "read": {"maybe"}},
	}
	for _, form := range bad {
		resp := a.act(t, "/mark", form)
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST /mark %v = %d, want 404 or 400", form, resp.StatusCode)
		}
	}
	for _, form := range []url.Values{{"kind": {"feed"}, "id": {"999"}}, {"kind": {"nothing"}}} {
		resp := a.act(t, "/update", form)
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST /update %v = %d, want 404 or 400", form, resp.StatusCode)
		}
	}
	if resp := a.post(t, "/mark", url.Values{"kind": {"all"}, "read": {"1"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /mark without CSRF = %d, want 403", resp.StatusCode)
	}
	if resp := a.post(t, "/update", url.Values{"kind": {"all"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /update without CSRF = %d, want 403", resp.StatusCode)
	}
}

// liveFeed is a feed server whose document and status tests control.
type liveFeed struct {
	mu     sync.Mutex
	xml    string
	status int
	URL    string
}

func (l *liveFeed) set(xml string, status int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.xml, l.status = xml, status
}

func subscribeLive(t *testing.T, a *app, xml string) (*liveFeed, feed.Feed) {
	t.Helper()
	l := &liveFeed{xml: xml, status: http.StatusOK}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		l.mu.Lock()
		defer l.mu.Unlock()
		w.WriteHeader(l.status)
		_, _ = w.Write([]byte(l.xml))
	}))
	t.Cleanup(srv.Close)
	l.URL = srv.URL
	f, err := a.feeds.AddFeed(ctx, feed.AddFeedRequest{URL: srv.URL})
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	return l, f
}

func TestUpdateFormDownloadsTheSelectionAndReportsTheOutcome(t *testing.T) {
	utc(t)
	a := newApp(t, web.Options{})
	a.install(t)
	good, goodFeed := subscribeLive(t, a, rss("Good", entry{title: "G1", link: "https://g.example/1", date: "Mon, 05 Jan 2026 10:00:00 GMT"}))
	bad, badFeed := subscribeLive(t, a, rss("Bad", entry{title: "B1", link: "https://b.example/1", date: "Mon, 05 Jan 2026 10:00:00 GMT"}))
	good.set(rss("Good",
		entry{title: "G1", link: "https://g.example/1", date: "Mon, 05 Jan 2026 10:00:00 GMT"},
		entry{title: "G2", link: "https://g.example/2", date: "Tue, 06 Jan 2026 10:00:00 GMT"},
		entry{title: "G3", link: "https://g.example/3", date: "Wed, 07 Jan 2026 10:00:00 GMT"},
	), http.StatusOK)
	bad.set("", http.StatusInternalServerError)
	_ = badFeed

	resp := a.act(t, "/update", url.Values{"kind": {"all"}, "return": {"/"}})

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("POST /update = %d → %q, want 303 → /", resp.StatusCode, resp.Header.Get("Location"))
	}
	page := body(t, a.get(t, "/"))
	for _, want := range []string{"G3", "G2", `<p class="flash error" role="alert">1 feed updated, 2 new items, 1 feed in error.</p>`} {
		if !strings.Contains(page, want) {
			t.Errorf("page after the update does not contain %q in:\n%s", want, page)
		}
	}
	if !strings.Contains(page, `class="error"`) {
		t.Error("the failing feed is not marked in the feed list")
	}

	// Updating one feed of the selection only touches that one.
	good.set(rss("Good", entry{title: "G4", link: "https://g.example/4", date: "Thu, 08 Jan 2026 10:00:00 GMT"}), http.StatusOK)
	a.act(t, "/update", url.Values{"kind": {"feed"}, "id": {fmt.Sprint(goodFeed.ID)}})
	if page := body(t, a.get(t, "/")); !strings.Contains(page, `<p class="flash" role="status">1 feed updated, 1 new item.</p>`) {
		t.Errorf("no success flash for a single feed in:\n%s", page)
	}
}

func TestATemplateFailureGivesAnErrorPageNotATruncatedOne(t *testing.T) {
	a, _ := seeded(t)
	web.BreakTemplate(t, "item-actions")

	resp := a.get(t, "/")
	page := body(t, resp)

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("GET / with a broken template = %d, want 500", resp.StatusCode)
	}
	if !strings.Contains(page, "Internal error") || strings.Contains(page, "<details") {
		t.Errorf("the response is not the clean error page:\n%s", page)
	}
}

func itoa(n int64) string { return fmt.Sprint(n) }

func TestLinksOfAnItemFollowTheAnonymisationAndBlankSettings(t *testing.T) {
	utc(t)
	a := newApp(t, web.Options{})
	a.install(t)
	a.subscribe(t, rss("Linked", entry{
		title: "Post", link: "https://site.example/post", guid: "https://other.example/p/1", date: "Mon, 05 Jan 2026 10:00:00 GMT",
		content: `<p><a href="https://ext.example/x">x</a> <img src="https://img.example/i.png" alt="i"></p>`,
	}))

	plain := body(t, a.get(t, "/"))
	for _, want := range []string{
		`<a class="item-link" href="https://site.example/post">Post</a>`,
		`<a class="item-via" href="https://other.example/p/1">Source</a>`,
		`<a href="https://ext.example/x">x</a>`,
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("without settings, page does not contain %q in:\n%s", want, plain)
		}
	}

	a.setSettings(t, func(s *store.Settings) { s.Redirector, s.Blank = "https://anonym.example/?", true })
	page := body(t, a.get(t, "/"))
	for _, want := range []string{
		`<a class="item-link" href="https://anonym.example/?https://site.example/post" rel="noopener noreferrer" target="_blank">Post</a>`,
		`<a class="item-via" href="https://anonym.example/?https://other.example/p/1" rel="noopener noreferrer" target="_blank">Source</a>`,
		`<a href="https://anonym.example/?https://ext.example/x" rel="noopener noreferrer" target="_blank">x</a>`,
		`<img src="https://img.example/i.png"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("with a redirector, page does not contain %q in:\n%s", want, page)
		}
	}

	a.setSettings(t, func(s *store.Settings) { s.Redirector, s.Blank = "noreferrer", false })
	page = body(t, a.get(t, "/"))
	for _, want := range []string{
		`<a class="item-link" href="https://site.example/post" rel="noopener noreferrer">Post</a>`,
		`<a href="https://ext.example/x" rel="noopener noreferrer">x</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("with noreferrer, page does not contain %q in:\n%s", want, page)
		}
	}
	// The same on the starred page and the expanded view.
	a.setSettings(t, func(s *store.Settings) { s.View = "expanded" })
	if page := body(t, a.get(t, "/")); !strings.Contains(page, `<a href="https://ext.example/x" rel="noopener noreferrer">x</a>`) {
		t.Error("the expanded view does not rewrite the links of the content")
	}
}
