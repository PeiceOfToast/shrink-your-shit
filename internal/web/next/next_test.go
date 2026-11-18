// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package next

import (
	"os"
	"testing"
)

// The release check (research ts-build-embed.md): a release build sets SYP_REQUIRE_WEBUI=1, so a
// binary never ships the placeholder in place of the app. Other builds may have no app built.
func TestReleaseHasTheApp(t *testing.T) {
	if os.Getenv("SYP_REQUIRE_WEBUI") != "1" {
		t.Skip("SYP_REQUIRE_WEBUI is not set: the app may be the placeholder")
	}
	if !Built() {
		t.Fatal("internal/web/next/dist has no index.html: run the web build (ui-shop's npm run build) before go build")
	}
}
