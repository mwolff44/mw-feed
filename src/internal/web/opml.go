// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/mwolff44/mw-feed/internal/auth"
	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/i18n"
	"github.com/mwolff44/mw-feed/internal/opml"
)

// maxImportBody bounds the body of an import request: the largest OPML file
// plus a margin for the other form fields and the multipart framing.
const maxImportBody = opml.MaxSize + 1<<20

// importView is the data of the import pages.
type importView struct {
	MaxSize  string
	Report   *feed.ImportResult
	Rejected []string // the listed rejections, translated
	More     string   // "…and N more", when the list is shorter than the count
}

func (s *server) exportOPML(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	data, err := s.feeds.ExportOPML(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="mw-feed.opml"`)
	_, _ = w.Write(data)
}

func (s *server) importForm(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	s.render(w, r, "import", http.StatusOK, page{CSRFToken: sess.CSRFToken, Import: &importView{MaxSize: "10 MB"}})
}

// importOPML handles POST /import: it reads the uploaded file and answers with the report.
func (s *server) importOPML(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.currentSession(r)
	if !ok || !sess.Authenticated {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	form := func(key string) {
		s.render(w, r, "import", http.StatusBadRequest,
			page{CSRFToken: sess.CSRFToken, Import: &importView{MaxSize: "10 MB"}, ErrorKey: key})
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBody)
	// Everything stays in memory: the body is bounded, and the image has no scratch space to rely on.
	if err := r.ParseMultipartForm(maxImportBody); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			form("import.error_too_large")
			return
		}
		s.badRequest(w, r)
		return
	}
	if !auth.VerifyCSRF(sess, r.PostFormValue("csrf_token")) {
		s.forbidden(w, r)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		form("import.error_no_file")
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, opml.MaxSize+1)) // one byte more than allowed, to see it
	if err != nil {
		s.badRequest(w, r)
		return
	}
	res, err := s.feeds.ImportOPML(r.Context(), data, feed.ImportOptions{Overwrite: r.PostFormValue("overwrite") != ""})
	switch {
	case err == nil:
	case errors.Is(err, opml.ErrTooLarge):
		form("import.error_too_large")
		return
	case errors.Is(err, opml.ErrInvalidFormat):
		form("import.error_format")
		return
	case errors.Is(err, opml.ErrEntityNotAllowed):
		form("import.error_entity")
		return
	case errors.Is(err, opml.ErrUnsupportedEncoding):
		form("import.error_encoding")
		return
	case errors.Is(err, opml.ErrTooDeep):
		form("import.error_deep")
		return
	default:
		s.serverError(w, r, err)
		return
	}
	st, err := s.store.Settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view := &importView{Report: &res}
	for _, rej := range res.Rejected {
		reason := rej.Reason
		if reason == opml.ReasonInvalidURL {
			reason = i18n.T(st.Locale, "import.reason_invalid_url")
		}
		view.Rejected = append(view.Rejected, fmt.Sprintf("%s (%s): %s", rej.Title, rej.URL, reason))
	}
	if more := res.RejectedCount - len(res.Rejected); more > 0 {
		view.More = fmt.Sprintf(i18n.T(st.Locale, "import.rejected_more"), more)
	}
	s.render(w, r, "import_report", http.StatusOK, page{CSRFToken: sess.CSRFToken, Import: view})
}
