// SPDX-License-Identifier: GPL-3.0-or-later

package feed

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mwolff44/mw-feed/internal/store"
)

// ErrInvalidRequest means a request contradicts itself, such as adding and
// removing the same folder.
var ErrInvalidRequest = errors.New("invalid request")

// EditFoldersRequest changes the folders of several feeds at once.
type EditFoldersRequest struct {
	FeedIDs []int64
	// AddFolderIDs are existing folders to add to each feed. NewFolder, if not
	// empty, names one more: the existing folder with that title, or a new one
	// (created once, whatever the number of feeds).
	AddFolderIDs []int64
	NewFolder    string
	// RemoveFolderIDs are removed from each feed after the additions.
	RemoveFolderIDs []int64
}

// EditFeedsFolders adds folders to, then removes folders from, each of the
// given feeds, and returns how many feeds it handled. It is all or nothing: an
// unknown feed or folder gives ErrNotFound, adding and removing the same folder
// gives ErrInvalidRequest, and in both cases nothing changes (no folder is
// created). Without feeds there is nothing to do.
func (s *Service) EditFeedsFolders(ctx context.Context, req EditFoldersRequest) (int, error) {
	feedIDs := dedupe(req.FeedIDs)
	if len(feedIDs) == 0 {
		return 0, nil
	}
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		feeds := make([]store.Feed, 0, len(feedIDs))
		for _, id := range feedIDs {
			f, err := tx.FeedByID(ctx, id)
			if err != nil {
				return fmt.Errorf("feed %d: %w", id, err)
			}
			feeds = append(feeds, f)
		}
		add, remove := dedupe(req.AddFolderIDs), dedupe(req.RemoveFolderIDs)
		for _, id := range append(slices.Clone(add), remove...) {
			if _, err := tx.FolderByID(ctx, id); err != nil {
				return fmt.Errorf("folder %d: %w", id, err)
			}
		}
		if name := strings.TrimSpace(req.NewFolder); name != "" {
			id, err := findOrCreateFolder(ctx, tx, name)
			if err != nil {
				return err
			}
			add = dedupe(append(add, id))
		}
		for _, id := range add {
			if slices.Contains(remove, id) {
				return fmt.Errorf("%w: folder %d is both added and removed", ErrInvalidRequest, id)
			}
		}
		for _, f := range feeds {
			folders := dedupe(append(slices.Clone(f.FolderIDs), add...))
			folders = slices.DeleteFunc(folders, func(id int64) bool { return slices.Contains(remove, id) })
			if err := tx.SetFeedFolders(ctx, f.ID, folders); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(feedIDs), nil
}

// DeleteFeeds deletes the given feeds, in one transaction, with the rules of
// DeleteFeed (starred items stay). Unknown feeds are ignored, so a stale list
// does not block anything; it returns how many feeds were deleted.
func (s *Service) DeleteFeeds(ctx context.Context, ids []int64) (int, error) {
	deleted := 0
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		for _, id := range dedupe(ids) {
			ok, err := tx.DeleteFeed(ctx, id)
			if err != nil {
				return err
			}
			if ok {
				deleted++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// dedupe returns ids without repeats, in first-seen order.
func dedupe(ids []int64) []int64 {
	var out []int64
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
