// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"os"
	"path/filepath"
	"testing"
)

func statsFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "stats", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The report comes from real ffmpeg 7.0.2 runs on a 3 s testsrc2 file with one audio track: testdata/stats
// holds their stderr (encode.txt: the video encoded; copy.txt: every stream copied).
func TestInputStreamStats(t *testing.T) {
	encode := statsFixture(t, "encode.txt")
	cases := []struct {
		name   string
		log    string
		in, st int
		want   StreamStats
	}{
		{"the video, encoded", encode, 0, 0, StreamStats{Packets: 75, Frames: 75}},
		{"the audio (never decoded)", encode, 0, 1, StreamStats{Packets: 131, Frames: -1}},
		{"the video, copied", statsFixture(t, "copy.txt"), 0, 0, StreamStats{Packets: 75, Frames: -1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := InputStreamStats(c.log, c.in, c.st)
			if !ok || got != c.want {
				t.Errorf("InputStreamStats(%d:%d) = %+v, %v; want %+v", c.in, c.st, got, ok, c.want)
			}
		})
	}
}

func TestInputStreamStatsNotFound(t *testing.T) {
	encode := statsFixture(t, "encode.txt")
	cases := []struct {
		name, log string
		in, st    int
	}{
		{"a stream the run did not read", encode, 0, 2},
		{"a second input file", encode, 1, 0},
		// at -v error there is no report at all (this is what a failed conversion writes)
		{"no report", "[h264 @ 0x1] error while decoding MB 3 4\nError while decoding stream #0:0: Invalid data found when processing input\n", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, ok := InputStreamStats(c.log, c.in, c.st); ok {
				t.Errorf("got %+v, want no report", got)
			}
		})
	}
}
