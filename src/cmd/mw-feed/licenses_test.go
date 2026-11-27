// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// squash makes texts comparable whatever their line breaks and indentation.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

var licenseFile = regexp.MustCompile(`(?i)^(license|licence|copying)([-._].*)?$`)

type linkedModule struct{ path, version, dir string }

// linkedModules lists the modules whose code is in the binary.
func linkedModules(t *testing.T) []linkedModule {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f", `{{with .Module}}{{if not .Main}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}{{end}}`, ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	seen := map[string]bool{}
	var modules []linkedModule
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "|")
		if len(parts) != 3 || seen[parts[0]] {
			continue
		}
		seen[parts[0]] = true
		modules = append(modules, linkedModule{parts[0], parts[1], parts[2]})
	}
	return modules
}

func readRoot(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// The licences of BSD and MIT code must travel with the program: every module
// linked into the binary has its licence text in THIRD_PARTY_LICENSES.md and a
// line in NOTICE. A new dependency fails here until both are updated
// (scripts/third-party-licenses.sh writes the first).
func TestEveryLinkedModuleHasItsLicenceTextAndANoticeLine(t *testing.T) {
	licenses := squash(readRoot(t, "THIRD_PARTY_LICENSES.md"))
	notice := readRoot(t, "NOTICE")
	modules := linkedModules(t)
	if len(modules) < 10 {
		t.Fatalf("only %d linked modules found, the listing is broken", len(modules))
	}

	for _, m := range modules {
		if !strings.Contains(licenses, m.path+" "+m.version) {
			t.Errorf("THIRD_PARTY_LICENSES.md does not mention %s %s", m.path, m.version)
		}
		if !strings.Contains(notice, m.path) && !strings.Contains(notice, strings.TrimPrefix(m.path, "github.com/")) {
			t.Errorf("NOTICE does not mention %s", m.path)
		}
		entries, err := os.ReadDir(m.dir)
		if err != nil {
			t.Fatalf("read %s: %v", m.dir, err)
		}
		found := 0
		for _, e := range entries {
			if e.IsDir() || !licenseFile.MatchString(e.Name()) {
				continue
			}
			found++
			text, err := os.ReadFile(filepath.Join(m.dir, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			if !strings.Contains(licenses, squash(string(text))) {
				t.Errorf("THIRD_PARTY_LICENSES.md lacks the text of %s/%s", m.path, e.Name())
			}
		}
		if found == 0 {
			t.Errorf("%s has no licence file: look at it by hand", m.path)
		}
	}
}

func TestGoRuntimeLicenceIsReproduced(t *testing.T) {
	licenses := readRoot(t, "THIRD_PARTY_LICENSES.md")
	notice := readRoot(t, "NOTICE")

	if !strings.Contains(licenses, "Go standard library and runtime") || !strings.Contains(licenses, "Copyright 2009 The Go Authors") {
		t.Error("THIRD_PARTY_LICENSES.md does not reproduce the licence of the Go standard library and runtime")
	}
	if !strings.Contains(notice, "Go standard library") {
		t.Error("NOTICE does not mention the Go standard library")
	}
}
