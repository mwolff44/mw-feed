// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/mwolff44/mw-feed/internal/auth"
	"github.com/mwolff44/mw-feed/internal/favicon"
	"github.com/mwolff44/mw-feed/internal/store"
)

// IconSource gives the icon of a feed's site. *favicon.Service implements it.
type IconSource interface {
	Icon(ctx context.Context, siteURL, xmlURL string) (favicon.Icon, bool)
}

const defaultIconPath = "/static/default-icon.png"

// iconURL is the URL of the icon of a feed; items whose feed was deleted get the default icon.
func iconURL(feedID int64) string {
	if feedID <= 0 {
		return defaultIconPath
	}
	return "/favicon/" + strconv.FormatInt(feedID, 10)
}

// favicon serves the icon of a feed: the downloaded one while the addFavicon
// setting is on and there is one, else the default icon embedded in the binary.
// The browser keeps the answer for a day.
func (s *server) favicon(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r)
		return
	}
	icon, found := favicon.Icon{}, false
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if settings.AddFavicon && s.opts.Icons != nil {
		f, err := s.store.FeedByID(r.Context(), id)
		switch {
		case err == nil:
			icon, found = s.opts.Icons.Icon(r.Context(), f.SiteURL, f.XMLURL)
		case !errors.Is(err, store.ErrNotFound):
			s.serverError(w, r, err)
			return
		}
	}
	if !found {
		data, err := fs.ReadFile(staticFiles, "static/default-icon.png")
		if err != nil {
			s.serverError(w, r, err) // the file is embedded at build time
			return
		}
		icon = favicon.Icon{Data: data, Type: "image/png"}
	}
	w.Header().Set("Content-Type", icon.Type)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(icon.Data)
}
