// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
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
