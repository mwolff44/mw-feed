// SPDX-License-Identifier: GPL-3.0-or-later

package feed

import (
	"context"
	"errors"
	"slices"

	"github.com/mwolff44/mw-feed/internal/opml"
	"github.com/mwolff44/mw-feed/internal/store"
)

// maxListedRejections bounds the list of rejected feeds in an ImportResult; the
// counter stays exact.
const maxListedRejections = 100

// Rejection is a feed of an imported file that could not be imported.
type Rejection = opml.Rejected

// ImportOptions tune ImportOPML.
type ImportOptions struct {
	// Overwrite replaces the title, description, site URL and folders of the
	// feeds that already exist. Without it, existing feeds are left untouched.
	Overwrite bool
}

// ImportResult is the report of an import.
type ImportResult struct {
	Imported       int // new feeds
	Updated        int // existing feeds overwritten
	Skipped        int // existing feeds left untouched
	FoldersCreated int
	RejectedCount  int         // exact
	Rejected       []Rejection // the first ones, at most maxListedRejections
}

// ImportOPML adds the feeds and folders of an OPML file, in one transaction:
// either everything is imported or, on an error, nothing is. Nothing is
// downloaded: new feeds are never updated yet, so the scheduler fetches them.
func (s *Service) ImportOPML(ctx context.Context, data []byte, opts ImportOptions) (ImportResult, error) {
	doc, err := opml.Parse(data)
	if err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{RejectedCount: len(doc.Rejected)}
	res.Rejected = doc.Rejected
	if len(res.Rejected) > maxListedRejections {
		res.Rejected = res.Rejected[:maxListedRejections]
	}

	err = s.store.InTx(ctx, func(tx *store.Tx) error {
		// Folders: the existing ones are reused, the others created in order of appearance.
		folderIDs := map[string]int64{}
		existing, err := tx.Folders(ctx)
		if err != nil {
			return err
		}
		for _, f := range existing {
			folderIDs[f.Title] = f.ID
		}
		for _, title := range doc.Folders {
			if _, ok := folderIDs[title]; ok {
				continue
			}
			id, err := tx.InsertFolder(ctx, title)
			if err != nil {
				return err
			}
			folderIDs[title] = id
			res.FoldersCreated++
		}
		idsOf := func(titles []string) []int64 {
			var ids []int64
			for _, t := range titles {
				ids = append(ids, folderIDs[t])
			}
			return ids
		}

		for _, f := range doc.Feeds {
			current, err := tx.FeedByXMLURL(ctx, f.XMLURL)
			switch {
			case err == nil && !opts.Overwrite:
				res.Skipped++
			case err == nil:
				if err := tx.UpdateFeedFields(ctx, current.ID, store.FeedEdit{
					Title: f.Title, SiteURL: f.SiteURL, Description: f.Description, XMLURL: current.XMLURL,
					UpdateInterval: current.UpdateInterval,
				}); err != nil {
					return err
				}
				if err := tx.SetFeedFolders(ctx, current.ID, idsOf(f.Folders)); err != nil {
					return err
				}
				res.Updated++
			case isNotFound(err):
				id, err := tx.InsertFeed(ctx, store.NewFeed{
					XMLURL: f.XMLURL, SiteURL: f.SiteURL, Title: f.Title, Description: f.Description,
				})
				if err != nil {
					return err
				}
				if err := tx.SetFeedFolders(ctx, id, idsOf(f.Folders)); err != nil {
					return err
				}
				res.Imported++
			default:
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

// ExportOPML writes every feed and folder as an OPML file: feeds in natural
// order of title, folders in display order, empty folders included.
func (s *Service) ExportOPML(ctx context.Context) ([]byte, error) {
	folders, err := s.store.Folders(ctx)
	if err != nil {
		return nil, err
	}
	feeds, err := s.Feeds(ctx)
	if err != nil {
		return nil, err
	}
	var doc opml.Document
	for _, folder := range folders {
		doc.Folders = append(doc.Folders, folder.Title)
	}
	for _, f := range feeds {
		of := opml.Feed{Title: f.Title, Description: f.Description, XMLURL: f.XMLURL, SiteURL: f.SiteURL}
		for _, folder := range folders { // in display order, whatever the order of the IDs
			if slices.Contains(f.FolderIDs, folder.ID) {
				of.Folders = append(of.Folders, folder.Title)
			}
		}
		doc.Feeds = append(doc.Feeds, of)
	}
	return opml.Generate(doc)
}

func isNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
