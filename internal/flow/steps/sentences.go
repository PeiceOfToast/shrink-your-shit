// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// Sentences are the three lines a workflow's panel shows (page 09): what it converts, what it leaves alone, and what
// happens to the original. Every word comes from a setting the flow has. The leave-alone line follows the same
// conditions the video step's Plan uses, so a file the line says is left alone gets nothing to do from the run.
type Sentences struct {
	Converts    string `json:"converts"`
	LeavesAlone string `json:"leavesAlone"`
	Original    string `json:"original"`
}

// WorkflowSentences reads the flow's steps and writes the three lines. keepDays is processing.keepOriginalsDays, read
// by the caller at request time; a negative value means it did not read. A step the flow does not register is an error.
func WorkflowSentences(f flow.Flow, keepDays int) (Sentences, error) {
	var video *VideoSettings
	var audio *AudioSettings
	var save *SaveSettings
	for _, ref := range f.Steps {
		_, settings, err := flow.DecodeStep(ref)
		if err != nil {
			return Sentences{}, err
		}
		switch s := settings.(type) {
		case VideoSettings:
			video = &s
		case AudioSettings:
			audio = &s
		case SaveSettings:
			save = &s
		}
	}
	var out Sentences
	out.Converts = convertsSentence(video, audio)
	out.LeavesAlone = leavesAloneSentence(video)
	out.Original = originalSentence(save, keepDays)
	return out, nil
}

// convertsSentence: the video's format, quality and card, then the audio's codec, as the run plans them.
func convertsSentence(video *VideoSettings, audio *AudioSettings) string {
	var parts []string
	if video != nil {
		parts = append(parts, fmt.Sprintf("Converts video to %s at the %s setting, %s.",
			codecName(encode.Codec(video.Format)), sliderLabel(video.Quality), cardPhrase(*video)))
	}
	if audio != nil {
		switch audio.AudioCodec() {
		case "copy", "":
			parts = append(parts, "Keeps the audio as it is.")
		default:
			// The exceptions are the audio step's keep rule (keepAsIs): a track already in this codec stays, and so does a
			// lossy track already at or below the target bitrate. A channel layout, a sample rate or loudness evening
			// forces the conversion (keepAsIs returns no keep for them), so the clause is left out then.
			codec := audioCodecLabel(audio.AudioCodec())
			switch {
			case audio.Loudnorm:
				// Loudness evening converts every track, so the keep clause does not apply.
				parts = append(parts, fmt.Sprintf("Converts audio to %s.", codec))
			case audio.SampleRate != nil || (audio.Channels != "" && audio.Channels != "source"):
				// A channel layout or a sample rate converts a track only when it differs from the track's own.
				parts = append(parts, fmt.Sprintf("Converts audio to %s, except tracks already in %s and lossy tracks already at or below the target bitrate; a channel layout or sample rate that differs from a track converts that track.", codec, codec))
			default:
				parts = append(parts, fmt.Sprintf("Converts audio to %s, except tracks already in %s and lossy tracks already at or below the target bitrate.", codec, codec))
			}
		}
	}
	if len(parts) == 0 {
		return "Converts nothing."
	}
	return strings.Join(parts, " ")
}

// cardPhrase says where the video is encoded and what happens when the card cannot do it (the run's IfGpuFails).
func cardPhrase(v VideoSettings) string {
	if !v.UseGpu {
		return "on the processor"
	}
	if v.IfGpuFails == "stop" {
		return "on the graphics card, and it stops if the card can't do the file"
	}
	return "on the graphics card, or the processor if the card can't do the file"
}

// leavesAloneSentence: the two skips the video step's Plan makes. A file already in the chosen format has nothing to
// change; an HDR file is left as it is when HDR is set to skip.
func leavesAloneSentence(video *VideoSettings) string {
	if video == nil {
		return "Leaves no files alone."
	}
	var parts []string
	// The claim is the video's only: the audio and the rest of the flow are planned on their own, so a file can still be
	// converted for them.
	parts = append(parts, fmt.Sprintf("Leaves the video alone when it is already in %s.",
		codecName(encode.Codec(video.Format))))
	if video.HDR != nil && *video.HDR == "skip" {
		parts = append(parts, "Leaves the video in HDR files alone.")
	}
	return strings.Join(parts, " ")
}

// originalSentence follows the Save mode, and the trash days are the setting the run reads, not a constant. A keepDays
// below zero means the setting did not read: the replace line is then left empty, never given a number.
func originalSentence(save *SaveSettings, keepDays int) string {
	if save == nil {
		return ""
	}
	switch save.Where {
	case "replace":
		if keepDays < 0 {
			return ""
		}
		return fmt.Sprintf("The original goes to the trash for %s.", dayCount(keepDays))
	case "folder":
		// The folder mode is not built yet; the step's own note says so, and the line repeats it.
		// The tag is "folder=<sentence>"; the sentence is what the form shows.
		return strings.TrimPrefix(fieldTag(reflect.TypeOf(SaveSettings{}), "Where", "notYet"), "folder=")
	}
	return "The original stays next to the new file."
}

func dayCount(n int) string {
	if n == 1 {
		return "1 day"
	}
	return strconv.Itoa(n) + " days"
}

// sliderLabel is the quality slider's own word for a level (video.go's sliderLabels tag).
func sliderLabel(level int) string {
	labels := strings.Split(fieldTag(reflect.TypeOf(VideoSettings{}), "Quality", "sliderLabels"), ",")
	i := level - 1
	if i < 0 {
		i = 0
	}
	if i >= len(labels) {
		i = len(labels) - 1
	}
	return labels[i]
}

// audioCodecLabel is the audio codec's name in the choices the step offers.
func audioCodecLabel(codec string) string {
	switch codec {
	case "aac":
		return "AAC"
	case "opus":
		return "Opus"
	case "ac3":
		return "AC-3"
	case "eac3":
		return "E-AC-3"
	case "flac":
		return "FLAC"
	}
	return codec
}

// fieldTag reads one struct tag of a field; empty when the field or tag is not there.
func fieldTag(t reflect.Type, field, tag string) string {
	f, ok := t.FieldByName(field)
	if !ok {
		return ""
	}
	return f.Tag.Get(tag)
}
