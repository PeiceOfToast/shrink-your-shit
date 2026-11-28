// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return Open(db)
}

func at(hh, mm int) time.Time {
	return time.Date(2026, 11, 28, hh, mm, 0, 0, time.Local)
}

// A fresh install has the one built-in worker, ready with the page's defaults.
func TestTheBuiltInWorkerIsThereWithDefaults(t *testing.T) {
	w, err := openStore(t).Get(context.Background(), LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Local || w.Name != "This computer" || w.Address != "" {
		t.Errorf("worker = %+v, want the built-in local worker", w)
	}
	if w.Slots != 1 || w.GPUSlots != 1 {
		t.Errorf("slots = %d, gpuSlots = %d, want 1 and 1", w.Slots, w.GPUSlots)
	}
	if w.Hours != nil || w.PrimaryEncoder != "" {
		t.Errorf("hours = %v, primaryEncoder = %q, want any time and automatic", w.Hours, w.PrimaryEncoder)
	}
	if !w.AllowsWork(at(3, 0)) {
		t.Error("a worker with no hours refused new work")
	}
}

// Every setting the page saves is stored and read back.
func TestSettingsAreStored(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	w, err := s.Get(ctx, LocalID)
	if err != nil {
		t.Fatal(err)
	}
	w.Slots = 3
	w.GPUSlots = 0
	w.Hours = &Hours{From: "22:00", To: "06:00"}
	w.PrimaryEncoder = "hevc_qsv"
	if err := s.Save(ctx, w); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slots != 3 || got.GPUSlots != 0 || got.PrimaryEncoder != "hevc_qsv" {
		t.Errorf("saved worker = %+v, want 3 slots, 0 graphics slots, hevc_qsv", got)
	}
	if got.Hours == nil || *got.Hours != (Hours{From: "22:00", To: "06:00"}) {
		t.Errorf("hours = %v, want 22:00 to 06:00", got.Hours)
	}
	// The hours are cleared by saving nil, not by an empty string pair.
	got.Hours = nil
	if err := s.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, err := s.Get(ctx, LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Hours != nil {
		t.Errorf("hours after clearing = %v, want none", again.Hours)
	}
}

// The page's refusals hold at the store too, on the field they belong to.
func TestSaveRefusesWhatThePageCannotShow(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	base, err := s.Get(ctx, LocalID)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		tweak func(*Worker)
		field string
	}{
		{"no files at once", func(w *Worker) { w.Slots = 0 }, "slots"},
		{"negative graphics slots", func(w *Worker) { w.GPUSlots = -1 }, "gpuSlots"},
		{"hours missing a digit", func(w *Worker) { w.Hours = &Hours{From: "9:00", To: "17:00"} }, "hours"},
		{"hours that never end", func(w *Worker) { w.Hours = &Hours{From: "09:00", To: "09:00"} }, "hours"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := base
			c.tweak(&w)
			err := s.Save(ctx, w)
			var invalid *InvalidError
			if !errors.As(err, &invalid) || invalid.Field != c.field {
				t.Fatalf("Save = %v, want an invalid %s", err, c.field)
			}
		})
	}
	if err := s.Save(ctx, Worker{ID: "nobody", Slots: 1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("saving an unknown worker = %v, want ErrNotFound", err)
	}
}
