// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"testing"
)

func TestRunVersionPrintsVersionAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if got, want := stdout.String(), "mw-feed dev\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunUnknownCommandFailsWithMessageOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"bogus"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if got, want := stderr.String(), "mw-feed: unknown command \"bogus\"\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}
