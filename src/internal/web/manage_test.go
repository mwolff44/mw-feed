// SPDX-License-Identifier: GPL-3.0-or-later

package web_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/store"
)

// feedURL serves an RSS document and returns its URL.
func feedURL(t *testing.T, xml string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(xml))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAddFormListsTheFoldersAndAddsTheFeed(t *testing.T) {
	a, _, _, tech := twoFeeds(t)

	form := body(t, a.get(t, "/add"))
	for _, want := range []string{
		`<form method="post" action="/add">`, `name="url"`, `name="new_folder"`,
		fmt.Sprintf(`<input type="checkbox" name="folder" value="%d">`, tech), "Tech",
	} {
		if !strings.Contains(form, want) {
			t.Errorf("add form does not contain %q", want)
		}
	}

	xml := rss("Gamma", entry{title: "G1", link: "https://g.example/1", date: "Mon, 05 Jan 2026 10:00:00 GMT"},
		entry{title: "G2", link: "https://g.example/2", date: "Tue, 06 Jan 2026 10:00:00 GMT"})
	resp := a.act(t, "/add", url.Values{"url": {feedURL(t, xml)}, "folder": {fmt.Sprint(tech)}, "new_folder": {"News"}})

	feeds, err := a.feeds.Feeds(ctx)
	if err != nil || len(feeds) != 3 {
		t.Fatalf("Feeds = %+v, %v; want 3 feeds", feeds, err)
	}
	var gamma feed.Feed
	for _, f := range feeds {
		if f.Title == "Gamma" {
			gamma = f
		}
	}
	if want := fmt.Sprintf("/feed/%d", gamma.ID); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != want {
		t.Fatalf("POST /add = %d → %q, want 303 → %q", resp.StatusCode, resp.Header.Get("Location"), want)
	}
	if len(gamma.FolderIDs) != 2 {
		t.Errorf("Gamma is in %d folders, want 2 (Tech and the new News)", len(gamma.FolderIDs))
	}
	if page := body(t, a.get(t, fmt.Sprintf("/feed/%d", gamma.ID))); !strings.Contains(page, `<p class="flash" role="status">Feed added: Gamma, 2 items imported.</p>`) {
		t.Errorf("no success flash in:\n%s", page)
	}
}

func TestAddFormShowsWhyItFailedAndKeepsWhatWasTyped(t *testing.T) {
	a, _, _, tech := twoFeeds(t)
	existing, err := a.feeds.Feeds(ctx)
	if err != nil {
		t.Fatalf("Feeds: %v", err)
	}
	notAFeed := feedURL(t, "<html>not a feed</html>")

	cases := []struct {
		name, url, want string
	}{
		{"invalid URL", "ftp://example.org/feed", "Only absolute http and https URLs are accepted."},
		{"already subscribed", existing[0].XMLURL, "This feed already exists."},
		{"not a feed", notAFeed, "The feed could not be added:"},
	}
	for _, tc := range cases {
		resp := a.act(t, "/add", url.Values{"url": {tc.url}, "folder": {fmt.Sprint(tech)}, "new_folder": {"Typed folder"}})
		page := body(t, resp)

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", tc.name, resp.StatusCode)
		}
		for _, want := range []string{
			`<p class="error" role="alert">` + tc.want,
			`name="url" value="` + strings.ReplaceAll(tc.url, "&", "&amp;") + `"`,
			fmt.Sprintf(`<input type="checkbox" name="folder" value="%d" checked>`, tech),
			`name="new_folder" value="Typed folder"`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("%s: page does not contain %q in:\n%s", tc.name, want, page)
			}
		}
	}
	if feeds, _ := a.feeds.Feeds(ctx); len(feeds) != len(existing) {
		t.Errorf("a failed add created a feed: %d, want %d", len(feeds), len(existing))
	}
	if resp := a.post(t, "/add", url.Values{"url": {notAFeed}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /add without CSRF = %d, want 403", resp.StatusCode)
	}
}

func folderTitles(t *testing.T, a *app) []string {
	t.Helper()
	folders, err := a.feeds.Folders(ctx)
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	var titles []string
	for _, f := range folders {
		titles = append(titles, f.Title)
	}
	return titles
}

func TestEditFolderRenamesDeletesAndRefusesDuplicates(t *testing.T) {
	a, alpha, _, tech := twoFeeds(t)
	other, err := a.feeds.CreateFolder(ctx, "Other")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	path := fmt.Sprintf("/edit/folder/%d", tech)

	if page := body(t, a.get(t, path)); !strings.Contains(page, `<form method="post" action="`+path+`">`) ||
		!strings.Contains(page, `name="title" value="Tech"`) || !strings.Contains(page, "Leave empty to delete") {
		t.Errorf("folder form is incomplete:\n%s", page)
	}

	resp := a.act(t, path, url.Values{"title": {"Technology"}})
	if want := fmt.Sprintf("/folder/%d", tech); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != want {
		t.Fatalf("rename = %d → %q, want 303 → %q", resp.StatusCode, resp.Header.Get("Location"), want)
	}
	if got := strings.Join(folderTitles(t, a), ","); got != "Technology,Other" {
		t.Errorf("folders = %q, want Technology,Other", got)
	}
	if page := body(t, a.get(t, fmt.Sprintf("/folder/%d", tech))); !strings.Contains(page, `<p class="flash" role="status">Folder saved.</p>`) {
		t.Error("no flash after the rename")
	}

	resp = a.act(t, path, url.Values{"title": {"Other"}})
	page := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(page, `<p class="error" role="alert">A folder with this title already exists.</p>`) ||
		!strings.Contains(page, `name="title" value="Other"`) {
		t.Errorf("duplicate title: %d\n%s", resp.StatusCode, page)
	}

	resp = a.act(t, path, url.Values{"title": {"   "}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("empty title = %d → %q, want 303 → /", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := strings.Join(folderTitles(t, a), ","); got != "Other" {
		t.Errorf("folders = %q, want only Other", got)
	}
	if feeds, _ := a.feeds.Feeds(ctx); len(feeds) != 2 {
		t.Errorf("deleting a folder removed feeds: %d left", len(feeds))
	}
	if page := body(t, a.get(t, "/")); !strings.Contains(page, "Folder deleted.") {
		t.Error("no flash after the deletion")
	}
	_, _ = alpha, other

	for _, p := range []string{"/edit/folder/999", "/edit/folder/abc"} {
		if resp := a.get(t, p); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, resp.StatusCode)
		}
		if resp := a.act(t, p, url.Values{"title": {"x"}}); resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", p, resp.StatusCode)
		}
	}
	if resp := a.post(t, fmt.Sprintf("/edit/folder/%d", other.ID), url.Values{"title": {"x"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST without CSRF = %d, want 403", resp.StatusCode)
	}
}

func TestFolderButtonsMoveAndCollapseFolders(t *testing.T) {
	a, _, _, tech := twoFeeds(t)
	var ids = []int64{tech}
	for _, title := range []string{"Second", "Third"} {
		f, err := a.feeds.CreateFolder(ctx, title)
		if err != nil {
			t.Fatalf("CreateFolder: %v", err)
		}
		ids = append(ids, f.ID)
	}
	second, third := ids[1], ids[2]
	order := func() string { return strings.Join(folderTitles(t, a), ",") }

	for _, step := range []struct {
		path, want string
	}{
		{fmt.Sprintf("/edit/folder/%d/up", second), "Second,Tech,Third"},
		{fmt.Sprintf("/edit/folder/%d/up", second), "Second,Tech,Third"}, // already first
		{fmt.Sprintf("/edit/folder/%d/down", tech), "Second,Third,Tech"},
		{fmt.Sprintf("/edit/folder/%d/down", tech), "Second,Third,Tech"}, // already last
		{fmt.Sprintf("/edit/folder/%d/up", third), "Third,Second,Tech"},
	} {
		resp := a.act(t, step.path, url.Values{"return": {"/edit"}})

		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/edit" {
			t.Fatalf("POST %s = %d → %q, want 303 → /edit", step.path, resp.StatusCode, resp.Header.Get("Location"))
		}
		if got := order(); got != step.want {
			t.Fatalf("after %s: order %q, want %q", step.path, got, step.want)
		}
	}
	if resp := a.act(t, "/edit/folder/999/up", url.Values{}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("moving an unknown folder = %d, want 404", resp.StatusCode)
	}

	resp := a.act(t, fmt.Sprintf("/folder/%d/toggle", tech), url.Values{"return": {"/feed/1"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/feed/1" {
		t.Fatalf("toggle = %d → %q, want 303 → /feed/1", resp.StatusCode, resp.Header.Get("Location"))
	}
	if folders, _ := a.feeds.Folders(ctx); !folders[2].Collapsed {
		t.Error("the folder was not collapsed")
	}
	if resp := a.act(t, "/folder/999/toggle", url.Values{}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("toggling an unknown folder = %d, want 404", resp.StatusCode)
	}
}

func TestFeedListFoldersHaveAToggleButton(t *testing.T) {
	a, _, _, tech := twoFeeds(t)

	page := body(t, a.get(t, "/"))

	if want := fmt.Sprintf(`action="/folder/%d/toggle"`, tech); !strings.Contains(page, want) {
		t.Errorf("the feed list has no toggle form %q", want)
	}
}

func TestEditFeedFormShowsTheFeedAndItsState(t *testing.T) {
	a, alpha, _, tech := twoFeeds(t)
	warning := "items_missed"
	err := a.store.InTx(ctx, func(tx *store.Tx) error {
		return tx.RecordFeedUpdate(ctx, alpha.ID, store.FeedUpdate{At: a.clock.now, Error: "download failed: timeout", Warning: &warning})
	})
	if err != nil {
		t.Fatalf("RecordFeedUpdate: %v", err)
	}
	path := fmt.Sprintf("/edit/feed/%d", alpha.ID)

	resp := a.get(t, path)
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
	}
	for _, want := range []string{
		`<form method="post" action="` + path + `">`,
		`name="title" value="Alpha"`, `name="xml_url" value="` + alpha.XMLURL + `"`,
		`name="site_url" value="https://site.example/"`, `name="description" value="About Alpha"`,
		fmt.Sprintf(`<input type="checkbox" name="folder" value="%d" checked>`, tech),
		`name="new_folder"`, `name="interval" value=""`,
		"Empty: automatic; between 5 and 60 minutes",
		"2026-01-01 12:00", "download failed: timeout",
		"Some items of this feed were missed", // the translated warning
		fmt.Sprintf(`href="%s/delete"`, path),
	} {
		if !strings.Contains(page, want) {
			t.Errorf("feed form does not contain %q in:\n%s", want, page)
		}
	}
}

func TestEditFeedSavesTheChangesAndShowsWhatIsWrong(t *testing.T) {
	a, alpha, beta, tech := twoFeeds(t)
	path := fmt.Sprintf("/edit/feed/%d", alpha.ID)
	valid := func() url.Values {
		return url.Values{
			"title": {"Alpha renamed"}, "xml_url": {alpha.XMLURL}, "site_url": {"https://alpha.example/"},
			"description": {"New description"}, "interval": {"15"}, "new_folder": {"Fresh"},
		}
	}

	resp := a.act(t, path, valid())

	if want := fmt.Sprintf("/feed/%d", alpha.ID); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != want {
		t.Fatalf("save = %d → %q, want 303 → %q", resp.StatusCode, resp.Header.Get("Location"), want)
	}
	got, err := a.store.FeedByID(ctx, alpha.ID)
	if err != nil {
		t.Fatalf("FeedByID: %v", err)
	}
	if got.Title != "Alpha renamed" || got.SiteURL != "https://alpha.example/" || got.Description != "New description" ||
		got.UpdateInterval == nil || *got.UpdateInterval != 15 {
		t.Errorf("saved feed = %+v", got)
	}
	if len(got.FolderIDs) != 1 { // the checkboxes were all unchecked: only the new folder remains
		t.Errorf("folders = %v, want only the new one", got.FolderIDs)
	}
	if page := body(t, a.get(t, fmt.Sprintf("/feed/%d", alpha.ID))); !strings.Contains(page, `<p class="flash" role="status">Feed saved.</p>`) {
		t.Error("no flash after saving")
	}

	// An empty delay means automatic again.
	form := valid()
	form.Set("interval", "")
	form.Set("new_folder", "")
	form.Add("folder", fmt.Sprint(tech))
	a.act(t, path, form)
	if got, _ := a.store.FeedByID(ctx, alpha.ID); got.UpdateInterval != nil || len(got.FolderIDs) != 1 || got.FolderIDs[0] != tech {
		t.Errorf("after an empty delay and one folder: %+v", got)
	}

	cases := []struct {
		name   string
		change func(url.Values)
		want   string
	}{
		{"empty title", func(v url.Values) { v.Set("title", " ") }, "The feed title must not be empty."},
		{"bad URL", func(v url.Values) { v.Set("xml_url", "ftp://x.example/") }, "Only absolute http and https URLs are accepted."},
		{"bad site URL", func(v url.Values) { v.Set("site_url", "nonsense") }, "Only absolute http and https URLs are accepted."},
		{"duplicate", func(v url.Values) { v.Set("xml_url", beta.XMLURL) }, "This feed already exists."},
		{"bad delay", func(v url.Values) { v.Set("interval", "soon") }, "The update delay must be a number of minutes."},
	}
	for _, tc := range cases {
		form := valid()
		tc.change(form)
		resp := a.act(t, path, form)
		page := body(t, resp)

		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(page, `<p class="error" role="alert">`+tc.want) {
			t.Errorf("%s: status %d, message missing in:\n%s", tc.name, resp.StatusCode, page)
		}
		if !strings.Contains(page, `name="description" value="New description"`) {
			t.Errorf("%s: what was typed is not kept", tc.name)
		}
	}
	if got, _ := a.store.FeedByID(ctx, alpha.ID); got.Title != "Alpha renamed" {
		t.Errorf("a refused save changed the feed: %q", got.Title)
	}

	for _, p := range []string{"/edit/feed/999", "/edit/feed/abc"} {
		if resp := a.get(t, p); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, resp.StatusCode)
		}
		if resp := a.act(t, p, valid()); resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", p, resp.StatusCode)
		}
	}
	if resp := a.post(t, path, valid()); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST without CSRF = %d, want 403", resp.StatusCode)
	}
}

func TestDeletingAFeedAsksForConfirmationAndKeepsStarredItems(t *testing.T) {
	a, alpha, beta, _ := twoFeeds(t)
	starred := mustFirstItem(t, a, alpha.ID)
	if err := a.feeds.SetStarred(ctx, starred.ID, true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	path := fmt.Sprintf("/edit/feed/%d/delete", alpha.ID)

	resp := a.get(t, path)
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
	}
	for _, want := range []string{
		"Delete the feed Alpha?", "except the ones you starred",
		`<form method="post" action="` + path + `">`,
		fmt.Sprintf(`<a href="/edit/feed/%d">Cancel</a>`, alpha.ID),
	} {
		if !strings.Contains(page, want) {
			t.Errorf("confirmation page does not contain %q in:\n%s", want, page)
		}
	}
	if feeds, _ := a.feeds.Feeds(ctx); len(feeds) != 2 {
		t.Fatal("showing the confirmation page deleted something")
	}
	if resp := a.post(t, path, url.Values{}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST without CSRF = %d, want 403", resp.StatusCode)
	}

	resp = a.act(t, path, url.Values{})

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("delete = %d → %q, want 303 → /", resp.StatusCode, resp.Header.Get("Location"))
	}
	feeds, _ := a.feeds.Feeds(ctx)
	if len(feeds) != 1 || feeds[0].ID != beta.ID {
		t.Errorf("feeds after the deletion = %+v, want only Beta", feeds)
	}
	if page := body(t, a.get(t, "/")); !strings.Contains(page, "Feed deleted.") {
		t.Error("no flash after the deletion")
	}
	if page := body(t, a.get(t, "/starred")); !strings.Contains(page, starred.Title) {
		t.Error("the starred item was deleted with its feed")
	}
	for _, p := range []string{"/edit/feed/999/delete", "/edit/feed/abc/delete"} {
		if resp := a.get(t, p); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, resp.StatusCode)
		}
		if resp := a.act(t, p, url.Values{}); resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestGlobalEditListsFoldersWithMoveButtonsAndFeedsWithCheckboxes(t *testing.T) {
	a, alpha, beta, tech := twoFeeds(t)
	second, err := a.feeds.CreateFolder(ctx, "Second")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	resp := a.get(t, "/edit")
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /edit = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{
		fmt.Sprintf(`<a href="/edit/folder/%d">Tech</a>`, tech),
		fmt.Sprintf(`action="/edit/folder/%d/down"`, tech),
		fmt.Sprintf(`action="/edit/folder/%d/up"`, second.ID),
		fmt.Sprintf(`<input type="checkbox" name="feed" value="%d">`, alpha.ID),
		fmt.Sprintf(`<a href="/edit/feed/%d">Beta</a>`, beta.ID),
		`<form method="post" action="/edit/feeds">`,
		`name="add_folder"`, `name="remove_folder"`, `name="new_folder"`,
		`formaction="/edit/delete"`,
		`<a href="/add">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("global edit page does not contain %q in:\n%s", want, page)
		}
	}
	for _, not := range []string{fmt.Sprintf(`action="/edit/folder/%d/up"`, tech), fmt.Sprintf(`action="/edit/folder/%d/down"`, second.ID)} {
		if strings.Contains(page, not) {
			t.Errorf("the first folder cannot go up and the last cannot go down: found %q", not)
		}
	}
}

func TestGlobalEditChangesTheFoldersOfTheCheckedFeeds(t *testing.T) {
	a, alpha, beta, tech := twoFeeds(t)

	resp := a.act(t, "/edit/feeds", url.Values{
		"feed": {fmt.Sprint(beta.ID)}, "add_folder": {fmt.Sprint(tech)}, "new_folder": {"Fresh"}, "remove_folder": {},
	})

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/edit" {
		t.Fatalf("POST /edit/feeds = %d → %q, want 303 → /edit", resp.StatusCode, resp.Header.Get("Location"))
	}
	got, _ := a.store.FeedByID(ctx, beta.ID)
	if len(got.FolderIDs) != 2 {
		t.Errorf("Beta is in %d folders, want 2 (Tech and Fresh)", len(got.FolderIDs))
	}
	if other, _ := a.store.FeedByID(ctx, alpha.ID); len(other.FolderIDs) != 1 {
		t.Errorf("an unchecked feed was changed: %v", other.FolderIDs)
	}
	if page := body(t, a.get(t, "/edit")); !strings.Contains(page, `<p class="flash" role="status">Folders updated for 1 feed.</p>`) {
		t.Errorf("no flash with the count in:\n%s", page)
	}
}

func TestGlobalEditRefusesWithAMessageWhenNothingIsChecked(t *testing.T) {
	a, _, beta, tech := twoFeeds(t)

	for name, form := range map[string]url.Values{
		"no feed":  {"add_folder": {fmt.Sprint(tech)}},
		"conflict": {"feed": {fmt.Sprint(beta.ID)}, "add_folder": {fmt.Sprint(tech)}, "remove_folder": {fmt.Sprint(tech)}},
	} {
		resp := a.act(t, "/edit/feeds", form)

		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/edit" {
			t.Errorf("%s: %d → %q, want 303 → /edit", name, resp.StatusCode, resp.Header.Get("Location"))
		}
		want := map[string]string{"no feed": "No feed selected.", "conflict": "A folder cannot be both added and removed."}[name]
		if page := body(t, a.get(t, "/edit")); !strings.Contains(page, `<p class="flash error" role="alert">`+want+`</p>`) {
			t.Errorf("%s: no error flash %q", name, want)
		}
	}
	if got, _ := a.store.FeedByID(ctx, beta.ID); len(got.FolderIDs) != 0 {
		t.Errorf("a refused request changed Beta: %v", got.FolderIDs)
	}
	if resp := a.post(t, "/edit/feeds", url.Values{"feed": {fmt.Sprint(beta.ID)}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("without CSRF = %d, want 403", resp.StatusCode)
	}
}

func TestGlobalDeleteAsksForConfirmationThenDeletesTheCheckedFeeds(t *testing.T) {
	a, alpha, beta, _ := twoFeeds(t)

	resp := a.act(t, "/edit/delete", url.Values{"feed": {fmt.Sprint(alpha.ID), fmt.Sprint(beta.ID)}})
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first POST = %d, want the confirmation page (200)", resp.StatusCode)
	}
	for _, want := range []string{
		"Delete these feeds?", "<li>Alpha</li>", "<li>Beta</li>", "except the ones you starred",
		fmt.Sprintf(`<input type="hidden" name="feed" value="%d">`, alpha.ID),
		`<input type="hidden" name="confirm" value="1">`, `<a href="/edit">Cancel</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("confirmation page does not contain %q in:\n%s", want, page)
		}
	}
	if feeds, _ := a.feeds.Feeds(ctx); len(feeds) != 2 {
		t.Fatal("asking for confirmation deleted something")
	}

	resp = a.act(t, "/edit/delete", url.Values{"feed": {fmt.Sprint(alpha.ID), fmt.Sprint(beta.ID), "999"}, "confirm": {"1"}})

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/edit" {
		t.Fatalf("confirmed delete = %d → %q, want 303 → /edit", resp.StatusCode, resp.Header.Get("Location"))
	}
	if feeds, _ := a.feeds.Feeds(ctx); len(feeds) != 0 {
		t.Errorf("%d feeds left, want 0", len(feeds))
	}
	if page := body(t, a.get(t, "/edit")); !strings.Contains(page, "2 feeds deleted.") {
		t.Errorf("no flash with the count in:\n%s", page)
	}

	resp = a.act(t, "/edit/delete", url.Values{"confirm": {"1"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/edit" {
		t.Errorf("nothing checked = %d → %q, want 303 → /edit", resp.StatusCode, resp.Header.Get("Location"))
	}
	if page := body(t, a.get(t, "/edit")); !strings.Contains(page, "No feed selected.") {
		t.Error("no message when nothing is checked")
	}
	if resp := a.post(t, "/edit/delete", url.Values{"feed": {"1"}, "confirm": {"1"}}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("without CSRF = %d, want 403", resp.StatusCode)
	}
}

func TestReadingMenuLinksToTheEditPagesOfTheSelectionAndToAddAFeed(t *testing.T) {
	a, alpha, _, tech := twoFeeds(t)

	for path, want := range map[string]string{
		"/":                               `<a data-shortcut="edit" href="/edit">Edit</a>`,
		fmt.Sprintf("/feed/%d", alpha.ID): fmt.Sprintf(`<a data-shortcut="edit" href="/edit/feed/%d">Edit</a>`, alpha.ID),
		fmt.Sprintf("/folder/%d", tech):   fmt.Sprintf(`<a data-shortcut="edit" href="/edit/folder/%d">Edit</a>`, tech),
		"/starred":                        `<a href="/add">Add a feed</a>`,
		fmt.Sprintf("/feed/%d", alpha.ID) + "?page=1": `<a href="/add">Add a feed</a>`,
	} {
		if page := body(t, a.get(t, path)); !strings.Contains(page, want) {
			t.Errorf("GET %s: the menu does not contain %q", path, want)
		}
	}
}
