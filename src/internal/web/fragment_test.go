// SPDX-License-Identifier: GPL-3.0-or-later

package web_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mwolff44/mw-feed/internal/store"
)

func TestItemFragmentIsTheOpenItemAloneWithItsCurrentState(t *testing.T) {
	a, list := seeded(t)
	item := list.Items[0]
	path := fmt.Sprintf("/item/%d?return=%s", item.ID, url.QueryEscape("/feed/1?page=2"))

	resp := a.get(t, path)
	frag := body(t, resp)

	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("status = %d, type = %q, want 200 text/html", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{
		fmt.Sprintf(`<details id="item-%d" class="item unread" open>`, item.ID),
		"<p>Body of newer</p>",
		fmt.Sprintf(`action="/item/%d/read"`, item.ID),
		fmt.Sprintf(`action="/item/%d/star"`, item.ID),
		`name="return" value="/feed/1?page=2"`,
	} {
		if !strings.Contains(frag, want) {
			t.Errorf("fragment does not contain %q:\n%s", want, frag)
		}
	}
	for _, not := range []string{"<html", `class="feeds"`, "<h1"} {
		if strings.Contains(frag, not) {
			t.Errorf("fragment contains %q: it must be the item alone", not)
		}
	}

	a.act(t, fmt.Sprintf("/item/%d/read", item.ID), url.Values{"return": {"/"}})
	a.act(t, fmt.Sprintf("/item/%d/star", item.ID), url.Values{"return": {"/"}})
	frag = body(t, a.get(t, path))
	for _, want := range []string{fmt.Sprintf(`<details id="item-%d" class="item" open>`, item.ID),
		fmt.Sprintf(`action="/item/%d/unread"`, item.ID), fmt.Sprintf(`action="/item/%d/unstar"`, item.ID)} {
		if !strings.Contains(frag, want) {
			t.Errorf("after read and star, fragment does not contain %q:\n%s", want, frag)
		}
	}
}

func TestItemFragmentFollowsTheViewAndTheStarredPages(t *testing.T) {
	a, list := seeded(t)
	item := list.Items[0]

	starred := body(t, a.get(t, fmt.Sprintf("/item/%d?return=/starred", item.ID)))
	if strings.Contains(starred, "/read") || !strings.Contains(starred, fmt.Sprintf(`action="/item/%d/star"`, item.ID)) {
		t.Errorf("starred page fragment must offer the star button only:\n%s", starred)
	}

	paged := body(t, a.get(t, fmt.Sprintf("/item/%d?return=%s", item.ID, url.QueryEscape("/starred?page=2"))))
	if strings.Contains(paged, "/read") {
		t.Errorf("starred page with a query string must offer the star button only:\n%s", paged)
	}

	a.setSettings(t, func(s *store.Settings) { s.View = "expanded" })
	expanded := body(t, a.get(t, fmt.Sprintf("/item/%d?return=/", item.ID)))
	if !strings.Contains(expanded, fmt.Sprintf(`<article id="item-%d" class="item unread">`, item.ID)) || strings.Contains(expanded, "<details") {
		t.Errorf("expanded view fragment must be an <article>:\n%s", expanded)
	}
}

func TestItemFragmentNeverEchoesAnUnsafeReturnAndKeepsTheFlash(t *testing.T) {
	a, list := seeded(t)
	item := list.Items[0]

	frag := body(t, a.get(t, fmt.Sprintf("/item/%d?return=%s", item.ID, url.QueryEscape("https://evil.example/"))))
	if !strings.Contains(frag, `name="return" value="/"`) || strings.Contains(frag, "evil.example") {
		t.Errorf("unsafe return was not replaced by /:\n%s", frag)
	}

	// The flash belongs to the next page the visitor sees, not to a fragment.
	a.act(t, "/mark", url.Values{"kind": {"all"}, "id": {"0"}, "read": {"1"}, "return": {"/"}})
	a.get(t, fmt.Sprintf("/item/%d?return=/", item.ID))
	if page := body(t, a.get(t, "/")); !strings.Contains(page, `class="flash"`) {
		t.Errorf("the fragment request consumed the flash message:\n%s", page)
	}
}

func TestItemFragmentRefusesAVisitorAndUnknownItems(t *testing.T) {
	a, list := seeded(t)
	visitor := a.anotherBrowser(t)

	if resp := visitor.get(t, fmt.Sprintf("/item/%d?return=/", list.Items[0].ID)); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Errorf("visitor: %d → %q, want 303 → /login", resp.StatusCode, resp.Header.Get("Location"))
	}
	for _, id := range []string{"9999", "abc", "0"} {
		if resp := a.get(t, "/item/"+id+"?return=/"); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /item/%s = %d, want 404", id, resp.StatusCode)
		}
	}
}
