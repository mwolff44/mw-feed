// SPDX-License-Identifier: GPL-3.0-or-later

// Package config reads the runtime configuration from flags and environment.
package config

import (
	"flag"
	"fmt"
	"io"
	"strconv"
)

// Config is the runtime configuration. Everything else lives in the database.
type Config struct {
	Addr         string
	DataDir      string
	TrustProxy   bool
	AllowPrivate bool
}

// Load builds the configuration. Priority: flag > environment > default.
func Load(args []string, getenv func(string) string) (Config, error) {
	cfg := Config{Addr: ":8080", DataDir: "./data"}

	if v := getenv("MW_FEED_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := getenv("MW_FEED_DATA"); v != "" {
		cfg.DataDir = v
	}
	for _, b := range []struct {
		name string
		dst  *bool
	}{
		{"MW_FEED_TRUST_PROXY", &cfg.TrustProxy},
		{"MW_FEED_ALLOW_PRIVATE", &cfg.AllowPrivate},
	} {
		v := getenv(b.name)
		if v == "" {
			continue
		}
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("%s: invalid boolean %q", b.name, v)
		}
		*b.dst = parsed
	}

	fs := flag.NewFlagSet("mw-feed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	fs.StringVar(&cfg.DataDir, "data", cfg.DataDir, "data directory")
	fs.BoolVar(&cfg.TrustProxy, "trust-proxy", cfg.TrustProxy, "trust X-Forwarded-* headers")
	fs.BoolVar(&cfg.AllowPrivate, "allow-private", cfg.AllowPrivate, "allow private addresses when fetching feeds")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
