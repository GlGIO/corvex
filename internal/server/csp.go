package server

import "net/http"

// contentSecurityPolicy is the fourth layer of the model in auth.go, and the only
// one that constrains the PAGE rather than the request: the first three decide
// whether a caller gets in, this one decides what the page it got back is allowed
// to load, execute and talk to. It is not a substitute for any of them — a CSP
// stops nothing that speaks HTTP directly, only a browser.
//
// It is written from what web/ actually does, checked line by line, because a
// policy copied from a template is a policy that either breaks the page or opens
// what the page never needed:
//
//   - default-src 'none' — the UI is embedded (web/embed.go): no CDN, no font
//     host, no image, no iframe, no analytics. Everything the page loads is
//     opened below by name; object-src, img-src, font-src and the rest stay shut,
//     which is the whole reason to start from 'none'.
//   - script-src 'self' — one file, /assets/app.js. index.html has no inline
//     <script> and app.js has no eval and no new Function, so neither a nonce nor
//     'unsafe-inline' is needed.
//   - style-src 'self' — one file, /assets/app.css. app.js sets the cost bar's
//     width through the CSSOM (node.style.width), which CSP does not police,
//     rather than through a style attribute, which would have forced
//     'unsafe-inline' on every inline style in the page.
//   - connect-src 'self' — app.js POSTs gate decisions, reads /api/state, and
//     opens the event stream at /api/events (stream.go): connect-src is the
//     directive that governs EventSource too, which is why adding the stream
//     needed no new directive and must not be allowed to grow one. This is the
//     one line without which the page renders and then shows nothing.
//   - base-uri 'none' — a single injected <base> repoints /assets/app.js at
//     someone else's origin without ever violating script-src.
//   - form-action 'none' — the page has no <form>, and a submit target is one of
//     the cheapest ways to post a gate's evidence somewhere else.
//   - frame-ancestors 'none' — nothing frames a page whose primary button
//     approves a production migration; clickjacking that button is the attack
//     this stops.
const contentSecurityPolicy = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"connect-src 'self'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

// withCSP sets the policy on every response. It wraps OUTSIDE the auth guard so
// that a 401 and a 403 carry it too, and it lives here — once, around the whole
// mux (see Server.Handler) — rather than in each handler: spread across
// handlers, the next handler is born without it.
func withCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}
