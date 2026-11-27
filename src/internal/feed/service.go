// SPDX-License-Identifier: GPL-3.0-or-later

package feed

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mwolff44/mw-feed/internal/fetch"
	"github.com/mwolff44/mw-feed/internal/store"
)

// ErrDuplicateFeed means a feed with that URL already exists.
var ErrDuplicateFeed = errors.New("feed already exists")

// Feed and Item are the stored feed and item.
type (
	Feed = store.Feed
	Item = store.Item
)

// Fetcher downloads a URL. *fetch.Fetcher implements it.
type Fetcher interface {
	Fetch(ctx context.Context, req fetch.Request) (fetch.Response, error)
}

// Clock provides the current time; tests inject a fake one.
type Clock interface{ Now() time.Time }

// Service holds the feed rules: adding, updating, editing, deleting.
type Service struct {
	store   *store.Store
	fetcher Fetcher
	clock   Clock
	// slots bounds the feed updates running at the same time, whoever asks.
	slots chan struct{}
	// locks keep two updates of the same feed from running together.
	locks feedLocks
}

// maxConcurrentUpdates is the number of feed updates that may run at once.
const maxConcurrentUpdates = 4

// NewService returns a Service.
func NewService(st *store.Store, f Fetcher, clock Clock) *Service {
	return &Service{store: st, fetcher: f, clock: clock, slots: make(chan struct{}, maxConcurrentUpdates)}
}

// AddFeedRequest is the input of AddFeed.
type AddFeedRequest struct {
	URL string
	// FolderIDs are existing folders to put the feed in. NewFolder, if not
	// empty, names one more folder: the existing one with that title, or a new one.
	FolderIDs []int64
	NewFolder string
}

// AddFeed downloads and subscribes to a feed, then imports its items.
func (s *Service) AddFeed(ctx context.Context, req AddFeedRequest) (Feed, error) {
	req.URL = strings.TrimSpace(req.URL)
	if _, err := s.store.FeedByXMLURL(ctx, req.URL); err == nil {
		return Feed{}, ErrDuplicateFeed
	} else if !errors.Is(err, store.ErrNotFound) {
		return Feed{}, err
	}
	for _, id := range req.FolderIDs { // refuse unknown folders before downloading anything
		if _, err := s.store.FolderByID(ctx, id); err != nil {
			return Feed{}, fmt.Errorf("%w: folder %d", err, id)
		}
	}
	resp, err := s.fetcher.Fetch(ctx, fetch.Request{URL: req.URL})
	if err != nil {
		return Feed{}, fmt.Errorf("download feed: %w", err)
	}
	now := s.clock.Now()
	parsed, err := Parse(resp.Body, now)
	if err != nil {
		return Feed{}, err
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return Feed{}, err
	}
	entries := newest(parsed.Items, settings.MaxItems)
	title := parsed.Title
	if title == "" {
		title = hostName(req.URL) // a feed without a title is named after its host
	}
	var newestDate time.Time // the date of the newest item imported
	if len(entries) > 0 {
		newestDate = entries[0].PublishedAt
	}
	var id int64
	err = s.store.InTx(ctx, func(tx *store.Tx) error {
		var err error
		id, err = tx.InsertFeed(ctx, store.NewFeed{
			XMLURL: req.URL, SiteURL: parsed.SiteURL, Title: title, Description: parsed.Description,
			ETag: resp.ETag, LastModified: resp.LastModified, LastUpdateAt: now, LastItemTime: newestDate,
		})
		if err != nil {
			return err
		}
		for _, e := range entries {
			if _, err := tx.InsertItem(ctx, newItem(id, title, parsed.SiteURL, e)); err != nil {
				return err
			}
		}
		folderIDs := slices.Clone(req.FolderIDs)
		if name := strings.TrimSpace(req.NewFolder); name != "" {
			folderID, err := findOrCreateFolder(ctx, tx, name)
			if err != nil {
				return err
			}
			folderIDs = append(folderIDs, folderID)
		}
		return tx.SetFeedFolders(ctx, id, folderIDs)
	})
	if err != nil {
		return Feed{}, err
	}
	return s.store.FeedByID(ctx, id)
}

// Feeds returns all feeds sorted by title, naturally and ignoring case.
func (s *Service) Feeds(ctx context.Context) ([]Feed, error) {
	feeds, err := s.store.Feeds(ctx) // by ID
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(feeds, func(a, b Feed) int { return naturalCompare(a.Title, b.Title) })
	return feeds, nil
}

// newItem builds the stored item of a parsed entry: "via" falls back to the
// site URL, and the author is prefixed with the feed title.
func newItem(feedID int64, feedTitle, siteURL string, e Entry) store.NewItem {
	via := e.Via
	if via == "" {
		via = siteURL
	}
	author := feedTitle
	if e.Author != "" {
		author = feedTitle + " (" + e.Author + ")"
	}
	return store.NewItem{
		FeedID: feedID, FeedTitle: feedTitle, Title: e.Title, Link: e.Link, Via: via, Author: author,
		Description: e.Description, Content: e.Content, PublishedAt: e.PublishedAt,
	}
}

// hostName returns the host of a URL, without the port.
func hostName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Hostname()
}

// newest returns the n newest entries, by date, newest first. Entries with the
// same date keep the order of the feed.
func newest(entries []Entry, n int) []Entry {
	sorted := slices.Clone(entries)
	slices.SortStableFunc(sorted, func(a, b Entry) int { return b.PublishedAt.Compare(a.PublishedAt) })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

// findOrCreateFolder returns the folder with that title, creating it if needed.
func findOrCreateFolder(ctx context.Context, tx *store.Tx, title string) (int64, error) {
	f, err := tx.FolderByTitle(ctx, title)
	if err == nil {
		return f.ID, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return 0, err
	}
	return tx.InsertFolder(ctx, title)
}

var (
	// ErrInvalidFeedTitle means the feed title is empty.
	ErrInvalidFeedTitle = errors.New("feed title must not be empty")
	// ErrInvalidURL means a URL is not an absolute http or https URL.
	ErrInvalidURL = errors.New("invalid URL: only absolute http and https URLs are accepted")
)

// minUpdateInterval is the shortest delay, in minutes, between two updates of a feed.
const minUpdateInterval = 5

// EditFeedRequest holds what the feed edit form sends. The folder list replaces
// the previous one, and an empty Description or SiteURL clears it.
type EditFeedRequest struct {
	Title       string
	SiteURL     string
	Description string
	XMLURL      string
	// UpdateInterval is in minutes; nil, or a value outside [5, maxUpdate], means automatic.
	UpdateInterval *int
	FolderIDs      []int64
	NewFolder      string
}

// EditFeed changes a feed. Nothing is downloaded: a new XML URL is only checked
// for shape and uniqueness. Either everything is saved, or nothing.
func (s *Service) EditFeed(ctx context.Context, id int64, req EditFeedRequest) (Feed, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return Feed{}, ErrInvalidFeedTitle
	}
	siteURL := strings.TrimSpace(req.SiteURL)
	if siteURL != "" && httpURL(siteURL) == "" {
		return Feed{}, fmt.Errorf("%w: site URL", ErrInvalidURL)
	}
	xmlURL := httpURL(req.XMLURL)
	if xmlURL == "" {
		return Feed{}, fmt.Errorf("%w: XML URL", ErrInvalidURL)
	}
	settings, err := s.store.Settings(ctx)
	if err != nil {
		return Feed{}, err
	}
	interval := req.UpdateInterval
	if interval != nil && (*interval < minUpdateInterval || *interval > settings.MaxUpdate) {
		interval = nil
	}

	err = s.store.InTx(ctx, func(tx *store.Tx) error {
		current, err := tx.FeedByID(ctx, id)
		if err != nil {
			return err
		}
		if xmlURL != current.XMLURL {
			if other, err := tx.FeedByXMLURL(ctx, xmlURL); err == nil && other.ID != id {
				return ErrDuplicateFeed
			} else if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		folderIDs := slices.Clone(req.FolderIDs)
		for _, fid := range folderIDs {
			if _, err := tx.FolderByID(ctx, fid); err != nil {
				return fmt.Errorf("%w: folder %d", err, fid)
			}
		}
		if name := strings.TrimSpace(req.NewFolder); name != "" {
			fid, err := findOrCreateFolder(ctx, tx, name)
			if err != nil {
				return err
			}
			folderIDs = append(folderIDs, fid)
		}
		if err := tx.UpdateFeedFields(ctx, id, store.FeedEdit{
			Title: title, SiteURL: siteURL, Description: strings.TrimSpace(req.Description), XMLURL: xmlURL,
			UpdateInterval: interval, ClearValidators: xmlURL != current.XMLURL,
		}); err != nil {
			return err
		}
		return tx.SetFeedFolders(ctx, id, folderIDs)
	})
	if err != nil {
		return Feed{}, err
	}
	return s.store.FeedByID(ctx, id)
}
