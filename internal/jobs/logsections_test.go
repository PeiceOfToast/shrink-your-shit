// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// log-spec R2/R13: a line is one plain line, with no colour codes and no line breaks.
func TestCleanLogTextMakesOnePlainLine(t *testing.T) {
	got := CleanLogText("\x1b[31mred\x1b[0m\nline2\r\nline3\rline4")
	if got != "red line2 line3 line4" {
		t.Fatalf("got %q", got)
	}
}

// log-spec R13: no line over 2,000 characters; a longer one is cut and says how long it was. The limit counts
// characters, so a multi-byte text is not cut early.
func TestCleanLogTextCutsLongLines(t *testing.T) {
	got := CleanLogText(strings.Repeat("é", 5000))
	if n := utf8.RuneCountInString(got); n != 2000 {
		t.Fatalf("%d characters, want 2000", n)
	}
	if !strings.HasSuffix(got, "… (cut, 5000 chars)") {
		t.Fatalf("no cut note at the end: %q", got[len(got)-40:])
	}
	exact := strings.Repeat("a", 2000)
	if CleanLogText(exact) != exact {
		t.Fatal("a line of exactly 2,000 characters must not be cut")
	}
}

// Lines written before sections and levels existed (migration 53 adds the columns with defaults) read as section ""
// and level info.
func TestOldLogLinesReadAsNoSectionAndInfo(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.Enqueue(Job{Library: "/m", Path: "/m/a.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	// the row as the store wrote it before migration 53: no section, no level
	if _, err := s.db.Exec(`INSERT INTO job_log_lines(job_id, seq, ts, step, stream, text) VALUES (?,?,?,?,?,?)`,
		id, 1, "2026-11-20T05:00:00Z", 0, "app", "written before sections"); err != nil {
		t.Fatal(err)
	}
	lines, err := s.LogLines(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("%d lines, want 1", len(lines))
	}
	if l := lines[0]; l.Section != "" || l.Level != LevelInfo || l.Text != "written before sections" {
		t.Fatalf("old line reads as section %q, level %q: %+v", l.Section, l.Level, l)
	}
}
