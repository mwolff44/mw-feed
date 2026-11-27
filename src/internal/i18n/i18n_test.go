// SPDX-License-Identifier: GPL-3.0-or-later

package i18n_test

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mwolff44/mw-feed/internal/i18n"
)

var locales = []string{"en", "fr", "de"}

func load(t *testing.T, locale string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("locales/" + locale + ".json")
	if err != nil {
		t.Fatalf("locale %s: %v", locale, err)
	}
	var texts map[string]string
	if err := json.Unmarshal(raw, &texts); err != nil {
		t.Fatalf("locale %s: %v", locale, err)
	}
	return texts
}

func TestEveryLocaleHasExactlyTheKeysOfEnglish(t *testing.T) {
	english := load(t, "en")
	for _, locale := range locales[1:] {
		other := load(t, locale)
		for _, key := range sortedKeys(english) {
			if _, ok := other[key]; !ok {
				t.Errorf("%s misses key %q", locale, key)
			}
		}
		for _, key := range sortedKeys(other) {
			if _, ok := english[key]; !ok {
				t.Errorf("%s has key %q unknown to English", locale, key)
			}
		}
	}
}

func TestTranslationsAreNotEmptyAndKeepTheFormatVerbsOfEnglish(t *testing.T) {
	verbs := regexp.MustCompile(`%[a-z]`)
	english := load(t, "en")
	for _, locale := range locales {
		for key, text := range load(t, locale) {
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s: key %q is empty", locale, key)
			}
			want := strings.Join(verbs.FindAllString(english[key], -1), " ")
			if got := strings.Join(verbs.FindAllString(text, -1), " "); got != want {
				t.Errorf("%s: key %q has verbs %q, English has %q", locale, key, got, want)
			}
		}
	}
}

func TestTranslateUsesTheLocaleThenEnglishThenTheKey(t *testing.T) {
	if got := i18n.T("fr", "login.submit"); got != load(t, "fr")["login.submit"] || got == load(t, "en")["login.submit"] {
		t.Errorf("T(fr, login.submit) = %q, want the French text", got)
	}
	if got, want := i18n.T("xx", "login.submit"), load(t, "en")["login.submit"]; got != want {
		t.Errorf("unknown locale: T = %q, want the English %q", got, want)
	}
	if got := i18n.T("fr", "no.such.key"); got != "no.such.key" {
		t.Errorf("unknown key: T = %q, want the key itself", got)
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestFormatTimeFollowsTheLocaleInTheServerTimeZone(t *testing.T) {
	previous := time.Local
	time.Local = time.FixedZone("test", 3600) // UTC+1
	t.Cleanup(func() { time.Local = previous })
	at := time.Date(2026, 11, 7, 14, 4, 0, 0, time.UTC) // 15:04 local

	cases := map[string]string{
		"en": "2026-11-07 15:04",
		"fr": "07/11/2026 15:04",
		"de": "07.11.2026 15:04",
		"xx": "2026-11-07 15:04", // unknown locale: English
	}
	for locale, want := range cases {
		if got := i18n.FormatTime(locale, at); got != want {
			t.Errorf("FormatTime(%s) = %q, want %q", locale, got, want)
		}
	}
	if got := i18n.FormatTime("en", time.Time{}); got != "" {
		t.Errorf("FormatTime of the zero time = %q, want empty", got)
	}
}

func TestNChoosesTheSingularOrPluralFormOfTheLanguage(t *testing.T) {
	cases := []struct {
		locale string
		n      int
		want   string
	}{
		{"en", 0, "0 items"}, {"en", 1, "1 item"}, {"en", 2, "2 items"},
		{"fr", 0, "0 élément"}, {"fr", 1, "1 élément"}, {"fr", 2, "2 éléments"},
		{"de", 0, "0 Einträge"}, {"de", 1, "1 Eintrag"}, {"de", 5, "5 Einträge"},
		{"xx", 1, "1 item"},
	}
	for _, tc := range cases {
		if got := i18n.N(tc.locale, "reading.items", tc.n); got != tc.want {
			t.Errorf("N(%s, items, %d) = %q, want %q", tc.locale, tc.n, got, tc.want)
		}
	}
}
