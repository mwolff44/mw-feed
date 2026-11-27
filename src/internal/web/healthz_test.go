// SPDX-License-Identifier: GPL-3.0-or-later

package web_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mwolff44/mw-feed/internal/web"
)

func TestHealthzAnswersOKToAnyoneEvenBeforeTheInstallation(t *testing.T) {
	a := newApp(t, web.Options{}) // not installed

	resp := a.get(t, "/healthz")

	if resp.StatusCode != http.StatusOK || strings.TrimSpace(body(t, resp)) != "ok" {
		t.Errorf("GET /healthz before installation = %d, want 200 ok", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("the security headers are missing")
	}

	a.install(t)
	visitor := a.anotherBrowser(t)
	if resp := visitor.get(t, "/healthz"); resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz by a visitor = %d, want 200", resp.StatusCode)
	}
}

func TestHealthzReportsAnUnavailableDatabaseWithoutDetails(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)
	if err := a.store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	resp := a.get(t, "/healthz")
	text := body(t, resp)

	if resp.StatusCode != http.StatusServiceUnavailable || strings.TrimSpace(text) != "unavailable" {
		t.Errorf("GET /healthz with a closed database = %d %q, want 503 unavailable", resp.StatusCode, text)
	}
}

func TestHealthzIsReadOnly(t *testing.T) {
	a := newApp(t, web.Options{})
	a.install(t)

	resp := a.post(t, "/healthz", nil)

	if resp.StatusCode == http.StatusOK {
		t.Errorf("POST /healthz = 200, want it refused")
	}
}
