// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import "testing"

// Slice 16: the Settings line names the cap and buffer when the encode uses them, not only the quality.
func TestEncoderDetailNamesMaxrateAndBufsize(t *testing.T) {
	args := []string{"-c:v", "libx265", "-b:v", "1800k", "-maxrate:v", "2700k", "-bufsize:v", "5400k"}
	want := "Encoder: libx265, -b:v 1800k, -maxrate 2700k, -bufsize 5400k, hardware: none"
	if got := encoderDetail(args); got != want {
		t.Errorf("encoderDetail = %q, want %q", got, want)
	}
}
