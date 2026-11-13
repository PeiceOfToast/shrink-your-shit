// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import "testing"

// User review 11-13 item E: each code of design's V6b table, from the stored code or from ffmpeg's own lines (the
// sample lines are ffmpeg's messages as it prints them).
func TestFailureCodes(t *testing.T) {
	for _, c := range []struct {
		code  string
		lines []string
		want  string
	}{
		{ReasonDiskFull, nil, "no_space"},
		{ReasonLowDiskSpace, nil, "no_space"},
		{ReasonCannotWrite, nil, "output_not_writable"},
		{ReasonStalled, nil, "stalled"},
		{ReasonWorkerLost, nil, "worker_lost"},
		{ReasonKeptOriginal, []string{"whatever"}, "check_failed"},
		{ReasonConvertFailed, []string{"[AVEncoder @ 0x1] Unknown encoder 'h264_qsv'"}, "encoder_unavailable"},
		{ReasonConvertFailed, []string{"Error while opening encoder for output stream #0:0 - maybe incorrect parameters"}, "encoder_unavailable"},
		{ReasonConvertFailed, []string{"[matroska,webm @ 0x2] EBML header parsing failed", "Invalid data found when processing input"}, "source_damaged"},
		{ReasonConvertFailed, []string{"[mov,mp4 @ 0x3] moov atom not found"}, "source_damaged"},
		{ReasonConvertFailed, []string{"[h264 @ 0x4] error while decoding MB 12 3"}, "source_damaged"},
		{ReasonConvertFailed, []string{"/media/Movie.mkv: No such file or directory"}, "source_missing"},
		{ReasonConvertFailed, []string{"/mnt/out/Movie.mkv: Permission denied"}, "output_not_writable"},
		{ReasonConvertFailed, []string{"Error writing trailer: No space left on device"}, "no_space"},
		{ReasonConvertFailed, []string{"Conversion failed!"}, "ffmpeg_error"},
		{ReasonConvertFailed, nil, "ffmpeg_error"},
		{"cannot_read", nil, "cannot_read"},
		{"retry_later", nil, "retry_later"},
	} {
		if got := FailureCode(c.code, c.lines); got != c.want {
			t.Errorf("%s %q: %s, want %s", c.code, c.lines, got, c.want)
		}
	}
}
