// SPDX-License-Identifier: GPL-3.0-or-later

// Package i18n translates interface texts. English is the reference language.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
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
