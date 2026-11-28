// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// The library preview golden (page 09): the same stored probes the count test uses, with fixed file times. The UI's
// types.ts names the type LibraryPreview.
func TestContractLibraryPreviewGolden(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	ctx := context.Background()
	wf, _, err := e.flows.CreateFromStarter(ctx, "shrink-videos", "Preview")
	if err != nil {
		t.Fatal(err)
	}
	lib := mustAddLibrary(t, e, cookie, "Films", t.TempDir(), "daily")
	libID, err := strconv.ParseInt(lib.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	mtime := time.Unix(1_700_000_000, 0)
	for i, info := range previewProbes() {
		tracks := media.Summarize(info)
		f := library.File{LibraryID: libID, RelPath: "f" + strconv.Itoa(i) + ".mp4", Size: info.Format.Size, MTime: mtime, FileID: "id" + strconv.Itoa(i)}
		if _, err := e.catalog.SaveFile(ctx, f, library.Skipped, "", &tracks); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.catalog.SaveFile(ctx, library.File{LibraryID: libID, RelPath: "never.mp4", Size: 1, MTime: mtime, FileID: "id-never"}, library.Skipped, "", nil); err != nil {
		t.Fatal(err)
	}
	path := "/api/v2/libraries/" + lib.ID + "/preview?workflowId=" + strconv.FormatInt(wf, 10)
	rec := e.do(http.MethodGet, path, "", withSession(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", rec.Code, rec.Body)
	}
	writeGolden(t, "library-preview", "Preview", "GET", "/api/v2/libraries/{id}/preview", rec)
}
