// SPDX-License-Identifier: GPL-3.0-or-later

package sanitize_test

import (
	"strings"
	"testing"

	"github.com/mwolff44/mw-feed/internal/sanitize"
)

func TestHTMLRemovesDangerousConstructs(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		absent []string // must not survive, case-insensitively
		kept   string   // harmless text that must survive
	}{
		{"script element", `<p>before</p><script>alert(1)</script>`, []string{"<script", "alert(1)"}, "before"},
		{"obfuscated script", `<ScRiPt>alert(1)</sCrIpT>ok`, []string{"script", "alert(1)"}, "ok"},
		{"event handler on img", `ok<img src="https://e.example/a.png" onerror="alert(1)">`, []string{"onerror", "alert"}, "ok"},
		{"event handler on link", `<a href="https://e.example/" onclick="alert(1)">link</a>`, []string{"onclick", "alert"}, "link"},
		{"javascript: link", `<a href="javascript:alert(1)">click</a>`, []string{"javascript:", "alert"}, "click"},
		{"javascript: link with leading space", `<a href=" javascript:alert(1)">click</a>`, []string{"javascript:", "alert"}, "click"},
		{"javascript: link with entity", `<a href="jav&#x61;script:alert(1)">click</a>`, []string{"script:", "alert"}, "click"},
		{"data: html link", `<a href="data:text/html,<script>alert(1)</script>">click</a>`, []string{"data:", "<script"}, "click"},
		{"data: svg image", `<img src="data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9YWxlcnQoMSk+">`, []string{"data:"}, ""},
		{"foreign iframe", `<iframe src="https://evil.example/"></iframe>after`, []string{"<iframe", "evil.example"}, "after"},
		{"youtube.com iframe, not the no-cookie host", `<iframe src="https://www.youtube.com/embed/abc"></iframe>`, []string{"<iframe", "youtube.com"}, ""},
		{"lookalike youtube-nocookie host", `<iframe src="https://www.youtube-nocookie.com.evil.example/embed/abc"></iframe>`, []string{"<iframe", "evil.example"}, ""},
		{"inline style", `<p style="background:url(javascript:alert(1))">text</p>`, []string{"style", "javascript:"}, "text"},
		{"style element", `<style>body{display:none}</style>text`, []string{"<style", "display"}, "text"},
		{"svg with handler", `<svg onload="alert(1)"><circle/></svg>text`, []string{"<svg", "onload", "alert"}, "text"},
		{"object and embed", `<object data="https://e.example/x.swf"></object><embed src="https://e.example/x.swf">text`, []string{"<object", "<embed"}, "text"},
		{"form and inputs", `<form action="https://e.example/"><input name="p"><button>go</button></form>text`, []string{"<form", "<input"}, "text"},
		{"meta refresh and base", `<meta http-equiv="refresh" content="0;url=https://e.example/"><base href="https://e.example/">text`, []string{"<meta", "<base"}, "text"},
		{"relative URL pointing at this application", `<a href="/logout">x</a><img src="/password">`, []string{`href="/logout"`, `src="/password"`}, ""},
		{"html comment", `a<!-- <script>alert(1)</script> -->b`, []string{"<!--", "alert"}, "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := sanitize.HTML(tc.in)

			for _, bad := range tc.absent {
				if strings.Contains(strings.ToLower(out), strings.ToLower(bad)) {
					t.Errorf("output still contains %q:\n%s", bad, out)
				}
			}
			if tc.kept != "" && !strings.Contains(out, tc.kept) {
				t.Errorf("harmless text %q was lost:\n%s", tc.kept, out)
			}
		})
	}
}

func TestHTMLKeepsLegitimateContent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // fragments that must survive
	}{
		{"text formatting", `<p>one <b>two</b> <i>three</i> <em>four</em> <strong>five</strong></p>`, []string{"<p>", "<b>two</b>", "<i>three</i>", "<em>four</em>", "<strong>five</strong>"}},
		{"headings and rules", `<h2>Title</h2><hr><br>`, []string{"<h2>Title</h2>", "<hr>"}},
		{"http and https links", `<a href="https://example.org/page">a</a><a href="http://example.org/">b</a>`, []string{`href="https://example.org/page"`, `href="http://example.org/"`, ">a</a>", ">b</a>"}},
		{"mailto link", `<a href="mailto:me@example.org">mail</a>`, []string{`href="mailto:me@example.org"`}},
		{"lists", `<ul><li>a</li></ul><ol><li>b</li></ol>`, []string{"<ul>", "<li>a</li>", "<ol>", "<li>b</li>"}},
		{"quotes and code", `<blockquote>q</blockquote><pre><code>x := 1</code></pre>`, []string{"<blockquote>q</blockquote>", "<pre>", "<code>x := 1</code>"}},
		{"table", `<table><tbody><tr><td>cell</td></tr></tbody></table>`, []string{"<table>", "<td>cell</td>"}},
		{"image", `<img src="https://example.org/a.png" alt="photo" width="10" height="20">`, []string{"<img", `src="https://example.org/a.png"`, `alt="photo"`, `width="10"`, `height="20"`}},
		{"unicode text", `<p>héllo wörld — 日本語 🙂</p>`, []string{"héllo wörld — 日本語 🙂"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := sanitize.HTML(tc.in)

			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "rel=") {
				t.Errorf("a rel attribute was added at storage time (rendering decides, see 4.7):\n%s", out)
			}
		})
	}
}

func TestHTMLKeepsMediaAndOnlyTheNoCookieYouTubeIframe(t *testing.T) {
	keep := []struct {
		name string
		in   string
		want []string
	}{
		{"audio", `<audio controls><source src="https://e.example/a.mp3"><a href="https://e.example/a.mp3">a</a></audio>`,
			[]string{"<audio controls", `<source src="https://e.example/a.mp3"`, "</audio>"}},
		{"video", `<video controls><source src="http://e.example/v.mp4"></video>`,
			[]string{"<video controls", `<source src="http://e.example/v.mp4"`, "</video>"}},
		{"youtube no-cookie iframe", `<iframe src="https://www.youtube-nocookie.com/embed/abc123" width="640" height="360" allowfullscreen referrerpolicy="strict-origin-when-cross-origin"></iframe>`,
			[]string{`<iframe src="https://www.youtube-nocookie.com/embed/abc123"`, `width="640"`, `height="360"`, "allowfullscreen", `referrerpolicy="strict-origin-when-cross-origin"`}},
	}
	for _, tc := range keep {
		t.Run("keeps "+tc.name, func(t *testing.T) {
			out := sanitize.HTML(tc.in)
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
		})
	}

	drop := []struct {
		name   string
		in     string
		absent []string
	}{
		{"source with javascript:", `<video><source src="javascript:alert(1)"></video>`, []string{"javascript:", "alert"}},
		{"source with data:", `<audio><source src="data:audio/mp3;base64,AAAA"></audio>`, []string{"data:"}},
		{"media event handlers", `<video controls onplay="alert(1)" onerror="alert(2)"></video>`, []string{"onplay", "onerror", "alert"}},
		{"autoplay is not needed and not kept", `<video autoplay controls></video>`, []string{"autoplay"}},
		{"no-cookie iframe over plain http", `<iframe src="http://www.youtube-nocookie.com/embed/abc"></iframe>`, []string{"youtube-nocookie"}},
		{"iframe with a handler", `<iframe src="https://www.youtube-nocookie.com/embed/abc" onload="alert(1)"></iframe>`, []string{"onload", "alert"}},
		{"iframe srcdoc", `<iframe srcdoc="<script>alert(1)</script>"></iframe>`, []string{"srcdoc", "alert"}},
		{"audio src attribute", `<audio src="javascript:alert(1)"></audio>`, []string{"javascript:", "alert"}},
		{"video poster and src", `<video poster="javascript:alert(1)" src="data:text/html,x"></video>`, []string{"poster", "javascript:", "data:"}},
		{"source with uppercase scheme tricks", `<video><source src="JaVaScRiPt:alert(1)"></video>`, []string{"javascript:", "alert"}},
		{"mutation XSS through noscript", `<noscript><p title="</noscript><img src=x onerror=alert(1)>"></noscript>`, []string{"onerror", "alert"}},
		{"track element", `<video><track src="https://e.example/x.vtt" onerror="alert(1)"></video>`, []string{"onerror", "alert"}},
	}
	for _, tc := range drop {
		t.Run("drops "+tc.name, func(t *testing.T) {
			out := strings.ToLower(sanitize.HTML(tc.in))
			for _, bad := range tc.absent {
				if strings.Contains(out, bad) {
					t.Errorf("output still contains %q:\n%s", bad, out)
				}
			}
		})
	}
}
