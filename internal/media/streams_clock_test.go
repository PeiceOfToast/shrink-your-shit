// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import "testing"

// mkv states a stream's length as a DURATION tag ("HH:MM:SS.fraction"); anything else reads as unknown.
func TestClockSeconds(t *testing.T) {
	cases := map[string]float64{"00:00:10.000000000": 10, "01:02:03.5": 3723.5, "": 0, "10": 0, "00:xx:01": 0}
	for in, want := range cases {
		if got := clockSeconds(in); got != want {
			t.Errorf("clockSeconds(%q) = %v, want %v", in, got, want)
		}
	}
}
