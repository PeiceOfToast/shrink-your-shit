// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package runlog keeps each job's full run log (S5, the Files view): one file per job in a folder,
// <dir>/<job id>.log, appended as the job runs. Limits (stated in the S5 PR):
//   - PerJob bytes per job: past it (by Slack, so a flood of lines is not rewritten line by line), the
//     first Head bytes and the newest PerJob-Head are kept, with one line saying how much was left out;
//   - a new log, and Sweep, keep the folder within Total, the oldest logs out first; Sweep also
//     removes the logs of jobs finished more than MaxAge ago;
//   - a job's log is removed with its job (Remove).
package runlog

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	PerJob = 1 << 20   // 1 MiB per job
	Head   = 256 << 10 // kept from the start when a log is cut
	Slack  = 64 << 10  // a log is cut once it passes PerJob by this much (then it is PerJob again)
	Total  = 100 << 20 // the whole folder (lead, 23:06Z: small boxes and LXCs)
	MaxAge = 30 * 24 * time.Hour
	// ReadMax is the most one Read returns; the page asks again from next.
	ReadMax = 64 << 10
)

// Logs is the folder of run logs. The zero Dir disables it (every call does nothing).
type Logs struct {
	Dir string
	mu  sync.Mutex
}

func (l *Logs) path(job int64) string { return filepath.Join(l.Dir, strconv.FormatInt(job, 10)+".log") }

// Append adds text to job's log (a line break is added if text has none), keeping it within PerJob.
func (l *Logs) Append(job int64, text string) error {
	if l == nil || l.Dir == "" {
		return nil
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return err
	}
	p := l.path(job)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(text)
	st, serr := f.Stat()
	f.Close()
	if werr != nil {
		return werr
	}
	if serr == nil && st.Size() > PerJob+Slack {
		return cut(p, st.Size())
	}
	if serr == nil && st.Size() == int64(len(text)) { // a new log: keep the folder within Total now (review 43e6f66)
		_, err := l.sweep(time.Now(), MaxAge, Total, p)
		return err
	}
	return nil
}

// cut keeps the first Head bytes and the last bytes that fit, with a line for what was left out.
func cut(p string, size int64) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	head := b[:Head]
	keep := PerJob - Head // the newest bytes kept
	left := (size - int64(Head) - int64(keep) + 1023) / 1024
	if strings.HasPrefix(string(b[Head:]), cutMark) { // cut before: the note right after the head counts on
		var before int64
		if _, err := fmt.Sscanf(string(b[Head+len(cutMark):]), "%d", &before); err == nil {
			left += before
		}
	}
	note := fmt.Sprintf("%s%d KiB left out …\n", cutMark, left)
	tail := b[len(b)-(keep-len(note)):]
	if i := strings.IndexByte(string(tail), '\n'); i >= 0 && i < len(tail)-1 { // start at a whole line
		tail = tail[i+1:]
	}
	out := append(append(append([]byte{}, head...), note...), tail...)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

const cutMark = "\n… "

// Read returns job's log from byte from, at most ReadMax bytes, and the offset to read from next.
// A log that does not exist reads as empty.
func (l *Logs) Read(job int64, from int64) (text string, next int64, err error) {
	if l == nil || l.Dir == "" {
		return "", 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path(job))
	if errors.Is(err, fs.ErrNotExist) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if from < 0 || from > st.Size() { // the log was cut since: read it again from the start
		from = 0
	}
	b := make([]byte, min(int64(ReadMax), st.Size()-from))
	n, err := f.ReadAt(b, from)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", 0, err
	}
	return string(b[:n]), from + int64(n), nil
}

// Remove deletes job's log (its job is gone).
func (l *Logs) Remove(job int64) error {
	if l == nil || l.Dir == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.Remove(l.path(job)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Sweep removes logs last written more than maxAge before now, then the oldest ones until the folder
// holds at most total bytes. It returns how many it removed.
func (l *Logs) Sweep(now time.Time, maxAge time.Duration, total int64) (int, error) {
	if l == nil || l.Dir == "" {
		return 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sweep(now, maxAge, total, "")
}

// sweep is Sweep with the lock held; keep is a log never removed (the one just made).
func (l *Logs) sweep(now time.Time, maxAge time.Duration, total int64, keep string) (int, error) {
	entries, err := os.ReadDir(l.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	type one struct {
		path string
		at   time.Time
		size int64
	}
	var logs []one
	var sum int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		if info, err := e.Info(); err == nil {
			logs = append(logs, one{filepath.Join(l.Dir, e.Name()), info.ModTime(), info.Size()})
			sum += info.Size()
		}
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].at.Before(logs[j].at) }) // oldest first
	removed := 0
	for _, g := range logs {
		if now.Sub(g.at) <= maxAge && sum <= total {
			break // the rest are newer and fit
		}
		if g.path == keep {
			continue
		}
		if err := os.Remove(g.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, err
		}
		sum -= g.size
		removed++
	}
	return removed, nil
}
