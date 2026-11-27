// SPDX-License-Identifier: GPL-3.0-or-later

package feed

import (
	"context"
	"errors"
	"strings"

	"github.com/mwolff44/mw-feed/internal/store"
)

// Folder is a group of feeds.
type Folder = store.Folder

var (
	// ErrFolderExists means a folder with that title already exists.
	ErrFolderExists = errors.New("folder already exists")
	// ErrInvalidFolderTitle means the folder title is empty.
	ErrInvalidFolderTitle = errors.New("folder title must not be empty")
)

// CreateFolder adds a folder at the end of the list. The title is trimmed and
// compared case-sensitively.
func (s *Service) CreateFolder(ctx context.Context, title string) (Folder, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Folder{}, ErrInvalidFolderTitle
	}
	var created Folder
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.FolderByTitle(ctx, title); err == nil {
			return ErrFolderExists
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		id, err := tx.InsertFolder(ctx, title)
		if err != nil {
			return err
		}
		created, err = tx.FolderByID(ctx, id)
		return err
	})
	return created, err
}

// Folders returns all folders in display order.
func (s *Service) Folders(ctx context.Context) ([]Folder, error) {
	return s.store.Folders(ctx)
}

var (
	// ErrNotFound means the feed, folder or item does not exist.
	ErrNotFound = store.ErrNotFound
	// ErrInvalidOrder means a folder order does not list every folder exactly once.
	ErrInvalidOrder = errors.New("folder order must list every folder exactly once")
)

// RenameFolder changes a folder title. An empty title deletes the folder (its
// feeds are kept). Renaming to the title of another folder is an error.
func (s *Service) RenameFolder(ctx context.Context, id int64, title string) error {
	title = strings.TrimSpace(title)
	return s.store.InTx(ctx, func(tx *store.Tx) error {
		if title == "" {
			ok, err := tx.DeleteFolder(ctx, id)
			if err == nil && !ok {
				err = ErrNotFound
			}
			return err
		}
		if other, err := tx.FolderByTitle(ctx, title); err == nil && other.ID != id {
			return ErrFolderExists
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		ok, err := tx.UpdateFolderTitle(ctx, id, title)
		if err == nil && !ok {
			err = ErrNotFound
		}
		return err
	})
}

// DeleteFolder removes a folder. Its feeds are kept.
func (s *Service) DeleteFolder(ctx context.Context, id int64) error {
	ok, err := s.store.DeleteFolder(ctx, id)
	if err == nil && !ok {
		err = ErrNotFound
	}
	return err
}

// ReorderFolders sets the display order. ids must list every folder exactly once.
func (s *Service) ReorderFolders(ctx context.Context, ids []int64) error {
	return s.store.InTx(ctx, func(tx *store.Tx) error {
		folders, err := tx.Folders(ctx)
		if err != nil {
			return err
		}
		existing := make(map[int64]bool, len(folders))
		for _, f := range folders {
			existing[f.ID] = true
		}
		seen := make(map[int64]bool, len(ids))
		for _, id := range ids {
			if !existing[id] || seen[id] {
				return ErrInvalidOrder
			}
			seen[id] = true
		}
		if len(ids) != len(folders) {
			return ErrInvalidOrder
		}
		for i, id := range ids {
			if err := tx.SetFolderPosition(ctx, id, i+1); err != nil {
				return err
			}
		}
		return nil
	})
}

// ToggleFolder collapses an expanded folder and expands a collapsed one.
func (s *Service) ToggleFolder(ctx context.Context, id int64) error {
	ok, err := s.store.ToggleFolderCollapsed(ctx, id)
	if err == nil && !ok {
		err = ErrNotFound
	}
	return err
}
