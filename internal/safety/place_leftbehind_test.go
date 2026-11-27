// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
)

// When the link lands but the staged copy cannot be unlinked, Place reports the placed file and ErrSourceLeftBehind,
// and the staging folder keeps the leftover. It must not try to remove that folder, which is not empty.
func TestPlaceReportsASourceLeftBehindAndKeepsStaging(t *testing.T) {
	st, dst := stagedWith(t, "x")
	old := renameNoReplace
	t.Cleanup(func() { renameNoReplace = old })
	renameNoReplace = func(oldpath, newpath string) error {
		if err := os.Link(oldpath, newpath); err != nil {
			return err
		}
		return fmt.Errorf("%w: simulated unlink refusal", fsx.ErrNotRemoved)
	}

	used, err := Place(context.Background(), st, dst)
	if !errors.Is(err, ErrSourceLeftBehind) {
		t.Fatalf("err = %v; want ErrSourceLeftBehind", err)
	}
	if used != dst {
		t.Fatalf("used = %q; want %q", used, dst)
	}
	if _, statErr := os.Stat(dst); statErr != nil {
		t.Fatalf("the placed file should exist: %v", statErr)
	}
	if _, statErr := os.Stat(st.Path); statErr != nil {
		t.Fatalf("the staged leftover should still be there: %v", statErr)
	}
	if _, statErr := os.Stat(st.Folder); statErr != nil {
		t.Fatalf("the staging folder must be kept while the leftover is in it: %v", statErr)
	}
}
