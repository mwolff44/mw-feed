// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"net/http"
	"strings"
)

const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src *; media-src *; " +
	"frame-src https://www.youtube-nocookie.com; connect-src 'self'; form-action 'self'; " +
	"base-uri 'none'; frame-ancestors 'none'"

// secure sets the security headers of every response. Pages and redirects are
// not to be stored; the static files and the icons set their own cache policy.
func (s *server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/static/") && !strings.HasPrefix(r.URL.Path, "/favicon/") {
			h.Set("Cache-Control", "no-store")
		}
		if s.isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
