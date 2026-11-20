package media

import "testing"

// The hearing_impaired disposition is read next to forced (SDH subtitles are named .sdh.srt).
func TestParseStreamHearingImpaired(t *testing.T) {
	s := parseStream(map[string]any{"codec_type": "subtitle", "disposition": map[string]any{"hearing_impaired": 1.0}}, 0)
	if !s.HearingImpaired || s.Forced {
		t.Errorf("%+v", s)
	}
}
