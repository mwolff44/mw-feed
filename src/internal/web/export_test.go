// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"html/template"
	"testing"
)

// BreakTemplate replaces a template by one that fails when executed, until the
// end of the test.
func BreakTemplate(t *testing.T, name string) {
	t.Helper()
	original := templates
	fresh := parseTemplates()
	fresh.Funcs(template.FuncMap{"fail": func() (string, error) { return "", errTest }})
	if _, err := fresh.New(name).Parse("{{fail}}"); err != nil {
		t.Fatalf("parse: %v", err)
	}
	templates = fresh
	t.Cleanup(func() { templates = original })
}

type testError string

func (e testError) Error() string { return string(e) }

const errTest = testError("template failure")
