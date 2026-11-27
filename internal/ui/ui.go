// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package ui embeds the built web interface.
package ui

import (
	"embed"
	"io/fs"
)

// dist is the Vite build's output folder. Only its .gitkeep is committed, so a fresh clone still
// satisfies the embed pattern without a built page ever being committed.
//
//go:embed all:dist
var dist embed.FS

// notbuilt is the page Files serves when the build has not run: it says how to build the interface.
//
//go:embed notbuilt/index.html
var notbuilt embed.FS

// Files is the web interface's root: the built dist once `npm run build` has written it, and the
// "not built" placeholder page before that.
func Files() fs.FS {
	built, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is in the embed by construction
	}
	if _, err := fs.Stat(built, "index.html"); err == nil {
		return built
	}
	placeholder, err := fs.Sub(notbuilt, "notbuilt")
	if err != nil {
		panic(err) // notbuilt is in the embed by construction
	}
	return placeholder
}
