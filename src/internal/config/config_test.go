// SPDX-License-Identifier: GPL-3.0-or-later

package config_test

import (
	"testing"

	"github.com/mwolff44/mw-feed/internal/config"
)

// env builds a getenv function backed by a map.
func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestLoadDefaults(t *testing.T) {
	got, err := config.Load(nil, env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := config.Config{Addr: ":8080", DataDir: "./data", TrustProxy: false, AllowPrivate: false}
	if got != want {
		t.Errorf("Config = %+v, want %+v", got, want)
	}
}

func TestLoadReadsEnvironment(t *testing.T) {
	got, err := config.Load(nil, env(map[string]string{
		"MW_FEED_ADDR":          "127.0.0.1:9000",
		"MW_FEED_DATA":          "/var/lib/mw-feed",
		"MW_FEED_TRUST_PROXY":   "true",
		"MW_FEED_ALLOW_PRIVATE": "1",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := config.Config{Addr: "127.0.0.1:9000", DataDir: "/var/lib/mw-feed", TrustProxy: true, AllowPrivate: true}
	if got != want {
		t.Errorf("Config = %+v, want %+v", got, want)
	}
}

func TestLoadFlagsOverrideEnvironment(t *testing.T) {
	got, err := config.Load(
		[]string{"-addr", ":9090", "-data=/srv/data", "-trust-proxy", "-allow-private=false"},
		env(map[string]string{
			"MW_FEED_ADDR":          ":1111",
			"MW_FEED_DATA":          "/env/data",
			"MW_FEED_TRUST_PROXY":   "false",
			"MW_FEED_ALLOW_PRIVATE": "true",
		}),
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := config.Config{Addr: ":9090", DataDir: "/srv/data", TrustProxy: true, AllowPrivate: false}
	if got != want {
		t.Errorf("Config = %+v, want %+v", got, want)
	}
}

func TestLoadRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{"invalid boolean in MW_FEED_TRUST_PROXY", nil, map[string]string{"MW_FEED_TRUST_PROXY": "oui"}},
		{"invalid boolean in MW_FEED_ALLOW_PRIVATE", nil, map[string]string{"MW_FEED_ALLOW_PRIVATE": "maybe"}},
		{"invalid boolean in flag", []string{"-trust-proxy=maybe"}, nil},
		{"unknown flag", []string{"-bogus"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := config.Load(tc.args, env(tc.env)); err == nil {
				t.Error("Load succeeded, want an error")
			}
		})
	}
}
