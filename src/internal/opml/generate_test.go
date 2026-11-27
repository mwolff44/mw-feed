// SPDX-License-Identifier: GPL-3.0-or-later

package opml_test

import (
	"encoding/xml"
	"reflect"
	"strings"
	"testing"

	"github.com/mwolff44/mw-feed/internal/opml"
)

func TestGenerateProducesTheDocumentedFormat(t *testing.T) {
	doc := opml.Document{
		Folders: []string{"Tech", "Empty"},
		Feeds: []opml.Feed{
			{Title: "Solo", XMLURL: "https://solo.example/feed"},
			{Title: "A", Description: "About <A>", XMLURL: "https://a.example/feed", SiteURL: "https://a.example/", Folders: []string{"Tech"}},
			{Title: "B", XMLURL: "https://b.example/feed", Folders: []string{"Tech"}},
		},
	}

	out, err := opml.Generate(doc)

	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<opml version="1.0">
  <head>
    <title>mw-feed</title>
  </head>
  <body>
    <outline title="Solo" text="Solo" xmlUrl="https://solo.example/feed" type="rss"></outline>
    <outline text="Tech" title="Tech">
      <outline title="A" text="A" description="About &lt;A&gt;" xmlUrl="https://a.example/feed" htmlUrl="https://a.example/" type="rss"></outline>
      <outline title="B" text="B" xmlUrl="https://b.example/feed" type="rss"></outline>
    </outline>
    <outline text="Empty" title="Empty"></outline>
  </body>
</opml>
`
	if string(out) != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
}

func TestGenerateEscapesSpecialCharactersExactlyOnce(t *testing.T) {
	doc := opml.Document{
		Folders: []string{`R&D "lab" 2<3`},
		Feeds: []opml.Feed{{
			Title: `Tom & Jerry's "show" <3`, Description: "line 1\nline 2 — café 日本語 🙂",
			XMLURL: "https://a.example/feed?x=1&y=2", Folders: []string{`R&D "lab" 2<3`},
		}},
	}

	out, err := opml.Generate(doc)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if err := xml.Unmarshal(out, new(struct{ XMLName xml.Name })); err != nil {
		t.Fatalf("the output is not well-formed XML: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "&amp;amp;") {
		t.Errorf("a character was escaped twice (the PHP bug):\n%s", out)
	}
	back, err := opml.Parse(out)
	if err != nil {
		t.Fatalf("Parse(Generate(doc)): %v", err)
	}
	if got := back.Feeds[0]; got.Title != doc.Feeds[0].Title || got.XMLURL != doc.Feeds[0].XMLURL ||
		got.Description != "line 1 line 2 — café 日本語 🙂" /* white space is normalised on import */ {
		t.Errorf("feed after a round trip = %+v", got)
	}
	if !reflect.DeepEqual(back.Folders, doc.Folders) {
		t.Errorf("folders after a round trip = %q, want %q", back.Folders, doc.Folders)
	}
}

func TestParseOfGenerateGivesTheSameDocument(t *testing.T) {
	docs := map[string]opml.Document{
		"nothing": {},
		"only root feeds": {Feeds: []opml.Feed{
			{Title: "A", XMLURL: "https://a.example/feed"}, {Title: "B", XMLURL: "https://b.example/feed", SiteURL: "https://b.example/"}}},
		"folders, a feed in two of them, an empty folder": {
			Folders: []string{"One", "Two", "Empty"},
			Feeds: []opml.Feed{
				{Title: "Free", XMLURL: "https://free.example/feed"},
				{Title: "Both", Description: "d", XMLURL: "https://both.example/feed", Folders: []string{"One", "Two"}},
				{Title: "Only two", XMLURL: "https://two.example/feed", Folders: []string{"Two"}},
			}},
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			out, err := opml.Generate(doc)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			back, err := opml.Parse(out)

			if err != nil {
				t.Fatalf("Parse: %v\n%s", err, out)
			}
			if !reflect.DeepEqual(back, doc) {
				t.Errorf("after a round trip:\n%+v\nwant:\n%+v\nfile:\n%s", back, doc, out)
			}
		})
	}
}
