// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package runlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendAndRead(t *testing.T) {
	l := &Logs{Dir: filepath.Join(t.TempDir(), "logs")} // made on first use
	if text, next, err := l.Read(1, 0); err != nil || text != "" || next != 0 {
		t.Fatalf("no log yet: %q %d %v", text, next, err)
	}
	l.Append(1, "found")
	l.Append(1, "checked\n")
	text, next, _ := l.Read(1, 0)
	if text != "found\nchecked\n" || next != int64(len(text)) {
		t.Errorf("%q %d", text, next)
	}
	l.Append(1, "converted")
	if more, n2, _ := l.Read(1, next); more != "converted\n" || n2 != next+10 {
		t.Errorf("from next: %q %d", more, n2)
	}
	if again, _, _ := l.Read(1, 999999); again == "" { // past the end (cut since): from the start
		t.Error("an offset past the end reads from the start")
	}
	l.Remove(1)
	if _, err := os.Stat(filepath.Join(l.Dir, "1.log")); !os.IsNotExist(err) {
		t.Error("removed with its job")
	}
	var off *Logs // no folder: everything is a no-op
	if off.Append(1, "x") != nil || off.Remove(1) != nil {
		t.Error("a nil Logs does nothing")
	}
}

// One job's log never passes PerJob: the first Head bytes and the newest lines are kept, with a line
// saying how much was left out; cut again, the count adds up.
func TestPerJobCap(t *testing.T) {
	l := &Logs{Dir: t.TempDir()}
	line := strings.Repeat("x", 1023) // 1 KiB with its line break
	l.Append(7, "FIRST LINE")
	for i := 0; i < 1500; i++ {
		l.Append(7, line)
	}
	l.Append(7, "LAST LINE")
	b, _ := os.ReadFile(filepath.Join(l.Dir, "7.log"))
	s := string(b)
	if len(b) > PerJob+Slack || !strings.HasPrefix(s, "FIRST LINE\n") || !strings.HasSuffix(s, "LAST LINE\n") || !strings.Contains(s, " KiB left out …\n") {
		t.Fatalf("size %d, head %q, tail %q", len(b), s[:20], s[len(s)-20:])
	}
	if strings.Count(s, "left out") != 1 {
		t.Errorf("%d cut notes, want one that counts on", strings.Count(s, "left out"))
	}
	i := strings.Index(s, "\n… ")
	var kib int
	for _, c := range s[i+len("\n… "):] {
		if c < '0' || c > '9' {
			break
		}
		kib = kib*10 + int(c-'0')
	}
	if kib < 400 || kib > 600 { // about 1.5 MiB written, 1 MiB kept
		t.Errorf("left out %d KiB", kib)
	}
}

// Sweep: logs older than maxAge go; then the oldest go until the folder is within total (lead 23:06Z).
func TestSweepOldestFirst(t *testing.T) {
	l := &Logs{Dir: t.TempDir()}
	now := time.Now()
	for i := int64(1); i <= 5; i++ {
		l.Append(i, strings.Repeat("y", 1000))
		at := now.Add(-time.Duration(6-i) * time.Hour) // 1 is the oldest
		os.Chtimes(filepath.Join(l.Dir, filepathName(i)), at, at)
	}
	old := now.Add(-40 * 24 * time.Hour)
	l.Append(9, "ancient")
	os.Chtimes(filepath.Join(l.Dir, filepathName(9)), old, old)
	n, err := l.Sweep(now, MaxAge, 3*1001+10) // room for three of the five
	if err != nil || n != 3 {
		t.Fatalf("removed %d %v", n, err)
	}
	for i, want := range map[int64]bool{9: false, 1: false, 2: false, 3: true, 4: true, 5: true} {
		if _, err := os.Stat(filepath.Join(l.Dir, filepathName(i))); (err == nil) != want {
			t.Errorf("log %d kept %v, want %v", i, err == nil, want)
		}
	}
	if n, _ := l.Sweep(now, MaxAge, Total); n != 0 {
		t.Errorf("a second sweep removed %d", n)
	}
}

func filepathName(id int64) string { return strings.TrimPrefix((&Logs{}).path(id), "/") }

// A cut keeps the contract's 1 MiB: the first 256 KiB and the newest 768 KiB (review 43e6f66).
func TestCutKeepsHeadAndTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "1.log")
	var b strings.Builder
	b.WriteString(strings.Repeat("h", Head))
	for b.Len() < 2*PerJob {
		b.WriteString(strings.Repeat("t", 1023) + "\n")
	}
	os.WriteFile(p, []byte(b.String()), 0o644)
	if err := cut(p, int64(b.Len())); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	s := string(out)
	i := strings.Index(s, cutMark)
	if len(out) > PerJob || len(out) < PerJob-2048 || i != Head || len(s)-strings.Index(s[i+1:], "\n")-i-2 < 768<<10-2048 {
		t.Errorf("size %d, note at %d", len(out), i)
	}
}

// A new log keeps the folder within the total at once, the oldest out first, never itself.
func TestNewLogKeepsTheTotal(t *testing.T) {
	l := &Logs{Dir: t.TempDir()}
	now := time.Now()
	for i := int64(1); i <= 3; i++ {
		os.WriteFile(filepath.Join(l.Dir, filepathName(i)), make([]byte, Total/3), 0o644)
		at := now.Add(-time.Duration(4-i) * time.Hour)
		os.Chtimes(filepath.Join(l.Dir, filepathName(i)), at, at)
	}
	l.Append(9, strings.Repeat("z", 100)) // the folder is now over Total
	if _, err := os.Stat(filepath.Join(l.Dir, filepathName(1))); !os.IsNotExist(err) {
		t.Error("the oldest log was not removed")
	}
	for _, i := range []int64{2, 3, 9} {
		if _, err := os.Stat(filepath.Join(l.Dir, filepathName(i))); err != nil {
			t.Errorf("log %d removed", i)
		}
	}
}
