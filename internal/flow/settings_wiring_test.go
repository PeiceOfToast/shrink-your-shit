// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
	"github.com/PeiceOfToast/shrink-your-shit/internal/settings"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// TestKeepZeroSettingDeletesTheTrashCopyAtOnce: with processing.keepOriginalsDays 0 saved, a verified swap removes the
// original's trash copy at once, the way the page promises.
func TestKeepZeroSettingDeletesTheTrashCopyAtOnce(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := settings.Patch(ctx, db, settings.Options{}, map[string]json.RawMessage{
		"processing": json.RawMessage(`{"keepOriginalsDays":0}`),
	}); err != nil {
		t.Fatal(err)
	}
	lib := t.TempDir()
	orig := filepath.Join(lib, "movie.mkv")
	staged := filepath.Join(lib, "out.staged.mkv")
	if err := os.WriteFile(orig, []byte("ORIGINAL-BYTES-0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("NEW-BYTES-ABCDEFGHIJKLMNOP"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := RealReplacer{Journal: safety.NewJournal(db)}
	res, err := rep.Swap(ctx, safety.SwapPlan{JobID: 1, Original: orig, Staged: staged, Final: orig, KeepDays: 7})
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	if !res.Purged {
		t.Errorf("the trash copy was kept with keepOriginalsDays 0")
	}
	if entries, _ := os.ReadDir(filepath.Join(lib, safety.TrashDir)); len(entries) != 0 {
		t.Errorf("the trash holds %d file(s) after a keep-0 swap", len(entries))
	}
}
