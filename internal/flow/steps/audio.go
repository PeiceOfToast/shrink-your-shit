// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// AudioSettings is page 07's Audio card: the format, the channels and the languages to keep,
// then Advanced's codec, bitrate, sample rate, downmix, loudness, commentary and default track,
// then the subtitles. Every field's tags are the page's form.
type AudioSettings struct {
	Format       string   `set:"format" label:"Format" choices:"aac=AAC,opus=Opus,copy=Same as the source" default:"copy"`
	Channels     string   `set:"channels" label:"Channels" choices:"stereo=Stereo,source=Same as the source,mono=Mono,5.1=5.1" default:"source" help:"A most, never an upmix: a stereo file stays stereo wherever 5.1 is asked for."`
	AllLanguages bool     `set:"allLanguages" label:"Keep every language" default:"true" help:"Off keeps only the languages below, or your own languages when none are picked."`
	Languages    []string `set:"languages" label:"Keep these languages" widget:"languages" showIf:"allLanguages=false" help:"Other tracks are removed; if none match, the main track stays. Original language keeps the film's own language, whatever it is."`

	Codec        *string `set:"codec" label:"Codec" group:"advanced" choices:"aac=AAC,opus=Opus,ac3=AC-3,eac3=E-AC-3,flac=FLAC,copy=Copy the original" unset:"As picked above" widget:"select"`
	BitrateKbps  *int    `set:"bitrateKbps" label:"Bitrate" group:"advanced" unit:"kb/s" min:"8" max:"1536"`
	BitratePer   string  `set:"bitratePer" label:"Bitrate is for" group:"advanced" choices:"channel=Each channel,track=The whole track" default:"channel" showIf:"bitrateKbps!=null"`
	SampleRate   *int    `set:"sampleRate" label:"Sample rate" group:"advanced" unit:"Hz" min:"8000" max:"192000"`
	Downmix      *string `set:"downmix" label:"Making stereo from surround" group:"advanced" choices:"speech=Clear speech,plain=Plain mix,dpl2=Dolby Pro Logic II" default:"plain" widget:"select"`
	Loudnorm     bool    `set:"loudnorm" label:"Even out loudness" group:"advanced" default:"false"`
	Commentary   string  `set:"commentary" label:"Commentary tracks" group:"advanced" choices:"keep=Keep,drop=Drop" default:"keep"`
	DefaultTrack *string `set:"defaultTrack" label:"Default track" group:"advanced" widget:"select"`
	KeepOriginal bool    `set:"keepOriginal" label:"Keep the original track as well" group:"advanced" default:"false"`

	Subtitles         string   `set:"subtitles" label:"Subtitles" group:"subtitles" choices:"keep_all=Keep all,my_languages=Keep my languages,text_only=Text only,srt=Make SRT files" default:"keep_all" help:"Text only drops picture subtitles, which some TVs can't show. Make SRT files saves them next to the video."`
	SubtitleLanguages []string `set:"subtitleLanguages" label:"Subtitle languages" group:"subtitles" widget:"languages" showIf:"subtitles=my_languages" help:"Empty keeps the same languages as the audio."`
}

func init() { flow.RegisterStep(AudioStep{}) }

// AudioStep chooses which tracks the output keeps and how each one is written.
type AudioStep struct{}

var _ flow.Step = AudioStep{}

// AudioCodec is one of the settings facts the flow's container rules read (flow/containers.go):
// Advanced's codec when it replaces Format, else the Format choice itself.
func (set AudioSettings) AudioCodec() string {
	if set.Codec != nil && *set.Codec != "" {
		return *set.Codec
	}
	return set.Format
}

// Def is the step's own description.
func (AudioStep) Def() flow.Definition {
	return flow.Definition{
		Type:     "audio.tracks",
		Version:  1,
		Name:     "Audio",
		Help:     "Keep every track as it is, or choose the format, the channels and the languages to keep.",
		Settings: AudioSettings{},
		Sections: []flow.Section{
			{ID: "advanced", Label: "Advanced"},
			{ID: "subtitles", Label: "Subtitles", Help: "The flow has three steps, so the subtitles live here."},
		},
		Events: []flow.EventDef{
			{Code: "audio.as_is", Level: events.LevelInfo, Text: "Audio copied as is",
				Fields: []string{"format", "audioTracks", "subtitleTracks"}, Keep: events.KeepLog},
			{Code: "audio.convert", Level: events.LevelInfo, Text: "Converting audio",
				Fields: []string{"format", "audioTracks", "kept", "converted", "dropped", "subtitleTracks"}, Keep: events.KeepLog},
			{Code: "audio.keep_languages", Level: events.LevelInfo, Text: "Keeping some audio tracks",
				Fields: []string{"format", "audioTracks", "kept", "dropped"}, Keep: events.KeepLog},
			{Code: "audio.default_track", Level: events.LevelInfo, Text: "Setting the default audio track",
				Fields: []string{"audioTracks", "defaultTrack"}, Keep: events.KeepLog},
			{Code: "audio.no_audio", Level: events.LevelInfo, Text: "The file has no audio",
				Fields: []string{"audioTracks", "subtitleTracks"}, Keep: events.KeepLog},
			{Code: "audio.subtitles", Level: events.LevelInfo, Text: "Choosing subtitles",
				Fields: []string{"subtitles", "subtitleTracks", "subtitlesKept", "subtitlesDropped", "subtitlesExtracted"}, Keep: events.KeepLog},
		},
	}
}

// The sentences the log carries beside the decision. They are notes, not refusals: the run goes
// on, and each one says why the plan is not exactly what the settings asked for.
const (
	// onlyCommentaryNote is K2's never-silent rule: dropping every track is never the answer.
	onlyCommentaryNote = "Every audio track is commentary, so they are kept; dropping them would leave the file silent."
	// origNotFoundNote is K1: "orig" matches nothing, and the log says so.
	origNotFoundNote = "The original language was not found, so it matches no track."
	// noMatchMainNote and noMatchAllNote are K2: what stays when no track is in the person's
	// languages, by whether the choice wanted one track or every surround track.
	noMatchMainNote     = "None of the audio is in your languages, so the main track is kept."
	noMatchAllNote      = "None of the audio is in your languages, so all audio tracks are kept."
	defaultNotFoundNote = "The default track's language is not among the kept tracks, so each file keeps its own default flag."
	smallKeptNote       = "Small stereo track kept as it is: converting would not make it smaller."
	surroundKeptNote    = "Surround track kept as it is: converting would not make it smaller."
	foldedNote          = "7.1 sound became 5.1 (E-AC-3 and AC-3 hold up to 5.1)."
)

// Plan keeps the tracks the language and commentary choices leave, writes each the way the format
// and channels ask, and names every dropped track so Build does not copy it. It also applies the
// subtitle choice: which subtitle tracks are dropped, and which become their own .srt files. A
// file with no audio is not an error (K2); its subtitles are still planned.
func (AudioStep) Plan(s flow.Settings, in flow.PlanInput, p *encode.Plan) (flow.Decision, error) {
	set := s.(AudioSettings)
	tracks := in.Info.Audio
	subs := planSubtitles(set, in.Info, p)
	if len(tracks) == 0 {
		detail := flow.F{
			"format": set.AudioCodec(), "audioTracks": 0, "subtitleTracks": len(in.Info.Subtitles),
			"subtitles": set.Subtitles, "subtitlesKept": subs.kept,
			"subtitlesDropped": subs.dropped, "subtitlesExtracted": subs.extracted,
		}
		if subs.work() {
			return flow.Decision{
				Work: true, Label: "The file has no audio; " + lowerFirst(subtitleWords(subs, len(in.Info.Subtitles))),
				Reason: "audio.subtitles", Notes: subs.notes, Detail: detail,
			}, nil
		}
		return flow.Decision{Work: false, Label: "The file has no audio", Reason: "audio.no_audio", Detail: detail}, nil
	}

	kept, notes := chooseTracks(set, tracks)
	notes = append(notes, subs.notes...)
	keptSet := make(map[int]bool, len(kept))
	for _, t := range kept {
		keptSet[t.Index] = true
	}

	codec := set.AudioCodec()
	var entries []encode.AudioPlan
	var keptTracks []media.AudioTrack
	converted, dropped := 0, 0
	for _, t := range tracks {
		if !keptSet[t.Index] {
			entries = append(entries, encode.AudioPlan{Index: t.Index, Action: encode.StreamDrop})
			dropped++
			continue
		}
		keptTracks = append(keptTracks, t)
		entry, note, didConvert := trackPlan(set, codec, t)
		if didConvert {
			converted++
		}
		if note != "" {
			notes = append(notes, note)
		}
		entries = append(entries, entry)
	}

	defaultChanged := false
	if set.DefaultTrack != nil && strings.TrimSpace(*set.DefaultTrack) != "" {
		if chosen, ok := chooseDefault(*set.DefaultTrack, keptTracks); ok {
			for i := range entries {
				if entries[i].Action == encode.StreamDrop {
					continue
				}
				flag := entries[i].Index == chosen
				entries[i].Default = &flag
				if t := trackByIndex(keptTracks, entries[i].Index); t != nil && t.Default != flag {
					defaultChanged = true
				}
			}
		} else {
			notes = append(notes, defaultNotFoundNote)
		}
	}

	p.Audio = append(p.Audio, entries...)

	label, reason := audioDecisionLabel(set, len(tracks), len(keptTracks), converted, dropped, defaultChanged, subs)
	return flow.Decision{
		Work: converted > 0 || dropped > 0 || defaultChanged || subs.work(),
		// Reason is the code the log types this decision with; Label is its sentence.
		Reason: reason, Label: label, Notes: notes,
		Detail: flow.F{
			"format": set.AudioCodec(), "audioTracks": len(tracks), "kept": len(keptTracks),
			"converted": converted, "dropped": dropped, "subtitleTracks": len(in.Info.Subtitles),
			"subtitles": set.Subtitles, "subtitlesKept": subs.kept,
			"subtitlesDropped": subs.dropped, "subtitlesExtracted": subs.extracted,
		},
	}, nil
}

// audioDecisionLabel is the decision's sentence and code, from what the plan does. The subtitle
// choice gets its own code when the audio itself is left as it is; beside an audio change the
// audio's own code stays the headline, and the sentence says what happened to the subtitles.
func audioDecisionLabel(set AudioSettings, total, kept, converted, dropped int, defaultChanged bool, subs subtitlePlan) (label, reason string) {
	audioWork := converted > 0 || dropped > 0 || defaultChanged
	if !audioWork && subs.work() {
		return subtitleWords(subs, total), "audio.subtitles"
	}
	label, reason = audioLabel(set, total, kept, converted, dropped, defaultChanged)
	if subs.work() {
		label += "; " + lowerFirst(subtitleWords(subs, total))
	}
	return label, reason
}

// audioLabel is the sentence for the audio choices alone.
func audioLabel(set AudioSettings, total, kept, converted, dropped int, defaultChanged bool) (label, reason string) {
	switch {
	case converted > 0:
		label = fmt.Sprintf("Converting %d of %d audio tracks to %s", converted, total, codecLabel(set.AudioCodec()))
		if dropped > 0 {
			label += "; dropping " + strconv.Itoa(dropped)
		}
		return label, "audio.convert"
	case dropped > 0:
		return fmt.Sprintf("Keeping %d of %d audio tracks", kept, total), "audio.keep_languages"
	case defaultChanged:
		return "Setting the default audio track", "audio.default_track"
	}
	return "Audio copied as is", "audio.as_is"
}

// subtitlePlan is what the subtitle choice did to the file's subtitles.
type subtitlePlan struct {
	kept      int // still inside the new file
	dropped   int
	extracted int
	notes     []string
}

// work reports whether the subtitle choice changes the file at all.
func (s subtitlePlan) work() bool { return s.dropped > 0 || s.extracted > 0 }

// subtitleWords is the sentence for the subtitle choice alone.
func subtitleWords(subs subtitlePlan, total int) string {
	if subs.extracted > 0 {
		if subs.extracted == 1 {
			return "Saving 1 subtitle as an SRT file"
		}
		return fmt.Sprintf("Saving %d subtitles as SRT files", subs.extracted)
	}
	if subs.dropped == 1 {
		return "Dropping 1 of " + strconv.Itoa(total) + " subtitles"
	}
	return fmt.Sprintf("Dropping %d of %d subtitles", subs.dropped, total)
}

// lowerFirst turns the first capital of a sentence into a clause ("Saving" -> "saving").
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// planSubtitles applies page 07's Subtitles choice:
//
//   - keep_all leaves every subtitle where it is (the file type's own conversion still applies).
//   - my_languages drops the subtitles in a language the person does not keep; a subtitle with no
//     language stays, and with no language list nothing is dropped (L-AUD-20).
//   - text_only drops picture subtitles only; a kind this program does not know stays (L-AUD-21).
//   - srt writes the text subtitles in the person's languages (or with no language) to their own
//     files and leaves the others inside the new file (L-AUD-22's trap).
//
// The language list is Subtitle languages, or the audio languages when that is empty (L-AUD-20).
func planSubtitles(set AudioSettings, info media.Info, p *encode.Plan) subtitlePlan {
	var subs subtitlePlan
	subs.kept = len(info.Subtitles)
	switch set.Subtitles {
	case "my_languages":
		codes, active, note := subtitleLanguages(set, info.Audio)
		if note != "" {
			subs.notes = append(subs.notes, note)
		}
		if !active {
			return subs
		}
		for _, s := range info.Subtitles {
			if l := flow.CanonLang(s.Language); l != "" && !codes[l] {
				p.Subtitles = append(p.Subtitles, encode.StreamPlan{Index: s.Index, Action: encode.StreamDrop})
				subs.dropped++
			}
		}
	case "text_only":
		for _, s := range info.Subtitles {
			if s.Kind() == media.SubtitleBitmap {
				p.Subtitles = append(p.Subtitles, encode.StreamPlan{Index: s.Index, Action: encode.StreamDrop})
				subs.dropped++
			}
		}
		if subs.dropped > 0 {
			subs.notes = append(subs.notes, pictureLeftOutNote(subs.dropped))
		}
	case "srt":
		codes, _, note := subtitleLanguages(set, info.Audio)
		if note != "" {
			subs.notes = append(subs.notes, note)
		}
		if codes == nil {
			// With no languages chosen, "yours" is the main audio's language (L-AUD-22).
			if len(info.Audio) > 0 {
				codes = map[string]bool{flow.CanonLang(mainTrack(info.Audio).Language): true}
			} else {
				codes = map[string]bool{}
			}
		}
		used := map[string]int{}
		for _, s := range info.Subtitles {
			l := flow.CanonLang(s.Language)
			if s.Kind() != media.SubtitleText || l != "" && !codes[l] {
				continue
			}
			part := srtPart(l, s, used)
			p.Subtitles = append(p.Subtitles, encode.StreamPlan{Index: s.Index, Action: encode.StreamExtract, Part: part})
			subs.extracted++
		}
	}
	subs.kept -= subs.dropped + subs.extracted
	return subs
}

// srtPart is L-AUD-22's name part: lang[.forced|.sdh][.N], where a repeated name is numbered
// ("eng", "eng.2"), an untagged subtitle has no language part, and forced beats SDH.
func srtPart(lang string, s media.SubtitleTrack, used map[string]int) string {
	parts := []string{}
	if lang != "" {
		parts = append(parts, lang)
	}
	switch {
	case s.Forced:
		parts = append(parts, "forced")
	case isSDH(s):
		parts = append(parts, "sdh")
	}
	name := strings.Join(parts, ".")
	used[name]++
	if n := used[name]; n > 1 {
		// The old app's wording: "eng" then "eng.2"; an untagged "2" rather than ".2".
		name = strings.TrimPrefix(name+"."+strconv.Itoa(n), ".")
	}
	return name
}

// isSDH is L-AUD-23: a subtitle for the hearing impaired by its disposition, or a title that says
// SDH or CC, or names hearing.
func isSDH(s media.SubtitleTrack) bool {
	if s.HearingImpaired {
		return true
	}
	t := strings.ToLower(s.Title)
	for _, w := range strings.FieldsFunc(t, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if w == "sdh" || w == "cc" {
			return true
		}
	}
	return strings.Contains(t, "hearing")
}

// subtitleLanguages is the subtitle language filter: Subtitle languages when set, else the audio
// languages. active is false when neither is set, which keeps every subtitle.
func subtitleLanguages(set AudioSettings, tracks []media.AudioTrack) (codes map[string]bool, active bool, note string) {
	list := set.SubtitleLanguages
	if len(list) == 0 {
		list = set.Languages
	}
	if len(list) == 0 {
		return nil, false, ""
	}
	return wantedLanguages(list, tracks)
}

// pictureLeftOutNote says how many picture subtitles the Text only choice dropped.
func pictureLeftOutNote(n int) string {
	if n == 1 {
		return "1 picture subtitle was left out: you chose text only."
	}
	return fmt.Sprintf("%d picture subtitles were left out: you chose text only.", n)
}

// trackPlan is one kept track's entry: copied when re-encoding would not make it smaller
// (L-AUD-4/5), converted otherwise.
func trackPlan(set AudioSettings, codec string, t media.AudioTrack) (encode.AudioPlan, string, bool) {
	if codec == "copy" {
		return encode.AudioPlan{Index: t.Index}, "", false
	}
	wanted := wantedChannels(set.Channels)
	outChannels := encode.TargetChannels(codec, t.Channels, wanted)
	target := targetBitrate(set, codec, outChannels)
	if keep, note := keepAsIs(set, codec, outChannels, target, t); keep {
		return encode.AudioPlan{Index: t.Index}, note, false
	}
	entry := encode.AudioPlan{
		Index: t.Index, Action: encode.StreamConvert, Codec: codec,
		Channels: wanted, SampleRate: sampleRateChoice(set), Downmix: downmixChoice(set),
		Loudnorm: set.Loudnorm,
	}
	if set.BitrateKbps != nil {
		entry.BitrateKbps = *set.BitrateKbps
		entry.BitratePer = set.BitratePer
	}
	note := ""
	// L-AUD-6: AC-3 and E-AC-3 hold at most 5.1, and the log says the 7.1 track became 5.1. The
	// sentence names those two codecs, so it is written only when it is one of them that folds;
	// another codec reaching six channels is the person's own choice and needs no note.
	if outChannels == 6 && t.Channels > 6 && (codec == "ac3" || codec == "eac3") {
		note = foldedNote
	}
	return entry, note, true
}

// keepAsIs answers whether the track can be copied: the same codec with nothing to change, or a
// small lossy track converting could not beat. A channel, sample-rate or loudness change always
// converts, and so does a lossless source, whose copy would not be smaller.
func keepAsIs(set AudioSettings, codec string, outChannels, targetKbps int, t media.AudioTrack) (bool, string) {
	if t.Channels > 0 && outChannels > 0 && outChannels != t.Channels {
		return false, ""
	}
	if set.SampleRate != nil && t.SampleRate > 0 && *set.SampleRate != t.SampleRate {
		return false, ""
	}
	if set.Loudnorm {
		return false, ""
	}
	if t.Codec == codec {
		return true, ""
	}
	if losslessAudio[t.Codec] {
		return false, ""
	}
	if t.BitRate == 0 || t.BitRate <= int64(targetKbps)*1000 {
		if t.Channels <= 2 {
			return true, smallKeptNote
		}
		return true, surroundKeptNote
	}
	return false, ""
}

// targetBitrate is the rate the conversion would write: the person's number, per channel or for
// the whole track, else the codec's automatic one (L-AUD-7).
func targetBitrate(set AudioSettings, codec string, channels int) int {
	if codec == "flac" {
		return 0
	}
	if set.BitrateKbps != nil && *set.BitrateKbps > 0 {
		if set.BitratePer == "track" {
			return *set.BitrateKbps
		}
		ch := channels
		if ch <= 0 {
			ch = 2
		}
		return *set.BitrateKbps * ch
	}
	return encode.AutoBitrate(codec, channels)
}

// chooseTracks applies the commentary and language choices, in that order: a commentary track is
// dropped before the language filter looks, so it can never be the main track a filter falls back
// to (K5).
func chooseTracks(set AudioSettings, tracks []media.AudioTrack) ([]media.AudioTrack, []string) {
	var notes []string
	work := append([]media.AudioTrack{}, tracks...)
	if set.Commentary == "drop" {
		var rest []media.AudioTrack
		for _, t := range work {
			if !isCommentary(t) {
				rest = append(rest, t)
			}
		}
		if len(rest) > 0 {
			work = rest
		} else {
			notes = append(notes, onlyCommentaryNote)
		}
	}

	codes, active, origNote := wantedLanguages(set.Languages, work)
	if origNote != "" {
		notes = append(notes, origNote)
	}
	if !active {
		return work, notes
	}
	var matched []media.AudioTrack
	for _, t := range work {
		if codes[flow.CanonLang(t.Language)] {
			matched = append(matched, t)
		}
	}
	if len(matched) == 0 {
		if stereoChoice(set.Channels) {
			matched = []media.AudioTrack{mainTrack(work)}
			notes = append(notes, noMatchMainNote)
		} else {
			matched = work
			notes = append(notes, noMatchAllNote)
		}
	}
	return matched, notes
}

// wantedLanguages is the language filter: the canonical codes to keep, whether a filter was asked
// for at all, and the sentence to log when "orig" matched nothing (K1).
func wantedLanguages(list []string, tracks []media.AudioTrack) (map[string]bool, bool, string) {
	if len(list) == 0 {
		return nil, false, ""
	}
	codes := map[string]bool{}
	note := ""
	for _, raw := range list {
		switch c := strings.ToLower(strings.TrimSpace(raw)); c {
		case "":
		case "orig":
			if orig, ok := originalLanguage(tracks); ok {
				codes[orig] = true
			} else {
				note = origNotFoundNote
			}
		case "und":
			codes[""] = true
		default:
			// L-AUD-16: an unknown code means no language, and it must not turn into a match for
			// the untagged tracks the way an explicit "und" does.
			if code := flow.CanonLang(c); code != "" {
				codes[code] = true
			}
		}
	}
	return codes, true, note
}

// originalLanguage is K1's "orig": the first track in file order whose language is set and not
// und. A connected downloader's language would come first, but no downloader is built.
func originalLanguage(tracks []media.AudioTrack) (string, bool) {
	for _, t := range tracks {
		if c := flow.CanonLang(t.Language); c != "" {
			return c, true
		}
	}
	return "", false
}

// stereoChoice is whether the Channels choice asks for one track (stereo or mono) rather than
// every surround track (source or 5.1).
func stereoChoice(channels string) bool {
	return channels == "stereo" || channels == "mono"
}

// mainTrack is K2's track when nothing matches: the default-flagged one, else the first.
func mainTrack(tracks []media.AudioTrack) media.AudioTrack {
	for _, t := range tracks {
		if t.Default {
			return t
		}
	}
	return tracks[0]
}

// chooseDefault is the Default track setting: the first kept track in that language, "und" for a
// track with no language, "orig" for the film's own.
func chooseDefault(raw string, tracks []media.AudioTrack) (int, bool) {
	var want string
	switch low := strings.ToLower(strings.TrimSpace(raw)); low {
	case "orig":
		orig, ok := originalLanguage(tracks)
		if !ok {
			return 0, false
		}
		want = orig
	case "und":
		want = ""
	default:
		// An unknown language names no track: the page's list is the only source of these codes.
		if want = flow.CanonLang(low); want == "" {
			return 0, false
		}
	}
	for _, t := range tracks {
		if flow.CanonLang(t.Language) == want {
			return t.Index, true
		}
	}
	return 0, false
}

// isCommentary is K5's rule: the comment disposition, or a title that says commentary in any
// case. "Comment" alone is not one, while "Commentaries" is.
func isCommentary(t media.AudioTrack) bool {
	if t.Comment {
		return true
	}
	return strings.Contains(strings.ToLower(t.Title), "commentar")
}

// wantedChannels is the Channels choice as a number; 0 keeps the source's.
func wantedChannels(channels string) int {
	switch channels {
	case "stereo":
		return 2
	case "mono":
		return 1
	case "5.1":
		return 6
	}
	return 0
}

func sampleRateChoice(set AudioSettings) int {
	if set.SampleRate != nil {
		return *set.SampleRate
	}
	return 0
}

func downmixChoice(set AudioSettings) string {
	if set.Downmix != nil {
		return *set.Downmix
	}
	return ""
}

func trackByIndex(tracks []media.AudioTrack, index int) *media.AudioTrack {
	for i := range tracks {
		if tracks[i].Index == index {
			return &tracks[i]
		}
	}
	return nil
}

// losslessAudio is the codecs whose copy is already the smallest form: converting them to a lossy
// format saves space but loses what the track is for, and L-AUD-5 converts them.
var losslessAudio = map[string]bool{
	"flac": true, "truehd": true, "mlp": true, "alac": true, "wavpack": true, "tta": true,
	"pcm_s16le": true, "pcm_s24le": true, "pcm_s32le": true, "pcm_f32le": true,
}

// Validate refuses what the engine cannot do yet and what the settings contradict, with a problem
// on each field, so page 07 marks them.
func (AudioStep) Validate(s flow.Settings, vc flow.ValidateContext) flow.Check {
	set := s.(AudioSettings)
	var c flow.Check
	c.Problems = append(c.Problems, flow.TagProblems(AudioStep{}.Def(), set)...)
	c.Disabled = append(c.Disabled, audioDisabled(set)...)
	c.Disabled = append(c.Disabled, audioInert(set)...)
	c.Problems = append(c.Problems, audioNotYet(set)...)
	c.Problems = append(c.Problems, audioCopyConflicts(set)...)
	c.Summary = audioSummary(set)
	return c
}

// audioDisabled is the greying the settings mean on their own: a codec in Advanced replaces the
// Format choice above it.
func audioDisabled(set AudioSettings) []flow.Disabled {
	if set.Codec == nil || *set.Codec == "" || *set.Codec == "copy" {
		return nil
	}
	return []flow.Disabled{{
		Field: "format",
		Why:   "Advanced is set: " + codecLabel(*set.Codec) + ". It replaces Format above.",
	}}
}

// audioInert greys the Advanced fields the current choices cannot use: a surroundings-to-stereo
// mix needs stereo as the target, and FLAC has no bitrate.
func audioInert(set AudioSettings) []flow.Disabled {
	var out []flow.Disabled
	if set.AudioCodec() != "copy" && !stereoChoice(set.Channels) {
		out = append(out, flow.Disabled{
			Field: "downmix",
			Why:   "Making stereo from surround happens when the channels go down to Stereo or Mono; pick one above.",
		})
	}
	if set.AudioCodec() == "flac" && set.BitrateKbps != nil {
		out = append(out, flow.Disabled{
			Field: "bitrateKbps",
			Why:   "FLAC is lossless, so it has no bitrate to set.",
		})
	}
	return out
}

// audioNotYet is every setting the engine cannot act on yet. Keeping the original track as well
// is the one left; the subtitles are built.
func audioNotYet(set AudioSettings) []flow.Problem {
	var out []flow.Problem
	add := func(field, message string) {
		out = append(out, flow.Problem{Field: field, Code: "audio.not_yet", Message: message})
	}
	if set.KeepOriginal {
		add("keepOriginal", "Keeping the original track as well is not built yet.") // item 7's second half
	}
	return out
}

// audioCopyConflicts refuses a number that needs conversion while the sound is copied: copying
// keeps each track exactly as the file has it, so the page must pick a format first.
func audioCopyConflicts(set AudioSettings) []flow.Problem {
	if set.AudioCodec() != "copy" {
		return nil
	}
	var out []flow.Problem
	add := func(field, message string) {
		out = append(out, flow.Problem{Field: field, Code: "audio.copy_conflict", Message: message})
	}
	if set.Channels != "" && set.Channels != "source" {
		add("channels", "Copying the sound keeps each track's channels. Pick a format above to change them.")
	}
	if set.BitrateKbps != nil {
		add("bitrateKbps", "Copying the sound keeps each track's bitrate. Pick a format above to set one.")
	}
	if set.SampleRate != nil {
		add("sampleRate", "Copying the sound keeps each track's sample rate. Pick a format above to change it.")
	}
	if set.Loudnorm {
		add("loudnorm", "Evening out loudness needs a format. Pick one above.")
	}
	if set.Downmix != nil && *set.Downmix != "" && *set.Downmix != "plain" {
		add("downmix", "Making stereo from surround needs a format. Pick one above.")
	}
	return out
}

// audioSummary is the closed card's line in page 07's words: the codec, the channels, the
// languages to keep and the subtitles choice.
func audioSummary(set AudioSettings) string {
	langs := "my languages"
	if len(set.Languages) > 0 {
		langs = languageNames(set.Languages)
	}
	subLangs := langs
	if len(set.SubtitleLanguages) > 0 {
		subLangs = languageNames(set.SubtitleLanguages)
	}
	subtitles := "subtitles: keep all"
	switch set.Subtitles {
	case "srt":
		subtitles = "SRT files"
	case "text_only":
		subtitles = "text only"
	case "my_languages":
		subtitles = "subtitles: " + subLangs
	}
	return codecLabel(set.AudioCodec()) + " · " + channelsLabel(set.Channels) + " · " + langs + " · " + subtitles
}

// languageNames joins the names of the codes the way page 07 writes them, "English and German".
func languageNames(codes []string) string {
	names := make([]string, 0, len(codes))
	for _, code := range codes {
		names = append(names, languageName(code))
	}
	return strings.Join(names, " and ")
}

// languageName is the name a person reads for a code; the two special choices have their own
// words, and an unknown code is left as typed.
func languageName(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	switch c {
	case "orig":
		return "Original language"
	case "und":
		return "Unknown"
	}
	for _, l := range flow.Languages() {
		if l.Code == c {
			return l.Name
		}
	}
	return strings.TrimSpace(code)
}

// codecLabel is how the pages write an audio codec.
func codecLabel(codec string) string {
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
	case "copy":
		return "Same as the source"
	}
	return codec
}

// channelsLabel is how the pages write a Channels choice.
func channelsLabel(channels string) string {
	switch channels {
	case "stereo":
		return "stereo"
	case "mono":
		return "mono"
	case "5.1":
		return "5.1"
	}
	return "same channels as the source"
}
