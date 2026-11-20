// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Healthy answers; a database busy with a long write (its one connection held) gives the caller's
// deadline back, which the health check counts as busy, not broken (user item 9).
func TestHealthyAndBusy(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Healthy(context.Background()); err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.Begin() // holds the only connection, as a long write does
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := st.Healthy(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("busy database: %v, want the deadline", err)
	}
}
