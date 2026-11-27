// SPDX-License-Identifier: GPL-3.0-or-later

// Package favicon finds the icon of a feed's site, downloads it once and keeps it.
//
// The icon comes from a third-party service (DuckDuckGo), which therefore
// learns which sites are followed: the caller decides whether to use this
// package at all (the addFavicon setting). The server never connects to the
// feed's own host to get an icon.
package favicon

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mwolff44/mw-feed/internal/fetch"
	"github.com/mwolff44/mw-feed/internal/store"
)

const (
	serviceURL = "https://icons.duckduckgo.com/ip3/"
	// MaxSize is the largest icon accepted, in bytes.
	MaxSize = 64 * 1024
	// Timeout bounds one download.
	Timeout = 10 * time.Second

	successTTL = 30 * 24 * time.Hour
	failureTTL = 24 * time.Hour
)

// hostPattern is what a host must look like to be put in the service URL.
var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// Fetcher downloads a URL. *fetch.Fetcher implements it.
type Fetcher interface {
	Fetch(ctx context.Context, req fetch.Request) (fetch.Response, error)
}

// Clock provides the current time; tests inject a fake one.
type Clock interface{ Now() time.Time }

// Icon is an icon ready to be served.
type Icon struct {
	Data []byte
	Type string // image/png, image/gif, image/jpeg or image/x-icon
}

// Service downloads and caches icons.
type Service struct {
	store   *store.Store
	fetcher Fetcher
	clock   Clock
}

// NewService returns a Service. The fetcher should be limited to MaxSize and Timeout.
func NewService(st *store.Store, f Fetcher, clock Clock) *Service {
	return &Service{store: st, fetcher: f, clock: clock}
}

// Icon returns the icon of a feed whose site is siteURL (xmlURL when there is
// none). The second result is false when there is no icon to show: the caller
// then uses its default one.
func (s *Service) Icon(ctx context.Context, siteURL, xmlURL string) (Icon, bool) {
	host := hostOf(siteURL)
	if host == "" {
		host = hostOf(xmlURL)
	}
	if host == "" {
		return Icon{}, false
	}
	now := s.clock.Now()
	cached, err := s.store.FaviconByHost(ctx, host)
	switch {
	case err == nil:
		ttl := successTTL
		if len(cached.Data) == 0 {
			ttl = failureTTL
		}
		if now.Sub(cached.FetchedAt) < ttl {
			return iconOf(cached)
		}
	case !errors.Is(err, store.ErrNotFound):
		return Icon{}, false
	}
	fresh := store.Favicon{Host: host, FetchedAt: now}
	if resp, err := s.fetcher.Fetch(ctx, fetch.Request{URL: serviceURL + host + ".ico"}); err == nil {
		if t := imageType(resp.Body); t != "" {
			fresh.Data, fresh.Type = resp.Body, t
		}
	}
	// A failure is stored as an empty icon so that it is not retried at once.
	if err := s.store.SaveFavicon(ctx, fresh); err != nil {
		return Icon{}, false
	}
	return iconOf(fresh)
}

func iconOf(f store.Favicon) (Icon, bool) {
	if len(f.Data) == 0 {
		return Icon{}, false
	}
	return Icon{Data: f.Data, Type: f.Type}, true
}

// hostOf returns the lower-case host name of a URL, or "" when it is not a
// plain ASCII host name that is safe to put in the service URL.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if len(host) > 253 || !hostPattern.MatchString(host) {
		return ""
	}
	return host
}

// imageType detects the content type of an icon from its bytes, never from
// what the server claims. Only raster formats are accepted: an SVG may hold a script.
func imageType(b []byte) string {
	if len(b) == 0 || len(b) > MaxSize {
		return ""
	}
	switch t := http.DetectContentType(b); t {
	case "image/png", "image/gif", "image/jpeg", "image/x-icon":
		return t
	}
	return ""
}
