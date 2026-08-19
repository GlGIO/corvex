package server

import (
	"net/http"
	"strings"

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

// assets is everything this route will ever serve, by name. The content type is
// written down rather than sniffed: the page is loaded under `script-src 'self';
// style-src 'self'`, and a browser that receives the script under the wrong type
// refuses to execute it — a blank screen that looks like the tool being broken.
var assets = map[string]string{
	"app.css": "text/css; charset=utf-8",
	"app.js":  "text/javascript; charset=utf-8",
}

// handleAsset serves the embedded stylesheet and script, those two and nothing
// else. They sit behind the same auth as everything else: the UI is not a public
// page that happens to talk to a private API, it is one surface.
//
// It used to be an http.FileServer over the root of the embed, which answered
// two questions nobody asked. `/assets/` served the whole of index.html as the
// directory's index, and `/assets/index.html` answered 301 to `./`. Neither was
// a policy hole — both carry the CSP and both sit behind the guard — and that is
// exactly why it is worth naming: they are the page's bytes at a URL that never
// reaches handleIndex, so neither of them sets the session cookie. A second way
// to get the page that quietly lacks the page's one side effect is surface that
// nobody chose and nobody would remember to reason about. A list of two names
// has no such answers to give.
func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	ctype, ok := assets[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := web.FS.ReadFile(name)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	_, _ = w.Write(body)
}
