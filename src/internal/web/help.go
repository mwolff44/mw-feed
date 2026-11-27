// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"net/http"

	"github.com/mwolff44/mw-feed/internal/auth"
)

// shortcut is a keyboard shortcut of the help page: the keys, and the key of
// its description.
type shortcut struct {
	Keys string
	Text string
}

// helpView is the data of the help page.
type helpView struct {
	Version string
	Items   []shortcut
	Menu    []shortcut
}

// shortcuts are those of the plan (section 6.3), without sharing (removed with
// Shaarli). Phase 8 implements them.
var (
	itemShortcuts = []shortcut{
		{"⎵, t", "help.sc_toggle"}, {"m", "help.sc_mark"}, {"⇧ + m", "help.sc_mark_open"},
		{"→, n", "help.sc_next"}, {"←, p", "help.sc_previous"},
		{"⇧ + n", "help.sc_next_page"}, {"⇧ + p", "help.sc_previous_page"},
		{"j", "help.sc_next_open"}, {"k", "help.sc_previous_open"},
		{"o", "help.sc_open_tab"}, {"⇧ + o", "help.sc_open_here"},
		{"a", "help.sc_read_all"}, {"*", "help.sc_star"}, {"z", "help.sc_open_unread"},
	}
	menuShortcuts = []shortcut{
		{"h", "help.sc_home"}, {"v", "help.sc_view"}, {"f", "help.sc_feeds"}, {"e", "help.sc_edit"},
		{"u", "help.sc_update"}, {"r", "help.sc_reload"}, {"?, F1", "help.sc_help"},
	}
)

func (s *server) help(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	version := s.opts.Version
	if version == "" {
		version = "dev"
	}
	s.render(w, r, "help", http.StatusOK, page{
		CSRFToken: sess.CSRFToken, Help: &helpView{Version: version, Items: itemShortcuts, Menu: menuShortcuts},
	})
}
