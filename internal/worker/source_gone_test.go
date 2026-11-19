// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// manyDamaged is ffmpeg's stderr for an original with more damaged pictures than are tolerated.
const manyDamaged = "[h264 @ 0x55] error while decoding MB 41 22, bytestream -5\n" +
	"[h264 @ 0x55] error while decoding MB 3 7, bytestream -5\n" +
	"[h264 @ 0x55] error while decoding MB 9 9, bytestream -5\n" +
	"[h264 @ 0x55] error while decoding MB 1 1, bytestream -5\n" +
	"[h264 @ 0x55] error while decoding MB 2 2, bytestream -5"

// dirNames is what is in the folder the original was in, sorted as ReadDir gives it.
func dirNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestSourceDeletedMidConversionFails(t *testing.T) {
	oneTry(t) // the outcome of one try
	for _, mode := range []OutputMode{Replace, Beside} {
		t.Run(string(mode), func(t *testing.T) {
			s := newScenario(t)
			s.onEncode = func() { os.Remove(s.src) }
			s.run(mode)
			if j := s.job(); j.Status != jobs.Failed || j.Error != goneWords {
				t.Errorf("status %s error %q, want failed %q", j.Status, j.Error, goneWords)
			}
			if names := dirNames(t, s.src); len(names) != 0 {
				t.Errorf("left in the folder: %v, want nothing", names)
			}
		})
	}
}

func TestSourceRenamedMidConversionFails(t *testing.T) {
	oneTry(t) // the outcome of one try
	moved := "moved.mkv"
	for _, mode := range []OutputMode{Replace, Beside} {
		t.Run(string(mode), func(t *testing.T) {
			s := newScenario(t)
			s.onEncode = func() { os.Rename(s.src, filepath.Join(filepath.Dir(s.src), moved)) }
			s.run(mode)
			if j := s.job(); j.Status != jobs.Failed || j.Error != goneWords {
				t.Errorf("status %s error %q, want failed %q", j.Status, j.Error, goneWords)
			}
			// only the person's moved file is there: no output at the old name, no temp, no trash
			if names := dirNames(t, s.src); len(names) != 1 || names[0] != moved {
				t.Errorf("folder has %v, want only %s", names, moved)
			}
			if b, _ := os.ReadFile(filepath.Join(filepath.Dir(s.src), moved)); string(b) != "original-bytes" {
				t.Errorf("the moved file was touched: %q", b)
			}
		})
	}
}

// A read error from ffmpeg after the original is gone says nothing about the file: it is not kept as damage.
func TestSourceGoneDuringReadErrorsIsNotDamage(t *testing.T) {
	oneTry(t)
	s := newScenario(t)
	s.encodeStderr = manyDamaged
	s.onEncode = func() { os.Remove(s.src) }
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Failed || j.Error != goneWords {
		t.Errorf("status %s error %q, want failed %q", j.Status, j.Error, goneWords)
	}
	if strings.Contains(s.job().Error, "damage") {
		t.Errorf("the gone file was called damaged: %q", s.job().Error)
	}
}

// A damaged original that is still there keeps its outcome: kept, with the damage reason.
func TestDamagedPresentOriginalKeepsItsOutcome(t *testing.T) {
	oneTry(t)
	s := newScenario(t)
	s.encodeStderr = manyDamaged
	s.run(Replace)
	j := s.job()
	if j.Status != jobs.Kept || !strings.Contains(j.Error, "has errors") {
		t.Errorf("status %s error %q, want kept for damage", j.Status, j.Error)
	}
	s.assertOriginalSafe()
}

// Gone between the encode and the swap (the checks pass, then the original is deleted): still the same
// failure, and nothing is put where the original was.
func TestSourceDeletedBeforeTheSwapFails(t *testing.T) {
	oneTry(t)
	for _, mode := range []OutputMode{Replace, Beside} {
		t.Run(string(mode), func(t *testing.T) {
			s := newScenario(t)
			swapHook = func(at string) error {
				if at == "before swap" {
					os.Remove(s.src)
				}
				return nil
			}
			t.Cleanup(func() { swapHook = nil })
			s.run(mode)
			if j := s.job(); j.Status != jobs.Failed || j.Error != goneWords {
				t.Errorf("status %s error %q, want failed %q", j.Status, j.Error, goneWords)
			}
			if names := dirNames(t, s.src); len(names) != 0 {
				t.Errorf("left in the folder: %v, want nothing", names)
			}
		})
	}
}
