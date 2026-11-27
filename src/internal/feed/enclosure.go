// SPDX-License-Identifier: GPL-3.0-or-later

package feed

import (
	"html"
	"net/url"
	"path"
	"strings"

	"github.com/mmcdole/gofeed"
)

// enclosureURLs returns the http(s) URLs of the enclosures of an item:
// <enclosure> and <media:content>, also inside <media:group>.
func enclosureURLs(it *gofeed.Item) []string {
	var urls []string
	add := func(raw string) {
		if u := httpURL(raw); u != "" {
			urls = append(urls, u)
		}
	}
	for _, e := range it.Enclosures {
		if e != nil {
			add(e.URL)
		}
	}
	media := it.Extensions["media"]
	for _, c := range media["content"] {
		add(c.Attrs["url"])
	}
	for _, g := range media["group"] {
		for _, c := range g.Children["content"] {
			add(c.Attrs["url"])
		}
	}
	return urls
}

// thumbnailURL returns the http(s) URL of the first <media:thumbnail>, if any.
func thumbnailURL(it *gofeed.Item) string {
	for _, t := range it.Extensions["media"]["thumbnail"] {
		if u := httpURL(t.Attrs["url"]); u != "" {
			return u
		}
	}
	return ""
}

// renderEnclosure returns the HTML showing an enclosure URL, by file type.
func renderEnclosure(raw string) string {
	escaped := html.EscapeString(raw)
	link := `<a href="` + escaped + `">` + escaped + `</a>`
	var ext string
	if u, err := url.Parse(raw); err == nil {
		ext = strings.ToLower(path.Ext(u.Path))
	}
	switch ext {
	case "":
		if strings.HasPrefix(raw, "https://www.youtube.com") {
			embed := strings.ReplaceAll(strings.ReplaceAll(raw, "www.youtube.com", "www.youtube-nocookie.com"), "/v/", "/embed/")
			return `<iframe src="` + html.EscapeString(embed) + `" width="640" height="360" allowfullscreen referrerpolicy="strict-origin-when-cross-origin"></iframe>`
		}
	case ".jpg", ".jpeg", ".png", ".gif":
		return `<img src="` + escaped + `">`
	case ".mp3", ".oga", ".wav":
		return `<audio controls><source src="` + escaped + `">` + link + `</audio>`
	case ".mp4", ".ogg", ".webm":
		return `<video controls><source src="` + escaped + `">` + link + `</video>`
	}
	return link
}

// fullContent returns the content of an item followed by its enclosures and
// thumbnail, as HTML that is not sanitised yet.
func fullContent(it *gofeed.Item, content string) string {
	var b strings.Builder
	b.WriteString(content)
	for _, u := range enclosureURLs(it) {
		b.WriteString("<br><br>" + renderEnclosure(u))
	}
	if t := thumbnailURL(it); t != "" {
		b.WriteString("<br>" + renderEnclosure(t))
	}
	return b.String()
}
