// SPDX-License-Identifier: GPL-3.0-or-later

package feed_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/fetch"
	"github.com/mwolff44/mw-feed/internal/store"
)

// feedServer is a fake feed server whose answer tests control.
type feedServer struct {
	*httptest.Server
	mu           sync.Mutex
	body         string
	status       int
	hits         int
	etag         string // when set, sent as ETag and honoured with a 304
	lastModified string
	// headers of the last request
	gotNoneMatch, gotModifiedSince string
}

func newFeedServer(t *testing.T, body string) *feedServer {
	t.Helper()
	fs := &feedServer{body: body, status: http.StatusOK}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		fs.hits++
		fs.gotNoneMatch, fs.gotModifiedSince = r.Header.Get("If-None-Match"), r.Header.Get("If-Modified-Since")
		if fs.etag != "" {
			w.Header().Set("ETag", fs.etag)
			if fs.gotNoneMatch == fs.etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		if fs.lastModified != "" {
			w.Header().Set("Last-Modified", fs.lastModified)
		}
		w.WriteHeader(fs.status)
		_, _ = w.Write([]byte(fs.body))
	}))
	t.Cleanup(fs.Close)
	return fs
}

func (fs *feedServer) set(body string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.body, fs.status = body, http.StatusOK
}

// entry describes one RSS item of a test feed.
type entry struct{ title, link, date, author string }

// rss builds an RSS 2.0 document.
func rss(title, site, description string, entries ...entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0"?><rss version="2.0"><channel><title>%s</title><link>%s</link><description>%s</description>`, title, site, description)
	for _, e := range entries {
		fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><pubDate>%s</pubDate>`, e.title, e.link, e.date)
		if e.author != "" {
			fmt.Fprintf(&b, `<author>nobody@example.org (%s)</author>`, e.author)
		}
		b.WriteString(`<description>About ` + e.title + `</description></item>`)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

// env is a feed service over a real SQLite store and the real downloader.
type env struct {
	svc    *feed.Service
	store  *store.Store
	clock  *fakeClock
	server *feedServer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := &fakeClock{now: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	fetcher := fetch.New(fetch.Options{AllowPrivate: true}) // the fake server is on loopback
	return &env{
		svc:    feed.NewService(st, fetcher, clock),
		store:  st,
		clock:  clock,
		server: newFeedServer(t, ""),
	}
}

var ctx = context.Background()

func TestAddFeedImportsTheFeedAndItsItems(t *testing.T) {
	e := newEnv(t)
	e.server.set(rss("Server Feed", "https://site.example/", "About things",
		entry{"One", "https://site.example/1", "Mon, 05 Jan 2026 10:00:00 GMT", ""},
		entry{"Two", "https://site.example/2", "Tue, 06 Jan 2026 10:00:00 GMT", ""},
		entry{"Three", "https://site.example/3", "Wed, 07 Jan 2026 10:00:00 GMT", ""},
	))

	added, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: e.server.URL})

	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	if added.ID == 0 || added.XMLURL != e.server.URL || added.Title != "Server Feed" ||
		added.SiteURL != "https://site.example/" || added.Description != "About things" {
		t.Errorf("added feed = %+v", added)
	}
	feeds, err := e.svc.Feeds(ctx)
	if err != nil || len(feeds) != 1 || feeds[0].ID != added.ID {
		t.Fatalf("Feeds = %+v, %v; want the added feed", feeds, err)
	}
	items, err := e.svc.Items(ctx, added.ID)
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	var titles []string
	for _, it := range items {
		titles = append(titles, it.Title)
		if it.Read || !it.StarredAt.IsZero() {
			t.Errorf("item %q is read or starred right after the import", it.Title)
		}
	}
	if got := strings.Join(titles, ","); got != "Three,Two,One" { // newest first
		t.Errorf("item titles = %s, want Three,Two,One", got)
	}
}

func (e *env) feedCount(t *testing.T) int {
	t.Helper()
	feeds, err := e.svc.Feeds(ctx)
	if err != nil {
		t.Fatalf("Feeds: %v", err)
	}
	return len(feeds)
}

func TestAddFeedFailuresCreateNothing(t *testing.T) {
	cases := []struct {
		name   string
		url    func(e *env) string
		serve  string
		status int
		want   error
		check  func(t *testing.T, err error)
	}{
		{name: "not an http URL", url: func(*env) string { return "ftp://example.org/feed.xml" }, want: feed.ErrInvalidURL},
		{name: "empty URL", url: func(*env) string { return "" }, want: feed.ErrInvalidURL},
		{name: "document that is not a feed", url: func(e *env) string { return e.server.URL }, serve: "<html><body>Not a feed</body></html>", want: feed.ErrInvalidFeed},
		{name: "server error", url: func(e *env) string { return e.server.URL }, status: http.StatusInternalServerError,
			check: func(t *testing.T, err error) {
				var se *fetch.StatusError
				if !errors.As(err, &se) || se.Code != 500 {
					t.Errorf("error = %v, want a *fetch.StatusError 500", err)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if tc.serve != "" {
				e.server.set(tc.serve)
			}
			if tc.status != 0 {
				e.server.mu.Lock()
				e.server.status = tc.status
				e.server.mu.Unlock()
			}

			_, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: tc.url(e)})

			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
			if tc.check != nil {
				tc.check(t, err)
			}
			if err == nil {
				t.Error("AddFeed succeeded, want an error")
			}
			if n := e.feedCount(t); n != 0 {
				t.Errorf("%d feed(s) created by a failed AddFeed", n)
			}
		})
	}
}

func TestAddFeedRefusesADuplicateURLWithoutDownloadingAgain(t *testing.T) {
	e := newEnv(t)
	e.server.set(rss("Dup", "https://dup.example/", "d", entry{"One", "https://dup.example/1", "Mon, 05 Jan 2026 10:00:00 GMT", ""}))
	if _, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: e.server.URL}); err != nil {
		t.Fatalf("first AddFeed: %v", err)
	}
	hits := e.server.hits

	_, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: e.server.URL})

	if !errors.Is(err, feed.ErrDuplicateFeed) {
		t.Errorf("error = %v, want feed.ErrDuplicateFeed", err)
	}
	if e.server.hits != hits {
		t.Error("the duplicate was downloaded again")
	}
	if n := e.feedCount(t); n != 1 {
		t.Errorf("feeds = %d, want 1", n)
	}
}

func TestAddFeedTrimsTheURL(t *testing.T) {
	e := newEnv(t)
	e.server.set(rss("Trim", "https://trim.example/", "d"))

	added, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: "  " + e.server.URL + " \n"})

	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	if added.XMLURL != e.server.URL {
		t.Errorf("XMLURL = %q, want %q", added.XMLURL, e.server.URL)
	}
}

func TestAddFeedWithoutATitleUsesTheHostName(t *testing.T) {
	for name, title := range map[string]string{"empty": "", "blank": "  \n ", "markup only": "<![CDATA[<b></b>]]>"} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.server.set(strings.Replace(rss("X", "https://s.example/", "d",
				entry{"One", "https://s.example/1", "Mon, 05 Jan 2026 10:00:00 GMT", ""}), "<title>X</title>", "<title>"+title+"</title>", 1))

			added, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: e.server.URL + "/feeds/x.xml"})

			if err != nil {
				t.Fatalf("AddFeed: %v", err)
			}
			if added.Title != "127.0.0.1" { // the test server host, without the port
				t.Errorf("Title = %q, want the host name 127.0.0.1", added.Title)
			}
			items, _ := e.svc.Items(ctx, added.ID)
			if items[0].FeedTitle != "127.0.0.1" {
				t.Errorf("item FeedTitle = %q, want the feed title", items[0].FeedTitle)
			}
		})
	}
}

func TestImportedItemsGetViaAuthorAndFeedTitle(t *testing.T) {
	e := newEnv(t)
	doc := `<?xml version="1.0"?><rss version="2.0"><channel><title>Blog</title><link>https://blog.example/</link><description>d</description>
<item><title>Cross-posted</title><link>https://blog.example/1</link><guid>https://elsewhere.example/orig</guid><pubDate>Mon, 05 Jan 2026 10:00:00 GMT</pubDate><author>a@x.example (Ann)</author></item>
<item><title>Plain</title><link>https://blog.example/2</link><pubDate>Tue, 06 Jan 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`
	e.server.set(doc)

	added, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: e.server.URL})
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	items, _ := e.svc.Items(ctx, added.ID)

	byTitle := map[string]feed.Item{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	if it := byTitle["Cross-posted"]; it.Via != "https://elsewhere.example/orig" || it.Author != "Blog (Ann)" || it.FeedTitle != "Blog" {
		t.Errorf("Cross-posted = via %q, author %q, feed title %q", it.Via, it.Author, it.FeedTitle)
	}
	if it := byTitle["Plain"]; it.Via != "https://blog.example/" || it.Author != "Blog" {
		t.Errorf("Plain = via %q (want the site URL), author %q (want the feed title)", it.Via, it.Author)
	}
}

func (e *env) setMaxItems(t *testing.T, n int) {
	t.Helper()
	st, err := e.store.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	st.MaxItems = n
	if err := e.store.SaveSettings(ctx, st); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
}

func itemTitles(items []feed.Item) string {
	var titles []string
	for _, it := range items {
		titles = append(titles, it.Title)
	}
	return strings.Join(titles, ",")
}

func TestAddFeedKeepsTheMaxItemsNewestEvenWhenTheFeedIsSortedOldestFirst(t *testing.T) {
	e := newEnv(t)
	e.setMaxItems(t, 2)
	e.server.set(rss("Old first", "https://o.example/", "d", // oldest first, the opposite of the usual order
		entry{"A", "https://o.example/a", "Mon, 05 Jan 2026 10:00:00 GMT", ""},
		entry{"B", "https://o.example/b", "Tue, 06 Jan 2026 10:00:00 GMT", ""},
		entry{"C", "https://o.example/c", "Wed, 07 Jan 2026 10:00:00 GMT", ""},
		entry{"D", "https://o.example/d", "Thu, 08 Jan 2026 10:00:00 GMT", ""},
	))

	added, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: e.server.URL})
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	items, _ := e.svc.Items(ctx, added.ID)
	if got := itemTitles(items); got != "D,C" {
		t.Errorf("items = %s, want the two newest: D,C", got)
	}
}

func folderTitles(folders []feed.Folder) string {
	var titles []string
	for _, f := range folders {
		titles = append(titles, f.Title)
	}
	return strings.Join(titles, ",")
}

func TestCreateFolderTrimsTheTitleAndKeepsCreationOrder(t *testing.T) {
	e := newEnv(t)

	for _, title := range []string{"  Tech ", "News", "tech"} { // case matters: "tech" is not "Tech"
		if _, err := e.svc.CreateFolder(ctx, title); err != nil {
			t.Fatalf("CreateFolder(%q): %v", title, err)
		}
	}

	folders, err := e.svc.Folders(ctx)
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if got := folderTitles(folders); got != "Tech,News,tech" {
		t.Errorf("folders = %s, want Tech,News,tech", got)
	}
	for _, f := range folders {
		if f.Collapsed {
			t.Errorf("folder %q is collapsed at creation", f.Title)
		}
	}
}

func TestCreateFolderRefusesDuplicatesAndEmptyTitles(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.CreateFolder(ctx, "Tech"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	if _, err := e.svc.CreateFolder(ctx, " Tech  "); !errors.Is(err, feed.ErrFolderExists) {
		t.Errorf("duplicate: error = %v, want feed.ErrFolderExists", err)
	}
	for _, title := range []string{"", "   ", "\t\n"} {
		if _, err := e.svc.CreateFolder(ctx, title); !errors.Is(err, feed.ErrInvalidFolderTitle) {
			t.Errorf("CreateFolder(%q): error = %v, want feed.ErrInvalidFolderTitle", title, err)
		}
	}
	if folders, _ := e.svc.Folders(ctx); len(folders) != 1 {
		t.Errorf("folders = %d, want 1", len(folders))
	}
}

func (e *env) folders(t *testing.T, titles ...string) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	for _, title := range titles {
		f, err := e.svc.CreateFolder(ctx, title)
		if err != nil {
			t.Fatalf("CreateFolder(%q): %v", title, err)
		}
		ids[title] = f.ID
	}
	return ids
}

func TestRenameFolder(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "Tech", "News")

	if err := e.svc.RenameFolder(ctx, ids["Tech"], "  Technology "); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if folders, _ := e.svc.Folders(ctx); folderTitles(folders) != "Technology,News" {
		t.Errorf("folders = %s, want Technology,News (same position)", folderTitles(folders))
	}

	if err := e.svc.RenameFolder(ctx, ids["Tech"], "News"); !errors.Is(err, feed.ErrFolderExists) {
		t.Errorf("rename to an existing title: error = %v, want feed.ErrFolderExists", err)
	}
	if err := e.svc.RenameFolder(ctx, ids["Tech"], "Technology"); err != nil {
		t.Errorf("rename to its own title: %v, want nil", err)
	}
	if err := e.svc.RenameFolder(ctx, 9999, "Whatever"); !errors.Is(err, feed.ErrNotFound) {
		t.Errorf("rename of an unknown folder: error = %v, want feed.ErrNotFound", err)
	}
	if folders, _ := e.svc.Folders(ctx); folderTitles(folders) != "Technology,News" {
		t.Errorf("folders = %s after refused renames", folderTitles(folders))
	}
}

func TestRenamingAFolderToAnEmptyTitleDeletesIt(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "Tech", "News")

	if err := e.svc.RenameFolder(ctx, ids["Tech"], "  "); err != nil {
		t.Fatalf("rename to empty: %v", err)
	}

	if folders, _ := e.svc.Folders(ctx); folderTitles(folders) != "News" {
		t.Errorf("folders = %s, want News", folderTitles(folders))
	}
}

func TestReorderFoldersNeedsEveryFolderExactlyOnce(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "A", "B", "C")

	if err := e.svc.ReorderFolders(ctx, []int64{ids["C"], ids["A"], ids["B"]}); err != nil {
		t.Fatalf("ReorderFolders: %v", err)
	}
	if folders, _ := e.svc.Folders(ctx); folderTitles(folders) != "C,A,B" {
		t.Fatalf("folders = %s, want C,A,B", folderTitles(folders))
	}
	e.folders(t, "D") // a new folder goes last
	if folders, _ := e.svc.Folders(ctx); folderTitles(folders) != "C,A,B,D" {
		t.Errorf("folders = %s, want C,A,B,D", folderTitles(folders))
	}

	d := func() int64 { f, _ := e.store.FolderByTitle(ctx, "D"); return f.ID }()
	bad := map[string][]int64{
		"missing one": {ids["A"], ids["B"], ids["C"]},
		"unknown id":  {ids["A"], ids["B"], ids["C"], d, 9999},
		"duplicate":   {ids["A"], ids["A"], ids["B"], ids["C"]},
		"empty":       {},
	}
	for name, order := range bad {
		if err := e.svc.ReorderFolders(ctx, order); !errors.Is(err, feed.ErrInvalidOrder) {
			t.Errorf("%s: error = %v, want feed.ErrInvalidOrder", name, err)
		}
	}
	if folders, _ := e.svc.Folders(ctx); folderTitles(folders) != "C,A,B,D" {
		t.Errorf("folders = %s after refused reorders, want C,A,B,D", folderTitles(folders))
	}
}

func TestToggleFolderCollapsesAndExpands(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "Tech")
	collapsed := func() bool { f, _ := e.store.FolderByID(ctx, ids["Tech"]); return f.Collapsed }

	if err := e.svc.ToggleFolder(ctx, ids["Tech"]); err != nil || !collapsed() {
		t.Errorf("first toggle: error = %v, collapsed = %v; want nil, true", err, collapsed())
	}
	if err := e.svc.ToggleFolder(ctx, ids["Tech"]); err != nil || collapsed() {
		t.Errorf("second toggle: error = %v, collapsed = %v; want nil, false", err, collapsed())
	}
	if err := e.svc.ToggleFolder(ctx, 9999); !errors.Is(err, feed.ErrNotFound) {
		t.Errorf("toggle of an unknown folder: error = %v, want feed.ErrNotFound", err)
	}
}

func sortedIDs(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	slices.Sort(out)
	return out
}

func TestAddFeedIntoSeveralFoldersAndANewOne(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "Tech", "News")
	e.server.set(rss("Multi", "https://m.example/", "d", entry{"One", "https://m.example/1", "Mon, 05 Jan 2026 10:00:00 GMT", ""}))

	added, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{
		URL: e.server.URL, FolderIDs: []int64{ids["Tech"], ids["News"]}, NewFolder: "  Fresh ",
	})
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	fresh, err := e.store.FolderByTitle(ctx, "Fresh")
	if err != nil {
		t.Fatalf("the new folder was not created: %v", err)
	}
	want := sortedIDs([]int64{ids["Tech"], ids["News"], fresh.ID})
	if got := sortedIDs(added.FolderIDs); !slices.Equal(got, want) {
		t.Errorf("FolderIDs = %v, want %v", got, want)
	}
	feeds, _ := e.svc.Feeds(ctx)
	if got := sortedIDs(feeds[0].FolderIDs); !slices.Equal(got, want) {
		t.Errorf("Feeds()[0].FolderIDs = %v, want %v", got, want)
	}
}

func TestAddFeedWithANewFolderThatAlreadyExistsReusesIt(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "Tech")
	e.server.set(rss("Reuse", "https://r.example/", "d"))

	added, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: e.server.URL, FolderIDs: []int64{ids["Tech"]}, NewFolder: "Tech"})

	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	if folders, _ := e.svc.Folders(ctx); len(folders) != 1 {
		t.Errorf("folders = %s, want the existing Tech only", folderTitles(folders))
	}
	if !slices.Equal(added.FolderIDs, []int64{ids["Tech"]}) {
		t.Errorf("FolderIDs = %v, want a single association to Tech", added.FolderIDs)
	}
}

func TestAddFeedFailuresDoNotCreateTheNewFolder(t *testing.T) {
	e := newEnv(t)
	e.server.set("not a feed")

	_, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: e.server.URL, NewFolder: "Ghost"})
	if !errors.Is(err, feed.ErrInvalidFeed) {
		t.Fatalf("error = %v, want feed.ErrInvalidFeed", err)
	}
	if folders, _ := e.svc.Folders(ctx); len(folders) != 0 {
		t.Errorf("a failed AddFeed created folder(s): %s", folderTitles(folders))
	}

	e.server.set(rss("Ok", "https://ok.example/", "d"))
	hits := e.server.hits
	_, err = e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: e.server.URL, FolderIDs: []int64{9999}})
	if !errors.Is(err, feed.ErrNotFound) {
		t.Errorf("unknown folder: error = %v, want feed.ErrNotFound", err)
	}
	if e.server.hits != hits || e.feedCount(t) != 0 {
		t.Error("an unknown folder must be refused before anything is downloaded or created")
	}
}

func TestDeletingAFolderKeepsItsFeeds(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "Tech", "News")
	e.server.set(rss("Kept", "https://k.example/", "d", entry{"One", "https://k.example/1", "Mon, 05 Jan 2026 10:00:00 GMT", ""}))
	added, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: e.server.URL, FolderIDs: []int64{ids["Tech"], ids["News"]}})
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	if err := e.svc.DeleteFolder(ctx, ids["Tech"]); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}

	feeds, _ := e.svc.Feeds(ctx)
	if len(feeds) != 1 || feeds[0].ID != added.ID || !slices.Equal(feeds[0].FolderIDs, []int64{ids["News"]}) {
		t.Errorf("feeds = %+v, want the feed kept, only in News", feeds)
	}
	if items, _ := e.svc.Items(ctx, added.ID); len(items) != 1 {
		t.Errorf("items = %d, want the feed items kept", len(items))
	}
	if err := e.svc.DeleteFolder(ctx, ids["Tech"]); !errors.Is(err, feed.ErrNotFound) {
		t.Errorf("deleting twice: error = %v, want feed.ErrNotFound", err)
	}
}

func (e *env) addFeed(t *testing.T, doc string, req feed.AddFeedRequest) feed.Feed {
	t.Helper()
	e.server.set(doc)
	req.URL = e.server.URL
	added, err := e.svc.AddFeed(ctx, req)
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	return added
}

func intPtr(n int) *int { return &n }

func TestEditFeedChangesEverythingTheFormEdits(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "Tech", "News")
	added := e.addFeed(t, rss("Old title", "https://old.example/", "Old description",
		entry{"One", "https://old.example/1", "Mon, 05 Jan 2026 10:00:00 GMT", "Ann"}), feed.AddFeedRequest{FolderIDs: []int64{ids["Tech"]}})

	edited, err := e.svc.EditFeed(ctx, added.ID, feed.EditFeedRequest{
		Title: "  New title ", SiteURL: "https://new.example/", Description: " New description ",
		XMLURL: e.server.URL + "/new.xml", UpdateInterval: intPtr(30),
		FolderIDs: []int64{ids["News"]}, NewFolder: "Fresh",
	})

	if err != nil {
		t.Fatalf("EditFeed: %v", err)
	}
	if edited.Title != "New title" || edited.SiteURL != "https://new.example/" || edited.Description != "New description" ||
		edited.XMLURL != e.server.URL+"/new.xml" || edited.UpdateInterval == nil || *edited.UpdateInterval != 30 {
		t.Errorf("edited feed = %+v", edited)
	}
	fresh, _ := e.store.FolderByTitle(ctx, "Fresh")
	if want := sortedIDs([]int64{ids["News"], fresh.ID}); !slices.Equal(sortedIDs(edited.FolderIDs), want) {
		t.Errorf("FolderIDs = %v, want %v (the list replaces the old one)", edited.FolderIDs, want)
	}
	items, _ := e.svc.Items(ctx, added.ID)
	if items[0].FeedTitle != "New title" {
		t.Errorf("item FeedTitle = %q, want it renamed with the feed", items[0].FeedTitle)
	}
	if items[0].Author != "Old title (Ann)" {
		t.Errorf("item Author = %q, want it untouched by the rename", items[0].Author)
	}
}

func TestEditFeedMayClearDescriptionSiteURLAndFolders(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "Tech")
	added := e.addFeed(t, rss("Feed", "https://s.example/", "A description"), feed.AddFeedRequest{FolderIDs: []int64{ids["Tech"]}})

	edited, err := e.svc.EditFeed(ctx, added.ID, feed.EditFeedRequest{Title: "Feed", XMLURL: added.XMLURL})

	if err != nil {
		t.Fatalf("EditFeed: %v", err)
	}
	if edited.Description != "" || edited.SiteURL != "" || len(edited.FolderIDs) != 0 || edited.UpdateInterval != nil {
		t.Errorf("edited = %+v, want description, site URL, folders and interval cleared", edited)
	}
}

func TestEditFeedUpdateIntervalOutsideTheAllowedRangeFallsBackToAutomatic(t *testing.T) {
	e := newEnv(t)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d"), feed.AddFeedRequest{})
	for _, tc := range []struct {
		in   *int
		want *int
	}{
		{nil, nil}, {intPtr(5), intPtr(5)}, {intPtr(4), nil}, {intPtr(0), nil}, {intPtr(-3), nil},
		{intPtr(60), intPtr(60)}, {intPtr(61), nil}, // maxUpdate is 60 by default
	} {
		edited, err := e.svc.EditFeed(ctx, added.ID, feed.EditFeedRequest{Title: "Feed", XMLURL: added.XMLURL, UpdateInterval: tc.in})
		if err != nil {
			t.Fatalf("EditFeed: %v", err)
		}
		got := "auto"
		if edited.UpdateInterval != nil {
			got = fmt.Sprint(*edited.UpdateInterval)
		}
		want := "auto"
		if tc.want != nil {
			want = fmt.Sprint(*tc.want)
		}
		in := "auto"
		if tc.in != nil {
			in = fmt.Sprint(*tc.in)
		}
		if got != want {
			t.Errorf("interval %s → %s, want %s", in, got, want)
		}
	}
	st, _ := e.store.Settings(ctx)
	st.MaxUpdate = 120
	if err := e.store.SaveSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	edited, _ := e.svc.EditFeed(ctx, added.ID, feed.EditFeedRequest{Title: "Feed", XMLURL: added.XMLURL, UpdateInterval: intPtr(100)})
	if edited.UpdateInterval == nil || *edited.UpdateInterval != 100 {
		t.Errorf("with maxUpdate=120, interval 100 → %v, want 100", edited.UpdateInterval)
	}
}

func TestEditFeedRefusalsChangeNothing(t *testing.T) {
	e := newEnv(t)
	other := e.addFeed(t, rss("Other", "https://other.example/", "d"), feed.AddFeedRequest{})
	e.server.Close() // a second server, so the two feeds have different URLs
	second := newFeedServer(t, rss("Mine", "https://mine.example/", "mine"))
	mine, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: second.URL})
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	valid := func() feed.EditFeedRequest {
		return feed.EditFeedRequest{Title: "Changed", SiteURL: "https://changed.example/", Description: "changed", XMLURL: mine.XMLURL}
	}
	cases := []struct {
		name   string
		id     int64
		mutate func(*feed.EditFeedRequest)
		want   error
	}{
		{"empty title", mine.ID, func(r *feed.EditFeedRequest) { r.Title = "" }, feed.ErrInvalidFeedTitle},
		{"blank title", mine.ID, func(r *feed.EditFeedRequest) { r.Title = "  \t" }, feed.ErrInvalidFeedTitle},
		{"site URL javascript:", mine.ID, func(r *feed.EditFeedRequest) { r.SiteURL = "javascript:alert(1)" }, feed.ErrInvalidURL},
		{"site URL ftp:", mine.ID, func(r *feed.EditFeedRequest) { r.SiteURL = "ftp://x.example/" }, feed.ErrInvalidURL},
		{"site URL relative", mine.ID, func(r *feed.EditFeedRequest) { r.SiteURL = "/relative" }, feed.ErrInvalidURL},
		{"XML URL empty", mine.ID, func(r *feed.EditFeedRequest) { r.XMLURL = "" }, feed.ErrInvalidURL},
		{"XML URL not http", mine.ID, func(r *feed.EditFeedRequest) { r.XMLURL = "file:///etc/passwd" }, feed.ErrInvalidURL},
		{"XML URL used by another feed", mine.ID, func(r *feed.EditFeedRequest) { r.XMLURL = other.XMLURL }, feed.ErrDuplicateFeed},
		{"unknown folder", mine.ID, func(r *feed.EditFeedRequest) { r.FolderIDs = []int64{9999} }, feed.ErrNotFound},
		{"unknown feed", 9999, func(*feed.EditFeedRequest) {}, feed.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := valid()
			tc.mutate(&req)

			_, err := e.svc.EditFeed(ctx, tc.id, req)

			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
			after, _ := e.store.FeedByID(ctx, mine.ID)
			if after.Title != "Mine" || after.SiteURL != "https://mine.example/" || after.Description != "mine" || after.XMLURL != mine.XMLURL {
				t.Errorf("a refused edit changed the feed: %+v", after)
			}
		})
	}
}

func TestEditFeedKeepsOrClearsTheHTTPValidatorsWithTheXMLURL(t *testing.T) {
	e := newEnv(t)
	e.server.etag, e.server.lastModified = `"v1"`, "Wed, 21 Oct 2026 07:28:00 GMT"
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d"), feed.AddFeedRequest{})
	if added.ETag != `"v1"` || added.LastModified != "Wed, 21 Oct 2026 07:28:00 GMT" {
		t.Fatalf("validators after AddFeed = %q %q", added.ETag, added.LastModified)
	}

	same, err := e.svc.EditFeed(ctx, added.ID, feed.EditFeedRequest{Title: "Renamed", XMLURL: added.XMLURL})
	if err != nil || same.ETag != `"v1"` {
		t.Errorf("same XML URL: validators = %q, error = %v; want them kept", same.ETag, err)
	}

	moved, err := e.svc.EditFeed(ctx, added.ID, feed.EditFeedRequest{Title: "Renamed", XMLURL: added.XMLURL + "/moved.xml"})
	if err != nil || moved.ETag != "" || moved.LastModified != "" {
		t.Errorf("new XML URL: validators = %q %q, error = %v; want them cleared", moved.ETag, moved.LastModified, err)
	}
}

func titles2(items []feed.Item) string { return itemTitles(items) }

func TestSetReadAndSetStarredOnOneItem(t *testing.T) {
	e := newEnv(t)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d",
		entry{"One", "https://s.example/1", "Mon, 05 Jan 2026 10:00:00 GMT", ""},
		entry{"Two", "https://s.example/2", "Tue, 06 Jan 2026 10:00:00 GMT", ""}), feed.AddFeedRequest{})
	items, _ := e.svc.Items(ctx, added.ID)
	two, one := items[0], items[1] // newest first

	if err := e.svc.SetRead(ctx, two.ID, true); err != nil {
		t.Fatalf("SetRead: %v", err)
	}
	e.clock.now = e.clock.now.Add(time.Hour)
	if err := e.svc.SetStarred(ctx, one.ID, true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	firstStar := e.clock.now
	e.clock.now = e.clock.now.Add(time.Hour)
	if err := e.svc.SetStarred(ctx, one.ID, true); err != nil { // starring again changes nothing
		t.Fatalf("SetStarred again: %v", err)
	}

	items, _ = e.svc.Items(ctx, added.ID)
	if !items[0].Read || items[1].Read {
		t.Errorf("read flags = %v, %v; want true, false", items[0].Read, items[1].Read)
	}
	if !items[1].StarredAt.Equal(firstStar) || !items[0].StarredAt.IsZero() {
		t.Errorf("starred at = %v / %v; want the first star date on the second item only", items[1].StarredAt, items[0].StarredAt)
	}

	if err := e.svc.SetRead(ctx, two.ID, false); err != nil {
		t.Fatalf("SetRead(false): %v", err)
	}
	if err := e.svc.SetStarred(ctx, one.ID, false); err != nil {
		t.Fatalf("SetStarred(false): %v", err)
	}
	items, _ = e.svc.Items(ctx, added.ID)
	if items[0].Read || !items[1].StarredAt.IsZero() {
		t.Errorf("flags were not cleared: %+v", items)
	}
	for _, f := range []func(int64) error{
		func(id int64) error { return e.svc.SetRead(ctx, id, true) },
		func(id int64) error { return e.svc.SetStarred(ctx, id, true) },
	} {
		if err := f(9999); !errors.Is(err, feed.ErrNotFound) {
			t.Errorf("unknown item: error = %v, want feed.ErrNotFound", err)
		}
	}
}

func TestStarredItemsComeNewestStarFirst(t *testing.T) {
	e := newEnv(t)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d",
		entry{"One", "https://s.example/1", "Mon, 05 Jan 2026 10:00:00 GMT", ""},
		entry{"Two", "https://s.example/2", "Tue, 06 Jan 2026 10:00:00 GMT", ""},
		entry{"Three", "https://s.example/3", "Wed, 07 Jan 2026 10:00:00 GMT", ""}), feed.AddFeedRequest{})
	items, _ := e.svc.Items(ctx, added.ID) // Three, Two, One
	for _, i := range []int{2, 0, 1} {     // star One, then Three, then Two
		if err := e.svc.SetStarred(ctx, items[i].ID, true); err != nil {
			t.Fatalf("SetStarred: %v", err)
		}
		e.clock.now = e.clock.now.Add(time.Minute)
	}

	starred, err := e.svc.StarredItems(ctx)

	if err != nil {
		t.Fatalf("StarredItems: %v", err)
	}
	if got := titles2(starred); got != "Two,Three,One" {
		t.Errorf("starred = %s, want Two,Three,One (most recently starred first)", got)
	}
}

func TestDeleteFeedKeepsItsStarredItemsWithTheFeedTitle(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "Tech")
	added := e.addFeed(t, rss("Doomed", "https://d.example/", "d",
		entry{"Keep me", "https://d.example/1", "Mon, 05 Jan 2026 10:00:00 GMT", ""},
		entry{"Read", "https://d.example/2", "Tue, 06 Jan 2026 10:00:00 GMT", ""},
		entry{"Unread", "https://d.example/3", "Wed, 07 Jan 2026 10:00:00 GMT", ""}), feed.AddFeedRequest{FolderIDs: []int64{ids["Tech"]}})
	items, _ := e.svc.Items(ctx, added.ID)
	_ = e.svc.SetRead(ctx, items[1].ID, true)
	_ = e.svc.SetStarred(ctx, items[2].ID, true) // "Keep me"
	survivor := newFeedServer(t, rss("Survivor", "https://s.example/", "d", entry{"Safe", "https://s.example/1", "Mon, 05 Jan 2026 10:00:00 GMT", ""}))
	other, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: survivor.URL})
	if err != nil {
		t.Fatalf("AddFeed: %v", err)
	}

	if err := e.svc.DeleteFeed(ctx, added.ID); err != nil {
		t.Fatalf("DeleteFeed: %v", err)
	}

	feeds, _ := e.svc.Feeds(ctx)
	if len(feeds) != 1 || feeds[0].ID != other.ID {
		t.Errorf("feeds = %+v, want only the other feed", feeds)
	}
	if folders, _ := e.svc.Folders(ctx); folderTitles(folders) != "Tech" {
		t.Errorf("folders = %s, want Tech kept", folderTitles(folders))
	}
	if left, _ := e.svc.Items(ctx, added.ID); len(left) != 0 {
		t.Errorf("%d item(s) of the deleted feed are still listed under it", len(left))
	}
	starred, _ := e.svc.StarredItems(ctx)
	if len(starred) != 1 || starred[0].Title != "Keep me" || starred[0].FeedTitle != "Doomed" || starred[0].FeedID != 0 {
		t.Errorf("starred = %+v, want the starred item, orphaned, with its feed title", starred)
	}
	if safe, _ := e.svc.Items(ctx, other.ID); len(safe) != 1 {
		t.Errorf("the other feed lost items: %d", len(safe))
	}

	// Un-starring an orphaned item deletes it: nothing refers to it any more.
	if err := e.svc.SetStarred(ctx, starred[0].ID, false); err != nil {
		t.Fatalf("SetStarred(false): %v", err)
	}
	if starred, _ := e.svc.StarredItems(ctx); len(starred) != 0 {
		t.Errorf("starred = %+v, want none", starred)
	}
	if err := e.svc.DeleteFeed(ctx, added.ID); !errors.Is(err, feed.ErrNotFound) {
		t.Errorf("deleting twice: error = %v, want feed.ErrNotFound", err)
	}
}

func TestFeedsAreSortedByTitleNaturallyIgnoringCase(t *testing.T) {
	e := newEnv(t)
	for i, title := range []string{"Flux 10", "flux 2", "Zed", "Banana", "Flux 1", "apple", "flux 02", "Flux 1"} {
		e.server.set(rss(title, "https://s.example/", "d"))
		if _, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: fmt.Sprintf("%s/%d.xml", e.server.URL, i)}); err != nil {
			t.Fatalf("AddFeed(%q): %v", title, err)
		}
	}

	feeds, err := e.svc.Feeds(ctx)

	if err != nil {
		t.Fatalf("Feeds: %v", err)
	}
	var got []string
	for _, f := range feeds {
		got = append(got, f.Title)
	}
	// "Flux 1" twice: equal titles keep the order of creation (by ID).
	want := "apple,Banana,Flux 1,Flux 1,flux 2,flux 02,Flux 10,Zed"
	if strings.Join(got, ",") != want {
		t.Errorf("feeds = %s\nwant    %s", strings.Join(got, ","), want)
	}
}

const (
	jan5  = "Mon, 05 Jan 2026 10:00:00 GMT"
	jan6  = "Tue, 06 Jan 2026 10:00:00 GMT"
	jan7  = "Wed, 07 Jan 2026 10:00:00 GMT"
	jan8  = "Thu, 08 Jan 2026 10:00:00 GMT"
	jan9  = "Fri, 09 Jan 2026 10:00:00 GMT"
	jan10 = "Sat, 10 Jan 2026 10:00:00 GMT"
)

func e1(title, date string) entry { return entry{title, "https://s.example/" + title, date, ""} }

func (e *env) update(t *testing.T, id int64, force bool) feed.UpdateResult {
	t.Helper()
	res, err := e.svc.UpdateFeed(ctx, id, feed.UpdateOptions{Force: force})
	if err != nil {
		t.Fatalf("UpdateFeed: %v", err)
	}
	return res
}

func TestUpdateImportsNewItemsWithoutTouchingTheOthers(t *testing.T) {
	e := newEnv(t)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d", e1("A", jan5), e1("B", jan6)), feed.AddFeedRequest{})
	items, _ := e.svc.Items(ctx, added.ID) // B, A
	_ = e.svc.SetRead(ctx, items[1].ID, true)
	_ = e.svc.SetStarred(ctx, items[0].ID, true)
	e.clock.now = e.clock.now.Add(2 * time.Hour)
	e.server.set(rss("Feed", "https://s.example/", "d", e1("C", jan7), e1("B", jan6), e1("A", jan5)))

	res := e.update(t, added.ID, false)

	if res.NewItems != 1 {
		t.Errorf("NewItems = %d, want 1", res.NewItems)
	}
	items, _ = e.svc.Items(ctx, added.ID)
	if got := itemTitles(items); got != "C,B,A" {
		t.Fatalf("items = %s, want C,B,A (no duplicate)", got)
	}
	cRead, bStarred, aRead := items[0].Read, !items[1].StarredAt.IsZero(), items[2].Read
	if cRead || !bStarred || !aRead {
		t.Errorf("states = C read:%v, B starred:%v, A read:%v; want false, true, true", cRead, bStarred, aRead)
	}
	after, _ := e.store.FeedByID(ctx, added.ID)
	if !after.LastUpdateAt.Equal(e.clock.now) || after.LastError != "" {
		t.Errorf("last update = %v, error = %q; want %v, none", after.LastUpdateAt, after.LastError, e.clock.now)
	}
}

func TestUpdateIgnoresUnknownItemsThatAreNotNewerThanTheNewestKnownOne(t *testing.T) {
	e := newEnv(t)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d", e1("A", jan5), e1("B", jan7)), feed.AddFeedRequest{})
	e.server.set(rss("Feed", "https://s.example/", "d",
		e1("Newer", jan8), // after the newest known item: imported
		e1("Same", jan7),  // same date as the newest known item: not "after", ignored
		e1("Older", jan6), // unknown but older: ignored
		e1("B", jan7), e1("A", jan5)))

	res := e.update(t, added.ID, false)

	if res.NewItems != 1 {
		t.Errorf("NewItems = %d, want 1", res.NewItems)
	}
	items, _ := e.svc.Items(ctx, added.ID)
	if got := itemTitles(items); got != "Newer,B,A" {
		t.Errorf("items = %s, want Newer,B,A", got)
	}
}

func (e *env) warning(t *testing.T, id int64) string {
	t.Helper()
	f, err := e.store.FeedByID(ctx, id)
	if err != nil {
		t.Fatalf("FeedByID: %v", err)
	}
	return f.LastWarning
}

func TestUpdateWarnsWhenEveryFetchedItemIsNewAndThereWereItemsBefore(t *testing.T) {
	e := newEnv(t)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d", e1("A", jan5), e1("B", jan6)), feed.AddFeedRequest{})

	// Nothing in common with what we had: some items may have been missed.
	e.server.set(rss("Feed", "https://s.example/", "d", e1("X", jan8), e1("Y", jan9)))
	e.update(t, added.ID, false)
	if got := e.warning(t, added.ID); got != feed.WarningItemsMissed {
		t.Errorf("warning = %q, want %q", got, feed.WarningItemsMissed)
	}

	// An item we already know is back: the warning goes away.
	e.server.set(rss("Feed", "https://s.example/", "d", e1("Z", jan10), e1("Y", jan9)))
	e.update(t, added.ID, false)
	if got := e.warning(t, added.ID); got != "" {
		t.Errorf("warning = %q after an update that overlaps, want none", got)
	}

	// Everything new again, then an empty feed: the rule no longer applies.
	e.server.set(rss("Feed", "https://s.example/", "d", e1("W1", "Sun, 11 Jan 2026 10:00:00 GMT")))
	e.update(t, added.ID, false)
	if got := e.warning(t, added.ID); got != feed.WarningItemsMissed {
		t.Fatalf("warning = %q, want it set again", got)
	}
	e.server.set(rss("Feed", "https://s.example/", "d"))
	e.update(t, added.ID, false)
	if got := e.warning(t, added.ID); got != "" {
		t.Errorf("warning = %q after an empty feed, want none", got)
	}
}

func TestUpdateDoesNotWarnWhenTheFeedHadNoItems(t *testing.T) {
	e := newEnv(t)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d"), feed.AddFeedRequest{})
	e.server.set(rss("Feed", "https://s.example/", "d", e1("A", jan5), e1("B", jan6)))

	e.update(t, added.ID, false)

	if got := e.warning(t, added.ID); got != "" {
		t.Errorf("warning = %q, want none for a feed that had no items", got)
	}
	if items, _ := e.svc.Items(ctx, added.ID); len(items) != 2 {
		t.Errorf("items = %d, want 2", len(items))
	}
}

func TestUpdateFailureIsRecordedAndKeepsTheData(t *testing.T) {
	e := newEnv(t)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d", e1("A", jan5), e1("B", jan6)), feed.AddFeedRequest{})
	e.clock.now = e.clock.now.Add(time.Hour)

	e.server.mu.Lock()
	e.server.status = http.StatusInternalServerError
	e.server.mu.Unlock()
	_, err := e.svc.UpdateFeed(ctx, added.ID, feed.UpdateOptions{})
	var se *fetch.StatusError
	if !errors.As(err, &se) || se.Code != 500 {
		t.Fatalf("error = %v, want a *fetch.StatusError 500", err)
	}
	after, _ := e.store.FeedByID(ctx, added.ID)
	if !strings.Contains(after.LastError, "500") || !after.LastUpdateAt.Equal(e.clock.now) {
		t.Errorf("recorded error = %q at %v; want the 500 error at %v", after.LastError, after.LastUpdateAt, e.clock.now)
	}
	if items, _ := e.svc.Items(ctx, added.ID); itemTitles(items) != "B,A" {
		t.Errorf("items = %s, want them kept", itemTitles(items))
	}

	e.server.set("<html>maintenance page</html>")
	if _, err := e.svc.UpdateFeed(ctx, added.ID, feed.UpdateOptions{}); !errors.Is(err, feed.ErrInvalidFeed) {
		t.Errorf("error = %v, want feed.ErrInvalidFeed", err)
	}
	after, _ = e.store.FeedByID(ctx, added.ID)
	if after.LastError == "" {
		t.Error("the parse error was not recorded")
	}

	e.clock.now = e.clock.now.Add(time.Hour)
	e.server.set(rss("Feed", "https://s.example/", "d", e1("C", jan7), e1("B", jan6), e1("A", jan5)))
	e.update(t, added.ID, false)
	after, _ = e.store.FeedByID(ctx, added.ID)
	if after.LastError != "" || !after.LastUpdateAt.Equal(e.clock.now) {
		t.Errorf("after a success: error = %q at %v; want none at %v", after.LastError, after.LastUpdateAt, e.clock.now)
	}
}

func TestUpdateOfAnUnknownFeed(t *testing.T) {
	e := newEnv(t)

	if _, err := e.svc.UpdateFeed(ctx, 9999, feed.UpdateOptions{}); !errors.Is(err, feed.ErrNotFound) {
		t.Errorf("error = %v, want feed.ErrNotFound", err)
	}
}

func (e *env) markRead(t *testing.T, feedID int64, titles ...string) {
	t.Helper()
	items, _ := e.svc.Items(ctx, feedID)
	for _, it := range items {
		if slices.Contains(titles, it.Title) {
			if err := e.svc.SetRead(ctx, it.ID, true); err != nil {
				t.Fatalf("SetRead: %v", err)
			}
		}
	}
}

func (e *env) star(t *testing.T, feedID int64, titles ...string) {
	t.Helper()
	items, _ := e.svc.Items(ctx, feedID)
	for _, it := range items {
		if slices.Contains(titles, it.Title) {
			if err := e.svc.SetStarred(ctx, it.ID, true); err != nil {
				t.Fatalf("SetStarred: %v", err)
			}
		}
	}
}

func TestRetentionDeletesReadItemsAbsentFromTheFeedButKeepsUnreadAndStarredOnes(t *testing.T) {
	e := newEnv(t)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d", e1("A", jan5), e1("B", jan6), e1("C", jan7), e1("D", jan8)), feed.AddFeedRequest{})
	e.markRead(t, added.ID, "A", "C", "D")
	e.star(t, added.ID, "C")
	// The feed now only shows D (still there) and a new E.
	e.server.set(rss("Feed", "https://s.example/", "d", e1("E", jan9), e1("D", jan8)))

	e.update(t, added.ID, false)

	items, _ := e.svc.Items(ctx, added.ID)
	// A: read and gone from the feed → deleted. B: unread → kept. C: starred → kept. D: still in the feed.
	if got := itemTitles(items); got != "E,D,C,B" {
		t.Errorf("items = %s, want E,D,C,B", got)
	}
}

func TestRetentionQuotaDeletesTheOldestNonStarredItems(t *testing.T) {
	e := newEnv(t)
	e.setMaxItems(t, 3)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d", e1("A", jan5), e1("B", jan6), e1("C", jan7)), feed.AddFeedRequest{})
	e.star(t, added.ID, "C")
	e.server.set(rss("Feed", "https://s.example/", "d", e1("F", jan10), e1("E", jan9), e1("D", jan8)))

	e.update(t, added.ID, false)

	// Six items for a quota of three: the oldest non-starred ones (A, B, then D) go.
	items, _ := e.svc.Items(ctx, added.ID)
	if got := itemTitles(items); got != "F,E,C" {
		t.Errorf("items = %s, want F,E,C", got)
	}
}

func TestRetentionNeverDeletesStarredItemsEvenOverTheQuota(t *testing.T) {
	e := newEnv(t)
	e.setMaxItems(t, 2)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d", e1("A", jan5), e1("B", jan6)), feed.AddFeedRequest{})
	e.star(t, added.ID, "A", "B")
	e.server.set(rss("Feed", "https://s.example/", "d", e1("D", jan8), e1("C", jan7)))

	e.update(t, added.ID, false)

	// Four items, quota of two, but only the non-starred ones can be deleted: here nothing is
	// left to delete among A and B, so the new ones are the oldest non-starred to go.
	items, _ := e.svc.Items(ctx, added.ID)
	if got := itemTitles(items); got != "B,A" {
		t.Errorf("items = %s, want the two starred items B,A", got)
	}
	if starred, _ := e.svc.StarredItems(ctx); len(starred) != 2 {
		t.Errorf("starred = %d, want 2", len(starred))
	}
}

func TestAnEmptyFeedAnswerDoesNotWipeTheReadItems(t *testing.T) {
	e := newEnv(t)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d", e1("A", jan5), e1("B", jan6)), feed.AddFeedRequest{})
	e.markRead(t, added.ID, "A", "B")
	e.server.set(rss("Feed", "https://s.example/", "d")) // a transient, empty answer

	e.update(t, added.ID, false)

	if items, _ := e.svc.Items(ctx, added.ID); itemTitles(items) != "B,A" {
		t.Errorf("items = %s, want them kept when the feed shows nothing", itemTitles(items))
	}
}

func TestUpdateFillsEmptyDescriptionAndSiteURLButNeverOverwritesOrRenames(t *testing.T) {
	e := newEnv(t)
	// An imported feed often lacks a description and a site URL.
	bare := `<?xml version="1.0"?><rss version="2.0"><channel><title>Bare</title></channel></rss>`
	added := e.addFeed(t, bare, feed.AddFeedRequest{})
	if added.SiteURL != "" || added.Description != "" {
		t.Fatalf("setup: site %q, description %q", added.SiteURL, added.Description)
	}
	e.server.set(rss("Renamed upstream", "https://site.example/", "Upstream description"))

	e.update(t, added.ID, false)

	filled, _ := e.store.FeedByID(ctx, added.ID)
	if filled.SiteURL != "https://site.example/" || filled.Description != "Upstream description" {
		t.Errorf("site %q, description %q; want them filled from the feed", filled.SiteURL, filled.Description)
	}
	if filled.Title != "Bare" {
		t.Errorf("Title = %q, an update must never rename the feed", filled.Title)
	}

	// Values that are set, by the feed or by the user, are never overwritten.
	if _, err := e.svc.EditFeed(ctx, added.ID, feed.EditFeedRequest{Title: "Mine", SiteURL: "https://mine.example/", Description: "my words", XMLURL: added.XMLURL}); err != nil {
		t.Fatalf("EditFeed: %v", err)
	}
	e.server.set(rss("Again", "https://other.example/", "Other description"))
	e.update(t, added.ID, false)
	kept, _ := e.store.FeedByID(ctx, added.ID)
	if kept.SiteURL != "https://mine.example/" || kept.Description != "my words" || kept.Title != "Mine" {
		t.Errorf("after update: %q %q %q; want the user's values kept", kept.Title, kept.SiteURL, kept.Description)
	}
}

func TestUpdateUsesConditionalRequestsAndTreats304AsNothingToDo(t *testing.T) {
	e := newEnv(t)
	e.server.etag, e.server.lastModified = `"v1"`, "Wed, 21 Oct 2026 07:28:00 GMT"
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d", e1("A", jan5), e1("B", jan6)), feed.AddFeedRequest{})
	// An all-new update leaves a warning behind, with new validators.
	e.server.etag = `"v2"`
	e.server.set(rss("Feed", "https://s.example/", "d", e1("X", jan8), e1("Y", jan9)))
	e.update(t, added.ID, false)
	if e.warning(t, added.ID) != feed.WarningItemsMissed {
		t.Fatal("setup: expected a warning")
	}
	// A failed attempt leaves an error behind; validators must survive it.
	e.server.mu.Lock()
	e.server.etag, e.server.status = "", http.StatusBadGateway
	e.server.mu.Unlock()
	if _, err := e.svc.UpdateFeed(ctx, added.ID, feed.UpdateOptions{}); err == nil {
		t.Fatal("setup: expected a failure")
	}
	failed, _ := e.store.FeedByID(ctx, added.ID)
	if failed.ETag != `"v2"` || failed.LastError == "" {
		t.Fatalf("after a failure: etag = %q, error = %q; want the validators kept and an error", failed.ETag, failed.LastError)
	}

	// The scheduled update sends the validators; the server says "not modified".
	e.clock.now = e.clock.now.Add(time.Hour)
	e.server.mu.Lock()
	e.server.etag, e.server.status = `"v2"`, http.StatusOK
	hits := e.server.hits
	e.server.mu.Unlock()
	res := e.update(t, added.ID, false)

	if !res.NotModified || res.NewItems != 0 {
		t.Errorf("result = %+v, want NotModified with no new item", res)
	}
	if e.server.gotNoneMatch != `"v2"` {
		t.Errorf("If-None-Match = %q, want %q", e.server.gotNoneMatch, `"v2"`)
	}
	after, _ := e.store.FeedByID(ctx, added.ID)
	if after.LastError != "" || !after.LastUpdateAt.Equal(e.clock.now) || after.LastWarning != feed.WarningItemsMissed || after.ETag != `"v2"` {
		t.Errorf("after a 304: error %q, update %v, warning %q, etag %q; want no error, now, the warning kept, etag kept",
			after.LastError, after.LastUpdateAt, after.LastWarning, after.ETag)
	}
	if e.server.hits != hits+1 {
		t.Errorf("server hits = %d, want exactly one request", e.server.hits-hits)
	}

	// A manual (forced) update downloads everything again, with no conditional header.
	e.server.set(rss("Feed", "https://s.example/", "d", e1("Z", jan10), e1("Y", jan9), e1("X", jan8)))
	res = e.update(t, added.ID, true)
	if res.NotModified || res.NewItems != 1 {
		t.Errorf("forced result = %+v, want a full download with 1 new item", res)
	}
	if e.server.gotNoneMatch != "" || e.server.gotModifiedSince != "" {
		t.Errorf("a forced update sent conditional headers: %q %q", e.server.gotNoneMatch, e.server.gotModifiedSince)
	}

	// A 200 replaces the validators with the ones of the response (here: none).
	e.server.mu.Lock()
	e.server.etag, e.server.lastModified = "", ""
	e.server.mu.Unlock()
	e.update(t, added.ID, false)
	last, _ := e.store.FeedByID(ctx, added.ID)
	if last.ETag != "" || last.LastModified != "" {
		t.Errorf("validators = %q %q after a 200 without validators, want them cleared", last.ETag, last.LastModified)
	}
}

func TestUpdateOnlyConsidersTheMaxItemsNewestEntries(t *testing.T) {
	e := newEnv(t)
	e.setMaxItems(t, 2)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d", e1("A", jan5)), feed.AddFeedRequest{})
	e.server.set(rss("Feed", "https://s.example/", "d", e1("B", jan6), e1("D", jan8), e1("C", jan7), e1("E", jan9)))

	res := e.update(t, added.ID, false)

	if res.NewItems != 2 {
		t.Errorf("NewItems = %d, want 2", res.NewItems)
	}
	if items, _ := e.svc.Items(ctx, added.ID); itemTitles(items) != "E,D" {
		t.Errorf("items = %s, want the two newest: E,D", itemTitles(items))
	}
}

func TestTheNewestKnownDateSurvivesTheDeletionOfItsItem(t *testing.T) {
	e := newEnv(t)
	added := e.addFeed(t, rss("Feed", "https://s.example/", "d", e1("A", jan5), e1("B", jan8)), feed.AddFeedRequest{})
	e.markRead(t, added.ID, "A", "B")
	// B, the newest item, is read and leaves the feed: retention deletes it.
	e.server.set(rss("Feed", "https://s.example/", "d", e1("C", jan9)))
	e.update(t, added.ID, false)
	if items, _ := e.svc.Items(ctx, added.ID); itemTitles(items) != "C" {
		t.Fatalf("setup: items = %s, want C", itemTitles(items))
	}
	e.markRead(t, added.ID, "C")
	// C goes too, and the feed now only offers an older, unknown item.
	e.server.set(rss("Feed", "https://s.example/", "d", e1("Old", jan6), e1("Fresh", jan10)))

	e.update(t, added.ID, false)

	if items, _ := e.svc.Items(ctx, added.ID); itemTitles(items) != "Fresh" {
		t.Errorf("items = %s, want only Fresh: the older item must not come back as new", itemTitles(items))
	}
}

func TestAddFeedRefusesPrivateAddressesByDefault(t *testing.T) {
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := &fakeClock{now: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	svc := feed.NewService(st, fetch.New(fetch.Options{}), clock)      // the default downloader: no private address
	local := newFeedServer(t, rss("Local", "https://l.example/", "d")) // on loopback

	_, err = svc.AddFeed(ctx, feed.AddFeedRequest{URL: local.URL})

	if !errors.Is(err, fetch.ErrBlockedAddress) {
		t.Errorf("error = %v, want fetch.ErrBlockedAddress", err)
	}
	if local.hits != 0 {
		t.Errorf("the loopback server received %d request(s), want 0", local.hits)
	}
	if feeds, _ := svc.Feeds(ctx); len(feeds) != 0 {
		t.Errorf("feeds = %d, want 0", len(feeds))
	}
}
