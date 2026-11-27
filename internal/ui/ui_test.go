// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package ui

import (
	"io/fs"
	"strings"
	"testing"
)

// The binary always carries a page: the placeholder when ui/ was not built, the real build when it
// was. This is what catches a go:embed pattern that stopped matching the dist folder.
func TestFilesHasAPage(t *testing.T) {
	page, err := fs.ReadFile(Files(), "index.html")
	if err != nil {
		t.Fatalf("read index.html from the embed: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(page)), "<html") {
		t.Fatalf("the embedded index.html is not a page: %q", page)
	}
}
