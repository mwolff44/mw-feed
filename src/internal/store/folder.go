// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Folder groups feeds. Titles are unique.
type Folder struct {
	ID        int64
	Title     string
	Position  int
	Collapsed bool
}

const folderColumns = "id, title, position, collapsed"

func scanFolder(row interface{ Scan(...any) error }) (Folder, error) {
	var f Folder
	err := row.Scan(&f.ID, &f.Title, &f.Position, &f.Collapsed)
	return f, err
}

// InsertFolder creates a folder at the end of the list and returns its ID.
func (q *queries) InsertFolder(ctx context.Context, title string) (int64, error) {
	res, err := q.db.ExecContext(ctx,
		"INSERT INTO folders (title, position) VALUES (?, (SELECT COALESCE(MAX(position), 0) + 1 FROM folders))", title)
	if err != nil {
		return 0, fmt.Errorf("insert folder: %w", err)
	}
	return res.LastInsertId()
}

// FolderByID returns a folder, or ErrNotFound.
func (q *queries) FolderByID(ctx context.Context, id int64) (Folder, error) {
	f, err := scanFolder(q.db.QueryRowContext(ctx, "SELECT "+folderColumns+" FROM folders WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Folder{}, ErrNotFound
	}
	if err != nil {
		return Folder{}, fmt.Errorf("read folder: %w", err)
	}
	return f, nil
}

// FolderByTitle returns the folder with exactly that title, or ErrNotFound.
func (q *queries) FolderByTitle(ctx context.Context, title string) (Folder, error) {
	f, err := scanFolder(q.db.QueryRowContext(ctx, "SELECT "+folderColumns+" FROM folders WHERE title = ?", title))
	if errors.Is(err, sql.ErrNoRows) {
		return Folder{}, ErrNotFound
	}
	if err != nil {
		return Folder{}, fmt.Errorf("read folder: %w", err)
	}
	return f, nil
}

// Folders returns all folders in display order.
func (q *queries) Folders(ctx context.Context) ([]Folder, error) {
	rows, err := q.db.QueryContext(ctx, "SELECT "+folderColumns+" FROM folders ORDER BY position, id")
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, fmt.Errorf("list folders: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// UpdateFolderTitle changes a folder title; it reports whether the folder exists.
func (q *queries) UpdateFolderTitle(ctx context.Context, id int64, title string) (bool, error) {
	res, err := q.db.ExecContext(ctx, "UPDATE folders SET title = ? WHERE id = ?", title, id)
	if err != nil {
		return false, fmt.Errorf("rename folder: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteFolder removes a folder and its associations with feeds; it reports
// whether the folder existed. The feeds themselves are kept.
func (q *queries) DeleteFolder(ctx context.Context, id int64) (bool, error) {
	res, err := q.db.ExecContext(ctx, "DELETE FROM folders WHERE id = ?", id)
	if err != nil {
		return false, fmt.Errorf("delete folder: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetFolderPosition sets the display position of a folder.
func (q *queries) SetFolderPosition(ctx context.Context, id int64, position int) error {
	if _, err := q.db.ExecContext(ctx, "UPDATE folders SET position = ? WHERE id = ?", position, id); err != nil {
		return fmt.Errorf("move folder: %w", err)
	}
	return nil
}

// ToggleFolderCollapsed flips the collapsed state of a folder; it reports
// whether the folder exists.
func (q *queries) ToggleFolderCollapsed(ctx context.Context, id int64) (bool, error) {
	res, err := q.db.ExecContext(ctx, "UPDATE folders SET collapsed = 1 - collapsed WHERE id = ?", id)
	if err != nil {
		return false, fmt.Errorf("toggle folder: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
