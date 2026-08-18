package server

import (
	"io/fs"
	"net/http"

	"github.com/giovannialves/corvex/internal/server/web"
)

// handleIndex serves the UI and, on the first load, exchanges the token in the
// URL for a cookie — so the secret stops travelling in the address bar, where it
// would end up in screenshots and pasted links.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.auth.SetCookie(w)
	page, err := web.FS.ReadFile("index.html")
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

// handleAsset serves the embedded stylesheet and script. They sit behind the
// same auth as everything else: the UI is not a public page that happens to talk
// to a private API, it is one surface.
func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(web.FS, ".")
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	http.StripPrefix("/assets/", http.FileServer(http.FS(sub))).ServeHTTP(w, r)
}
