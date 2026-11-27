// SPDX-License-Identifier: GPL-3.0-or-later

package feed_test

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/mwolff44/mw-feed/internal/feed"
)

var fixedNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func parse(t *testing.T, doc string) feed.Parsed {
	t.Helper()
	p, err := feed.Parse([]byte(doc), fixedNow)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p
}

func TestParseRSS(t *testing.T) {
	p := parse(t, `<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/">
<channel>
  <title>My Blog</title>
  <link>https://blog.example/</link>
  <description>All about things</description>
  <item>
    <title>First post</title>
    <link>https://blog.example/1</link>
    <pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate>
    <description>Short summary</description>
    <content:encoded><![CDATA[<p>Full text</p>]]></content:encoded>
    <dc:creator>Alice</dc:creator>
  </item>
</channel></rss>`)

	if p.Title != "My Blog" || p.SiteURL != "https://blog.example/" || p.Description != "All about things" {
		t.Errorf("feed = %q %q %q", p.Title, p.SiteURL, p.Description)
	}
	if len(p.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(p.Items))
	}
	it := p.Items[0]
	if it.Title != "First post" || it.Link != "https://blog.example/1" || it.Author != "Alice" || it.Description != "Short summary" {
		t.Errorf("item = %+v", it)
	}
	if want := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC); !it.PublishedAt.Equal(want) {
		t.Errorf("PublishedAt = %v, want %v", it.PublishedAt, want)
	}
	if it.Content != "<p>Full text</p>" {
		t.Errorf("Content = %q, want the full text", it.Content)
	}
}

func TestParseAtomFallsBackToTheUpdatedDate(t *testing.T) {
	p := parse(t, `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Blog</title>
  <subtitle>Atom description</subtitle>
  <link rel="self" href="https://a.example/atom.xml"/>
  <link rel="alternate" href="https://a.example/"/>
  <entry>
    <title>Entry one</title>
    <link rel="alternate" href="https://a.example/e1"/>
    <id>urn:uuid:1</id>
    <updated>2026-05-01T10:00:00Z</updated>
    <summary>Entry summary</summary>
    <content type="html">&lt;p&gt;Entry content&lt;/p&gt;</content>
    <author><name>Bob</name></author>
  </entry>
</feed>`)

	if p.Title != "Atom Blog" || p.SiteURL != "https://a.example/" || p.Description != "Atom description" {
		t.Errorf("feed = %q %q %q", p.Title, p.SiteURL, p.Description)
	}
	it := p.Items[0]
	if it.Title != "Entry one" || it.Link != "https://a.example/e1" || it.Author != "Bob" || it.Description != "Entry summary" || it.Content != "<p>Entry content</p>" {
		t.Errorf("item = %+v", it)
	}
	if want := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC); !it.PublishedAt.Equal(want) {
		t.Errorf("PublishedAt = %v, want the updated date %v", it.PublishedAt, want)
	}
}

func TestParseJSONFeed(t *testing.T) {
	p := parse(t, `{
  "version": "https://jsonfeed.org/version/1.1",
  "title": "JSON Blog",
  "home_page_url": "https://j.example/",
  "description": "JSON description",
  "items": [{
    "id": "1", "url": "https://j.example/1", "title": "JSON item",
    "content_html": "<p>JSON content</p>", "summary": "JSON summary",
    "date_published": "2026-04-02T08:30:00Z",
    "authors": [{"name": "Carol"}]
  }]
}`)

	if p.Title != "JSON Blog" || p.SiteURL != "https://j.example/" || p.Description != "JSON description" {
		t.Errorf("feed = %q %q %q", p.Title, p.SiteURL, p.Description)
	}
	it := p.Items[0]
	if it.Title != "JSON item" || it.Link != "https://j.example/1" || it.Author != "Carol" || it.Description != "JSON summary" || it.Content != "<p>JSON content</p>" {
		t.Errorf("item = %+v", it)
	}
	if want := time.Date(2026, 4, 2, 8, 30, 0, 0, time.UTC); !it.PublishedAt.Equal(want) {
		t.Errorf("PublishedAt = %v, want %v", it.PublishedAt, want)
	}
}

func TestParseRejectsDocumentsThatAreNotFeeds(t *testing.T) {
	cases := map[string]string{
		"empty":                   "",
		"blank":                   "  \n\t ",
		"plain text":              "this is not a feed",
		"HTML page":               "<!doctype html><html><head><title>Hi</title></head><body><p>Not a feed</p></body></html>",
		"truncated RSS":           `<rss version="2.0"><channel><title>x</title><item><title>cut`,
		"XML that is not a feed":  `<?xml version="1.0"?><note><to>you</to></note>`,
		"JSON that is not a feed": `{"hello": "world"}`,
		"binary garbage":          "\x00\x01\x02\xff\xfe",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := feed.Parse([]byte(doc), fixedNow)

			if !errors.Is(err, feed.ErrInvalidFeed) {
				t.Errorf("error = %v, want feed.ErrInvalidFeed", err)
			}
		})
	}
}

func TestParseAcceptsAFeedWithoutItems(t *testing.T) {
	p := parse(t, `<rss version="2.0"><channel><title>Empty</title></channel></rss>`)

	if p.Title != "Empty" || len(p.Items) != 0 {
		t.Errorf("Parsed = %+v, want a feed titled Empty with no item", p)
	}
}

func TestParseItemDates(t *testing.T) {
	doc := `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom"><title>T</title>
  <entry><title>both</title><link href="https://x.example/both"/><id>1</id>
    <published>2026-01-01T00:00:00Z</published><updated>2026-03-01T00:00:00Z</updated></entry>
  <entry><title>updated only</title><link href="https://x.example/upd"/><id>2</id>
    <updated>2026-02-01T00:00:00Z</updated></entry>
  <entry><title>none</title><link href="https://x.example/none"/><id>3</id></entry>
  <entry><title>invalid</title><link href="https://x.example/bad"/><id>4</id>
    <published>not a date</published></entry>
</feed>`
	want := map[string]time.Time{
		"both":         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), // publication wins over update
		"updated only": time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		"none":         fixedNow,
		"invalid":      fixedNow,
	}

	p := parse(t, doc)

	if len(p.Items) != len(want) {
		t.Fatalf("items = %d, want %d", len(p.Items), len(want))
	}
	for _, it := range p.Items {
		if !it.PublishedAt.Equal(want[it.Title]) {
			t.Errorf("%q: PublishedAt = %v, want %v", it.Title, it.PublishedAt, want[it.Title])
		}
	}
}

// rssItem wraps item XML in a minimal RSS document.
func rssItem(item string) string {
	return `<?xml version="1.0"?><rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:media="http://search.yahoo.com/mrss/"><channel>
<title> My  <![CDATA[<i>Blog</i>]]> &amp; co </title><description><![CDATA[<p>Feed   about <b>things</b></p>]]></description>` + item + `</channel></rss>`
}

func TestParseTextFieldsArePlainText(t *testing.T) {
	cases := []struct {
		name, item, wantTitle, wantAuthor string
	}{
		{"tags, entities and spaces", `<item><title><![CDATA[<b>Bold</b> &amp; "quoted"
		   title]]></title><link>https://x.example/1</link><author>a@x.example (  <b>Ann</b>  Lee )</author></item>`,
			`Bold & "quoted" title`, "Ann Lee"},
		{"entity-encoded markup", `<item><title>Tom &amp; Jerry &lt;3 &lt;b&gt;fun&lt;/b&gt;</title><link>https://x.example/2</link></item>`,
			"Tom & Jerry <3 fun", ""},
		{"empty title falls back to the link", `<item><title></title><link>https://x.example/3</link></item>`,
			"https://x.example/3", ""},
		{"title made only of markup falls back to the link", `<item><title><![CDATA[<img src="https://x.example/a.png">]]></title><link>https://x.example/4</link></item>`,
			"https://x.example/4", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parse(t, rssItem(tc.item))

			if p.Items[0].Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", p.Items[0].Title, tc.wantTitle)
			}
			if p.Items[0].Author != tc.wantAuthor {
				t.Errorf("Author = %q, want %q", p.Items[0].Author, tc.wantAuthor)
			}
		})
	}
	p := parse(t, rssItem(`<item><title>x</title><link>https://x.example/5</link></item>`))
	if p.Title != "My Blog & co" || p.Description != "Feed about things" {
		t.Errorf("feed title/description = %q / %q, want plain text", p.Title, p.Description)
	}
}

func TestParseDescriptionIsPlainTextTruncatedTo500Characters(t *testing.T) {
	long := strings.Repeat("é", 600) // multi-byte: 500 characters, not 500 bytes
	cases := []struct {
		name, item, want string
	}{
		{"markup stripped before truncation", `<item><title>t</title><link>https://x.example/1</link><description><![CDATA[<p>Hello <b>world</b> &amp; all</p>]]></description></item>`, "Hello world & all"},
		{"truncated to 500 characters", `<item><title>t</title><link>https://x.example/2</link><description>` + long + `</description></item>`, strings.Repeat("é", 500)},
		{"markup is not counted nor cut", `<item><title>t</title><link>https://x.example/3</link><description><![CDATA[<a href="https://x.example/` + strings.Repeat("u", 600) + `">` + strings.Repeat("a", 499) + `</a>bcd]]></description></item>`, strings.Repeat("a", 499) + "b"},
		{"falls back to the content", `<item><title>t</title><link>https://x.example/4</link><content:encoded><![CDATA[<p>Only <i>content</i></p>]]></content:encoded></item>`, "Only content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parse(t, rssItem(tc.item))

			if got := p.Items[0].Description; got != tc.want {
				t.Errorf("Description = %q (%d runes), want %q", got, utf8.RuneCountInString(got), tc.want)
			}
		})
	}
}

func TestParseContentFallsBackToTheDescription(t *testing.T) {
	p := parse(t, rssItem(`<item><title>t</title><link>https://x.example/1</link><description><![CDATA[<p>Summary only</p>]]></description></item>`))

	if got := p.Items[0].Content; got != "<p>Summary only</p>" {
		t.Errorf("Content = %q, want the description as content", got)
	}
}

func TestParseLinksAndVia(t *testing.T) {
	cases := []struct {
		name     string
		item     string
		wantKept bool
		wantLink string
		wantVia  string
	}{
		{"guid on another host becomes via", `<item><title>t</title><link>https://blog.example/1</link><guid>https://other.example/x</guid></item>`, true, "https://blog.example/1", "https://other.example/x"},
		{"guid on the same host is not via", `<item><title>t</title><link>https://blog.example/1</link><guid>https://blog.example/guid/1</guid></item>`, true, "https://blog.example/1", ""},
		{"guid that is not a URL is not via", `<item><title>t</title><link>https://blog.example/1</link><guid isPermaLink="false">tag:blog.example,2026:1</guid></item>`, true, "https://blog.example/1", ""},
		{"no link: an http(s) guid is the link", `<item><title>t</title><guid>https://blog.example/guid/9</guid></item>`, true, "https://blog.example/guid/9", ""},
		{"no link and no usable guid: item ignored", `<item><title>t</title><guid isPermaLink="false">abc123</guid></item>`, false, "", ""},
		{"no link and no guid: item ignored", `<item><title>t</title></item>`, false, "", ""},
		{"javascript: link is never kept", `<item><title>t</title><link>javascript:alert(1)</link></item>`, false, "", ""},
		{"javascript: link falls back to an http(s) guid", `<item><title>t</title><link>javascript:alert(1)</link><guid>https://blog.example/ok</guid></item>`, true, "https://blog.example/ok", ""},
		{"data: and ftp: links are not kept", `<item><title>t</title><link>ftp://blog.example/f</link><guid>data:text/html,x</guid></item>`, false, "", ""},
		{"relative link is not kept", `<item><title>t</title><link>/post/1</link></item>`, false, "", ""},
		{"link is trimmed", "<item><title>t</title><link>\n  https://blog.example/trim \n</link></item>", true, "https://blog.example/trim", ""},
		{"via that is not http(s) is dropped", `<item><title>t</title><link>https://blog.example/1</link><guid>javascript:alert(1)</guid></item>`, true, "https://blog.example/1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parse(t, rssItem(tc.item))

			if !tc.wantKept {
				if len(p.Items) != 0 {
					t.Fatalf("item was kept: %+v", p.Items[0])
				}
				return
			}
			if len(p.Items) != 1 {
				t.Fatalf("items = %d, want 1", len(p.Items))
			}
			if p.Items[0].Link != tc.wantLink || p.Items[0].Via != tc.wantVia {
				t.Errorf("Link = %q, Via = %q; want %q, %q", p.Items[0].Link, p.Items[0].Via, tc.wantLink, tc.wantVia)
			}
		})
	}
}

func TestParseSiteURLMustBeHTTP(t *testing.T) {
	for doc, want := range map[string]string{
		`<rss version="2.0"><channel><title>t</title><link>https://site.example/</link></channel></rss>`: "https://site.example/",
		`<rss version="2.0"><channel><title>t</title><link>javascript:alert(1)</link></channel></rss>`:   "",
		`<rss version="2.0"><channel><title>t</title><link>/relative</link></channel></rss>`:             "",
		`<rss version="2.0"><channel><title>t</title></channel></rss>`:                                   "",
	} {
		if got := parse(t, doc).SiteURL; got != want {
			t.Errorf("SiteURL = %q, want %q for %s", got, want, doc)
		}
	}
}

// attributeNames returns the names of all attributes in an HTML fragment.
func attributeNames(t *testing.T, fragment string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	z := html.NewTokenizer(strings.NewReader(fragment))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return names
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			for _, a := range tok.Attr {
				names[a.Key] = true
			}
		}
	}
}

func TestParseRendersEnclosuresAndThumbnails(t *testing.T) {
	const base = `<title>t</title><link>https://x.example/1</link><content:encoded><![CDATA[<p>Body</p>]]></content:encoded>`
	cases := []struct {
		name string
		item string
		want []string // fragments of the final content, in order
	}{
		{"image", `<item>` + base + `<enclosure url="https://e.example/a.png" type="image/png" length="1"/></item>`,
			[]string{"<p>Body</p>", "<br><br>", `<img src="https://e.example/a.png">`}},
		{"extension is case-insensitive and read from the path", `<item>` + base + `<enclosure url="https://e.example/B.JPG?size=large" type="image/jpeg" length="1"/></item>`,
			[]string{`<img src="https://e.example/B.JPG?size=large">`}},
		{"audio", `<item>` + base + `<enclosure url="https://e.example/a.mp3" type="audio/mpeg" length="1"/></item>`,
			[]string{"<audio controls", `<source src="https://e.example/a.mp3">`, `<a href="https://e.example/a.mp3">https://e.example/a.mp3</a>`, "</audio>"}},
		{"video", `<item>` + base + `<enclosure url="https://e.example/v.webm" type="video/webm" length="1"/></item>`,
			[]string{"<video controls", `<source src="https://e.example/v.webm">`, "</video>"}},
		{"youtube", `<item>` + base + `<enclosure url="https://www.youtube.com/v/abc123" type="application/x-shockwave-flash" length="1"/></item>`,
			[]string{`<iframe src="https://www.youtube-nocookie.com/embed/abc123"`, `width="640"`, `height="360"`, "allowfullscreen", `referrerpolicy="strict-origin-when-cross-origin"`}},
		{"no extension, not youtube: plain link", `<item>` + base + `<enclosure url="https://e.example/stream" type="audio/mpeg" length="1"/></item>`,
			[]string{`<a href="https://e.example/stream">https://e.example/stream</a>`}},
		{"unknown extension: plain link", `<item>` + base + `<enclosure url="https://e.example/doc.pdf" type="application/pdf" length="1"/></item>`,
			[]string{`<a href="https://e.example/doc.pdf">https://e.example/doc.pdf</a>`}},
		{"media:content", `<item>` + base + `<media:content url="https://e.example/m.gif" type="image/gif"/></item>`,
			[]string{`<img src="https://e.example/m.gif">`}},
		{"media:group", `<item>` + base + `<media:group><media:content url="https://e.example/g.mp4"/></media:group></item>`,
			[]string{"<video controls", `<source src="https://e.example/g.mp4">`}},
		{"thumbnail comes after the enclosures, with a single break", `<item>` + base +
			`<enclosure url="https://e.example/a.png" type="image/png" length="1"/><media:thumbnail url="https://e.example/t.png"/></item>`,
			[]string{`<img src="https://e.example/a.png">`, "<br>", `<img src="https://e.example/t.png">`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parse(t, rssItem(tc.item))

			content := p.Items[0].Content
			pos := 0
			for _, want := range tc.want {
				i := strings.Index(content[pos:], want)
				if i < 0 {
					t.Fatalf("content lacks %q after offset %d:\n%s", want, pos, content)
				}
				pos += i + len(want)
			}
		})
	}
}

func TestParseSanitizesTheWholeContentIncludingEnclosures(t *testing.T) {
	cases := map[string]string{
		"script in the feed content":                   `<item><title>t</title><link>https://x.example/1</link><content:encoded><![CDATA[<p>ok</p><script>alert(1)</script><img src="https://e.example/a.png" onerror="alert(2)">]]></content:encoded></item>`,
		"quote breaking out of an enclosure attribute": `<item><title>t</title><link>https://x.example/2</link><enclosure url="https://e.example/a.png&quot; onerror=&quot;alert(1)" type="image/png" length="1"/></item>`,
		"javascript: enclosure":                        `<item><title>t</title><link>https://x.example/3</link><enclosure url="javascript:alert(1)//x.jpg" type="image/jpeg" length="1"/></item>`,
		"javascript: audio enclosure":                  `<item><title>t</title><link>https://x.example/4</link><enclosure url="javascript:alert(1)//x.mp3" type="audio/mpeg" length="1"/></item>`,
		"html in an enclosure URL":                     `<item><title>t</title><link>https://x.example/5</link><enclosure url="https://e.example/&lt;script&gt;alert(1)&lt;/script&gt;" type="x/y" length="1"/></item>`,
		"javascript: thumbnail":                        `<item><title>t</title><link>https://x.example/6</link><media:thumbnail url="javascript:alert(1)//t.png"/></item>`,
	}
	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			content := parse(t, rssItem(item)).Items[0].Content

			if strings.Contains(strings.ToLower(content), "<script") || strings.Contains(strings.ToLower(content), "javascript:") {
				t.Errorf("dangerous markup survived:\n%s", content)
			}
			for attr := range attributeNames(t, content) {
				if strings.HasPrefix(attr, "on") {
					t.Errorf("event handler attribute %q survived:\n%s", attr, content)
				}
			}
		})
	}
}
