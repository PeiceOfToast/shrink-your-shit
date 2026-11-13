// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"fmt"
	"regexp"
	"strings"
)

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
	case ReasonConvertFailed, "":
		return classifyKeyLines(keyLines)
	}
	return errorCode
}

// classifyKeyLines is the code of a generic ffmpeg failure, from the lines ffmpeg printed (lower case match).
func classifyKeyLines(lines []string) string {
	text := strings.ToLower(strings.Join(lines, "\n"))
	switch {
	case strings.Contains(text, "unknown encoder"), strings.Contains(text, "encoder not found"),
		strings.Contains(text, "error while opening encoder"), strings.Contains(text, "could not open encoder"):
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
}

var encoderName = regexp.MustCompile(`unknown encoder '([^']+)'`)

// FailureWords is design's sentence and "what you can do" for a failed job's code (USER-REVIEW 11-13 item E, V6b).
// A fact the server does not have is left out of the sentence rather than guessed. A code the table does not name
// keeps the stored words and no hint.
func FailureWords(code string, f FailureFacts) (message, hint string) {
	switch code {
	case "encoder_unavailable":
		what := "The encoder"
		if m := encoderName.FindStringSubmatch(strings.ToLower(strings.Join(f.KeyLines, "\n"))); m != nil {
			what = m[1]
		}
		return "The encoder couldn't start: " + what + " isn't available on this worker.",
			"Test the encoders on the Workers page, or convert on the processor."
	case "output_not_writable":
		return "Couldn't write to " + f.Folder + ": permission denied.", "Let shrink-your-shit write to that folder, then Retry."
	case "no_space":
		return "Not enough space in " + f.Folder + " to finish converting.", "Free some space there, then Retry."
	case "source_damaged":
		msg := "The source file is damaged."
		if f.MediaTime != "" {
			msg = "The source file is damaged at " + f.MediaTime + "."
		}
		return msg, "Play the file around that point to check it. Retry won't help with the same file."
	case "source_missing":
		return "Couldn't read " + f.Path + ": the file is missing.", "Check that the file is still there, then Retry."
	case "check_failed":
		return "The new file didn't pass the picture check. The original is untouched.",
			"Pick a higher quality on the Convert step, or lower the check on the workflow."
	case "worker_lost":
		return "The worker stopped answering during the conversion.", "Check that the worker is running. The file can be retried on any worker."
	case "stalled":
		return "The conversion stopped making progress and was stopped.",
			"Retry. If it stops again at the same point, the last ffmpeg lines above show where."
	case "ffmpeg_error":
		if f.ExitCode >= 0 {
			return fmt.Sprintf("ffmpeg stopped with an error (exit code %d).", f.ExitCode), ""
		}
		return "ffmpeg stopped with an error.", ""
	}
	return f.Stored, ""
}

// MatchedKeyLines are the failure's key lines: the lines that give the code classifyKeyLines chose (the lines that
// say why), else the lines that report an error, else the last lines (user review 11-13 item E, QA of #214).
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
		if low := strings.ToLower(l); strings.Contains(low, "error") || strings.Contains(low, "invalid") {
			errs = append(errs, l)
		}
	}
	if len(errs) > 0 {
		return errs
	}
	if len(lines) > 5 {
		return lines[len(lines)-5:]
	}
	return lines
}
