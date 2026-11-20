// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// sectionLines is job id's lines in one section, in order.
func sectionLines(t *testing.T, store *jobs.Store, id int64, section string) []jobs.LogLine {
	t.Helper()
	lines, err := store.LogLines(id)
	if err != nil {
		t.Fatal(err)
	}
	var out []jobs.LogLine
	for _, l := range lines {
		if l.Section == section {
			out = append(out, l)
		}
	}
	return out
}

// findLine is the first line in section whose text contains all of want (nil when there is none).
func findLine(t *testing.T, store *jobs.Store, id int64, section string, want ...string) *jobs.LogLine {
	t.Helper()
	for _, l := range sectionLines(t, store, id, section) {
		all := true
		for _, w := range want {
			if !strings.Contains(l.Text, w) {
				all = false
				break
			}
		}
		if all {
			return &l
		}
	}
	return nil
}

// R5: the encode section says what hardware was found and each encoder considered with its reason, then the
// encoder the encode uses, why, and its settings. A rejected candidate appears with the reason it was rejected.
func TestEncoderCandidatesInTheEncodeSection(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	var gotCodec string
	var gotNeed10, gotHDR bool
	candidates := []string{
		"Hardware found: NVIDIA NVENC (graphics).",
		"NVIDIA NVENC (graphics): found, but it failed its test on this computer (the test wrote: Impossible to open the device).",
		"No graphics encoder for HEVC can do this file's 10-bit colour; the processor does the encoding.",
	}
	w.EncoderCandidates = func(codec string, need10 bool, hdr *rules.HDR) []string {
		gotCodec, gotNeed10, gotHDR = codec, need10, hdr != nil
		return candidates
	}
	w.Encoder = func(string, bool, *rules.HDR) (string, string) {
		return "", "No graphics encoder passed its test on this computer, so the processor does the encoding"
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if gotCodec != "libx265" || gotNeed10 || gotHDR {
		t.Errorf("candidates asked for codec %q, need10 %v, hdr %v: want libx265 and an SDR 8-bit file", gotCodec, gotNeed10, gotHDR)
	}
	encode := sectionLines(t, s.store, s.id, "encode")
	var texts []string
	for _, l := range encode {
		texts = append(texts, l.Text)
		if l.Level != jobs.LevelInfo {
			t.Errorf("line %q: level %s, want info", l.Text, l.Level)
		}
	}
	joined := strings.Join(texts, "\n")
	pos := -1
	for _, want := range append(candidates, "No graphics encoder passed its test on this computer, so the processor does the encoding",
		"Chosen: x265 (processor)", "Settings: Encoder: libx265") {
		i := strings.Index(joined, want)
		if i < 0 {
			t.Errorf("the encode section does not say %q:\n%s", want, joined)
			continue
		}
		if i < pos {
			t.Errorf("%q comes before the line that should precede it:\n%s", want, joined)
		}
		pos = i
	}
	// the rejected candidate says why, in our words, not raw ffmpeg output
	if !strings.Contains(joined, "failed its test on this computer") {
		t.Errorf("the rejected candidate gives no reason:\n%s", joined)
	}
	// the encoder's own reason is said once: the Chosen line follows it, rather than repeating it
	if n := strings.Count(joined, "No graphics encoder passed its test on this computer, so the processor does the encoding"); n != 1 {
		t.Errorf("the encoder's reason appears %d times, want once:\n%s", n, joined)
	}
}

// R8: a known harmless line is kept with its note, at info level; it is not one of the failure's key lines, so
// the failure summary still points at the real error.
func TestBenignLineIsLabelledAndNotAKeyLine(t *testing.T) {
	s := newScenario(t)
	s.encodeErr = errors.New("ffmpeg exited 1")
	s.encodeStderr = strings.Join([]string{
		"[h264 @ 0x55] Last message repeated 7 times",
		"[h264 @ 0x55] no frame!",
		"[h264 @ 0x55] Invalid data found when processing input",
	}, "\n")
	w := s.worker(Replace)
	if _, err := w.RunOne(context.Background()); err == nil {
		t.Fatal("RunOne: want the failed conversion's error")
	}
	benign := findLine(t, s.store, s.id, "encode", "Last message repeated 7 times")
	if benign == nil {
		t.Fatal("the harmless line was dropped from the encode section")
	}
	if !strings.Contains(benign.Text, "(harmless: ") || benign.Level != jobs.LevelInfo || benign.Stream != "stderr" {
		t.Errorf("the harmless line: %+v", *benign)
	}
	real := findLine(t, s.store, s.id, "encode", "Invalid data found")
	if real == nil || strings.Contains(real.Text, "(harmless:") {
		t.Errorf("the real error: %+v", real)
	}
	f, err := s.store.FailureOf(s.id)
	if err != nil || f == nil {
		t.Fatalf("failure summary: %+v, %v", f, err)
	}
	for _, l := range f.KeyLines {
		if strings.Contains(l, "Last message repeated") || strings.Contains(l, "no frame!") {
			t.Errorf("a harmless line is a key line: %q", l)
		}
	}
	if len(f.KeyLines) != 1 || !strings.Contains(f.KeyLines[0], "Invalid data found") {
		t.Errorf("key lines %q, want only the real error", f.KeyLines)
	}
}

// R9: ffmpeg printing nothing for quietAfter gets one warning per silence, at warning level in the encode
// section; the next output starts the count again. A fake clock, so nothing sleeps.
func TestQuietWarningWithAFakeClock(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	w.quietOn.Store(true)
	w.outputSeen(base)
	w.quietCheck(s.id, base.Add(59*time.Second))
	if n := logCount(t, s, "no output for"); n != 0 {
		t.Fatalf("a warning after 59 s: %d", n)
	}
	w.quietCheck(s.id, base.Add(60*time.Second))
	if n := logCount(t, s, "no output for"); n != 1 {
		t.Fatalf("warnings after 60 s: %d, want 1", n)
	}
	w.quietCheck(s.id, base.Add(10*time.Minute))
	if n := logCount(t, s, "no output for"); n != 1 {
		t.Fatalf("the warning repeats in one silence: %d", n)
	}
	w.outputSeen(base.Add(10 * time.Minute))
	w.quietCheck(s.id, base.Add(10*time.Minute+30*time.Second))
	if n := logCount(t, s, "no output for"); n != 1 {
		t.Fatalf("a warning 30 s after output: %d", n)
	}
	w.quietCheck(s.id, base.Add(11*time.Minute))
	if n := logCount(t, s, "no output for"); n != 2 {
		t.Fatalf("the second silence: %d warnings, want 2", n)
	}
	line := findLine(t, s.store, s.id, "encode", "Still running, no output for")
	if line == nil || line.Level != jobs.LevelWarning || !strings.Contains(line.Text, "no output for 60 s") {
		t.Errorf("the warning: %+v", line)
	}
	w.quietOn.Store(false)
	w.quietCheck(s.id, base.Add(30*time.Minute))
	if n := logCount(t, s, "no output for"); n != 2 {
		t.Errorf("a warning outside a local encode: %d", n)
	}
}

// R14: the replace step's file actions are recorded in the order they happen; a refused owner is a failed
// chown with the system's words, and the cleanup line says how many bytes the temp folder held.
func TestReplaceMovesInOrderWithFailedChown(t *testing.T) {
	s := newScenario(t)
	old := setOwner
	setOwner = func(string, os.FileInfo) (ownerOutcome, error) {
		return ownerRefused, os.ErrPermission // as a worker that is not an administrator gets it
	}
	t.Cleanup(func() { setOwner = old })
	w := s.worker(Replace)
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s: %s", j.Status, j.Error)
	}
	moves, err := s.store.JobMoves(s.id)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, m := range moves {
		actions = append(actions, m.Action)
	}
	want := []string{"chmod", "chown", "move", "remove"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("actions %v, want %v", actions, want)
	}
	if m := moves[0]; !m.OK || m.From != m.To || !strings.Contains(m.From, ".syp-tmp") {
		t.Errorf("chmod: %+v", m)
	}
	if m := moves[1]; m.OK || !strings.Contains(m.Error, "permission denied") {
		t.Errorf("the refused owner: %+v", m)
	}
	if m := moves[2]; !m.OK || m.To != s.src || !strings.Contains(m.From, ".syp-tmp") {
		t.Errorf("the move into place: %+v", m)
	}
	if m := moves[3]; !m.OK || m.From == "" {
		t.Errorf("the cleanup: %+v", m)
	}
	if _, err := os.Stat(moves[3].From); !os.IsNotExist(err) {
		t.Errorf("the temp folder is still there: %v", err)
	}
	refused := findLine(t, s.store, s.id, "replace", "Could not give the new file the source's owner")
	if refused == nil || refused.Level != jobs.LevelWarning {
		t.Errorf("the refused owner in the replace section: %+v", refused)
	}
	clean := findLine(t, s.store, s.id, "replace", "Removed the temp folder")
	if clean == nil || !regexp.MustCompile(`\(0 bytes left in it\)`).MatchString(clean.Text) {
		t.Errorf("the cleanup line: %+v", clean)
	}
}

// Review #491 P3: an AMF encode's quality is -qp_i (with -rc cqp), which the Settings line must name rather than
// say "none".
func TestEncoderLineNamesAMFsQuality(t *testing.T) {
	got := encoderDetail([]string{"-c:v", "hevc_amf", "-rc", "cqp", "-qp_i", "24", "-qp_p", "26"})
	if !strings.Contains(got, "hevc_amf, -qp_i 24, ") {
		t.Errorf("encoder line %q, want the AMF quality", got)
	}
}

// R14: when the encode fails, the temp folder still holds its partial output; the cleanup line says how many
// bytes it held, and the folder is gone.
func TestCleanupLineSaysBytesLeft(t *testing.T) {
	s := newScenario(t)
	s.failAllEncodes = true
	w := s.worker(Replace)
	if _, err := w.RunOne(context.Background()); err == nil {
		t.Fatal("RunOne: want the failed conversion's error")
	}
	clean := findLine(t, s.store, s.id, "replace", "Removed the temp folder")
	if clean == nil {
		t.Fatal("no cleanup line in the replace section")
	}
	if !regexp.MustCompile(`\([1-9][0-9,]* bytes left in it\)`).MatchString(clean.Text) {
		t.Errorf("the cleanup line claims no bytes were left: %q", clean.Text)
	}
	moves, err := s.store.JobMoves(s.id)
	if err != nil || len(moves) == 0 {
		t.Fatalf("moves: %+v, %v", moves, err)
	}
	last := moves[len(moves)-1]
	if last.Action != "remove" || !last.OK {
		t.Fatalf("last move %+v, want a successful remove", last)
	}
	if _, err := os.Stat(last.From); !os.IsNotExist(err) {
		t.Errorf("the temp folder is still there: %v", err)
	}
	if filepath.Dir(last.From) != s.dir {
		t.Errorf("the temp folder %q is not beside the original %q", last.From, s.src)
	}
}
