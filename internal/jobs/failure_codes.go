// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import "strings"

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
