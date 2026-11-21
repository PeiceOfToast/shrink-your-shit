// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// PictureTooDifferentWords begins the picture check's refusal in a kept job's stored reason (the worker writes it).
const PictureTooDifferentWords = "the picture of the new file is too different from the original"

// FailureCode is a failed job's code in design's V6b table (user review 11-13 item E): the stored error code mapped
// onto those names. A generic failure is told apart by its ffmpeg key lines: a missing encoder, a damaged source, a
// missing source, a write refused or a full disk. Codes that fit no row (cannot_read, retry_later, removed) stay as they are.
func FailureCode(errorCode string, keyLines []string) string {
	switch errorCode {
	case ReasonDiskFull, ReasonLowDiskSpace:
		return "no_space"
	case ReasonCannotWrite:
		return "output_not_writable"
	case ReasonStalled:
		return "stalled"
	case ReasonWorkerLost:
		return "worker_lost"
	case ReasonKeptOriginal:
		return "check_failed"
	case ReasonSettingsRejected:
		return "settings_rejected"
	case ReasonConvertFailed, "":
		return classifyKeyLines(keyLines)
	}
	return errorCode
}

// encoderUnavailablePhrases are the phrases that say an encoder can't be used here: the generic ones, then each
// graphics family's own (rules.UnavailablePhrases).
func encoderUnavailablePhrases() []string {
	return append([]string{"unknown encoder", "encoder not found", "error while opening encoder", "could not open encoder",
		"error initializing output stream", "no device", "device not available", "encoder not available"}, rules.UnavailablePhrases()...)
}

// containsAny says whether text (lower case) has any of the phrases.
func containsAny(text string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// classifyKeyLines is the code of a generic ffmpeg failure, from the lines ffmpeg printed (lower case match).
func classifyKeyLines(lines []string) string {
	text := strings.ToLower(strings.Join(lines, "\n"))
	switch {
	case strings.Contains(text, "could not write header") && strings.Contains(text, "supported for"):
		// s4: ffmpeg stopped at the new file's header because its type can't hold a track the plan maps
		// in. Both lines are needed: a bare "Could not write header" stays ffmpeg_error (slice 15).
		return "settings_rejected"
	case containsAny(text, encoderUnavailablePhrases()):
		return "encoder_unavailable"
	case strings.Contains(text, "no space left on device"):
		return "no_space"
	case strings.Contains(text, "permission denied"):
		return "output_not_writable"
	case strings.Contains(text, "no such file or directory"):
		return "source_missing"
	case strings.Contains(text, "invalid data found when processing input"), strings.Contains(text, "moov atom not found"),
		strings.Contains(text, "error while decoding"), strings.Contains(text, "decode_slice_header error"),
		strings.Contains(text, "non-existing pps"), strings.Contains(text, "invalid nal unit"):
		return "source_damaged"
	}
	return "ffmpeg_error"
}

// FailureFacts are what a failed job's sentence can name: its file, its output folder, the encoder and exit code
// ffmpeg reported, the place in the video where it stopped (when known) and the stored message.
type FailureFacts struct {
	Path, Folder string
	ExitCode     int // -1: none
	MediaTime    string
	Stored       string // the stored plain words, used for a code the table does not name
	KeyLines     []string
	Needed, Free uint64 // space needed/free for no_space (0 = unknown)
	WorkerName   string // the worker's display name ("this computer" for local)
	FfmpegRan    bool   // ffmpeg started (false: never ran or was never started)
}

// encoderFamily maps the suffix after the codec (e.g. h264_qsv → Intel Quick Sync)
// from the -c:v arg or the "Unknown encoder" line.
var encoderFamily = map[string]string{
	"qsv":          "Intel Quick Sync",
	"nvenc":        "NVIDIA",
	"amf":          "AMD",
	"amdhw":        "AMD",
	"videotoolbox": "Apple",
	"vaapi":        "graphics card",
}

var encoderName = regexp.MustCompile(`unknown encoder '([^']+)'`)

// FailureWords is design's sentence and "what you can do" for a failed job's code (USER-REVIEW 11-13 item E, V6b).
// A fact the server does not have is left out of the sentence rather than guessed. A code the table does not name
// keeps the stored words and no hint.
func FailureWords(code string, f FailureFacts) (message, hint string) {
	switch code {
	case "encoder_unavailable":
		name := ""
		family := "The encoder"
		if m := encoderName.FindStringSubmatch(strings.ToLower(strings.Join(f.KeyLines, "\n"))); m != nil {
			name = m[1] // e.g. "h264_qsv"
			// extract the suffix after the first underscore: qsv, nvenc, amf, videotoolbox, vaapi
			if i := strings.LastIndex(name, "_"); i >= 0 {
				if f, found := encoderFamily[name[i+1:]]; found {
					family = f
				}
			}
		}
		worker := "this computer"
		if f.WorkerName != "" {
			worker = f.WorkerName
		}
		if name != "" {
			return "The encoder couldn't start: " + family + " (" + name + ") isn't available on " + worker + ".",
				"Test the encoders on the Workers page, or convert on the processor."
		}
		return "The encoder couldn't start: " + family + " isn't available on " + worker + ".",
			"Test the encoders on the Workers page, or convert on the processor."
	case "output_not_writable":
		where := "the output folder"
		if f.Folder != "" {
			where = f.Folder
		}
		return "Couldn't write to " + where + ": permission denied.", "Let Shrink Your Shit write to that folder, then Retry."
	case "no_space":
		where := "the output folder"
		if f.Folder != "" {
			where = f.Folder
		}
		if f.Needed > 0 && f.Free > 0 {
			return "Not enough space in " + where + ": it needs about " + gb(f.Needed) + " and " + gb(f.Free) + " is free.",
				"Free some space there, then Retry."
		}
		return "Not enough space in " + where + " to finish converting.", "Free some space there, then Retry."
	case "source_damaged":
		msg := "The source file is damaged."
		if f.MediaTime != "" {
			msg = "The source file is damaged at " + f.MediaTime + "."
		}
		return msg, "Play the file around that point to check it. Retry won't help with the same file."
	case "source_missing":
		return "Couldn't read " + f.Path + ": the file is missing.", "Check that the file is still there, then Retry."
	case "check_differs":
		return "The new file isn't what the workflow asked for: " + firstProblem(f.Stored) + ". The original is untouched.",
			"Open the Check step in the Log to see what differs. Retrying gives the same result. If the workflow asks for something this encoder can't make, change its Convert step."
	case "output_damaged":
		return "The new file came out damaged: " + firstProblem(f.Stored) + ". The original is untouched.",
			"Retry. If it happens again, convert this file on the processor."
	case "check_not_run":
		return "A check couldn't run, so the original is kept: " + firstProblem(f.Stored) + ".",
			"Retry later. If it keeps happening, Settings > About says what is wrong."
	case "check_failed":
		// only the picture-similarity refusal is about quality; a size, stream or could-not-run refusal is not
		if strings.Contains(f.Stored, PictureTooDifferentWords) {
			return "The new file didn't pass the picture check. The original is untouched.",
				"Pick a higher quality on the Convert step, or lower the check on the workflow."
		}
		return "The new file didn't pass a check. The original is untouched.", "Check the workflow's Convert step, then Retry."
	case "worker_lost":
		return "The worker stopped answering during the conversion.", "Check that the worker is running. The file can be retried on any worker."
	case "stalled":
		return "The conversion stopped making progress and was stopped.",
			"Retry. If it stops again at the same point, the last ffmpeg lines above show where."
	case "settings_rejected":
		// slice 15: the worker's own sentence is headerWords' ("ffmpeg couldn't write the new file: …
		// Your original was not touched."), which it stored as the job's words
		if f.Stored != "" {
			return f.Stored, "Change the workflow: this file type can't hold a track the way the workflow maps it. Retrying gives the same result."
		}
		return "ffmpeg couldn't write the new file. Your original was not touched.",
			"Change the workflow: this file type can't hold a track the way the workflow maps it. Retrying gives the same result."
	case "ffmpeg_error":
		// ffmpeg never ran (a folder setup failure, say): the worker's own sentence is the true one
		if !f.FfmpegRan && f.ExitCode < 0 && f.Stored != "" {
			return f.Stored, ""
		}
		// 255 = ffmpeg was stopped; -1 = killed by a signal
		if f.ExitCode == 255 {
			return "ffmpeg was stopped before it finished (exit code 255).", ""
		}
		if f.ExitCode == -1 && f.FfmpegRan {
			return "ffmpeg was stopped before it finished (stopped by a signal).", ""
		}
		if f.ExitCode >= 0 {
			return fmt.Sprintf("ffmpeg stopped with an error (exit code %d).", f.ExitCode), ""
		}
		return "ffmpeg stopped with an error.", ""
	}
	return f.Stored, ""
}

// HeaderSupport is ffmpeg's own sentence about what the new file's type supports, when it stopped at
// the file's header because the type can't hold a track as the plan maps it: a "Could not write header"
// line and a "… supported for …" one, s4's WebM refusal (slice 15). ok is false otherwise, so a bare
// header stop or a damaged file is never called a file type's limit.
func HeaderSupport(stderr string) (line string, ok bool) {
	if !strings.Contains(stderr, "Could not write header") {
		return "", false
	}
	for _, l := range strings.Split(stderr, "\n") {
		if !strings.Contains(l, "supported for") {
			continue
		}
		if i := strings.Index(l, "] "); i >= 0 { // "[webm @ 0x...] Only VP8 ... are supported for WebM."
			line = strings.TrimSpace(l[i+2:])
		}
	}
	return line, line != ""
}

// InfoLine returns true for ffmpeg informational lines that should not be key lines.
func InfoLine(l string) bool {
	low := strings.ToLower(l)
	return strings.Contains(low, "[info]") || strings.Contains(low, "[verbose]") ||
		strings.HasPrefix(low, "frame=") || strings.HasPrefix(low, "stream mapping") ||
		strings.HasPrefix(low, "press [q]") || strings.Contains(low, "encoder settings") || strings.HasPrefix(low, "libva info:")
}

// MatchedKeyLines are the failure's key lines: the lines that give the code classifyKeyLines chose (the lines that
// say why), else the lines that report an error, else [] (never informational lines, never the last 5 lines).
func MatchedKeyLines(lines []string) []string {
	code := classifyKeyLines(lines)
	if code != "ffmpeg_error" {
		var out []string
		for _, l := range lines {
			if classifyKeyLines([]string{l}) == code {
				out = append(out, l)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	var errs []string
	for _, l := range lines {
		if InfoLine(l) {
			continue
		}
		if low := strings.ToLower(l); strings.Contains(low, "error") || strings.Contains(low, "invalid") {
			errs = append(errs, l)
		}
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

var deniedOutput = regexp.MustCompile(`(?i)output\s+(\S.*?):\s*permission denied`)

// deniedWrite is the worker's own line for a write refused while putting the new file in place:
// "Couldn't write <path>: <the system's words>" (worker placeError.keyLine).
var deniedWrite = regexp.MustCompile(`(?i)^couldn't write (\S.*): (?:permission denied|read-only file system|access is denied)`)

// LastName is the last name of a path, for / and \ separators alike: a remote worker can send Windows paths to a
// Linux server, where filepath.Base would not split them.
func LastName(p string) string {
	t := strings.TrimRight(p, `/\`)
	return t[strings.LastIndexAny(t, `/\`)+1:]
}

// DeniedFolder is the folder a write was refused in: from ffmpeg's "Error opening output <path>: Permission denied"
// line (user review 11-13 item E, QA of #214), or the worker's "Couldn't write <path>: ..." line when putting the new
// file in place was refused (the manager's check). It is the new file's folder, never the source's. A path inside a
// staging folder (".syp…") names the folder above it. "" when no line names a path.
func DeniedFolder(lines []string) string {
	for _, l := range lines {
		m := deniedOutput.FindStringSubmatch(l)
		if m == nil {
			m = deniedWrite.FindStringSubmatch(l)
		}
		if m == nil || !strings.ContainsAny(m[1], `/\`) {
			continue
		}
		dir := m[1]
		if i := strings.LastIndexAny(dir, `/\`); i >= 0 {
			dir = dir[:i]
		}
		for {
			i := strings.LastIndexAny(dir, `/\`)
			if i < 0 {
				break
			}
			base := dir[i+1:]
			if !strings.HasPrefix(base, ".syp") && !strings.HasSuffix(base, ".syp-tmp") {
				break
			}
			dir = dir[:i]
		}
		return dir
	}
	return ""
}

// classifier is one code in classifyKeyLines' precedence: patterns match when any of their lines does,
// all only when every one of them does.
type classifier struct {
	code     string
	patterns []string
	all      []string
}

// classifierOrder is classifyKeyLines' precedence: the first code whose lines match is the failure's.
var classifierOrder = []classifier{
	{code: "settings_rejected", all: []string{"could not write header", "supported for"}},
	{code: "encoder_unavailable", patterns: encoderUnavailablePhrases()},
	{code: "no_space", patterns: []string{"no space left on device"}},
	{code: "output_not_writable", patterns: []string{"permission denied", "read-only file system", "access is denied"}},
	{code: "source_missing", patterns: []string{"no such file or directory"}},
	{code: "source_damaged", patterns: []string{"invalid data found when processing input", "moov atom not found", "error while decoding",
		"decode_slice_header error", "non-existing pps", "invalid nal unit"}},
}

// ReasonCondition is the SQL condition (on jobs as j; the failure's key lines are read from job_failures) of a
// design failure code, for ?reason=: the stored error code, or for a generic failure the key lines that
// classifyKeyLines would give it. ok is false for a code no failure has. Its args go in order.
func ReasonCondition(code string) (cond string, args []any, ok bool) {
	stored := map[string][]string{"no_space": {ReasonDiskFull, ReasonLowDiskSpace}, "output_not_writable": {ReasonCannotWrite},
		"check_failed": {ReasonKeptOriginal}, "stalled": {ReasonStalled}, "worker_lost": {ReasonWorkerLost},
		"settings_rejected": {ReasonSettingsRejected}, "check_differs": {ReasonCheckDiffers}, "output_damaged": {ReasonOutputDamaged}, "check_not_run": {ReasonCheckNotRun}}
	if codes, found := stored[code]; found {
		marks := strings.Repeat(",?", len(codes)-1)
		return "j.error_code IN (?" + marks + ")", anys(codes), true
	}
	cond, args, ok = keyLinesCondition(code)
	if ok && (code == "source_damaged" || code == "source_missing") { // a refusal of the original stores the code; a failed job shows its key lines
		return "(j.error_code=? OR " + cond + ")", append(anys([]string{code}), args...), true
	}
	return cond, args, ok
}

// keyLinesCondition is ReasonCondition for a code the stored code does not name: a failed job's key lines.
func keyLinesCondition(code string) (cond string, args []any, ok bool) {
	keys := `lower(COALESCE((SELECT key_lines FROM job_failures WHERE job_id=j.id), ''))`
	anyOf := func(patterns []string) string {
		parts := make([]string, len(patterns))
		for i, p := range patterns {
			parts[i] = keys + " LIKE ?"
			args = append(args, "%"+p+"%")
		}
		return "(" + strings.Join(parts, " OR ") + ")"
	}
	// match is one classifier's lines: any of its patterns, or every one of its all
	match := func(c classifier) string {
		parts := []string{}
		if len(c.patterns) > 0 {
			parts = append(parts, anyOf(c.patterns))
		}
		if len(c.all) > 0 {
			alls := make([]string, len(c.all))
			for i, p := range c.all {
				alls[i] = keys + " LIKE ?"
				args = append(args, "%"+p+"%")
			}
			parts = append(parts, "("+strings.Join(alls, " AND ")+")")
		}
		return "(" + strings.Join(parts, " OR ") + ")"
	}
	conds := []string{"j.error_code=?"}
	args = []any{ReasonConvertFailed}
	found, pos := code == "ffmpeg_error", len(classifierOrder) // ffmpeg_error: matches none of them
	for i, c := range classifierOrder {
		if c.code == code {
			found, pos = true, i
		}
	}
	if !found {
		return "", nil, false
	}
	for i := 0; i < pos; i++ { // an earlier code's lines win over this one
		conds = append(conds, "NOT "+match(classifierOrder[i]))
	}
	if code != "ffmpeg_error" {
		conds = append(conds, match(classifierOrder[pos]))
	} else {
		for _, c := range classifierOrder {
			conds = append(conds, "NOT "+match(c))
		}
	}
	return strings.Join(conds, " AND "), args, true
}

// firstProblem is the first of a refusal's reasons (they are joined with "; "), without its full stop.
func firstProblem(stored string) string {
	first, _, _ := strings.Cut(stored, "; ")
	return strings.TrimSuffix(first, ".")
}

func anys(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// gb shows a byte count in GB, to one decimal, for the no_space figures.
func gb(n uint64) string {
	return strconv.FormatFloat(float64(n)/(1<<30), 'f', 1, 64) + " GB"
}
