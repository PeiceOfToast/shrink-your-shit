package worker

import (
	"strings"
	"testing"
)

// ffmpeg 8's message for the last Opus packet of a good file is not damage; any other error beside it is.
func TestOpusParserMessageIsNotDamage(t *testing.T) {
	msg := "[opus @ 0x62344050b540] Error parsing Opus packet header.\n"
	if got := strings.TrimSpace(opusParserRe.ReplaceAllString(msg, "")); got != "" {
		t.Errorf("left %q", got)
	}
	other := "[h264 @ 0x1] error while decoding MB 3 4\n"
	if got := strings.TrimSpace(opusParserRe.ReplaceAllString(msg+other, "")); got != strings.TrimSpace(other) {
		t.Errorf("other errors must stay: %q", got)
	}
}
