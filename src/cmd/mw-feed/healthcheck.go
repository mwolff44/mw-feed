// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/mwolff44/mw-feed/internal/config"
)

const healthcheckTimeout = 3 * time.Second

// runHealthcheck asks the local server whether it is healthy. It exists for the
// container HEALTHCHECK: the image has no shell and no curl. Exit code 0 means
// healthy.
func runHealthcheck(args []string, stderr io.Writer) int {
	cfg, err := config.Load(args, os.Getenv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "mw-feed: %v\n", err)
		return 2
	}
	url, err := healthURL(cfg.Addr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "mw-feed: %v\n", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "mw-feed: %v\n", err)
		return 2
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "mw-feed: unhealthy: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(stderr, "mw-feed: unhealthy: HTTP %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

// healthURL is the local URL of /healthz for a listen address: a wildcard
// address (empty host, 0.0.0.0, ::) becomes the loopback address.
func healthURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}
