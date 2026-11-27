// SPDX-License-Identifier: GPL-3.0-or-later

package anonymize_test

import (
	"testing"

	"github.com/mwolff44/mw-feed/internal/anonymize"
)

const prefix = "https://anonym.example/?"

func TestLinksRewritesOnlyAbsoluteHTTPLinks(t *testing.T) {
	cases := []struct {
		name string
		opts anonymize.Options
		in   string
		want string
	}{
		{"nothing to do", anonymize.Options{}, `<p>Hi <a href="https://x.example/a">t</a></p>`, `<p>Hi <a href="https://x.example/a">t</a></p>`},
		{"prefix", anonymize.Options{Redirector: prefix},
			`<p><a href="https://x.example/a?b=c&amp;d=e">t</a></p>`,
			`<p><a href="https://anonym.example/?https://x.example/a?b=c&amp;d=e" rel="noopener noreferrer">t</a></p>`},
		{"noreferrer", anonymize.Options{Redirector: "noreferrer"},
			`<a href="http://x.example/a">t</a>`, `<a href="http://x.example/a" rel="noopener noreferrer">t</a>`},
		{"blank alone", anonymize.Options{Blank: true},
			`<a href="https://x.example/">t</a>`, `<a href="https://x.example/" rel="noopener" target="_blank">t</a>`},
		{"blank and noreferrer", anonymize.Options{Redirector: "noreferrer", Blank: true},
			`<a href="https://x.example/">t</a>`, `<a href="https://x.example/" rel="noopener noreferrer" target="_blank">t</a>`},
		{"existing rel is kept and completed", anonymize.Options{Redirector: "noreferrer"},
			`<a href="https://x.example/" rel="nofollow noopener">t</a>`, `<a href="https://x.example/" rel="nofollow noopener noreferrer">t</a>`},
		{"existing target is replaced", anonymize.Options{Blank: true},
			`<a href="https://x.example/" target="_self">t</a>`, `<a href="https://x.example/" target="_blank" rel="noopener">t</a>`},
		{"other links are left alone", anonymize.Options{Redirector: prefix, Blank: true},
			`<a href="/relative">a</a><a href="#top">b</a><a href="mailto:a@b.example">c</a><a href="ftp://x.example/f">d</a><a name="n">e</a>`,
			`<a href="/relative">a</a><a href="#top">b</a><a href="mailto:a@b.example">c</a><a href="ftp://x.example/f">d</a><a name="n">e</a>`},
		{"images and media are not links", anonymize.Options{Redirector: prefix, Blank: true},
			`<img src="https://i.example/p.png" alt="x"/><video src="https://v.example/v.mp4"></video><iframe src="https://www.youtube-nocookie.com/embed/x"></iframe>`,
			`<img src="https://i.example/p.png" alt="x"/><video src="https://v.example/v.mp4"></video><iframe src="https://www.youtube-nocookie.com/embed/x"></iframe>`},
		{"markup around is kept", anonymize.Options{Redirector: "noreferrer"},
			`<p>One <strong>two</strong></p><blockquote><a href="https://x.example/">q</a></blockquote>`,
			`<p>One <strong>two</strong></p><blockquote><a href="https://x.example/" rel="noopener noreferrer">q</a></blockquote>`},
		{"a link whose text holds markup", anonymize.Options{Redirector: "noreferrer"},
			`<a href="https://x.example/"><img src="https://i.example/p.png" alt="x"/> pic</a>`,
			`<a href="https://x.example/" rel="noopener noreferrer"><img src="https://i.example/p.png" alt="x"/> pic</a>`},
		{"text is escaped, not interpreted", anonymize.Options{Redirector: "noreferrer"},
			`<p>1 &lt; 2 &amp; 3</p>`, `<p>1 &lt; 2 &amp; 3</p>`},
		{"empty content", anonymize.Options{Redirector: prefix}, ``, ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := anonymize.Links(tc.in, tc.opts); got != tc.want {
				t.Errorf("Links(%q) =\n%q\nwant\n%q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLinkGivesTheAttributesOfOneLink(t *testing.T) {
	cases := []struct {
		name string
		opts anonymize.Options
		url  string
		want anonymize.Attrs
	}{
		{"nothing to do", anonymize.Options{}, "https://x.example/a", anonymize.Attrs{Href: "https://x.example/a"}},
		{"prefix", anonymize.Options{Redirector: prefix}, "https://x.example/a?b=1",
			anonymize.Attrs{Href: prefix + "https://x.example/a?b=1", Rel: "noopener noreferrer"}},
		{"noreferrer and blank", anonymize.Options{Redirector: "noreferrer", Blank: true}, "http://x.example/",
			anonymize.Attrs{Href: "http://x.example/", Rel: "noopener noreferrer", Target: "_blank"}},
		{"blank alone", anonymize.Options{Blank: true}, "https://x.example/",
			anonymize.Attrs{Href: "https://x.example/", Rel: "noopener", Target: "_blank"}},
		{"not an http link", anonymize.Options{Redirector: prefix, Blank: true}, "javascript:alert(1)",
			anonymize.Attrs{Href: "javascript:alert(1)"}},
		{"empty", anonymize.Options{Redirector: prefix}, "", anonymize.Attrs{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := anonymize.Link(tc.url, tc.opts); got != tc.want {
				t.Errorf("Link(%q) = %+v, want %+v", tc.url, got, tc.want)
			}
		})
	}
}
