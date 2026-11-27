// SPDX-License-Identifier: GPL-3.0-or-later

// Package i18n translates interface texts. English is the reference language.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const defaultLocale = "en"

//go:embed locales/*.json
var files embed.FS

var bundle = sync.OnceValue(func() map[string]map[string]string {
	entries, err := files.ReadDir("locales")
	if err != nil {
		panic(err)
	}
	out := make(map[string]map[string]string)
	for _, e := range entries {
		raw, err := files.ReadFile("locales/" + e.Name())
		if err != nil {
			panic(err)
		}
		var texts map[string]string
		if err := json.Unmarshal(raw, &texts); err != nil {
			panic(fmt.Sprintf("locale %s: %v", e.Name(), err))
		}
		out[e.Name()[:len(e.Name())-len(".json")]] = texts
	}
	return out
})

// T returns the text of key in locale. A key missing in locale falls back to
// English; a key missing everywhere is returned as is, so the gap is visible.
func T(locale, key string) string {
	b := bundle()
	if text, ok := b[locale][key]; ok {
		return text
	}
	if text, ok := b[defaultLocale][key]; ok {
		return text
	}
	return key
}

var timeLayouts = map[string]string{
	"en": "2006-01-02 15:04",
	"fr": "02/01/2006 15:04",
	"de": "02.01.2006 15:04",
}

// FormatTime formats t for locale, in the server time zone (time.Local, which
// follows the TZ variable). The zero time gives an empty string; an unknown
// locale uses the English format.
func FormatTime(locale string, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	layout, ok := timeLayouts[locale]
	if !ok {
		layout = timeLayouts[defaultLocale]
	}
	return t.In(time.Local).Format(layout)
}

// N translates a counted text: it picks key+"_one" or key+"_other" by the
// plural rule of the locale (French treats 0 as singular) and formats n in it.
func N(locale, key string, n int) string {
	singular := n == 1
	if locale == "fr" {
		singular = n >= 0 && n < 2
	}
	form := "_other"
	if singular {
		form = "_one"
	}
	return fmt.Sprintf(T(locale, key+form), n)
}
