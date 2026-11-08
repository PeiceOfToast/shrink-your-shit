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
	Index    int    `json:"index"`              // the stream's index in the original (ffprobe's)
	Type     string `json:"type"`               // audio | subtitle
	Drop     bool   `json:"drop,omitempty"`     // left out of the new file
	Encoder  string `json:"encoder,omitempty"`  // converted with this ffmpeg encoder (aac, eac3, libopus)
	Codec    string `json:"codec,omitempty"`    // the codec the new track has then, as ffprobe names it
	Kbps     int    `json:"kbps,omitempty"`     // its bitrate
	Channels int    `json:"channels,omitempty"` // downmixed to this many channels (S4's stereo AAC: 2); 0 = as it is
	// Extract: a subtitle saved as a separate .srt file beside the new file (and left out of it); the
	// name part between the new file's name and ".srt": "eng", "eng.forced", "" (no language), "eng.2".
	Extract string `json:"extract,omitempty"`
	ToFile  bool   `json:"toFile,omitempty"` // it is saved as an .srt file (Extract may be "")
	// Add: a new track made from this one (S6's "Stereo AAC, plus surround"), placed after the kept
	// audio tracks; the original track is kept as it is.
	Add      bool   `json:"add,omitempty"`
	Language string `json:"language,omitempty"` // an added track's language (its source's)
}

// audioTargets are the codecs the audio choices convert to: ffmpeg encoder, codec name, kbps per channel.
var audioTargets = map[string]struct {
	encoder, codec string
	perChannel     int
}{
	"aac":  {"aac", "aac", 64},
	"opus": {"libopus", "opus", 48},
	"eac3": {"eac3", "eac3", 96},
	"ac3":  {"ac3", "ac3", 96},
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

// StandardAudio is a saved audio value in S6's terms: the choice and "Only my languages". Values from
// before S6 (25b's, and Basic's my_languages and keep_all) map as design's E says; changed reports a
// value that was moved, so its flow says so once (notes).
func StandardAudio(a string, only bool) (choice string, onlyMine bool, changed bool) {
	switch a {
	case "keep_all":
		return "as_is", false, false
	case "my_languages":
		return "as_is", true, false
	case "aac":
		return "stereo_aac", false, true // it converted every track: none is dropped now either
	case "opus":
		return "stereo_opus", false, true
	case "eac3", "lossless_only":
		return "eac3_surround", false, true
	}
	return a, only, false
}

// AudioWords are the choices' names, for the notes and the flow's tag.
var AudioWords = map[string]string{"stereo_aac": "Stereo AAC", "stereo_aac_surround": "Stereo AAC, plus surround as it is",
	"eac3_surround": "Surround E-AC-3", "ac3_surround": "Surround AC-3", "stereo_opus": "Stereo Opus", "as_is": "As it is"}

// FoldedNote is said (once, in the job's log) when a 7.1 track became 5.1 E-AC-3 or AC-3 (design, 01:52Z).
const FoldedNote = "7.1 sound became 5.1 (E-AC-3 and AC-3 hold up to 5.1)."

// SmallKeptNote is said (once, in the job's log) when a small stereo track is kept as it is instead of
// made stereo AAC/Opus: converting would not make it smaller (design, 02:26Z, REDESIGN A1).
const SmallKeptNote = "Small stereo track kept as it is: converting would not make it smaller."

// mp4Audio are the audio codecs an MP4 file can hold; any other kept track is made E-AC-3 there.
var mp4Audio = map[string]bool{"aac": true, "ac3": true, "eac3": true, "mp3": true, "alac": true, "opus": true, "flac": true}

// planAudio decides each audio track of info (design E, S6): which tracks are kept (onlyMine: those in
// langs and those with no language), then what each choice does to a kept track:
//   - stereo_aac / stereo_opus: 2-channel AAC (160 kbps, S4's rule) / Opus (128 kbps); a track already
//     that codec with at most 2 channels is copied;
//   - stereo_aac_surround: a track with 3 or more channels is copied, and a stereo AAC made from it is
//     added next to it; the others as stereo_aac;
//   - eac3_surround / ac3_surround: a lossless or DTS track with 3 or more channels becomes E-AC-3 /
//     AC-3 (at most 5.1: ffmpeg's encoders make no more); AC-3/E-AC-3 surround and tracks of 2
//     channels or fewer are copied; a lossy track is never converted when it would not get smaller;
//   - as_is: every kept track copied.
//
// With no track in langs, the stereo choices keep the main track (mainUsed); the others keep every
// track (allKept). A track the new file's container (mp4, webm; "" any other) cannot hold is never kept
// as it is: it becomes the chosen stereo track, or else E-AC-3 in MP4 (TrueHD, DTS, PCM) and Opus in
// WebM (QA 894cb18).
func planAudio(choice string, onlyMine bool, langs []string, info media.Info, container string) (ops []TrackOp, allKept, mainUsed, smallKept bool) {
	holds := func(codec string) bool {
		switch container {
		case "mp4":
			return mp4Audio[codec]
		case "webm":
			return codec == "opus" || codec == "vorbis"
		}
		return true
	}
	audio := info.Audio()
	stereo := choice == "stereo_aac" || choice == "stereo_opus" || choice == "stereo_aac_surround"
	keep := map[int]bool{}
	for _, a := range audio {
		keep[a.Index] = true
	}
	if onlyMine && len(langs) > 0 && len(audio) > 0 {
		want := map[string]bool{}
		for _, l := range langs {
			want[CanonLang(l)] = true
		}
		mine := map[int]bool{}
		for _, a := range audio {
			if l := CanonLang(a.Language); l == "" || want[l] {
				mine[a.Index] = true
			}
		}
		switch {
		case len(mine) > 0:
			keep = mine
		case stereo: // the main track (default disposition, else the first)
			main := audio[0]
			for _, a := range audio {
				if a.Default {
					main = a
					break
				}
			}
			keep, mainUsed = map[int]bool{main.Index: true}, true
		default:
			allKept = true
		}
	}
	stereoOp := func(a media.Stream, codec, encoder string, kbps int) (TrackOp, bool) {
		if a.Codec == codec && a.Channels > 0 && a.Channels <= 2 {
			return TrackOp{}, false // already stereo in that codec: copied
		}
		// Never bigger (25b, review 6cafc6f): a lossy stereo track at or below the target's bitrate, or of
		// unknown bitrate, is copied. Surround is still made stereo: a downmix to 2 channels at 160 kbps
		// is smaller than any lossy surround track.
		if !a.Lossless() && a.Channels > 0 && a.Channels <= 2 && (a.BitrateKbps == 0 || a.BitrateKbps <= kbps) && holds(a.Codec) {
			smallKept = true
			return TrackOp{}, false
		}
		return TrackOp{Index: a.Index, Type: "audio", Encoder: encoder, Codec: codec, Kbps: kbps, Channels: 2}, true
	}
	var added []TrackOp
	for _, a := range audio {
		if !keep[a.Index] {
			ops = append(ops, TrackOp{Index: a.Index, Type: "audio", Drop: true})
			continue
		}
		var op TrackOp
		var has bool
		switch choice {
		case "stereo_aac":
			op, has = stereoOp(a, "aac", "aac", stereoAACKbps)
		case "stereo_opus":
			op, has = stereoOp(a, "opus", "libopus", stereoOpusKbps)
		case "stereo_aac_surround":
			if a.Channels >= 3 {
				added = append(added, TrackOp{Index: a.Index, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: stereoAACKbps, Channels: 2, Add: true, Language: a.Language})
			} else {
				op, has = stereoOp(a, "aac", "aac", stereoAACKbps)
			}
		case "eac3_surround", "ac3_surround":
			t := strings.TrimSuffix(choice, "_surround")
			if a.Channels >= 3 && (a.Lossless() || a.Codec == "dts") && a.Codec != "ac3" && a.Codec != "eac3" {
				ch := min(a.Channels, 6)
				kbps := AutoAudioKbps(t, ch)
				if a.Lossless() || a.BitrateKbps > kbps { // never bigger, except lossless; unknown lossy (DTS) is copied
					op, has = TrackOp{Index: a.Index, Type: "audio", Encoder: t, Codec: t, Kbps: kbps, Channels: chIfFolded(a.Channels)}, true
				}
			}
		}
		if !has && !holds(a.Codec) { // the container can't hold it as it is
			switch {
			case container == "webm": // WebM's audio is Stereo Opus (Validate)
				op, has = TrackOp{Index: a.Index, Type: "audio", Encoder: "libopus", Codec: "opus", Kbps: stereoOpusKbps, Channels: 2}, true
			default: // MP4: E-AC-3 (the log says so)
				ch := min(max(a.Channels, 2), 6)
				op, has = TrackOp{Index: a.Index, Type: "audio", Encoder: "eac3", Codec: "eac3", Kbps: AutoAudioKbps("eac3", ch), Channels: chIfFolded(a.Channels)}, true
			}
		}
		if has {
			ops = append(ops, op)
		}
	}
	return append(ops, added...), allKept, mainUsed, smallKept
}

// chIfFolded is the channels to ask for: 6 when a track has more than 5.1 (folded), else as it is.
func chIfFolded(n int) int {
	if n > 6 {
		return 6
	}
	return 0
}

// BasicAudio is a Basic flow's audio choice, its default for an empty one (S4).
func BasicAudio(a string) string {
	if a == "" {
		return "stereo_aac"
	}
	return a
}

// AvailableBasicAudio are Basic's audio choices (S4, DESIGN/REDESIGN A1).
var AvailableBasicAudio = []string{"stereo_aac", "my_languages", "keep_all"}

// Basic's stereo AAC (S4, RESEARCH/audio.md): AAC-LC stereo at 160 kbps plays on every TV and phone.
const (
	stereoAACKbps  = 160
	stereoOpusKbps = 128
	// NoAudioInMyLanguageMain is said (in the job's log) when no track is in the person's languages:
	// the main one is kept, as stereo AAC (design's words).
	NoAudioInMyLanguageMain = "None of the audio is in your language, so the main audio track was used."
)

// planStereoAAC keeps the tracks in langs and those with no language (all of them when no language is
// chosen, so none is dropped unasked), each as stereo AAC: one already AAC with at most 2 channels is
// copied as it is (never re-encoded); every other becomes AAC, 2 channels, 160 kbps (a 5.1 track is
// downmixed; stereo is never made into more). With no track in langs, the main one (default
// disposition, else the first) is kept, and mainUsed says so.
func planStereoAAC(langs []string, audio []media.Stream) (ops []TrackOp, mainUsed bool) {
	keep := map[int]bool{}
	if len(langs) == 0 {
		for _, a := range audio {
			keep[a.Index] = true
		}
	} else {
		want := map[string]bool{}
		for _, l := range langs {
			want[CanonLang(l)] = true
		}
		for _, a := range audio {
			if l := CanonLang(a.Language); l == "" || want[l] {
				keep[a.Index] = true
			}
		}
		if len(keep) == 0 && len(audio) > 0 {
			main := audio[0]
			for _, a := range audio {
				if a.Default {
					main = a
					break
				}
			}
			keep[main.Index], mainUsed = true, true
		}
	}
	for _, a := range audio {
		switch {
		case !keep[a.Index]:
			ops = append(ops, TrackOp{Index: a.Index, Type: "audio", Drop: true})
		case a.Codec == "aac" && a.Channels > 0 && a.Channels <= 2: // already stereo AAC: copied
		default:
			ops = append(ops, TrackOp{Index: a.Index, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: stereoAACKbps, Channels: 2})
		}
	}
	return ops, mainUsed
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
