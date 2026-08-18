package server

import (
	"net/http"
)

// handleIndex serves the UI shell and, on the first load, exchanges the token in
// the URL for a cookie — so the secret stops travelling in the address bar,
// where it would end up in screenshots and pasted links.
//
// The page itself is a placeholder until the screens land: what already works is
// the API underneath it, and shipping the server before the SPA is deliberate —
// the roadmap's rule for F7 is contract first, screens after, because eight
// screens built in parallel without a fixed contract produce eight contracts.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.auth.SetCookie(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(shellHTML))
}

const shellHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>corvex</title>
<style>
:root{color-scheme:dark;--bg:#0f1115;--fg:#e6e6e6;--dim:#8a8f98;--acc:#7aa2f7}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;padding:2rem}
h1{font-size:1rem;letter-spacing:.08em;text-transform:uppercase;color:var(--dim)}
a{color:var(--acc)}
pre{background:#151821;padding:1rem;border-radius:6px;overflow:auto}
</style></head>
<body>
<h1>corvex ui</h1>
<p>The API is up. The screens land in the next step; everything below is already live.</p>
<pre id="state">loading…</pre>
<script>
fetch('/api/state').then(r=>r.json()).then(s=>{
  document.getElementById('state').textContent = JSON.stringify(s, null, 2);
}).catch(e=>{document.getElementById('state').textContent = String(e)});
</script>
</body></html>
`
