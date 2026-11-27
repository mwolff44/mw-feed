// SPDX-License-Identifier: GPL-3.0-or-later

// Package anonymize rewrites the external links of article content at display
// time: a redirector prefix or rel="noreferrer", and opening in a new tab.
// Images, media and anything that is not an absolute http(s) link are left alone.
package anonymize

import (
	"bytes"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// NoReferrer is the Redirector value that asks for rel="noreferrer" instead of a prefix.
const NoReferrer = "noreferrer"

// Options are the settings that change how links are displayed.
type Options struct {
	// Redirector is empty, NoReferrer, or a URL prefix put before the link.
	Redirector string
	// Blank opens the links in a new tab.
	Blank bool
}

// Attrs are the attributes of one link; empty ones are not to be written.
type Attrs struct {
	Href, Rel, Target string
}

func (o Options) isZero() bool { return o.Redirector == "" && !o.Blank }

// isHTTP reports whether raw is an absolute http or https URL.
func isHTTP(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// relTokens are the rel values the options ask for, in order.
func (o Options) relTokens() []string {
	var tokens []string
	if o.Blank || o.Redirector != "" {
		tokens = append(tokens, "noopener")
	}
	if o.Redirector != "" {
		tokens = append(tokens, "noreferrer")
	}
	return tokens
}

// Link returns the attributes of one link to rawURL. A link that is not an
// absolute http(s) URL keeps its address and gets nothing else.
func Link(rawURL string, o Options) Attrs {
	if o.isZero() || !isHTTP(rawURL) {
		return Attrs{Href: rawURL}
	}
	a := Attrs{Href: rawURL, Rel: strings.Join(o.relTokens(), " ")}
	if o.Redirector != "" && o.Redirector != NoReferrer {
		a.Href = o.Redirector + rawURL
	}
	if o.Blank {
		a.Target = "_blank"
	}
	return a
}

// Links rewrites the <a href> of an HTML fragment as Link does. Content that
// cannot be parsed, or options that ask for nothing, give the input unchanged.
func Links(content string, o Options) string {
	if o.isZero() || content == "" {
		return content
	}
	body := &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"}
	nodes, err := html.ParseFragment(strings.NewReader(content), body)
	if err != nil {
		return content
	}
	var out bytes.Buffer
	for _, n := range nodes {
		rewrite(n, o)
		if err := html.Render(&out, n); err != nil {
			return content
		}
	}
	return out.String()
}

// rewrite applies the options to every link under n, n included.
func rewrite(n *html.Node, o Options) {
	if n.Type == html.ElementNode && n.DataAtom == atom.A {
		if href, ok := attr(n, "href"); ok && isHTTP(href) {
			a := Link(href, o)
			setAttr(n, "href", a.Href)
			setAttr(n, "rel", mergeRel(n, a.Rel))
			if a.Target != "" {
				setAttr(n, "target", a.Target)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		rewrite(c, o)
	}
}

// mergeRel keeps the rel tokens the link already has and adds the missing ones.
func mergeRel(n *html.Node, wanted string) string {
	existing, _ := attr(n, "rel")
	tokens := strings.Fields(existing)
	for _, t := range strings.Fields(wanted) {
		if !slices.Contains(tokens, t) {
			tokens = append(tokens, t)
		}
	}
	return strings.Join(tokens, " ")
}

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// setAttr changes an attribute in place, or appends it.
func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}
