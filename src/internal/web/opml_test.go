// SPDX-License-Identifier: GPL-3.0-or-later

package web_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const importFile = `<?xml version="1.0" encoding="UTF-8"?><opml version="1.0"><head><title>t</title></head><body>
<outline text="Tech"><outline type="rss" title="Imported A" xmlUrl="http://a.example/feed" htmlUrl="http://a.example/"/></outline>
<outline type="rss" title="Bad feed" xmlUrl="ftp://x.example/feed"/>
</body></opml>`

// upload posts a multipart import form: the CSRF token, the overwrite box and a file.
func (a *app) upload(t *testing.T, content string, overwrite bool, withFile bool) *http.Response {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("csrf_token", a.token(t))
	if overwrite {
		_ = w.WriteField("overwrite", "1")
	}
	if withFile {
		part, err := w.CreateFormFile("file", "feeds.opml")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		_, _ = part.Write([]byte(content))
	}
	_ = w.Close()
	resp, err := a.client.Post(a.server.URL+"/import", w.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("POST /import: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestExportDownloadsAnOPMLFileOfTheFeedsAndFolders(t *testing.T) {
	a, alpha, _, _ := twoFeeds(t)

	resp := a.get(t, "/export")
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /export = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/x-opml; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename="mw-feed.opml"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	for _, want := range []string{`<opml version="1.0">`, `xmlUrl="` + alpha.XMLURL + `"`, `text="Tech"`, `title="Beta"`} {
		if !strings.Contains(page, want) {
			t.Errorf("export does not contain %q in:\n%s", want, page)
		}
	}
	if resp := a.anotherBrowser(t).get(t, "/export"); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Errorf("a visitor got the export: %d", resp.StatusCode)
	}
}

func TestImportFormAndReport(t *testing.T) {
	a, _ := seeded(t)

	form := body(t, a.get(t, "/import"))
	for _, want := range []string{
		`<form method="post" action="/import" enctype="multipart/form-data">`,
		`<input type="file" name="file"`, `name="overwrite"`, "10 MB",
	} {
		if !strings.Contains(form, want) {
			t.Errorf("import form does not contain %q", want)
		}
	}

	resp := a.upload(t, importFile, false, true)
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /import = %d, want 200:\n%s", resp.StatusCode, page)
	}
	for _, want := range []string{
		"<li>Feeds imported: 1</li>", "<li>Feeds updated: 0</li>", "<li>Feeds skipped (already present): 0</li>",
		"<li>Feeds rejected: 1</li>", "<li>Folders created: 1</li>",
		"<li>Bad feed (ftp://x.example/feed): Invalid URL</li>",
		`action="/update"`, "Update all",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("report does not contain %q in:\n%s", want, page)
		}
	}
	feeds, err := a.feeds.Feeds(ctx)
	if err != nil || len(feeds) != 2 { // the seeded feed and the imported one
		t.Fatalf("Feeds = %+v, %v; want 2", feeds, err)
	}

	// The same file again: the feed exists, so it is skipped; with overwrite it is updated.
	if page := body(t, a.upload(t, importFile, false, true)); !strings.Contains(page, "<li>Feeds skipped (already present): 1</li>") {
		t.Errorf("second import does not skip the existing feed:\n%s", page)
	}
	if page := body(t, a.upload(t, importFile, true, true)); !strings.Contains(page, "<li>Feeds updated: 1</li>") {
		t.Errorf("an import with overwrite does not update the existing feed:\n%s", page)
	}
}

func TestImportShowsWhyAFileWasRefusedAndImportsNothing(t *testing.T) {
	a, _ := seeded(t)
	cases := []struct {
		name, content string
		withFile      bool
		want          string
	}{
		{"no file", "", false, "Choose an OPML file to import."},
		{"not an OPML file", "<html><body>hello</body></html>", true, "This file is not an OPML file."},
		{"entity", `<?xml version="1.0"?><!DOCTYPE opml [<!ENTITY x "y">]><opml version="1.0"><body/></opml>`, true, "The file declares XML entities, which are not allowed."},
		{"encoding", `<?xml version="1.0" encoding="nope-9"?><opml version="1.0"><body/></opml>`, true, "The character encoding of the file is not supported."},
		{"too deep", `<opml version="1.0"><body>` + strings.Repeat("<outline text=\"x\">", 20) + strings.Repeat("</outline>", 20) + `</body></opml>`, true, "The file is nested too deeply."},
		{"too large", strings.Repeat("a", 11<<20), true, "The file is too large (10 MB at most)."},
	}
	for _, tc := range cases {
		resp := a.upload(t, tc.content, false, tc.withFile)
		page := body(t, resp)

		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(page, `<p class="error" role="alert">`+tc.want+`</p>`) {
			t.Errorf("%s: status %d, message %q missing in:\n%s", tc.name, resp.StatusCode, tc.want, page)
		}
		if !strings.Contains(page, `action="/import"`) {
			t.Errorf("%s: the form is not shown again", tc.name)
		}
	}
	if feeds, _ := a.feeds.Feeds(ctx); len(feeds) != 1 {
		t.Errorf("a refused import created feeds: %d, want 1", len(feeds))
	}
	if resp := a.post(t, "/import", url.Values{}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /import without CSRF = %d, want 403", resp.StatusCode)
	}
}
