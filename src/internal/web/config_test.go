// SPDX-License-Identifier: GPL-3.0-or-later

package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mwolff44/mw-feed/internal/store"
	"github.com/mwolff44/mw-feed/internal/web"
)

func TestConfigFormShowsTheCurrentSettings(t *testing.T) {
	utc(t)
	a := newApp(t, web.Options{})
	a.install(t)
	a.setSettings(t, func(s *store.Settings) {
		s.Title, s.Locale, s.Theme, s.View, s.ByPage, s.Autohide, s.Autofocus = "My reader", "fr", "dark", "expanded", 25, true, false
	})

	resp := a.get(t, "/config")
	page := body(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /config = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{
		`<form method="post" action="/config">`,
		`<input type="text" name="title" value="My reader"`,
		`<option value="fr" selected>`, `<option value="en">`, `<option value="de">`,
		`<option value="dark" selected>`, `<option value="expanded" selected>`,
		`<input type="number" name="by_page" value="25" min="1"`,
		`<input type="number" name="max_items" value="100" min="1"`,
		`<input type="number" name="max_update" value="60" min="1"`,
		`<input type="checkbox" name="list_feeds" value="1" checked>`,
		`<input type="checkbox" name="autohide" value="1" checked>`,
		`<input type="checkbox" name="autofocus" value="1">`, // unchecked
		`<input type="checkbox" name="swipe" value="1" checked>`,
		`name="redirector"`, `href="/password"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("config form does not contain %q", want)
		}
	}
}

func validConfig() url.Values {
	return url.Values{
		"title": {"Renamed"}, "locale": {"de"}, "theme": {"light"}, "redirector": {"https://anonym.example/?"},
		"view": {"expanded"}, "filter": {"all"}, "order": {"olderFirst"}, "list_feeds": {"1"},
		"by_page": {"20"}, "max_items": {"50"}, "max_update": {"30"},
		"blank": {"1"},
	}
}

func TestConfigSavesEverySettingAndTheCheckboxesThatAreNotChecked(t *testing.T) {
	utc(t)
	a := newApp(t, web.Options{})
	a.install(t)

	resp := a.act(t, "/config", validConfig())

	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/config" {
		t.Fatalf("POST /config = %d → %q, want 303 → /config", resp.StatusCode, resp.Header.Get("Location"))
	}
	st, err := a.store.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	want := store.Settings{
		Title: "Renamed", Locale: "de", Theme: "light", Redirector: "https://anonym.example/?", View: "expanded",
		Filter: "all", Order: "olderFirst", ListFeeds: true, ByPage: 20, MaxItems: 50, MaxUpdate: 30,
		Blank: true, // every other box was not sent: false
	}
	if st != want {
		t.Errorf("settings = %+v\nwant     %+v", st, want)
	}
	page := body(t, a.get(t, "/config"))
	if !strings.Contains(page, `<html lang="de" data-theme="light">`) || !strings.Contains(page, "Einstellungen gespeichert.") {
		t.Errorf("the language and theme must apply at once, with the message, in:\n%s", page)
	}
}

func TestConfigRefusesWhatIsWrongAndSavesNothing(t *testing.T) {
	utc(t)
	a := newApp(t, web.Options{})
	a.install(t)
	before, _ := a.store.Settings(ctx)

	cases := []struct {
		name   string
		change func(url.Values)
		status int
		want   string
	}{
		{"empty title", func(v url.Values) { v.Set("title", "  ") }, http.StatusBadRequest, "The title must not be empty."},
		{"zero items per page", func(v url.Values) { v.Set("by_page", "0") }, http.StatusBadRequest, "must be whole numbers of at least 1."},
		{"text instead of a number", func(v url.Values) { v.Set("max_items", "many") }, http.StatusBadRequest, "must be whole numbers of at least 1."},
		{"negative delay", func(v url.Values) { v.Set("max_update", "-5") }, http.StatusBadRequest, "must be whole numbers of at least 1."},
		{"script as prefix", func(v url.Values) { v.Set("redirector", "javascript:alert(1)") }, http.StatusBadRequest, "The prefix must be empty, noreferrer or an http(s) URL."},
		{"relative prefix", func(v url.Values) { v.Set("redirector", "/go?") }, http.StatusBadRequest, "The prefix must be empty, noreferrer or an http(s) URL."},
		{"unknown language", func(v url.Values) { v.Set("locale", "xx") }, http.StatusBadRequest, "The request is not valid."},
		{"unknown view", func(v url.Values) { v.Set("view", "cube") }, http.StatusBadRequest, "The request is not valid."},
	}
	for _, tc := range cases {
		form := validConfig()
		tc.change(form)
		resp := a.act(t, "/config", form)
		page := body(t, resp)

		if resp.StatusCode != tc.status || !strings.Contains(page, tc.want) {
			t.Errorf("%s: status %d, %q missing in:\n%s", tc.name, resp.StatusCode, tc.want, page)
		}
	}
	if st, _ := a.store.Settings(ctx); st != before {
		t.Errorf("a refused request changed the settings: %+v", st)
	}

	// What was typed comes back with the error.
	form := validConfig()
	form.Set("by_page", "0")
	if page := body(t, a.act(t, "/config", form)); !strings.Contains(page, `name="title" value="Renamed"`) {
		t.Error("the typed values are not kept")
	}

	for _, ok := range []string{"", "noreferrer", "http://anonym.example/?", "https://anonym.example/redirect.php?url="} {
		form := validConfig()
		form.Set("redirector", ok)
		if resp := a.act(t, "/config", form); resp.StatusCode != http.StatusSeeOther {
			t.Errorf("redirector %q refused: %d", ok, resp.StatusCode)
		}
	}
	if resp := a.post(t, "/config", validConfig()); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /config without CSRF = %d, want 403", resp.StatusCode)
	}
}

func TestConfigPageHasNoPreloadOption(t *testing.T) {
	// The server renders the content of every item of the page, so there is
	// nothing left to preload: the option no longer exists.
	a, _ := seeded(t)

	if page := body(t, a.get(t, "/config")); strings.Contains(page, `name="preload"`) {
		t.Errorf("the configuration page still offers preload:\n%s", page)
	}
}
