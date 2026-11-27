// SPDX-License-Identifier: GPL-3.0-or-later

// Package feed parses downloaded feeds and applies the update rules.
package feed

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/mwolff44/mw-feed/internal/sanitize"
)

// maxDescriptionChars is the length of the short description, in characters.
const maxDescriptionChars = 500

// ErrInvalidFeed means the document is not a readable feed.
var ErrInvalidFeed = errors.New("invalid feed")

// Parsed is a feed as read from a document, normalised.
type Parsed struct {
	Title       string
	Description string
	SiteURL     string
	Items       []Entry
}

// Entry is one normalised entry of a feed.
type Entry struct {
	Title       string
	Link        string
	Via         string
	Author      string
	Description string
	Content     string
	PublishedAt time.Time
}

// Parse reads a feed document (RSS, Atom, RDF or JSON Feed). now replaces
// missing or invalid item dates.
func Parse(data []byte, now time.Time) (Parsed, error) {
	f, err := gofeed.NewParser().Parse(bytes.NewReader(data))
	if err != nil {
		return Parsed{}, fmt.Errorf("%w: %v", ErrInvalidFeed, err)
	}
	// gofeed takes any JSON object for a JSON Feed: require the declared version.
	if f.FeedType == "json" && !strings.HasPrefix(f.FeedVersion, "https://jsonfeed.org/version/") {
		return Parsed{}, fmt.Errorf("%w: not a JSON Feed", ErrInvalidFeed)
	}
	out := Parsed{Title: sanitize.Text(f.Title), Description: sanitize.Text(f.Description), SiteURL: httpURL(f.Link)}
	for _, gi := range f.Items {
		link := httpURL(gi.Link)
		if link == "" {
			link = httpURL(gi.GUID)
		}
		if link == "" {
			continue // an item without a usable link is ignored
		}
		it := Entry{Link: link, Via: via(gi.GUID, link)}
		content := gi.Content
		if content == "" {
			content = gi.Description
		}
		// One sanitising pass, at the end, on everything that will be shown.
		it.Content = sanitize.HTML(fullContent(gi, content))
		it.Title = sanitize.Text(gi.Title)
		if it.Title == "" {
			it.Title = it.Link
		}
		it.Author = sanitize.Text(authorName(gi))
		summary := gi.Description
		if summary == "" {
			summary = gi.Content
		}
		it.Description = truncate(sanitize.Text(summary), maxDescriptionChars)
		it.PublishedAt = now
		switch {
		case gi.PublishedParsed != nil:
			it.PublishedAt = *gi.PublishedParsed
		case gi.UpdatedParsed != nil:
			it.PublishedAt = *gi.UpdatedParsed
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

// authorName returns the name of the first author of an item, if any.
func authorName(it *gofeed.Item) string {
	if it.Author != nil && it.Author.Name != "" {
		return it.Author.Name
	}
	for _, a := range it.Authors {
		if a != nil && a.Name != "" {
			return a.Name
		}
	}
	return ""
}

// truncate keeps the first n characters (Unicode code points) of s.
func truncate(s string, n int) string {
	count := 0
	for pos := range s {
		if count == n {
			return s[:pos]
		}
		count++
	}
	return s
}

// httpURL returns raw, trimmed, if it is an absolute http or https URL; else "".
func httpURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return raw
}

// via returns the GUID of an item when it is an http(s) URL on another host
// than the item link, and "" otherwise.
func via(guid, link string) string {
	guid = httpURL(guid)
	if guid == "" {
		return ""
	}
	g, errG := url.Parse(guid)
	l, errL := url.Parse(link)
	if errG != nil || errL != nil || strings.EqualFold(g.Hostname(), l.Hostname()) {
		return ""
	}
	return guid
}
