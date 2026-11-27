// SPDX-License-Identifier: GPL-3.0-or-later

package feed_test

import (
	"errors"
	"testing"

	"github.com/mwolff44/mw-feed/internal/feed"
)

func (f *fixture) foldersOf(t *testing.T, feeds ...feed.Feed) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, in := range feeds {
		current, err := f.store.FeedByID(ctx, in.ID)
		if err != nil {
			t.Fatalf("FeedByID: %v", err)
		}
		out[in.Title] = f.folderTitlesOf(t, current)
	}
	return out
}

func (f *fixture) folderCount(t *testing.T) int {
	t.Helper()
	folders, err := f.svc.Folders(ctx)
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	return len(folders)
}

func TestEditFeedsFoldersAddsThenRemovesOnTheSelectedFeedsOnly(t *testing.T) {
	f := newFixture(t) // A in X,Y ; B in Y ; C in none

	n, err := f.svc.EditFeedsFolders(ctx, feed.EditFoldersRequest{
		FeedIDs: []int64{f.a.ID, f.c.ID}, AddFolderIDs: []int64{f.x}, NewFolder: "Z", RemoveFolderIDs: []int64{f.y},
	})

	if err != nil || n != 2 {
		t.Fatalf("EditFeedsFolders = %d, %v; want 2, nil", n, err)
	}
	got := f.foldersOf(t, f.a, f.b, f.c)
	want := map[string]string{"Feed A": "X,Z", "Feed B": "Y", "Feed C": "X,Z"}
	for title, folders := range want {
		if got[title] != folders {
			t.Errorf("%s is in %q, want %q", title, got[title], folders)
		}
	}
	if count := f.folderCount(t); count != 3 {
		t.Errorf("%d folders, want 3: Z is created once for both feeds", count)
	}
}

func TestEditFeedsFoldersUsesTheExistingFolderOfTheSameTitle(t *testing.T) {
	f := newFixture(t)

	if _, err := f.svc.EditFeedsFolders(ctx, feed.EditFoldersRequest{FeedIDs: []int64{f.c.ID}, NewFolder: " X "}); err != nil {
		t.Fatalf("EditFeedsFolders: %v", err)
	}

	if got := f.foldersOf(t, f.c)["Feed C"]; got != "X" {
		t.Errorf("C is in %q, want X", got)
	}
	if count := f.folderCount(t); count != 2 {
		t.Errorf("%d folders, want 2: no duplicate of X", count)
	}
}

func TestEditFeedsFoldersChangesNothingWhenAnythingIsUnknownOrContradictory(t *testing.T) {
	cases := []struct {
		name string
		req  func(f *fixture) feed.EditFoldersRequest
		want error
	}{
		{"unknown feed", func(f *fixture) feed.EditFoldersRequest {
			return feed.EditFoldersRequest{FeedIDs: []int64{f.a.ID, 999}, AddFolderIDs: []int64{f.x}, NewFolder: "Z"}
		}, feed.ErrNotFound},
		{"unknown folder to add", func(f *fixture) feed.EditFoldersRequest {
			return feed.EditFoldersRequest{FeedIDs: []int64{f.c.ID}, AddFolderIDs: []int64{f.x, 999}, NewFolder: "Z"}
		}, feed.ErrNotFound},
		{"unknown folder to remove", func(f *fixture) feed.EditFoldersRequest {
			return feed.EditFoldersRequest{FeedIDs: []int64{f.a.ID}, RemoveFolderIDs: []int64{999}, NewFolder: "Z"}
		}, feed.ErrNotFound},
		{"same folder added and removed", func(f *fixture) feed.EditFoldersRequest {
			return feed.EditFoldersRequest{FeedIDs: []int64{f.c.ID}, AddFolderIDs: []int64{f.x}, RemoveFolderIDs: []int64{f.x}, NewFolder: "Z"}
		}, feed.ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			before := f.foldersOf(t, f.a, f.b, f.c)

			n, err := f.svc.EditFeedsFolders(ctx, tc.req(f))

			if !errors.Is(err, tc.want) || n != 0 {
				t.Fatalf("EditFeedsFolders = %d, %v; want 0, %v", n, err, tc.want)
			}
			after := f.foldersOf(t, f.a, f.b, f.c)
			for title, folders := range before {
				if after[title] != folders {
					t.Errorf("%s changed from %q to %q", title, folders, after[title])
				}
			}
			if count := f.folderCount(t); count != 2 {
				t.Errorf("%d folders, want 2: the new folder must not be created", count)
			}
		})
	}
}

func TestEditFeedsFoldersWithoutFeedsDoesNothing(t *testing.T) {
	f := newFixture(t)

	n, err := f.svc.EditFeedsFolders(ctx, feed.EditFoldersRequest{AddFolderIDs: []int64{f.x}, NewFolder: "Z"})

	if err != nil || n != 0 {
		t.Fatalf("EditFeedsFolders = %d, %v; want 0, nil", n, err)
	}
	if count := f.folderCount(t); count != 2 {
		t.Errorf("%d folders, want 2: nothing to apply the new folder to", count)
	}
}

func TestDeleteFeedsRemovesTheExistingOnesKeepsStarredItemsAndIgnoresTheRest(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.SetStarred(ctx, f.itemByTitle["a2"].ID, true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}

	n, err := f.svc.DeleteFeeds(ctx, []int64{f.a.ID, f.b.ID, f.a.ID, 999})

	if err != nil || n != 2 {
		t.Fatalf("DeleteFeeds = %d, %v; want 2, nil", n, err)
	}
	feeds, err := f.svc.Feeds(ctx)
	if err != nil || len(feeds) != 1 || feeds[0].ID != f.c.ID {
		t.Fatalf("Feeds = %+v, %v; want only C", feeds, err)
	}
	starred, err := f.svc.ListStarred(ctx, feed.StarredQuery{})
	if err != nil || len(starred.Items) != 1 || starred.Items[0].Title != "a2" || starred.Items[0].FeedTitle != "Feed A" {
		t.Errorf("starred = %+v, %v; want a2 kept with its feed title", starred.Items, err)
	}
	if count := f.folderCount(t); count != 2 {
		t.Errorf("%d folders, want 2: deleting feeds keeps folders", count)
	}
}

func TestDeleteFeedsWithNothingToDeleteIsNotAnError(t *testing.T) {
	f := newFixture(t)

	n, err := f.svc.DeleteFeeds(ctx, nil)

	if err != nil || n != 0 {
		t.Fatalf("DeleteFeeds(nil) = %d, %v; want 0, nil", n, err)
	}
}

func TestAddFeedRefusesAURLThatIsNotAbsoluteHTTPWithoutDownloading(t *testing.T) {
	e := newEnv(t)
	for _, u := range []string{"ftp://example.org/feed", "example.org/feed", "javascript:alert(1)", "", "  "} {
		_, err := e.svc.AddFeed(ctx, feed.AddFeedRequest{URL: u})

		if !errors.Is(err, feed.ErrInvalidURL) {
			t.Errorf("AddFeed(%q) error = %v, want ErrInvalidURL", u, err)
		}
	}
	if e.server.hits != 0 {
		t.Errorf("the feed server was hit %d times", e.server.hits)
	}
}
