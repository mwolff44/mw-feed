// SPDX-License-Identifier: GPL-3.0-or-later

package web_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mwolff44/mw-feed/internal/store"
	"github.com/mwolff44/mw-feed/internal/web"
)

func TestEveryPageLoadsTheScriptAsAnExternalModuleAndNoneInline(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	visitor := a.anotherBrowser(t)

	pages := map[string]string{
		"reading": body(t, a.get(t, "/")),
		"config":  body(t, a.get(t, "/config")),
		"login":   body(t, visitor.get(t, "/login")),
	}

	for name, page := range pages {
		if !strings.Contains(page, `<script type="module" src="/static/app.js"></script>`) {
			t.Errorf("%s page does not load /static/app.js as a module:\n%s", name, page)
		}
		if n := strings.Count(page, "<script"); n != 1 {
			t.Errorf("%s page has %d script elements, want only the external one (the CSP forbids inline scripts)", name, n)
		}
	}
}

func TestScriptFilesAreServedAsJavaScriptToEveryone(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	visitor := a.anotherBrowser(t)

	for _, path := range []string{"/static/app.js"} {
		resp := visitor.get(t, path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Errorf("GET %s: Content-Type = %q, want a JavaScript type (modules are refused otherwise)", path, ct)
		}
	}
}

func TestReadingPageTellsTheScriptTheSettingsItNeedsAndMarksThePagerLinks(t *testing.T) {
	a, _ := seeded(t)
	a.setSettings(t, func(s *store.Settings) { s.ByPage = 1; s.Autofocus = true })

	page := body(t, a.get(t, "/?page=2"))
	for _, want := range []string{`<div class="reading" data-autofocus`, `<a rel="prev" href=`} {
		if !strings.Contains(page, want) {
			t.Errorf("page 2 does not contain %q:\n%s", want, page)
		}
	}
	if page := body(t, a.get(t, "/")); !strings.Contains(page, `<a rel="next" href=`) {
		t.Errorf("page 1 has no rel=next link:\n%s", page)
	}

	a.setSettings(t, func(s *store.Settings) { s.Autofocus = false })
	if page := body(t, a.get(t, "/")); strings.Contains(page, "data-autofocus") {
		t.Error("data-autofocus is present although the setting is off")
	}
}

func TestReadingPageTellsTheScriptWhetherSwipingIsOn(t *testing.T) {
	a, _ := seeded(t)
	a.setSettings(t, func(s *store.Settings) { s.Swipe = true })
	if page := body(t, a.get(t, "/")); !strings.Contains(page, ` data-swipe`) {
		t.Errorf("data-swipe is missing although the setting is on:\n%s", page)
	}

	a.setSettings(t, func(s *store.Settings) { s.Swipe = false })
	if page := body(t, a.get(t, "/")); strings.Contains(page, "data-swipe") {
		t.Error("data-swipe is present although the setting is off")
	}
}

func TestMarkAllReadButtonCarriesTheTextTheScriptAsksToConfirm(t *testing.T) {
	a, _ := seeded(t)

	page := body(t, a.get(t, "/"))
	if !strings.Contains(page, `name="read" value="1" data-confirm="Mark all these items as read?"`) {
		t.Errorf("the mark-read button has no data-confirm:\n%s", page)
	}
	if strings.Contains(page, `name="read" value="0" data-confirm`) {
		t.Error("only the mark-read button of the menu carries a confirmation text")
	}
	if starred := body(t, a.get(t, "/starred")); strings.Contains(starred, "data-confirm") {
		t.Error("the starred pages have no mark-read button, so no confirmation text")
	}
}

func TestMenuLinksTheShortcutsUseAreMarked(t *testing.T) {
	a, _ := seeded(t)

	page := body(t, a.get(t, "/"))
	for _, want := range []string{
		`<a class="brand" data-shortcut="home" href="/">`,
		`data-shortcut="edit"`,
		`<a data-shortcut="help" href="/help">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("reading page does not contain %q:\n%s", want, page)
		}
	}
	starred := body(t, a.get(t, "/starred"))
	if strings.Contains(starred, `data-shortcut="edit"`) || !strings.Contains(starred, `data-shortcut="help"`) {
		t.Errorf("the starred pages have no Edit link but keep home and help:\n%s", starred)
	}
}

func TestReadingPageTellsTheScriptWhetherItemsAreReadWhenLeft(t *testing.T) {
	a, _ := seeded(t)
	a.setSettings(t, func(s *store.Settings) { s.AutoreadItem = true })
	if page := body(t, a.get(t, "/")); !strings.Contains(page, ` data-autoread-item`) {
		t.Errorf("data-autoread-item is missing although the setting is on:\n%s", page)
	}

	a.setSettings(t, func(s *store.Settings) { s.AutoreadItem = false })
	if page := body(t, a.get(t, "/")); strings.Contains(page, "data-autoread-item") {
		t.Error("data-autoread-item is present although the setting is off")
	}
}
