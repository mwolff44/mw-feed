// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mwolff44/mw-feed/internal/store"
)

func TestOpenCreatesMissingDataDirWithRestrictedPermissions(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "a", "b", "data")

	s, err := store.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("data dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dataDir)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o750); got != want {
		t.Errorf("permissions = %o, want %o", got, want)
	}
}

func openTemp(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSettingsOnFreshDatabaseReturnDefaults(t *testing.T) {
	s := openTemp(t)

	got, err := s.Settings(context.Background())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}

	want := store.Settings{
		Title:        "mw-feed",
		Locale:       "en",
		Redirector:   "",
		MaxItems:     100,
		MaxUpdate:    60,
		Order:        "newerFirst",
		View:         "list",
		Filter:       "unread",
		ListFeeds:    true,
		ByPage:       10,
		AutoreadItem: false,
		Autohide:     false,
		Autofocus:    true,
		AddFavicon:   false,
		Blank:        false,
		Swipe:        true,
		Theme:        "auto",
	}
	if got != want {
		t.Errorf("Settings = %+v, want %+v", got, want)
	}
}

func TestOpenExistingDatabaseSucceeds(t *testing.T) {
	dataDir := t.TempDir()
	first, err := store.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := store.Open(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	if _, err := second.Settings(context.Background()); err != nil {
		t.Errorf("Settings after reopen: %v", err)
	}
}

func TestOpenRefusesDatabaseNewerThanBinary(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "mw-feed.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec("PRAGMA user_version = 9999"); err != nil {
		t.Fatalf("set version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s, err := store.Open(context.Background(), dataDir)
	if err == nil {
		_ = s.Close()
		t.Fatal("Open succeeded, want an error")
	}
	if !errors.Is(err, store.ErrSchemaTooNew) {
		t.Errorf("error = %v, want store.ErrSchemaTooNew", err)
	}
}

func nonDefaultSettings() store.Settings {
	return store.Settings{
		Title:        "My feeds",
		Locale:       "fr",
		Redirector:   "https://r.example/?",
		MaxItems:     50,
		MaxUpdate:    30,
		Order:        "olderFirst",
		View:         "expanded",
		Filter:       "all",
		ListFeeds:    false,
		ByPage:       25,
		AutoreadItem: true,
		Autohide:     true,
		Autofocus:    false,
		AddFavicon:   true,
		Blank:        true,
		Swipe:        false,
		Theme:        "dark",
	}
}

func TestSavedSettingsSurviveReopen(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	first, err := store.Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := nonDefaultSettings()
	if err := first.SaveSettings(ctx, want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := store.Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	got, err := second.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got != want {
		t.Errorf("Settings = %+v, want %+v", got, want)
	}
}

func TestSaveSettingsRejectsInvalidValuesAndKeepsPreviousOnes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*store.Settings)
	}{
		{"order unknown", func(s *store.Settings) { s.Order = "sideways" }},
		{"view unknown", func(s *store.Settings) { s.View = "grid" }},
		{"filter unknown", func(s *store.Settings) { s.Filter = "none" }},
		{"theme unknown", func(s *store.Settings) { s.Theme = "pink" }},
		{"locale unknown", func(s *store.Settings) { s.Locale = "es" }},
		{"by_page zero", func(s *store.Settings) { s.ByPage = 0 }},
		{"by_page negative", func(s *store.Settings) { s.ByPage = -1 }},
		{"max_items zero", func(s *store.Settings) { s.MaxItems = 0 }},
		{"max_update zero", func(s *store.Settings) { s.MaxUpdate = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := openTemp(t)
			original, err := s.Settings(ctx)
			if err != nil {
				t.Fatalf("Settings: %v", err)
			}
			bad := nonDefaultSettings()
			tc.mutate(&bad)

			err = s.SaveSettings(ctx, bad)

			if !errors.Is(err, store.ErrInvalidSettings) {
				t.Fatalf("error = %v, want store.ErrInvalidSettings", err)
			}
			after, err := s.Settings(ctx)
			if err != nil {
				t.Fatalf("Settings: %v", err)
			}
			if after != original {
				t.Errorf("settings changed after rejected save: %+v", after)
			}
		})
	}
}

func TestSaveSettingsAcceptsEveryAllowedValue(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	for _, order := range []string{"newerFirst", "olderFirst"} {
		for _, view := range []string{"list", "expanded"} {
			for _, filter := range []string{"unread", "all"} {
				for _, theme := range []string{"auto", "light", "dark"} {
					for _, locale := range []string{"en", "fr", "de"} {
						st := nonDefaultSettings()
						st.Order, st.View, st.Filter, st.Theme, st.Locale = order, view, filter, theme, locale
						if err := s.SaveSettings(ctx, st); err != nil {
							t.Errorf("SaveSettings(%s,%s,%s,%s,%s): %v", order, view, filter, theme, locale, err)
						}
					}
				}
			}
		}
	}
}

func TestConcurrentSettingsWritesAllSucceed(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	const writers = 16

	errs := make(chan error, writers)
	for i := range writers {
		go func() {
			st := nonDefaultSettings()
			st.ByPage = i + 1
			errs <- s.SaveSettings(ctx, st)
		}()
	}

	for range writers {
		if err := <-errs; err != nil {
			t.Errorf("concurrent SaveSettings: %v", err)
		}
	}
}

func TestOpenWorksWithSpecialCharactersInDataDirPath(t *testing.T) {
	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "my data #1?x%20y")

	s, err := store.Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.SaveSettings(ctx, nonDefaultSettings()); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "mw-feed.db")); err != nil {
		t.Errorf("database file not in data dir: %v", err)
	}
}
