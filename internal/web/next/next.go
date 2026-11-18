// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package next embeds the new single-page app, which the web UI is moving to (UI rebuild, 2026-11-09).
// ui-shop builds it from web/ with Vite straight into dist/, which git ignores except for dist/.keep,
// so a clone without Node still compiles and serves a "not built" page at /next/.
package next

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Files are the built app (index.html, assets/...), or only .keep when it wasn't built.
func Files() fs.FS {
	sub, _ := fs.Sub(dist, "dist")
	return sub
}

// Built reports whether a real build is embedded, not only the placeholder.
func Built() bool {
	_, err := fs.Stat(Files(), "index.html")
	return err == nil
}
