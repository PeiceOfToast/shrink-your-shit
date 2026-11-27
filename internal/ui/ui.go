// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package ui embeds the built web interface.
package ui

import (
	"embed"
	"io/fs"
)

// dist is the Vite build output. The committed index.html is a placeholder; `npm run build` in
// ui/ writes the real build here. A binary built without that step still starts, and its page
// says the interface was not built.
//
//go:embed all:dist
var dist embed.FS

// Files is the built interface's root: index.html and the hashed assets beside it.
func Files() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is in the embed by construction
	}
	return sub
}
