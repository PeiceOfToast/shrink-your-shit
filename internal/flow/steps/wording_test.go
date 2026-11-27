// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"encoding/json"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// The wording table: every problem code and every greyed choice a step or the container rules can
// put in the flow check, with page 07's exact sentence for it. A row triggers its sentence through
// flow.Validate the way the page does; the sentence is written out here, so a rewording shows up
// as a one-line diff in review instead of drifting silently into the page.
//
// Every sentence in this file comes from page 07. When ui-shop rewords one, the row changes with
// it. Rows may produce extra entries (the starter greys ifGpuFails, for one); a row names only the
// entry it is about.

// wordingCase is one row: the settings that trigger a sentence, the server it is checked against,
// and the exact words expected.
type wordingCase struct {
	name     string
	video    string // settings JSON; empty keeps the starter's
	audio    string
	save     string
	env      flow.Env
	problems []string // "field|code|message"
	disabled []string // "stepId|field|value|why"
}

func TestEverySentenceIsPinned(t *testing.T) {
	const base = `{"format":"hevc","quality":3,"useGpu":false,"ifGpuFails":"cpu"` // closed per row
	video := func(extra string) string { return base + extra + "}" }

	cases := []wordingCase{
		// Video's Advanced settings: each is a not_yet until item 6 or 8 builds it.
		{name: "video size", video: video(`,"size":"1080"`),
			problems: []string{`size|video.not_yet|Choosing the picture size is not built yet; each file keeps its own size.`}},
		{name: "video width", video: video(`,"width":1280`),
			problems: []string{`width|video.not_yet|Choosing the picture width is not built yet; each file keeps its own size.`}},
		{name: "video height", video: video(`,"height":720`),
			problems: []string{`height|video.not_yet|Choosing the picture height is not built yet; each file keeps its own size.`}},
		{name: "video bitrate", video: video(`,"rateControl":"vbr"`),
			disabled: []string{`video|quality||Not used: Advanced sets a bitrate instead.`}},
		{name: "video VBR needs a format that offers it",
			video:    video(`,"format":"av1","rateControl":"vbr"`),
			disabled: []string{`video|rateControl|vbr|Variable bitrate doesn't work with AV1 yet. Pick Constant bitrate or Quality, or another format.`}},
		{name: "video custom with no average",
			video:    video(`,"rateControl":"cbr","bitrateLevel":"custom"`),
			problems: []string{`avgMbps|video.no_bitrate|Type an average bitrate for Custom, or pick Low, Medium or High.`}},
		{name: "video two passes", video: video(`,"twoPass":true`),
			problems: []string{`twoPass|video.not_yet|Two passes are not built yet; the encode makes one pass.`}},
		{name: "video speed", video: video(`,"preset":"slower"`),
			problems: []string{`preset|video.not_yet|Choosing the encoder speed is not built yet; the processor picks its own speed.`}},
		{name: "video frame rate", video: video(`,"fps":30`),
			problems: []string{`fps|video.not_yet|Changing the frame rate is not built yet; each file keeps its frame rate.`}},
		{name: "video colour depth", video: video(`,"bitDepth":10`),
			problems: []string{`bitDepth|video.not_yet|Changing the colour depth is not built yet; each file keeps its own.`}},
		{name: "video HDR", video: video(`,"hdr":"tonemap"`),
			problems: []string{`hdr|video.not_yet|Converting HDR is not built yet; HDR files keep theirs.`}},
		{name: "video Dolby Vision", video: video(`,"keepDolbyVision":true`),
			problems: []string{`keepDolbyVision|video.not_yet|Keeping the Dolby Vision layer is not built yet.`}},
		{name: "video Dolby Vision needs HEVC",
			video:    video(`,"format":"h264","keepDolbyVision":true`),
			disabled: []string{`video|keepDolbyVision||Only with HEVC.`}},

		// The card fields: greyed by the settings or by the server.
		{name: "video no card, card not used", video: video(``),
			disabled: []string{`video|ifGpuFails||The graphics card is not used.`}},
		{name: "video asks for a card none has", video: video(`,"useGpu":true`), env: flow.Env{},
			disabled: []string{`video|useGpu||No worker has a graphics card.`}},
		{name: "video encoder not installed", video: video(`,"format":"h264"`),
			env:      flow.Env{Encoders: []string{"libx265"}},
			disabled: []string{`video|format|h264|No worker can encode H.264: libx264 is not installed.`}},
		{name: "video format is not a choice", video: video(`,"format":"avi"`),
			problems: []string{`settings|setting.invalid|video.convert settings: Format must be HEVC, H.264 or AV1.`}},
		{name: "video quality out of range", video: video(`,"quality":9`),
			problems: []string{`settings|setting.invalid|video.convert settings: Quality must be at most 5.`}},

		// Audio: a number that needs conversion is refused while the sound is copied, beyond
		// copying needs a format, and the subtitles are item 7's second half.
		{name: "audio codec", audio: `{"format":"copy","codec":"opus"}`,
			disabled: []string{`audio|format||Advanced is set: Opus. It replaces Format above.`}},
		{name: "audio channels while copying", audio: `{"format":"copy","channels":"stereo"}`,
			problems: []string{`channels|audio.copy_conflict|Copying the sound keeps each track's channels. Pick a format above to change them.`}},
		{name: "audio bitrate while copying", audio: `{"format":"copy","bitrateKbps":128}`,
			problems: []string{`bitrateKbps|audio.copy_conflict|Copying the sound keeps each track's bitrate. Pick a format above to set one.`}},
		{name: "audio sample rate while copying", audio: `{"format":"copy","sampleRate":48000}`,
			problems: []string{`sampleRate|audio.copy_conflict|Copying the sound keeps each track's sample rate. Pick a format above to change it.`}},
		{name: "audio loudness while copying", audio: `{"format":"copy","loudnorm":true}`,
			problems: []string{`loudnorm|audio.copy_conflict|Evening out loudness needs a format. Pick one above.`}},
		{name: "audio downmix while copying", audio: `{"format":"copy","downmix":"speech"}`,
			problems: []string{`downmix|audio.copy_conflict|Making stereo from surround needs a format. Pick one above.`}},
		{name: "audio downmix needs stereo", audio: `{"format":"aac"}`,
			disabled: []string{`audio|downmix||Making stereo from surround happens when the channels go down to Stereo or Mono; pick one above.`}},
		{name: "audio FLAC has no bitrate", audio: `{"format":"aac","codec":"flac","bitrateKbps":128}`,
			disabled: []string{
				`audio|format||Advanced is set: FLAC. It replaces Format above.`,
				`audio|bitrateKbps||FLAC is lossless, so it has no bitrate to set.`,
			}},
		{name: "audio keep original", audio: `{"format":"copy","keepOriginal":true}`,
			problems: []string{`keepOriginal|audio.not_yet|Keeping the original track as well is not built yet.`}},
		{name: "audio subtitles choice", audio: `{"format":"copy","subtitles":"srt"}`,
			problems: []string{`subtitles|audio.not_yet|Choosing which subtitles to keep is not built yet; every subtitle is kept.`}},
		{name: "audio subtitle languages", audio: `{"format":"copy","subtitleLanguages":["eng"]}`,
			problems: []string{`subtitleLanguages|audio.not_yet|Keeping only some subtitle languages is not built yet; every subtitle is kept.`}},

		// Save's own rules and the file types the flow rules out.
		{name: "save replace", save: `{"container":"mkv","where":"replace"}`,
			problems: []string{`where|save.not_yet|Replacing the original comes in a later update; for now the new file is saved next to it.`},
			disabled: []string{`save|where|replace|Replacing the original comes in a later update; for now the new file is saved next to it.`}},
		{name: "save folder", save: `{"container":"mkv","where":"folder","folder":"/library/Shrunk"}`,
			problems: []string{`where|save.not_yet|Saving to a different folder comes in a later update; for now the new file is saved next to it.`},
			disabled: []string{`save|where|folder|Saving to a different folder comes in a later update; for now the new file is saved next to it.`}},
		{name: "save folder missing", save: `{"container":"mkv","where":"folder"}`,
			problems: []string{
				`where|save.not_yet|Saving to a different folder comes in a later update; for now the new file is saved next to it.`,
				`folder|save.folder_missing|Choose the folder the new file goes in.`,
			}},
		{name: "save suffix emptied", save: `{"container":"keep","where":"next_to","nameSuffix":"///"}`,
			problems: []string{`nameSuffix|save.empty_suffix|The suffix has no characters a file name can keep, so the new file would take the original's name.`}},
		{name: "save file type is not a choice", save: `{"container":"avi","where":"next_to"}`,
			problems: []string{`settings|setting.invalid|file.save settings: File type must be MKV, MP4, WebM or Keep the same.`}},
		{name: "container WebM", save: `{"container":"webm","where":"next_to"}`,
			problems: []string{`container|container_unsupported|WebM needs AV1 video. Pick AV1 in Video to use it. Or pick another file type.`},
			disabled: []string{`save|container|webm|WebM needs AV1 video. Pick AV1 in Video to use it.`}},
		{name: "container WebM with AV1", video: `{"format":"av1"}`, save: `{"container":"webm","where":"next_to"}`,
			problems: []string{`container|container_unsupported|WebM needs Opus audio. Pick Opus in Audio to use it. Or pick another file type.`},
			disabled: []string{`save|container|webm|WebM needs Opus audio. Pick Opus in Audio to use it.`}},
		{name: "container MP4", audio: `{"format":"opus"}`, save: `{"container":"mp4","where":"next_to"}`,
			problems: []string{`container|container_unsupported|MP4 can't hold Opus audio. Or pick another file type.`},
			disabled: []string{`save|container|mp4|MP4 can't hold Opus audio.`}},
	}

	// Every problem code and every greyed sentence the check can carry, so a new one cannot arrive
	// without its row here.
	wantCodes := []string{"video.not_yet", "video.no_bitrate", "audio.not_yet", "audio.copy_conflict",
		"save.not_yet", "save.folder_missing", "save.empty_suffix", "container_unsupported", "setting.invalid"}
	wantWhys := []string{
		"Not used: Advanced sets a bitrate instead.",
		"Variable bitrate doesn't work with AV1 yet. Pick Constant bitrate or Quality, or another format.",
		"Only with HEVC.",
		"The graphics card is not used.",
		"No worker has a graphics card.",
		"No worker can encode H.264: libx264 is not installed.",
		"Advanced is set: Opus. It replaces Format above.",
		"Advanced is set: FLAC. It replaces Format above.",
		"Making stereo from surround happens when the channels go down to Stereo or Mono; pick one above.",
		"FLAC is lossless, so it has no bitrate to set.",
		"Replacing the original comes in a later update; for now the new file is saved next to it.",
		"Saving to a different folder comes in a later update; for now the new file is saved next to it.",
		"WebM needs AV1 video. Pick AV1 in Video to use it.",
		"WebM needs Opus audio. Pick Opus in Audio to use it.",
		"MP4 can't hold Opus audio.",
	}

	seenCodes := map[string]bool{}
	seenWhys := map[string]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := flow.Validate(flowFor(t, c.video, c.audio, c.save), flow.ValidateContext{Env: c.env})
			problems := map[string]bool{}
			for _, p := range rep.Problems {
				problems[p.Field+"|"+p.Code+"|"+p.Message] = true
				seenCodes[p.Code] = true
			}
			for _, want := range c.problems {
				if !problems[want] {
					t.Errorf("problems = %+v\nwant %s", rep.Problems, want)
				}
			}
			disabled := map[string]bool{}
			for _, d := range rep.Disabled {
				disabled[d.StepID+"|"+d.Field+"|"+d.Value+"|"+d.Why] = true
				seenWhys[d.Why] = true
			}
			for _, want := range c.disabled {
				if !disabled[want] {
					t.Errorf("disabled = %+v\nwant %s", rep.Disabled, want)
				}
			}
		})
	}
	for _, code := range wantCodes {
		if !seenCodes[code] {
			t.Errorf("no row pins the sentence for problem code %s", code)
		}
	}
	for _, why := range wantWhys {
		if !seenWhys[why] {
			t.Errorf("no row pins the greyed-choice sentence %q", why)
		}
	}
	for code := range seenCodes {
		if !contains(wantCodes, code) {
			t.Errorf("problem code %s has no entry in wantCodes", code)
		}
	}
	for why := range seenWhys {
		if !contains(wantWhys, why) {
			t.Errorf("greyed-choice sentence %q has no entry in wantWhys", why)
		}
	}
}

// flowFor is the starter flow with the settings JSON given for each step; an empty string keeps
// that step's starter settings.
func flowFor(t *testing.T, video, audio, save string) flow.Flow {
	t.Helper()
	s, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	f := s.Flow
	for i, raw := range []string{video, audio, save} {
		if raw != "" {
			f.Steps[i].Settings = json.RawMessage(raw)
		}
	}
	return f
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
