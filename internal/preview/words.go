// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package preview

import (
	"fmt"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The per-track lines of the guide's "Try it on one file" (S1): each says what the file has now and what
// it becomes, in the words of proposal §3. Sample's lines (the library preview) keep their own wording.

// trackVideo says what happens to the picture: "Video: H.264 2160p → HEVC 1080p, same length".
func trackVideo(plan rules.Plan, info media.Info) string {
	src := info.Video()
	if src == nil {
		return videoLine(plan, info)
	}
	from := rules.CodecWords(src.Codec)
	if src.Height > 0 {
		from += fmt.Sprintf(" %dp", src.Height)
	}
	var text string
	if plan.Video.Copy {
		container := "MKV"
		if plan.Container != "" {
			container = strings.ToUpper(plan.Container)
		}
		text = fmt.Sprintf("Video: %s kept as it is; only repackaged as %s", from, container)
	} else {
		to := targetWords(plan.Video.Codec)
		same := "same picture size and length"
		if plan.Video.Height > 0 && plan.Video.Height != src.Height {
			to += fmt.Sprintf(" %dp", plan.Video.Height)
			same = "same length"
		}
		text = fmt.Sprintf("Video: %s → %s, %s", from, to, same)
	}
	if plan.Video.MaxRateKbps > 0 && !plan.Video.IsVP9Target() {
		text += fmt.Sprintf(", at most %d Mbps", plan.Video.MaxRateKbps/1000)
	}
	return text
}

// targetWords is the format an encoder makes, as people know it ("libx265" is HEVC).
func targetWords(encoder string) string {
	switch {
	case strings.Contains(encoder, "265") || strings.Contains(encoder, "hevc"):
		return "HEVC"
	case strings.Contains(encoder, "264") || strings.Contains(encoder, "h264"):
		return "H.264"
	case strings.Contains(encoder, "av1"):
		return "AV1"
	case strings.Contains(encoder, "vp9"):
		return "VP9"
	}
	return encoder
}

// trackAudio says what happens to each audio track: "Audio: English 5.1 kept, stereo AAC added; French and
// Spanish dropped" (the dropped tracks are named together, last).
func trackAudio(plan rules.Plan, info media.Info) string {
	if plan.AllAudioKept {
		return "Audio: " + rules.NoAudioInMyLanguages
	}
	ops := trackOps(plan, "audio")
	var parts, dropped []string
	for i, s := range info.Audio() {
		lang := langWords(s.Language, i+1)
		name := lang
		if l := layoutWords(s.Channels); l != "" {
			name += " " + l
		}
		op, has := ops[s.Index]
		switch {
		case has && op.Drop:
			dropped = append(dropped, lang)
			continue
		case has && op.Encoder != "" && !op.Add:
			parts = append(parts, name+" → "+madeTrackWords(op, s.Channels))
			continue
		}
		parts = append(parts, name+" kept")
		if has && op.Add {
			parts = append(parts, madeTrackWords(op, s.Channels)+" added")
		}
	}
	if len(dropped) > 0 {
		parts = append(parts, joinAnd(dropped)+" dropped")
	}
	if len(parts) == 0 {
		return "Audio: none kept"
	}
	return "Audio: " + strings.Join(parts, ", ")
}

// madeTrackWords is a track the plan makes, by its layout and codec: "stereo AAC". It keeps the source's
// layout when the plan does not downmix.
func madeTrackWords(op rules.TrackOp, from int) string {
	ch := op.Channels
	if ch == 0 {
		ch = from
	}
	codec := audioCodecWords[op.Codec]
	if codec == "" {
		codec = strings.ToUpper(op.Codec)
	}
	return strings.TrimSpace(layoutWords(ch) + " " + codec)
}

var audioCodecWords = map[string]string{"aac": "AAC", "eac3": "E-AC-3", "ac3": "AC-3", "opus": "Opus", "libopus": "Opus"}

// trackSubtitles says what happens to the subtitles, or "" when there are none: "Subtitles: English and
// German kept, 1 picture subtitle kept, French dropped". A subtitle saved as a file is named as such.
func trackSubtitles(plan rules.Plan, info media.Info) string {
	subs := info.Subtitles()
	if len(subs) == 0 {
		return ""
	}
	ops := trackOps(plan, "subtitle")
	var text, files, dropped []string
	pictures := 0
	for i, s := range subs {
		lang := langWords(s.Language, i+1)
		op, has := ops[s.Index]
		switch {
		case has && op.Drop:
			dropped = append(dropped, lang)
		case has && op.ToFile:
			files = append(files, lang+" saved as a separate .srt file")
		case s.IsBitmapSub():
			pictures++
		default:
			text = append(text, lang)
		}
	}
	var parts []string
	if len(text) > 0 {
		parts = append(parts, joinAnd(text)+" kept")
	}
	if pictures > 0 {
		parts = append(parts, tracks(pictures, "picture subtitle"))
	}
	parts = append(parts, files...)
	if len(dropped) > 0 {
		parts = append(parts, joinAnd(dropped)+" dropped")
	}
	return "Subtitles: " + strings.Join(parts, ", ")
}

// trackOps is the plan's operations on the streams of one type, by the stream's index in the file.
func trackOps(plan rules.Plan, typ string) map[int]rules.TrackOp {
	ops := map[int]rules.TrackOp{}
	for _, op := range plan.Tracks {
		if op.Type == typ {
			ops[op.Index] = op
		}
	}
	return ops
}

// layoutWords is a track's channel layout as people say it ("5.1"), or "" when the count is unknown.
func layoutWords(ch int) string {
	switch ch {
	case 0:
		return ""
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return fmt.Sprintf("%d channels", ch)
}

// langWords is a track's language as people say it. An untagged track is named by its place in the file
// ("track 2"); "und" is a tag that says the language is not known.
func langWords(tag string, n int) string {
	switch tag {
	case "":
		return fmt.Sprintf("track %d", n)
	case "und":
		return "unknown language"
	}
	if w, ok := languageWords[tag]; ok {
		return w
	}
	return strings.ToUpper(tag)
}

// languageWords names the common ISO 639-2 codes (both forms where a language has two).
var languageWords = map[string]string{
	"eng": "English", "fre": "French", "fra": "French", "ger": "German", "deu": "German", "spa": "Spanish",
	"ita": "Italian", "por": "Portuguese", "rus": "Russian", "jpn": "Japanese", "kor": "Korean",
	"chi": "Chinese", "zho": "Chinese", "dut": "Dutch", "nld": "Dutch", "pol": "Polish", "swe": "Swedish",
	"nor": "Norwegian", "dan": "Danish", "fin": "Finnish", "hun": "Hungarian", "cze": "Czech", "ces": "Czech",
	"gre": "Greek", "ell": "Greek", "tur": "Turkish", "ara": "Arabic", "heb": "Hebrew", "hin": "Hindi",
	"tha": "Thai", "vie": "Vietnamese",
}

// joinAnd is "a", "a and b" or "a, b and c".
func joinAnd(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
