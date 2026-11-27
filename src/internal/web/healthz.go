// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"net/http"
)

// healthz answers whether the server can do its job: it is public, works
// before the installation, and says nothing about the system beyond ok or not.
func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := s.store.Ping(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("unavailable\n"))
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}
