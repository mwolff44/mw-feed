// SPDX-License-Identifier: GPL-3.0-or-later

package feed_test

import (
	"context"
	"errors"
	"fmt"
	"html"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/opml"
)

// opmlFile wraps outlines in a minimal OPML document.
func opmlFile(outlines string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><opml version="1.0"><head><title>t</title></head><body>` + outlines + `</body></opml>`)
}

func (e *env) feedByURL(t *testing.T, url string) feed.Feed {
	t.Helper()
	f, err := e.store.FeedByXMLURL(ctx, url)
	if err != nil {
		t.Fatalf("FeedByXMLURL(%s): %v", url, err)
	}
	return f
}

func (e *env) folderTitlesOf(t *testing.T, f feed.Feed) string {
	t.Helper()
	var titles []string
	for _, id := range f.FolderIDs {
		folder, err := e.store.FolderByID(ctx, id)
		if err != nil {
			t.Fatalf("FolderByID: %v", err)
		}
		titles = append(titles, folder.Title)
	}
	slices.Sort(titles)
	return strings.Join(titles, ",")
}

func TestImportOPMLCreatesFeedsAndFoldersWithoutDownloadingAnything(t *testing.T) {
	e := newEnv(t)
	e.folders(t, "Existing") // a folder that is already there
	data := opmlFile(`
		<outline xmlUrl="https://free.example/feed" title="Free" description="d" htmlUrl="https://free.example/"/>
		<outline text="Existing"><outline xmlUrl="https://a.example/feed" title="A"/></outline>
		<outline text="New one">
			<outline xmlUrl="https://a.example/feed" title="A again"/>
			<outline text="Inner"><outline xmlUrl="https://b.example/feed" title="B"/></outline>
		</outline>
		<outline text="Empty"/>`)

	res, err := e.svc.ImportOPML(ctx, data, feed.ImportOptions{})

	if err != nil {
		t.Fatalf("ImportOPML: %v", err)
	}
	want := feed.ImportResult{Imported: 3, FoldersCreated: 3} // New one, Inner, Empty; "Existing" is reused
	if res.Imported != want.Imported || res.FoldersCreated != want.FoldersCreated || res.Updated != 0 || res.Skipped != 0 || res.RejectedCount != 0 {
		t.Errorf("result = %+v, want %+v", res, want)
	}
	if e.server.hits != 0 {
		t.Errorf("%d download(s) during an import, want none", e.server.hits)
	}
	free := e.feedByURL(t, "https://free.example/feed")
	if free.Title != "Free" || free.Description != "d" || free.SiteURL != "https://free.example/" || len(free.FolderIDs) != 0 {
		t.Errorf("free = %+v", free)
	}
	if !free.LastUpdateAt.IsZero() || free.UpdateInterval != nil || free.ETag != "" {
		t.Errorf("an imported feed must be never updated, with the automatic delay: %+v", free)
	}
	if got := e.folderTitlesOf(t, e.feedByURL(t, "https://a.example/feed")); got != "Existing,New one" {
		t.Errorf("A is in %q, want Existing,New one (merged, the existing folder reused)", got)
	}
	if got := e.folderTitlesOf(t, e.feedByURL(t, "https://b.example/feed")); got != "Inner,New one" {
		t.Errorf("B is in %q, want Inner,New one (every ancestor folder)", got)
	}
	folders, _ := e.svc.Folders(ctx)
	if got := folderTitles(folders); got != "Existing,New one,Inner,Empty" {
		t.Errorf("folders = %s, want the new ones after the existing one, in order of appearance", got)
	}
	// Never-updated feeds are due: the scheduler downloads them on its next pass.
	due, err := e.svc.DueFeeds(ctx)
	if err != nil || len(due) != 3 {
		t.Errorf("due feeds = %d (error %v), want the 3 imported feeds", len(due), err)
	}
}

func TestImportOPMLOfAnEmptyListChangesNothing(t *testing.T) {
	e := newEnv(t)

	res, err := e.svc.ImportOPML(ctx, opmlFile(""), feed.ImportOptions{})

	if err != nil || !reflect.DeepEqual(res, feed.ImportResult{}) {
		t.Errorf("result = %+v, error = %v; want zeros and no error", res, err)
	}
}

func TestImportOPMLFailsOnAnInvalidFileAndImportsNothing(t *testing.T) {
	e := newEnv(t)

	for name, data := range map[string]string{
		"not OPML":   "<html><body>hello</body></html>",
		"entity":     `<?xml version="1.0"?><!DOCTYPE opml [<!ENTITY x "y">]><opml><body><outline text="F"><outline xmlUrl="https://a.example/feed"/></outline></body></opml>`,
		"too nested": string(opmlFile(strings.Repeat(`<outline text="f">`, 20) + `<outline xmlUrl="https://a.example/feed"/>` + strings.Repeat(`</outline>`, 20))),
	} {
		if _, err := e.svc.ImportOPML(ctx, []byte(data), feed.ImportOptions{}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := e.svc.ImportOPML(ctx, []byte("<html/>"), feed.ImportOptions{}); !errors.Is(err, opml.ErrInvalidFormat) {
		t.Errorf("error = %v, want opml.ErrInvalidFormat", err)
	}
	if n := e.feedCount(t); n != 0 {
		t.Errorf("%d feed(s) after failed imports", n)
	}
	if folders, _ := e.svc.Folders(ctx); len(folders) != 0 {
		t.Errorf("folders after failed imports: %s", folderTitles(folders))
	}
}

func TestImportOPMLReportsRejectedFeedsAndImportsTheOthers(t *testing.T) {
	e := newEnv(t)
	var b strings.Builder
	b.WriteString(`<outline xmlUrl="https://ok.example/feed" title="Fine"/>`)
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&b, `<outline xmlUrl="ftp://bad.example/%d" title="Bad %d"/>`, i, i)
	}

	res, err := e.svc.ImportOPML(ctx, opmlFile(b.String()), feed.ImportOptions{})

	if err != nil {
		t.Fatalf("ImportOPML: %v", err)
	}
	if res.Imported != 1 || res.RejectedCount != 150 {
		t.Errorf("result = imported %d, rejected %d; want 1 and 150 (an exact count)", res.Imported, res.RejectedCount)
	}
	if len(res.Rejected) != 100 {
		t.Fatalf("%d rejected feeds listed, want the first 100", len(res.Rejected))
	}
	if r := res.Rejected[0]; r.Title != "Bad 0" || r.URL != "ftp://bad.example/0" || r.Reason != opml.ReasonInvalidURL {
		t.Errorf("first rejection = %+v", r)
	}
	if n := e.feedCount(t); n != 1 {
		t.Errorf("%d feed(s), want only the valid one", n)
	}
}

func TestImportOPMLNeverTouchesAnExistingFeedWithoutOverwrite(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "Mine")
	added := e.addFeed(t, rss("Original", "https://o.example/", "my description", entry{"One", "https://o.example/1", jan5, ""}),
		feed.AddFeedRequest{FolderIDs: []int64{ids["Mine"]}})
	data := opmlFile(`<outline text="Theirs"><outline xmlUrl="` + added.XMLURL + `" title="Renamed" description="other" htmlUrl="https://other.example/"/></outline>`)

	res, err := e.svc.ImportOPML(ctx, data, feed.ImportOptions{})

	if err != nil {
		t.Fatalf("ImportOPML: %v", err)
	}
	if res.Imported != 0 || res.Skipped != 1 || res.Updated != 0 {
		t.Errorf("result = %+v, want 1 skipped", res)
	}
	after := e.feedByURL(t, added.XMLURL)
	if after.Title != "Original" || after.Description != "my description" || after.SiteURL != "https://o.example/" || e.folderTitlesOf(t, after) != "Mine" {
		t.Errorf("an existing feed was modified: %+v (folders %s)", after, e.folderTitlesOf(t, after))
	}
}

func TestImportOPMLWithOverwriteReplacesOnlyWhatTheFileDescribes(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "Mine")
	e.server.etag = `"v1"`
	added := e.addFeed(t, rss("Original", "https://o.example/", "my description",
		entry{"One", "https://o.example/1", jan5, ""}, entry{"Two", "https://o.example/2", jan6, ""}),
		feed.AddFeedRequest{FolderIDs: []int64{ids["Mine"]}})
	items, _ := e.svc.Items(ctx, added.ID)
	_ = e.svc.SetRead(ctx, items[0].ID, true)
	_ = e.svc.SetStarred(ctx, items[1].ID, true)
	if _, err := e.svc.EditFeed(ctx, added.ID, feed.EditFeedRequest{
		Title: "Original", SiteURL: "https://o.example/", Description: "my description", XMLURL: added.XMLURL,
		UpdateInterval: intPtr(30), FolderIDs: []int64{ids["Mine"]},
	}); err != nil {
		t.Fatalf("EditFeed: %v", err)
	}
	before := e.feedByURL(t, added.XMLURL)
	// The file describes the feed with another title, no description and no site: it is the reference.
	data := opmlFile(`<outline text="Theirs"><outline xmlUrl="` + added.XMLURL + `" title="Renamed"/></outline>`)

	res, err := e.svc.ImportOPML(ctx, data, feed.ImportOptions{Overwrite: true})

	if err != nil {
		t.Fatalf("ImportOPML: %v", err)
	}
	if res.Updated != 1 || res.Imported != 0 || res.Skipped != 0 {
		t.Errorf("result = %+v, want 1 updated", res)
	}
	after := e.feedByURL(t, added.XMLURL)
	if after.Title != "Renamed" || after.Description != "" || after.SiteURL != "" || e.folderTitlesOf(t, after) != "Theirs" {
		t.Errorf("feed = %q, description %q, site %q, folders %q; want what the file says, the folder list replaced",
			after.Title, after.Description, after.SiteURL, e.folderTitlesOf(t, after))
	}
	if after.UpdateInterval == nil || *after.UpdateInterval != 30 || !after.LastUpdateAt.Equal(before.LastUpdateAt) || after.ETag != `"v1"` {
		t.Errorf("delay %v, last update %v, etag %q; want them kept", after.UpdateInterval, after.LastUpdateAt, after.ETag)
	}
	kept, _ := e.svc.Items(ctx, added.ID)
	if len(kept) != 2 || !kept[0].Read || kept[1].StarredAt.IsZero() || kept[0].FeedTitle != "Renamed" || kept[1].FeedTitle != "Renamed" {
		t.Errorf("items = %+v, want both kept (newest read, oldest starred), renamed with the feed", kept)
	}
}

func TestImportOPMLWithACanceledContextImportsNothing(t *testing.T) {
	e := newEnv(t)
	canceled, cancel := context.WithCancel(ctx)
	cancel()

	_, err := e.svc.ImportOPML(canceled, opmlFile(`<outline text="F"><outline xmlUrl="https://a.example/feed"/></outline>`), feed.ImportOptions{})

	if err == nil {
		t.Error("no error with a canceled context")
	}
	if n := e.feedCount(t); n != 0 {
		t.Errorf("%d feed(s) imported", n)
	}
	if folders, _ := e.svc.Folders(ctx); len(folders) != 0 {
		t.Errorf("folders created: %s", folderTitles(folders))
	}
}

// summary describes the feeds and folders of a reader in a form that does not
// depend on database IDs: folders in display order, and for each feed URL its
// title, description, site URL and sorted folder titles.
type feedSummary struct {
	Title, Description, SiteURL, Folders string
}

func (e *env) summary(t *testing.T) (folders string, feeds map[string]feedSummary) {
	t.Helper()
	list, err := e.svc.Folders(ctx)
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	all, err := e.svc.Feeds(ctx)
	if err != nil {
		t.Fatalf("Feeds: %v", err)
	}
	feeds = map[string]feedSummary{}
	for _, f := range all {
		feeds[f.XMLURL] = feedSummary{f.Title, f.Description, f.SiteURL, e.folderTitlesOf(t, f)}
	}
	return folderTitles(list), feeds
}

// subscribe creates a feed without downloading it, as an import does.
func (e *env) subscribe(t *testing.T, url, title, description, site string, folderIDs ...int64) {
	t.Helper()
	esc := html.EscapeString
	data := opmlFile(fmt.Sprintf(`<outline xmlUrl="%s" title="%s" description="%s" htmlUrl="%s"/>`, esc(url), esc(title), esc(description), esc(site)))
	if _, err := e.svc.ImportOPML(ctx, data, feed.ImportOptions{}); err != nil {
		t.Fatalf("setup import: %v", err)
	}
	f := e.feedByURL(t, url)
	if _, err := e.svc.EditFeed(ctx, f.ID, feed.EditFeedRequest{
		Title: f.Title, SiteURL: f.SiteURL, Description: f.Description, XMLURL: f.XMLURL, FolderIDs: folderIDs,
	}); err != nil {
		t.Fatalf("setup EditFeed: %v", err)
	}
}

func TestExportThenImportIntoAnEmptyReaderGivesTheSameFeedsAndFolders(t *testing.T) {
	src := newEnv(t)
	ids := src.folders(t, "Tech", "News", "Empty", `R&D "lab" 2<3`)
	// Folders in a manual order that is not the creation order.
	if err := src.svc.ReorderFolders(ctx, []int64{ids["News"], ids[`R&D "lab" 2<3`], ids["Empty"], ids["Tech"]}); err != nil {
		t.Fatalf("ReorderFolders: %v", err)
	}
	src.subscribe(t, "https://free.example/feed", "Free", "", "")
	src.subscribe(t, "https://both.example/feed?a=1&b=2", `Tom & Jerry's "show"`, "A description,\nwith a line break", "https://both.example/", ids["Tech"], ids["News"])
	src.subscribe(t, "https://news.example/feed", "feed 10", "d", "", ids["News"])
	src.subscribe(t, "https://odd.example/feed", "feed 2", "", "https://odd.example/", ids[`R&D "lab" 2<3`])

	data, err := src.svc.ExportOPML(ctx)
	if err != nil {
		t.Fatalf("ExportOPML: %v", err)
	}
	dst := newEnv(t)
	res, err := dst.svc.ImportOPML(ctx, data, feed.ImportOptions{})

	if err != nil {
		t.Fatalf("ImportOPML of the export: %v\n%s", err, data)
	}
	if res.Imported != 4 || res.FoldersCreated != 4 || res.RejectedCount != 0 {
		t.Errorf("import result = %+v, want 4 feeds and 4 folders, nothing rejected", res)
	}
	wantFolders, wantFeeds := src.summary(t)
	gotFolders, gotFeeds := dst.summary(t)
	if gotFolders != wantFolders {
		t.Errorf("folders = %s, want %s (same order, the empty one included)", gotFolders, wantFolders)
	}
	// The description's line break is turned into a space on import: text is normalised.
	wantFeeds["https://both.example/feed?a=1&b=2"] = feedSummary{`Tom & Jerry's "show"`, "A description, with a line break", "https://both.example/", "News,Tech"}
	if !reflect.DeepEqual(gotFeeds, wantFeeds) {
		t.Errorf("feeds after a round trip:\n%+v\nwant:\n%+v", gotFeeds, wantFeeds)
	}
	// A second round trip changes nothing any more.
	again, _ := dst.svc.ExportOPML(ctx)
	third := newEnv(t)
	if _, err := third.svc.ImportOPML(ctx, again, feed.ImportOptions{}); err != nil {
		t.Fatalf("second import: %v", err)
	}
	if f, fs := third.summary(t); f != gotFolders || !reflect.DeepEqual(fs, gotFeeds) {
		t.Error("a second export and import changed something")
	}
}

func TestExportOfAnEmptyReaderIsAValidFileWithoutOutlines(t *testing.T) {
	e := newEnv(t)

	data, err := e.svc.ExportOPML(ctx)

	if err != nil {
		t.Fatalf("ExportOPML: %v", err)
	}
	doc, err := opml.Parse(data)
	if err != nil || len(doc.Feeds) != 0 || len(doc.Folders) != 0 {
		t.Errorf("parsed export = %+v, error %v; want an empty valid document", doc, err)
	}
}

func TestExportListsFeedsInNaturalOrderAndFoldersInDisplayOrder(t *testing.T) {
	e := newEnv(t)
	ids := e.folders(t, "A", "B")
	_ = e.svc.ReorderFolders(ctx, []int64{ids["B"], ids["A"]})
	for _, f := range []struct{ url, title string }{{"https://x.example/10", "feed 10"}, {"https://x.example/2", "Feed 2"}, {"https://x.example/a", "alpha"}} {
		e.subscribe(t, f.url, f.title, "", "", ids["A"])
	}

	data, _ := e.svc.ExportOPML(ctx)

	doc, err := opml.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var titles []string
	for _, f := range doc.Feeds {
		titles = append(titles, f.Title)
	}
	if got := strings.Join(titles, ","); got != "alpha,Feed 2,feed 10" {
		t.Errorf("feeds = %s, want natural order alpha,Feed 2,feed 10", got)
	}
	if !reflect.DeepEqual(doc.Folders, []string{"B", "A"}) {
		t.Errorf("folders = %v, want the display order B,A", doc.Folders)
	}
}
