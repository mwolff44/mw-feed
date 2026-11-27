// SPDX-License-Identifier: GPL-3.0-or-later

package opml

import (
	"encoding/xml"
	"fmt"
	"slices"
)

type feedOutline struct {
	XMLName     xml.Name `xml:"outline"`
	Title       string   `xml:"title,attr"`
	Text        string   `xml:"text,attr"`
	Description string   `xml:"description,attr,omitempty"`
	XMLURL      string   `xml:"xmlUrl,attr"`
	HTMLURL     string   `xml:"htmlUrl,attr,omitempty"`
	Type        string   `xml:"type,attr"`
}

type folderOutline struct {
	XMLName  xml.Name      `xml:"outline"`
	Text     string        `xml:"text,attr"`
	Title    string        `xml:"title,attr"`
	Children []feedOutline `xml:"outline"`
}

type opmlFile struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    struct {
		Title string `xml:"title"`
	} `xml:"head"`
	Body struct {
		Outlines []any `xml:",any"`
	} `xml:"body"`
}

func newFeedOutline(f Feed) feedOutline {
	return feedOutline{
		Title: f.Title, Text: f.Title, Description: f.Description,
		XMLURL: f.XMLURL, HTMLURL: f.SiteURL, Type: "rss",
	}
}

// Generate writes an OPML file: first the feeds that are in no folder, then one
// outline per folder, empty ones included. A feed in several folders appears in
// each of them. Folders are flat: an OPML written here has no sub-folder.
func Generate(doc Document) ([]byte, error) {
	var file opmlFile
	file.Version = "1.0"
	file.Head.Title = "mw-feed"

	// Folders in the given order, then any folder only the feeds mention.
	folders := slices.Clone(doc.Folders)
	for _, f := range doc.Feeds {
		for _, name := range f.Folders {
			if !slices.Contains(folders, name) {
				folders = append(folders, name)
			}
		}
	}

	for _, f := range doc.Feeds {
		if len(f.Folders) == 0 {
			file.Body.Outlines = append(file.Body.Outlines, newFeedOutline(f))
		}
	}
	for _, name := range folders {
		folder := folderOutline{Text: name, Title: name}
		for _, f := range doc.Feeds {
			if slices.Contains(f.Folders, name) {
				folder.Children = append(folder.Children, newFeedOutline(f))
			}
		}
		file.Body.Outlines = append(file.Body.Outlines, folder)
	}

	body, err := xml.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("write OPML: %w", err)
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}
