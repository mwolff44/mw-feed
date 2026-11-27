// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Every message key used by a template (`.T "key"`) or by a handler
// ("error.*", "flash.*" literals) must exist in the English texts.
func TestEveryKeyUsedByTheWebPackageExistsInEnglish(t *testing.T) {
	raw, err := os.ReadFile("../i18n/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	var english map[string]string
	if err := json.Unmarshal(raw, &english); err != nil {
		t.Fatal(err)
	}

	templateKey := regexp.MustCompile(`\.(?:T|Format) "([^"]+)"`)
	goKey := regexp.MustCompile(`"((?:error|flash|help)\.[a-z_]+)"`)
	used := map[string]string{} // key -> where
	collect := func(pattern string, re *regexp.Regexp) {
		files, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if filepath.Base(f) == "i18n_test.go" {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				used[m[1]] = f
			}
		}
	}
	collect("templates/*.html", templateKey)
	collect("*.go", goKey)

	if len(used) == 0 {
		t.Fatal("no key found: the patterns no longer match the code")
	}
	for key, where := range used {
		_, plain := english[key]
		_, one := english[key+"_one"]
		_, other := english[key+"_other"]
		if !plain && (!one || !other) { // counted texts have _one and _other forms
			t.Errorf("key %q (used in %s) is missing from en.json", key, where)
		}
	}
}
