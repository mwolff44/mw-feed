// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// listenOn starts a server answering status on /healthz and returns its port.
func listenOn(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	return port
}

func healthcheck(args ...string) (code int, stderr string) {
	var out, errOut bytes.Buffer
	code = run(append([]string{"healthcheck"}, args...), &out, &errOut)
	return code, errOut.String()
}

func TestHealthcheckSucceedsWhenTheServerIsHealthy(t *testing.T) {
	port := listenOn(t, http.StatusOK)

	if code, stderr := healthcheck("--addr", ":"+port); code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr: %q)", code, stderr)
	}
}

func TestHealthcheckReadsTheAddressLikeServeDoes(t *testing.T) {
	port := listenOn(t, http.StatusOK)
	t.Setenv("MW_FEED_ADDR", "0.0.0.0:"+port)

	if code, stderr := healthcheck(); code != 0 {
		t.Errorf("exit code = %d, want 0 with MW_FEED_ADDR=0.0.0.0:%s (stderr: %q)", code, port, stderr)
	}
}

func TestHealthcheckFailsWhenTheServerIsUnhealthyOrAbsent(t *testing.T) {
	unhealthy := listenOn(t, http.StatusServiceUnavailable)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	_, closed, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close() // nobody listens there now

	for name, port := range map[string]string{"503": unhealthy, "connection refused": closed} {
		code, stderr := healthcheck("--addr", ":"+port)
		if code != 1 || stderr == "" {
			t.Errorf("%s: exit code = %d, stderr = %q, want 1 and a message", name, code, stderr)
		}
	}
}

func TestHealthcheckDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	if code, _ := healthcheck("--addr", ":"+port); code != 1 {
		t.Errorf("exit code = %d, want 1: a redirect is not a healthy answer", code)
	}
}
