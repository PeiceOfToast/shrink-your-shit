package media

import "testing"

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
