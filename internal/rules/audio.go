// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// TrackOp is what happens to one audio or subtitle track of the original (slice 25b). Tracks without an
// op are copied as they are.
type TrackOp struct {
	Index   int    `json:"index"`             // the stream's index in the original (ffprobe's)
	Type    string `json:"type"`              // audio | subtitle
	Drop    bool   `json:"drop,omitempty"`    // left out of the new file
	Encoder string `json:"encoder,omitempty"` // converted with this ffmpeg encoder (aac, eac3, libopus)
	Codec   string `json:"codec,omitempty"`   // the codec the new track has then, as ffprobe names it
	Kbps    int    `json:"kbps,omitempty"`    // its bitrate
	// Extract: a subtitle saved as a separate .srt file beside the new file (and left out of it); the
	// name part between the new file's name and ".srt": "eng", "eng.forced", "" (no language), "eng.2".
	Extract string `json:"extract,omitempty"`
	ToFile  bool   `json:"toFile,omitempty"` // it is saved as an .srt file (Extract may be "")
}

// audioTargets are the codecs the audio choices convert to: ffmpeg encoder, codec name, kbps per channel.
var audioTargets = map[string]struct {
	encoder, codec string
	perChannel     int
}{
	"aac":  {"aac", "aac", 64},
	"opus": {"libopus", "opus", 48},
	"eac3": {"eac3", "eac3", 96},
}

// maxAudioKbps caps the automatic bitrate of one converted track.
const maxAudioKbps = 640

// AutoAudioKbps is the bitrate a track of n channels is converted to in target (aac, opus, eac3):
// a fixed amount per channel, capped. Standard has no bitrate choice (lead, 13:44Z).
func AutoAudioKbps(target string, channels int) int {
	if channels <= 0 {
		channels = 2
	}
	return min(audioTargets[target].perChannel*channels, maxAudioKbps)
}

// NoAudioInMyLanguages is said when "Keep only my languages" would leave a file without sound: then
// every audio track is kept (no choice drops all audio; design's words).
const NoAudioInMyLanguages = "None of the audio is in your languages, so all audio tracks are kept."

// SubtitlesStayInside is said when "Save as separate .srt files" leaves subtitles inside the new file:
// pictures (PGS, VobSub) and formats ffmpeg cannot turn into text (TTML) (design's words).
const SubtitlesStayInside = "Picture subtitles stay inside the file; only text subtitles are saved as .srt files."

// LanguagesNeeded is the 400 for "my languages" before any language is chosen (design's words).
const LanguagesNeeded = "Choose your languages first, in Settings > General."

// planAudio decides each audio track of info under choice (Selectors.Audio) and the person's languages:
//   - keep_all: every track copied (no ops);
//   - my_languages: tracks in langs, and tracks with no language (we cannot tell), are kept; the rest
//     dropped. If none would be left, every track is kept (allKept true);
//   - aac / eac3 / opus: every track converted to that codec at the automatic bitrate, except one
//     already in that codec, or one whose bitrate is known to be at or below the target (it would not
//     get smaller); a lossless track is always converted. A lossy track of unknown bitrate is copied:
//     it could get bigger;
//   - lossless_only: only lossless tracks converted, to E-AC-3.
func planAudio(choice string, langs []string, info media.Info) (ops []TrackOp, allKept bool) {
	audio := info.Audio()
	switch choice {
	case "my_languages":
		if len(langs) == 0 { // no languages chosen (any more): nothing is dropped
			return nil, false
		}
		want := map[string]bool{}
		for _, l := range langs {
			want[CanonLang(l)] = true
		}
		kept := 0
		for _, a := range audio {
			l := CanonLang(a.Language)
			if l == "" || want[l] {
				kept++
				continue
			}
			ops = append(ops, TrackOp{Index: a.Index, Type: "audio", Drop: true})
		}
		if kept == 0 && len(audio) > 0 {
			return nil, true
		}
		return ops, false
	case "aac", "eac3", "opus":
		t := audioTargets[choice]
		for _, a := range audio {
			kbps := AutoAudioKbps(choice, a.Channels)
			switch {
			case a.Codec == t.codec:
			case a.Lossless():
				ops = append(ops, TrackOp{Index: a.Index, Type: "audio", Encoder: t.encoder, Codec: t.codec, Kbps: kbps})
			case a.BitrateKbps > kbps:
				ops = append(ops, TrackOp{Index: a.Index, Type: "audio", Encoder: t.encoder, Codec: t.codec, Kbps: kbps})
			}
		}
	case "lossless_only":
		t := audioTargets["eac3"]
		for _, a := range audio {
			if a.Lossless() {
				ops = append(ops, TrackOp{Index: a.Index, Type: "audio", Encoder: t.encoder, Codec: t.codec, Kbps: AutoAudioKbps("eac3", a.Channels)})
			}
		}
	}
	return ops, false
}

// SplitLanguages is a template's Languages as a list.
func SplitLanguages(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// planSubtitles decides each subtitle track of info under choice (Selectors.Subtitles):
//   - keep_all: every one copied;
//   - my_languages: those in langs, and those with no language, kept; the rest dropped (forced ones
//     follow their language). With no languages chosen nothing is dropped;
//   - text_only: picture subtitles (PGS, VobSub, DVB) dropped; text ones and unknown kinds kept.
func planSubtitles(choice string, langs []string, info media.Info) (ops []TrackOp) {
	subs := info.Subtitles()
	switch choice {
	case "my_languages":
		if len(langs) == 0 {
			return nil
		}
		want := map[string]bool{}
		for _, l := range langs {
			want[CanonLang(l)] = true
		}
		for _, s := range subs {
			if l := CanonLang(s.Language); l != "" && !want[l] {
				ops = append(ops, TrackOp{Index: s.Index, Type: "subtitle", Drop: true})
			}
		}
	case "text_only":
		for _, s := range subs {
			if s.SubKind == "bitmap" {
				ops = append(ops, TrackOp{Index: s.Index, Type: "subtitle", Drop: true})
			}
		}
	case "srt":
		// text subtitles become name.<lang>[.forced].srt beside the new file; picture ones stay inside
		used := map[string]int{}
		for _, s := range subs {
			if s.SubKind != "text" {
				continue
			}
			var parts []string
			if l := CanonLang(s.Language); l != "" {
				parts = append(parts, l)
			}
			if s.Forced {
				parts = append(parts, "forced")
			}
			name := strings.Join(parts, ".")
			used[name]++
			if n := used[name]; n > 1 {
				name = strings.TrimPrefix(name+"."+strconv.Itoa(n), ".")
			}
			ops = append(ops, TrackOp{Index: s.Index, Type: "subtitle", Drop: true, Extract: name, ToFile: true})
		}
	}
	return ops
}

// SubtitleFiles are the Plan's subtitles saved as .srt files, in the order ffmpeg writes them.
func (p Plan) SubtitleFiles() []TrackOp {
	var out []TrackOp
	for _, o := range p.Tracks {
		if o.ToFile {
			out = append(out, o)
		}
	}
	return out
}

// SrtName is the name of an extracted subtitle beside the new file final ("Movie.mkv" with part "eng":
// "Movie.eng.srt"; with no part: "Movie.srt").
func SrtName(final, part string) string {
	base := strings.TrimSuffix(final, filepath.Ext(final))
	if part == "" {
		return base + ".srt"
	}
	return base + "." + part + ".srt"
}

// StagedSrt is where ffmpeg writes the k-th extracted subtitle: next to the staged new file.
func StagedSrt(out string, k int) string {
	return filepath.Join(filepath.Dir(out), "sub."+strconv.Itoa(k)+".srt")
}
