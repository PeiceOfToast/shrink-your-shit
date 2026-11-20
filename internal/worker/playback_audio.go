// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// decoderRe is the name in front of an ffmpeg error line: "[aac @ 0x55d1c2a0] ...". ffmpeg 7 adds its own
// prefixes to it, as in "[aist#0:0/aac @ ...]" and "[dec:aac @ ...]"; the decoder is the part after the last
// "/" or ":".
var decoderRe = regexp.MustCompile(`^\[([^ \]]+) @ `)

func decoderName(line string) (string, bool) {
	m := decoderRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	name := m[1]
	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		name = name[i+1:]
	}
	return name, true
}

// audioErrors says which audio tracks the playback test's error lines are about. Each line names the decoder
// that reported it; a line is the audio track's when its decoder is that track's codec ("aac" for AAC, "mp3float"
// for MP3). Tracks are numbered from 1 among the file's audio tracks. other is true when some line is not
// clearly an audio track's (a video decoder's, or the demuxer's): those are not named here.
func audioErrors(text string, out media.Info) (tracks []int, other bool) {
	audio := out.Audio()
	seen := map[int]bool{}
	for _, l := range strings.Split(text, "\n") {
		name, ok := decoderName(l)
		if !ok {
			other = true
			continue
		}
		matched := false
		for i, a := range audio {
			if sameDecoder(name, a.Codec) {
				matched = true
				if !seen[i+1] {
					seen[i+1] = true
					tracks = append(tracks, i+1)
				}
			}
		}
		if !matched {
			other = true
		}
	}
	sort.Ints(tracks)
	return tracks, other
}

// sameDecoder is whether an ffmpeg decoder name belongs to a codec: the same name, or a variant of it
// ("mp3float" for "mp3", "ac3_fixed" for "ac3").
func sameDecoder(decoder, codec string) bool {
	return codec != "" && (decoder == codec || strings.HasPrefix(decoder, codec))
}

// muxerDTSRe is the null muxer's timestamp warning, "Application provided invalid, non monotonically increasing dts
// to muxer in stream N". The decode check maps the video first and the audio after, so stream 0 is the video and
// any other stream is an audio track.
var muxerDTSRe = regexp.MustCompile(`non monotonically increasing dts to muxer in stream ([0-9]+)`)

var repeatedRe = regexp.MustCompile(`^Last message repeated [0-9]+ times$`)

// dropAudioTiming removes the muxer's dts warnings for audio streams, and the "Last message repeated" lines that
// follow them. They are timing in the muxer, not decode errors, so audio is judged by the decoder's errors alone
// (R2). Video warnings stay. It returns the text kept and how many warnings were dropped.
func dropAudioTiming(text string) (string, int) {
	var kept []string
	dropped := 0
	afterAudio := false
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if m := muxerDTSRe.FindStringSubmatch(t); m != nil && m[1] != "0" {
			dropped++
			afterAudio = true
			continue
		}
		if afterAudio && repeatedRe.MatchString(t) {
			continue
		}
		afterAudio = false
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n"), dropped
}

// audioReason is the plain reason for a file whose audio does not play: which track, in words.
func audioReason(tracks []int, other bool) string {
	names := make([]string, len(tracks))
	for i, n := range tracks {
		names[i] = fmt.Sprint(n)
	}
	track := "audio track " + names[0]
	if len(names) > 1 {
		track = "audio tracks " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
	if other {
		return "the new file does not play cleanly: " + track + " has errors"
	}
	return "the new file's " + track + " does not play cleanly"
}
