// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "serve" {
		return runServe(args[1:], stderr)
	}
	if len(args) == 1 && args[0] == "version" {
		_, _ = fmt.Fprintf(stdout, "mw-feed %s\n", version)
		return 0
	}
	_, _ = fmt.Fprintf(stderr, "mw-feed: unknown command %q\n", strings.Join(args, " "))
	return 2
}
