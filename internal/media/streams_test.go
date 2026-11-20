package media

import "testing"

// The main picture is the first video stream that is not cover art, found in one place: the four callers
// that pick "the film" (Video, MainVideoOrdinal, ParseProbe's headline, the worker's picture count) take
// their position from here.
func TestMainVideoIndex(t *testing.T) {
	cases := []struct {
		name    string
		streams []Stream
		want    int
	}{
		{"no video", []Stream{{Type: "audio"}, {Type: "subtitle"}}, -1},
		{"one video", []Stream{{Type: "audio"}, {Type: "video"}}, 1},
		{"cover first, then the film", []Stream{{Type: "video", AttachedPic: true}, {Type: "video"}}, 1},
		{"cover only", []Stream{{Type: "audio"}, {Type: "video", AttachedPic: true}}, -1},
		{"two real videos: the first wins", []Stream{{Type: "video", Codec: "h264"}, {Type: "video", Codec: "hevc"}}, 0},
		{"data and attachment before the film", []Stream{{Type: "data"}, {Type: "attachment"}, {Type: "video"}}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (Info{Streams: c.streams}).MainVideoIndex(); got != c.want {
				t.Errorf("MainVideoIndex() = %d; want %d", got, c.want)
			}
		})
	}
}

// The hearing_impaired disposition is read next to forced (SDH subtitles are named .sdh.srt).
func TestParseStreamHearingImpaired(t *testing.T) {
	s := parseStream(map[string]any{"codec_type": "subtitle", "disposition": map[string]any{"hearing_impaired": 1.0}}, 0)
	if !s.HearingImpaired || s.Forced {
		t.Errorf("%+v", s)
	}
}

// Audio streams state their own length too (the timing check compares it): the stream field and the mkv DURATION
// tag are both read for every stream type, not only video.
func TestParseStreamAudioDuration(t *testing.T) {
	if s := parseStream(map[string]any{"codec_type": "audio", "duration": "10.5"}, 1); s.Duration != 10.5 {
		t.Errorf("stream field: duration %v, want 10.5", s.Duration)
	}
	s := parseStream(map[string]any{"codec_type": "audio", "tags": map[string]any{"DURATION": "00:00:06.250000000"}}, 1)
	if s.Duration != 6.25 {
		t.Errorf("DURATION tag: duration %v, want 6.25", s.Duration)
	}
}
