// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// logSectionNames are the job log's sections in the order they are shown, with their headings (log-spec sections 4
// and 5). Lines written before sections existed have section "" and come last.
var logSectionNames = []struct{ name, heading string }{
	{"start", "Start"}, {"input", "Source file"}, {"encode", "Encode"}, {"output", "Output check"},
	{"replace", "Save the result"}, {"summary", "Summary"}, {"", "Other lines"},
}

// logSection is one section of a job's log: its name, its heading and its lines, in order.
type logSection struct {
	name, heading string
	lines         []jobs.LogLine
}

// logSectionLines groups a job's lines by section, in logSectionNames order. A section with no lines is left out, and
// a line whose section is not one of those goes to "" (Other lines).
func logSectionLines(lines []jobs.LogLine) []logSection {
	known := map[string]bool{}
	for _, s := range logSectionNames {
		known[s.name] = true
	}
	by := map[string][]jobs.LogLine{}
	for _, l := range lines {
		name := l.Section
		if !known[name] {
			name = ""
		}
		by[name] = append(by[name], l)
	}
	var out []logSection
	for _, s := range logSectionNames {
		if ls := by[s.name]; len(ls) > 0 {
			out = append(out, logSection{name: s.name, heading: s.heading, lines: ls})
		}
	}
	return out
}

// sectionsJSON is the job's log as GET /log gives it: sections[] in order, each with its lines.
func sectionsJSON(lines []jobs.LogLine) []map[string]any {
	out := []map[string]any{}
	for _, sec := range logSectionLines(lines) {
		ls := []map[string]any{}
		for _, l := range sec.lines {
			ls = append(ls, map[string]any{"at": l.At, "step": l.Step, "level": levelOf(l), "stream": l.Stream, "text": l.Text})
		}
		out = append(out, map[string]any{"id": sec.name, "title": sec.heading, "lines": ls}) // contract v2: id and title
	}
	return out
}

// streamLines is a batch of lines as the job.log event gives them: each with its section, step and level.
func streamLines(lines []jobs.LogLine) []map[string]any {
	out := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		out = append(out, map[string]any{"at": l.At, "section": l.Section, "step": l.Step, "level": levelOf(l), "stream": l.Stream, "text": l.Text})
	}
	return out
}

// levelOf is a line's level; a line written before levels existed reads as info.
func levelOf(l jobs.LogLine) string {
	if l.Level == "" {
		return jobs.LevelInfo
	}
	return l.Level
}

// verdictOf is a job's final result as summary.result names it: "" while the job has none (queued, running, or
// removed). It is the only verdict (log-spec R1); the summary's last line repeats it in words.
func verdictOf(status jobs.Status) string {
	switch status {
	case jobs.Done:
		return "converted"
	case jobs.Failed:
		return "failed"
	case jobs.Kept:
		return "kept"
	}
	return ""
}

// verdictWords is the verdict as the download's Result line says it.
func verdictWords(status jobs.Status) string {
	switch status {
	case jobs.Done:
		return "Converted"
	case jobs.Failed:
		return "Failed"
	case jobs.Kept:
		return "Kept the original"
	}
	return "No final result"
}

// jobTookMs is how long the job's log spans, from its first line to its last (0 when it has fewer than two lines
// with times).
func jobTookMs(lines []jobs.LogLine) int64 {
	if len(lines) == 0 {
		return 0
	}
	a, errA := time.Parse(time.RFC3339Nano, lines[0].At)
	b, errB := time.Parse(time.RFC3339Nano, lines[len(lines)-1].At)
	if errA != nil || errB != nil {
		return 0
	}
	return b.Sub(a).Milliseconds()
}

// startJSON is the start object of GET /log. Only what the store keeps is filled: the app version, commit, ffmpeg,
// the reason the job started and the workflow's name are not stored yet, so they are null (listed in the L1 PR).
func startJSON(store *jobs.Store, id int64, j jobs.Job, lines []jobs.LogLine) map[string]any {
	rev, _ := store.JobWorkflowRev(id)
	var revision, started any
	if rev > 0 {
		revision = rev
	}
	if len(lines) > 0 {
		started = lines[0].At
	}
	return map[string]any{"startedAt": started, "worker": nilIfEmpty(j.Worker), "library": nilIfEmpty(j.Library),
		"attempt": j.Attempts + 1, "workflow": map[string]any{"name": nil, "revision": revision},
		"appVersion": nil, "commit": nil, "ffmpeg": nil, "reason": nil}
}

// summaryJSON is the summary object of GET /log: null while the job has no final result. The fields the store
// cannot fill yet (before and after, the steps, tempRemoved) are null.
func summaryJSON(j jobs.Job, lines []jobs.LogLine) map[string]any {
	result := verdictOf(j.Status)
	if result == "" {
		return nil
	}
	var finalBytes, percent any
	if j.SizeAfter > 0 {
		finalBytes = j.SizeAfter
	}
	if _, pct, ok := savedOf(j); ok {
		percent = pct
	}
	return map[string]any{"result": result, "originalBytes": j.SizeBefore, "finalBytes": finalBytes,
		"savedPercent": percent, "outputPath": nilIfEmpty(j.OutputPath), "tookMs": jobTookMs(lines),
		"before": nil, "after": nil, "steps": nil, "tempRemoved": nil}
}

// savedOf is the bytes a converted job saved and the share of the original that is, when it made the file smaller.
func savedOf(j jobs.Job) (saved int64, pct float64, ok bool) {
	if j.SizeBefore <= 0 || j.SizeAfter <= 0 || j.SizeAfter >= j.SizeBefore {
		return 0, 0, false
	}
	saved = j.SizeBefore - j.SizeAfter
	return saved, float64(saved) * 100 / float64(j.SizeBefore), true
}

// clockSpan is a duration in milliseconds as h:mm:ss.
func clockSpan(ms int64) string {
	s := ms / 1000
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}

// jobLogText is the job's whole log as /log/download gives it (log-spec section 5): a header, then one heading per
// section. Our lines are "HH:MM:SS.mmm  LEVEL  text"; tool output is indented with "| " and has no level.
func jobLogText(j jobs.Job, id int64, lines []jobs.LogLine) string {
	var b strings.Builder
	fmt.Fprintln(&b, "shrink-your-shit job log")
	fmt.Fprintf(&b, "File:     %s\n", j.Path)
	fmt.Fprintf(&b, "Job:      %d, attempt %d\n", id, j.Attempts+1)
	fmt.Fprintf(&b, "Result:   %s\n", verdictWords(j.Status))
	saved := "none"
	if n, pct, ok := savedOf(j); ok {
		saved = fmt.Sprintf("%s bytes (%.1f%%)", rules.GroupDigits(n), pct)
	}
	fmt.Fprintf(&b, "Took:     %s   Saved: %s\n", clockSpan(jobTookMs(lines)), saved)
	fmt.Fprintln(&b, "Times:    UTC")
	for _, sec := range logSectionLines(lines) {
		fmt.Fprintf(&b, "\n== %s ==\n", sec.heading)
		for _, l := range sec.lines {
			writeDownloadLine(&b, l)
		}
	}
	return b.String()
}

// writeDownloadLine writes one line of the download: a worker line with its level, or tool output indented.
func writeDownloadLine(b *strings.Builder, l jobs.LogLine) {
	clock := clockMsOf(l.At)
	if l.Stream == "stderr" || l.Stream == "stdout" || l.Stream == "info" {
		fmt.Fprintf(b, "%s         | %s\n", clock, l.Text)
		return
	}
	fmt.Fprintf(b, "%s  %-5s  %s\n", clock, levelLabel(levelOf(l)), l.Text)
}

// levelLabel is a level's word in the download's level column, five characters wide at most.
func levelLabel(level string) string {
	switch level {
	case jobs.LevelWarning:
		return "WARN"
	case jobs.LevelError:
		return "ERROR"
	}
	return "INFO"
}

// clockMsOf is a line's time as the download shows it: HH:MM:SS.mmm, UTC (log-spec section 5).
func clockMsOf(at string) string {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return at
	}
	return t.UTC().Format("15:04:05.000")
}

// unsafeFileChars is what a download's file name cannot keep.
var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// logFileName is the download's file name: syp-job-<id>-<title>.log, the title being the file's name without its
// extension, with anything but letters, digits, dots, dashes and underscores replaced.
func logFileName(id int64, path string) string {
	title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	title = strings.Trim(unsafeFileChars.ReplaceAllString(title, "-"), "-")
	if title == "" {
		title = "job"
	}
	if len(title) > 80 {
		title = title[:80]
	}
	return fmt.Sprintf("syp-job-%d-%s.log", id, title)
}

// atOnOrAfter reports whether log time at is not before start (both RFC 3339, possibly with milliseconds). Compared
// as times, so "05:30:00Z" and "05:30:00.250Z" order correctly.
func atOnOrAfter(at, start string) bool {
	a, errA := time.Parse(time.RFC3339Nano, at)
	b, errB := time.Parse(time.RFC3339Nano, start)
	if errA != nil || errB != nil {
		return at >= start
	}
	return !a.Before(b)
}
