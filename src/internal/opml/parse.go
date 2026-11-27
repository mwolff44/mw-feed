// SPDX-License-Identifier: GPL-3.0-or-later

// Package opml reads and writes OPML files, the usual way to move a list of
// feeds from one reader to another.
package opml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/net/html/charset"

	"github.com/mwolff44/mw-feed/internal/sanitize"
)

// Feed is a feed listed in an OPML file.
type Feed struct {
	Title       string
	Description string
	XMLURL      string
	SiteURL     string
	// Folders are the titles of the folders the feed is in, outermost first.
	Folders []string
}

// Rejected is a feed of the file that cannot be imported.
type Rejected struct {
	Title  string
	URL    string
	Reason string
}

// Limits and errors of Parse.
const (
	// MaxSize is the largest file accepted, in bytes.
	MaxSize = 10 << 20
	// MaxDepth is the deepest nesting of outlines accepted.
	MaxDepth = 16
)

var (
	// ErrInvalidFormat means the file is not an OPML document with a body.
	ErrInvalidFormat = errors.New("not an OPML file")
	// ErrTooLarge means the file is larger than MaxSize.
	ErrTooLarge = errors.New("OPML file too large")
	// ErrEntityNotAllowed means the file declares XML entities, which OPML never needs.
	ErrEntityNotAllowed = errors.New("XML entity declarations are not allowed")
	// ErrTooDeep means outlines are nested deeper than MaxDepth.
	ErrTooDeep = errors.New("OPML file nested too deeply")
	// ErrUnsupportedEncoding means the declared character encoding is unknown.
	ErrUnsupportedEncoding = errors.New("unsupported character encoding")
)

// ReasonInvalidURL is the reason of a feed rejected for its URL.
const ReasonInvalidURL = "invalid URL"

// Document is the content of an OPML file.
type Document struct {
	// Folders lists every folder of the file, empty ones included, in order of first appearance.
	Folders  []string
	Feeds    []Feed
	Rejected []Rejected
}

// level is one open outline: a folder, or an outline that is not one.
type level struct {
	folder string // "" if the outline is not a folder
	feed   bool   // a feed: whatever it contains is ignored
}

// Parse reads an OPML file.
func Parse(data []byte) (Document, error) {
	if len(data) > MaxSize {
		return Document{}, ErrTooLarge
	}
	var (
		doc   Document
		stack []level
		seen  = map[string]bool{} // folders already listed
		index = map[string]int{}  // feed URL -> position in doc.Feeds
	)
	inFeed := func() bool {
		for _, l := range stack {
			if l.feed {
				return true
			}
		}
		return false
	}
	folders := func() []string {
		var out []string
		for _, l := range stack {
			if l.folder != "" {
				out = append(out, l.folder)
			}
		}
		return out
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		r, err := charset.NewReaderLabel(label, input)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedEncoding, label)
		}
		return r, nil
	}
	var depth int // depth of XML elements: 1 is the root
	var sawRoot, inBody, sawBody bool
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if !sawBody {
				return Document{}, fmt.Errorf("%w: no body", ErrInvalidFormat)
			}
			return doc, nil
		}
		if errors.Is(err, ErrUnsupportedEncoding) {
			return Document{}, err
		}
		if err != nil {
			return Document{}, fmt.Errorf("%w: %v", ErrInvalidFormat, err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			if bytes.Contains(bytes.ToUpper(t), []byte("<!ENTITY")) {
				return Document{}, ErrEntityNotAllowed
			}
		case xml.StartElement:
			depth++
			if !sawRoot {
				sawRoot = true
				if !strings.EqualFold(t.Name.Local, "opml") {
					return Document{}, fmt.Errorf("%w: root element is %q", ErrInvalidFormat, t.Name.Local)
				}
				continue
			}
			if depth == 2 && strings.EqualFold(t.Name.Local, "body") {
				inBody, sawBody = true, true
				continue
			}
			if !inBody || !strings.EqualFold(t.Name.Local, "outline") {
				continue
			}
			if len(stack) >= MaxDepth {
				return Document{}, ErrTooDeep
			}
			a := attrs(t)
			if inFeed() {
				stack = append(stack, level{})
				continue
			}
			if a["xmlurl"] != "" {
				ancestors := folders()
				title := sanitize.Text(a["title"])
				if title == "" {
					title = sanitize.Text(a["text"])
				}
				xmlURL := httpURL(a["xmlurl"])
				if xmlURL == "" {
					doc.Rejected = append(doc.Rejected, Rejected{Title: title, URL: strings.TrimSpace(a["xmlurl"]), Reason: ReasonInvalidURL})
				} else if i, dup := index[xmlURL]; dup {
					for _, f := range ancestors { // the first copy wins; only its folders grow
						if !slices.Contains(doc.Feeds[i].Folders, f) {
							doc.Feeds[i].Folders = append(doc.Feeds[i].Folders, f)
						}
					}
				} else {
					if title == "" {
						title = hostName(xmlURL)
					}
					index[xmlURL] = len(doc.Feeds)
					doc.Feeds = append(doc.Feeds, Feed{
						Title: title, Description: sanitize.Text(a["description"]), XMLURL: xmlURL,
						SiteURL: httpURL(a["htmlurl"]), Folders: ancestors,
					})
				}
				stack = append(stack, level{feed: true})
				continue
			}
			name := sanitize.Text(a["title"])
			if name == "" {
				name = sanitize.Text(a["text"])
			}
			if name != "" && !seen[name] {
				seen[name] = true
				doc.Folders = append(doc.Folders, name)
			}
			stack = append(stack, level{folder: name})
		case xml.EndElement:
			depth--
			if depth == 1 && strings.EqualFold(t.Name.Local, "body") {
				inBody = false
			}
			if inBody && strings.EqualFold(t.Name.Local, "outline") && len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
}

// attrs returns the attributes of an element, with lower-case names: some
// exporters write xmlurl or htmlurl.
func attrs(e xml.StartElement) map[string]string {
	out := make(map[string]string, len(e.Attr))
	for _, a := range e.Attr {
		out[strings.ToLower(a.Name.Local)] = a.Value
	}
	return out
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

// hostName returns the host of a URL, without the port.
func hostName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Hostname()
}
