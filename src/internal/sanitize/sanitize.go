// SPDX-License-Identifier: GPL-3.0-or-later

// Package sanitize cleans HTML coming from feeds with a single policy.
package sanitize

import (
	"html"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

var policy = newPolicy()

func newPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// Relative URLs would point at this application, not at the feed's site.
	p.AllowRelativeURLs(false)
	// Link attributes (rel, redirector) are decided when rendering, not stored.
	p.RequireNoFollowOnLinks(false)

	// Media rendered from enclosures (plan 4.4). bluemonday only checks the URL
	// scheme of some elements and <source> is not one of them, so the scheme is
	// enforced here: http(s) only (no data:, javascript:...).
	p.AllowElements("audio", "video", "source")
	p.AllowAttrs("controls").OnElements("audio", "video")
	p.AllowAttrs("src").Matching(httpURL).OnElements("source")

	// The only iframes allowed are YouTube's no-cookie player, over https.
	p.AllowElements("iframe")
	p.AllowAttrs("src").Matching(youtubeNoCookie).OnElements("iframe")
	p.AllowAttrs("width", "height").Matching(bluemonday.Number).OnElements("iframe")
	p.AllowAttrs("allowfullscreen").Matching(regexp.MustCompile(`^(allowfullscreen)?$`)).OnElements("iframe")
	p.AllowAttrs("referrerpolicy").Matching(regexp.MustCompile(`^strict-origin-when-cross-origin$`)).OnElements("iframe")
	return p
}

var httpURL = regexp.MustCompile(`(?i)^https?://[^\s]+$`)

var youtubeNoCookie = regexp.MustCompile(`^https://www\.youtube-nocookie\.com/[^\s]*$`)

// HTML returns s with everything dangerous removed.
func HTML(s string) string {
	return policy.Sanitize(s)
}

var textPolicy = bluemonday.StrictPolicy()

// Text turns s into plain text: tags are removed, entities decoded, and runs of
// white space (including line breaks) collapsed to one space. The result must
// still be escaped when written into HTML.
func Text(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(textPolicy.Sanitize(s))), " ")
}
