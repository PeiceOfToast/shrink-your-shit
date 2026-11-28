package steps

import (
	"strings"
	"testing"
)

// The closed Audio card says what the run keeps: "every language" when "Keep every language" is on (whatever the list
// holds, since the engine ignores it), the named list when it is off with one, and "my languages" when it is off with none.
func TestAudioSummarySaysWhatTheRunKeeps(t *testing.T) {
	cases := []struct {
		name string
		set  AudioSettings
		want string
		not  string
	}{
		{"keep every language", AudioSettings{AllLanguages: true}, " · every language · ", "my languages"},
		{"keep every language with a list", AudioSettings{AllLanguages: true, Languages: []string{"eng"}}, " · every language · ", "English"},
		{"my languages", AudioSettings{AllLanguages: false}, " · my languages · ", "every language"},
		{"a list", AudioSettings{AllLanguages: false, Languages: []string{"eng", "ger"}}, " · English and German · ", "my languages"},
	}
	for _, c := range cases {
		got := audioSummary(c.set)
		if !strings.Contains(got, c.want) || strings.Contains(got, c.not) {
			t.Errorf("%s: summary %q, want %q and not %q", c.name, got, c.want, c.not)
		}
	}
}

// Subtitles in "my languages" mode follow the audio languages unless they have their own list.
func TestAudioSummarySubtitlesFollowTheAudioLanguages(t *testing.T) {
	got := audioSummary(AudioSettings{AllLanguages: true, Subtitles: "my_languages"})
	if !strings.HasSuffix(got, "subtitles: every language") {
		t.Errorf("summary %q, want it to end with %q", got, "subtitles: every language")
	}
}
