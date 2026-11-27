// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/store"
)

var binaryPath string

// TestMain builds the real binary once: these tests run it as a process.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mw-feed-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binaryPath = filepath.Join(dir, "mw-feed")
	build := exec.Command("go", "build", "-o", binaryPath, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// freeAddr returns a local address that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// startServe starts "mw-feed serve" with a clean environment plus extra variables.
func startServe(t *testing.T, addr string, extraEnv ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	cmd := exec.Command(binaryPath, "serve", "-addr", addr, "-data", t.TempDir())
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, extraEnv...)
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd, &stderr
}

func TestServeAnswersRequestsAndStopsCleanlyOnSIGTERM(t *testing.T) {
	addr := freeAddr(t)
	cmd, stderr := startServe(t, addr)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	var resp *http.Response
	deadline := time.Now().Add(10 * time.Second)
	for {
		var err error
		resp, err = client.Get("http://" + addr + "/")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not answer within 10 s: %v\nstderr:\n%s", err, stderr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/install" {
		t.Errorf("GET / = %d → %q, want 303 → /install", resp.StatusCode, resp.Header.Get("Location"))
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit after SIGTERM: %v\nstderr:\n%s", err, stderr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not stop within 10 s of SIGTERM")
	}
}

func TestServeRefusesToStartWithInvalidConfiguration(t *testing.T) {
	cmd, stderr := startServe(t, freeAddr(t), "MW_FEED_TRUST_PROXY=oui")

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("serve started despite MW_FEED_TRUST_PROXY=oui")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit on an invalid configuration")
	}
	if !bytes.Contains(stderr.Bytes(), []byte("MW_FEED_TRUST_PROXY")) {
		t.Errorf("stderr = %q, want it to name MW_FEED_TRUST_PROXY", stderr)
	}
}

// startServeIn starts "mw-feed serve" on a given data directory, allowing the
// loopback addresses the fake feed servers of the tests live on.
func startServeIn(t *testing.T, dataDir string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	cmd := exec.Command(binaryPath, "serve", "-addr", freeAddr(t), "-data", dataDir, "-allow-private")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd, &stderr
}

// subscribe records a never-updated feed in the database of dataDir, before serve starts.
func subscribe(t *testing.T, dataDir, xmlURL string) int64 {
	t.Helper()
	st, err := store.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()
	id, err := st.InsertFeed(context.Background(), store.NewFeed{XMLURL: xmlURL, Title: "Test feed"})
	if err != nil {
		t.Fatalf("InsertFeed: %v", err)
	}
	return id
}

func readFeed(t *testing.T, dataDir string, id int64) (store.Feed, int) {
	t.Helper()
	st, err := store.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()
	f, err := st.FeedByID(context.Background(), id)
	if err != nil {
		t.Fatalf("FeedByID: %v", err)
	}
	n, err := st.CountItems(context.Background(), store.Selection{Kind: store.SelectFeed, ID: id}, false)
	if err != nil {
		t.Fatalf("CountItems: %v", err)
	}
	return f, n
}

func waitExit(t *testing.T, cmd *exec.Cmd, stderr *bytes.Buffer, within time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit after SIGTERM: %v\nstderr:\n%s", err, stderr)
		}
	case <-time.After(within):
		t.Fatalf("the server did not stop within %v of SIGTERM\nstderr:\n%s", within, stderr)
	}
}

const testFeed = `<?xml version="1.0"?><rss version="2.0"><channel><title>Test feed</title><link>https://t.example/</link>
<item><title>One</title><link>https://t.example/1</link><pubDate>Mon, 05 Jan 2026 10:00:00 GMT</pubDate></item>
<item><title>Two</title><link>https://t.example/2</link><pubDate>Tue, 06 Jan 2026 10:00:00 GMT</pubDate></item>
</channel></rss>`

func TestServeUpdatesDueFeedsAtStartup(t *testing.T) {
	var hits atomic.Int32
	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(testFeed))
	}))
	t.Cleanup(feedServer.Close)
	dataDir := t.TempDir()
	id := subscribe(t, dataDir, feedServer.URL+"/feed.xml")

	cmd, stderr := startServeIn(t, dataDir)

	// The first pass of the scheduler is immediate, not one minute later.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if f, items := readFeed(t, dataDir, id); !f.LastUpdateAt.IsZero() && items == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the due feed was not updated within 10 s (%d request(s))\nstderr:\n%s", hits.Load(), stderr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	waitExit(t, cmd, stderr, 10*time.Second)
}

func TestSIGTERMAbortsADownloadInProgressWithoutRecordingAnError(t *testing.T) {
	started := make(chan struct{}, 1)
	stuck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done() // never answers: the feed server is stuck
	}))
	t.Cleanup(stuck.Close)
	dataDir := t.TempDir()
	id := subscribe(t, dataDir, stuck.URL+"/feed.xml")
	cmd, stderr := startServeIn(t, dataDir)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatalf("the scheduler did not start the download\nstderr:\n%s", stderr)
	}

	start := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}

	waitExit(t, cmd, stderr, 8*time.Second) // far below the 30 s of a download timeout
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Errorf("shutdown took %v", elapsed)
	}
	f, items := readFeed(t, dataDir, id)
	if f.LastError != "" || !f.LastUpdateAt.IsZero() || items != 0 {
		t.Errorf("feed = error %q, last update %v, %d item(s); want it untouched by the interrupted update", f.LastError, f.LastUpdateAt, items)
	}
}
