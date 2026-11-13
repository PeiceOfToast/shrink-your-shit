// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"strings"
	"testing"
)

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

// User review 11-13 item E: design's sentences; a fact the server lacks is left out, not guessed.
func TestFailureWords(t *testing.T) {
	f := FailureFacts{Path: "/media/movies/Movie.mkv", Folder: "/mnt/out", ExitCode: 1, MediaTime: "00:41:12", Stored: "Converting did not finish",
		KeyLines: []string{"[AVEncoder @ 0x1] Unknown encoder 'h264_qsv'"}}
	for _, c := range []struct{ code, message string }{
		{"encoder_unavailable", "The encoder couldn't start: h264_qsv isn't available on this worker."},
		{"output_not_writable", "Couldn't write to /mnt/out: permission denied."},
		{"no_space", "Not enough space in /mnt/out to finish converting."},
		{"source_damaged", "The source file is damaged at 00:41:12."},
		{"source_missing", "Couldn't read /media/movies/Movie.mkv: the file is missing."},
		{"ffmpeg_error", "ffmpeg stopped with an error (exit code 1)."},
		{"cannot_read", "Converting did not finish"},
	} {
		if got, _ := FailureWords(c.code, f); got != c.message {
			t.Errorf("%s: %q, want %q", c.code, got, c.message)
		}
	}
	if _, hint := FailureWords("source_damaged", FailureFacts{ExitCode: -1}); hint == "" {
		t.Error("a damaged source has a hint")
	}
	if msg, _ := FailureWords("source_damaged", FailureFacts{ExitCode: -1}); msg != "The source file is damaged." {
		t.Errorf("no media time: %q", msg)
	}
	if msg, _ := FailureWords("ffmpeg_error", FailureFacts{ExitCode: -1}); msg != "ffmpeg stopped with an error." {
		t.Errorf("no exit code: %q", msg)
	}
}

// QA of #214: the key lines are the ones that matched the code (the QA run's real lines), not any line with "error".
func TestMatchedKeyLines(t *testing.T) {
	enc := []string{"frame=   12 fps=0.0", "Input #0, matroska", "[vost#0:0/h264_qsv] Unknown encoder 'h264_qsv'",
		"Error while opening encoder for output stream #0:0", "Conversion failed!"}
	if got := MatchedKeyLines(enc); len(got) != 2 || got[0] != "[vost#0:0/h264_qsv] Unknown encoder 'h264_qsv'" {
		t.Errorf("encoder: %q", got)
	}
	dmg := []string{"Input #0, matroska", "Invalid NAL unit size (3307 > 1706).", "Invalid data found when processing input"}
	if got := MatchedKeyLines(dmg); len(got) != 2 || got[0] != "Invalid NAL unit size (3307 > 1706)." {
		t.Errorf("damaged: %q", got)
	}
	perm := []string{"Error opening output /x/new.mkv: Permission denied", "Error opening output files: Permission denied"}
	if got := MatchedKeyLines(perm); len(got) != 2 {
		t.Errorf("permission: %q", got)
	}
	generic := []string{"a", "b", "c", "d", "e", "f", "g"}
	if got := MatchedKeyLines(generic); len(got) != 5 || got[0] != "c" {
		t.Errorf("no error line: %q", got)
	}
}

// QA of #214: the folder a write was refused in is the new file's (never the source's, never the staging folder's name).
func TestDeniedFolder(t *testing.T) {
	for _, c := range []struct {
		lines []string
		want  string
	}{
		{[]string{"Error opening output /mnt/out/new.mkv: Permission denied"}, "/mnt/out"},
		{[]string{"Error opening output /srv/media/.syp-tmp/.Movie.abc.syp-tmp.mkv: Permission denied"}, "/srv/media"},
		{[]string{"Error opening output files: Permission denied"}, ""}, // no path in it
		{[]string{"Conversion failed!"}, ""},
	} {
		if got := DeniedFolder(c.lines); got != c.want {
			t.Errorf("%q: %q, want %q", c.lines, got, c.want)
		}
	}
}

// QA of #214: a refused output with no folder named says so, and never names the source.
func TestOutputNotWritableWords(t *testing.T) {
	if msg, _ := FailureWords("output_not_writable", FailureFacts{ExitCode: -1}); msg != "Couldn't write to the output folder: permission denied." {
		t.Errorf("no folder: %q", msg)
	}
	if msg, _ := FailureWords("output_not_writable", FailureFacts{Folder: "/mnt/out", ExitCode: -1}); msg != "Couldn't write to /mnt/out: permission denied." {
		t.Errorf("folder: %q", msg)
	}
}

// QA of #214, the real stderr of the QSV failure: the encoder-open lines are key lines, and the failure is the encoder's.
func TestQSVKeyLines(t *testing.T) {
	qsv := []string{
		"[h264_qsv @ 0x5f1] Error creating a MFX session: -9.",
		"[vost#0:0/h264_qsv @ 0x5f2] Error while opening encoder for output stream #0:0 - maybe incorrect parameters such as bit_rate, rate, width or height",
		"[vost#0:0/h264_qsv @ 0x5f2] Error sending frames to consumers: Unknown error occurred",
		"Error sending frames to consumers: Unknown error occurred",
		"Conversion failed!",
	}
	if got := classifyKeyLines(qsv); got != "encoder_unavailable" {
		t.Fatalf("code %s", got)
	}
	got := MatchedKeyLines(qsv)
	have := strings.Join(got, "\n")
	if !strings.Contains(have, "Error creating a MFX session: -9.") || !strings.Contains(have, "Error while opening encoder") {
		t.Errorf("key lines leave out the encoder lines: %q", got)
	}
}
