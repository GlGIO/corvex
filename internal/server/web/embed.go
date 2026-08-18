// Package web holds the UI the binary serves.
//
// Embedded rather than fetched: a tool that approves production migrations must
// not load its own interface from a CDN, and a UI that only works online is a UI
// that stops working exactly when the network is what broke.
package web

import "embed"

//go:embed index.html app.css app.js
var FS embed.FS
