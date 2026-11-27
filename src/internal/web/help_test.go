// SPDX-License-Identifier: GPL-3.0-or-later

package web_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mwolff44/mw-feed/internal/web"
)

func TestHelpPageHasTheShortcutsAndTheCredits(t *testing.T) {
	a := newApp(t, web.Options{Version: "1.2.3"})
	a.install(t)

	resp := a.get(t, "/help")
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /help = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{
		"<h1>Help</h1>", "Keyboard shortcuts", "Items navigation", "Menu navigation",
		"<dt>m</dt>", "Go to next item", "Star/Unstar current item", "<dt>?, F1</dt>",
		"Version: 1.2.3",
		"mw-feed is a feed reader written in Go. It is an independent rewrite, from scratch, of the behaviour of KrISS feed, created by Tontof (" +
			`<a href="https://github.com/tontof/kriss_feed" rel="noreferrer noopener">https://github.com/tontof/kriss_feed</a>` +
			"). No code from the original project is reused. mw-feed is free software, licensed under the GNU GPL version 3 or later.",
		"NOTICE",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("help page does not contain %q in:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<dt>s</dt>") {
		t.Error("the help lists the share shortcut, which was removed with Shaarli")
	}
}

func TestHelpShowsDevWhenNoVersionIsGiven(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)

	if page := body(t, a.get(t, "/help")); !strings.Contains(page, "Version: dev") {
		t.Errorf("no default version in:\n%s", page)
	}
}

func TestEveryLoggedInPageHasConfigurationHelpAndLogoutInItsMenu(t *testing.T) {
	a, alpha, _, _ := twoFeeds(t)

	for _, path := range []string{"/", "/starred", "/edit", "/add", "/config", "/help", "/import", "/password", "/feed/" + itoa(alpha.ID), "/no-such-page"} {
		page := body(t, a.get(t, path))
		for _, want := range []string{`<a href="/config">Configuration</a>`, `<a href="/help">Help</a>`, `action="/logout"`} {
			if !strings.Contains(page, want) {
				t.Errorf("GET %s: the menu does not contain %q", path, want)
			}
		}
	}
	visitor := a.anotherBrowser(t)
	if page := body(t, visitor.get(t, "/login")); strings.Contains(page, `href="/config"`) {
		t.Error("the login page shows the menu")
	}
}
