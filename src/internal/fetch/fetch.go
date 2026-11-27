// SPDX-License-Identifier: GPL-3.0-or-later

// Package fetch downloads feeds over HTTP(S) with SSRF, size and redirect limits.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// ErrUnsupportedScheme means the URL is not an absolute http or https URL.
var ErrUnsupportedScheme = errors.New("only http and https URLs are supported")

// ErrTooManyRedirects means the server redirected more than maxRedirects times.
var ErrTooManyRedirects = errors.New("too many redirects")

// Defaults of Options (decision no. 35 of the plan).
const (
	maxRedirects    = 10
	defaultTimeout  = 30 * time.Second
	defaultMaxBytes = 10 << 20 // 10 MiB
	userAgent       = "mw-feed"
)

// ErrTooLarge means the response body is larger than the size limit.
var ErrTooLarge = errors.New("response body too large")

// StatusError is returned for a response whose status is not 2xx.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("unexpected HTTP status %d", e.Code) }

// ErrBlockedAddress means the destination address is not allowed.
var ErrBlockedAddress = errors.New("destination address is not allowed")

// Resolver resolves host names. *net.Resolver implements it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Options configure a Fetcher. The zero value of every field means "default".
type Options struct {
	// AllowPrivate lifts the restrictions on destination addresses.
	AllowPrivate bool

	// Timeout bounds a whole download (default 30 s); MaxBytes bounds the
	// response body (default 10 MiB).
	Timeout  time.Duration
	MaxBytes int64

	// Resolver and Dial replace the system ones; tests use them.
	Resolver Resolver
	Dial     func(ctx context.Context, network, address string) (net.Conn, error)
}

// Request is a download request.
type Request struct {
	URL string
	// ETag and LastModified are the validators of the last successful download.
	// When set, they make the request conditional.
	ETag         string
	LastModified string
}

// Response is the result of a successful download.
type Response struct {
	Body []byte
	// NotModified is true when the server answered 304 to a conditional request.
	NotModified bool
	// ETag and LastModified are the validators sent by the server.
	ETag         string
	LastModified string
}

// Fetcher downloads URLs.
type Fetcher struct {
	client       *http.Client
	maxBytes     int64
	allowPrivate bool
	resolver     Resolver
	dial         func(ctx context.Context, network, address string) (net.Conn, error)
}

// New returns a Fetcher.
func New(opts Options) *Fetcher {
	f := &Fetcher{allowPrivate: opts.AllowPrivate, resolver: opts.Resolver, dial: opts.Dial, maxBytes: opts.MaxBytes}
	if f.maxBytes <= 0 {
		f.maxBytes = defaultMaxBytes
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if f.resolver == nil {
		f.resolver = net.DefaultResolver
	}
	if f.dial == nil {
		f.dial = (&net.Dialer{}).DialContext
	}
	// Proxy is left nil on purpose: environment proxies would bypass the address checks.
	f.client = &http.Client{
		Transport:     &http.Transport{DialContext: f.dialContext},
		CheckRedirect: checkRedirect,
		Timeout:       timeout,
	}
	return f
}

// dialContext resolves the host, checks every address, then connects to an
// address it has checked: what is checked is what is used, so a DNS answer
// that changes between the check and the connection cannot bypass the rules.
func (f *Fetcher) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var addrs []netip.Addr
	if ip, perr := netip.ParseAddr(host); perr == nil {
		addrs = []netip.Addr{ip}
	} else if addrs, err = f.resolver.LookupNetIP(ctx, "ip", host); err != nil {
		return nil, err
	}
	if !f.allowPrivate {
		for _, a := range addrs {
			if blocked(a) {
				return nil, fmt.Errorf("%w: %s", ErrBlockedAddress, a)
			}
		}
	}
	var lastErr error
	for _, a := range addrs {
		conn, err := f.dial(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// cgnat is the carrier-grade NAT shared address space (RFC 6598).
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// blocked reports whether a must not be contacted: loopback, private,
// link-local, unspecified, multicast and CGNAT addresses.
func blocked(a netip.Addr) bool {
	a = a.Unmap() // ::ffff:10.0.0.1 is the IPv4 address 10.0.0.1
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsMulticast() ||
		a.IsUnspecified() || cgnat.Contains(a)
}

// Fetch downloads req.URL.
func (f *Fetcher) Fetch(ctx context.Context, req Request) (Response, error) {
	if err := checkURL(req.URL); err != nil {
		return Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return Response{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("User-Agent", userAgent)
	conditional := req.ETag != "" || req.LastModified != ""
	if req.ETag != "" {
		httpReq.Header.Set("If-None-Match", req.ETag)
	}
	if req.LastModified != "" {
		httpReq.Header.Set("If-Modified-Since", req.LastModified)
	}
	resp, err := f.client.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified && conditional {
		return Response{NotModified: true}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Response{}, &StatusError{Code: resp.StatusCode}
	}
	// Read one byte more than allowed to tell "exactly at the limit" from "over".
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > f.maxBytes {
		return Response{}, ErrTooLarge
	}
	return Response{
		Body:         body,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, nil
}

// checkURL accepts absolute http and https URLs with a host.
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ErrUnsupportedScheme
	}
	return nil
}

// checkRedirect limits the number of redirects and keeps them on http(s).
// Destination addresses are checked at dial time, for every hop.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return ErrTooManyRedirects
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return ErrUnsupportedScheme
	}
	return nil
}
