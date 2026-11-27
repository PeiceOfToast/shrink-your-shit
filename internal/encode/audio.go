// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// AudioPlan is one input audio stream's place in the output: copied as it is, converted, or
// dropped. A stream the plan does not name is copied.
type AudioPlan struct {
	Index  int
	Action StreamAction
	// Codec is the codec to write when Action is StreamConvert: aac, opus, ac3, eac3 or flac.
	Codec string
	// Channels is the most channels the output may have: 0 keeps the source's, and a source with
	// fewer channels is never grown (L-AUD-9). AC-3 and E-AC-3 hold six at most (L-AUD-6).
	Channels int
	// BitrateKbps is the per-channel rate, or the whole track's when BitratePer is "track". Zero
	// takes the codec's own per-channel rate (L-AUD-7).
	BitrateKbps int
	BitratePer  string
	// SampleRate is the output's rate; 0 keeps the source's.
	SampleRate int
	// Downmix is "speech" or "dpl2" when stereo is made from surround (K4). "" and "plain" are
	// ffmpeg's own mix, and it only applies when the output has fewer channels than the source.
	Downmix string
	// Loudnorm evens this track's loudness when it is converted (L-AUD-15). Loudness is the
	// measurement the engine took first; nil leaves the track's loudness alone even when Loudnorm
	// is set.
	Loudnorm bool
	Loudness *Loudness
	// Default is the output's default flag: nil keeps the source's, true sets it and false clears
	// it, so one track can be the default and the rest not (L-AUD-12).
	Default *bool
}

// Loudness is one track's loudness as ffmpeg's loudnorm measured it: the numbers the second pass
// needs to apply the same target without analysing the track again (L-AUD-15).
type Loudness struct {
	I, TP, LRA, Threshold, Offset float64
}

// The loudness target is page 07's, in EBU R128's numbers: the programmes' -24 LUFS, a -2 dBTP
// ceiling and a 7 LU range.
const (
	LoudI   = -24.0
	LoudTP  = -2.0
	LoudLRA = 7.0

	// MaxAudioKbps caps the automatic per-channel rate (L-AUD-7).
	MaxAudioKbps = 640
)

// audioRates is one audio codec's encoder, its automatic rate per channel and the most channels
// its format holds (0: no limit).
var audioRates = map[string]struct {
	Encoder     string
	Kbps        int
	MaxChannels int
}{
	"aac":  {"aac", 64, 0},
	"opus": {"libopus", 48, 0},
	"ac3":  {"ac3", 96, 6},
	"eac3": {"eac3", 96, 6},
	"flac": {"flac", 0, 0},
}

// AudioEncoder is ffmpeg's encoder for a plan codec (L-AUD-8).
func AudioEncoder(codec string) (string, bool) {
	r, ok := audioRates[codec]
	return r.Encoder, ok
}

// AutoBitrate is page 07's automatic rate: the codec's per-channel number times the output's
// channels, capped at 640 kbps; an unknown channel count counts as stereo (L-AUD-7).
func AutoBitrate(codec string, channels int) int {
	r, ok := audioRates[codec]
	if !ok || r.Kbps == 0 {
		return 0
	}
	if channels <= 0 {
		channels = 2
	}
	kbps := r.Kbps * channels
	if kbps > MaxAudioKbps {
		kbps = MaxAudioKbps
	}
	return kbps
}

// TargetChannels is the output's channel count: the person's choice, but never more than the
// source has (K3, never upmix) and never more than the codec holds (7.1 becomes 5.1 for AC-3 and
// E-AC-3, L-AUD-6). Zero means the count cannot be chosen safely: the source's is unknown, and
// guessing is how an upmix happens.
func TargetChannels(codec string, source, wanted int) int {
	if source <= 0 {
		return 0
	}
	out := wanted
	if out <= 0 || out > source {
		out = source
	}
	if max := audioRates[codec].MaxChannels; max > 0 && out > max {
		out = max
	}
	return out
}

// audioOut is one kept audio stream after the container's own rule is applied: the source track
// and what the output writes.
type audioOut struct {
	track       media.AudioTrack
	action      StreamAction
	codec       string // "" when the track is copied
	channels    int    // 0 when the track is copied or the source's count is unknown
	bitrate     int
	sampleRate  int
	downmix     string
	loudness    *Loudness
	defaultFlag *bool
}

// AudioStream is one audio stream the output keeps, as the result check should find it.
type AudioStream struct {
	Index    int
	Codec    string
	Channels int
	Language string
}

// AudioOutputs is the audio part of the plan after Build's rules: which streams are kept, what
// each becomes and any sentence the log should carry.
func AudioOutputs(plan Plan, info media.Info) ([]AudioStream, []string, error) {
	outs, notes, err := audioOutputs(plan, info)
	if err != nil {
		return nil, nil, err
	}
	streams := make([]AudioStream, 0, len(outs))
	for _, o := range outs {
		s := AudioStream{Index: o.track.Index, Codec: o.track.Codec, Channels: o.track.Channels, Language: o.track.Language}
		if o.action == StreamConvert {
			s.Codec = o.codec
			if o.channels > 0 {
				s.Channels = o.channels
			}
		}
		streams = append(streams, s)
	}
	return streams, notes, nil
}

// audioOutputs resolves the plan's audio streams against the file and the container. Audio is
// never dropped because of the container (L-AUD-10): a codec the file type cannot hold is
// converted to one it can, and the note says so.
func audioOutputs(plan Plan, info media.Info) ([]audioOut, []string, error) {
	named := make(map[int]AudioPlan, len(plan.Audio))
	for _, p := range plan.Audio {
		if !audioExists(info.Audio, p.Index) {
			return nil, nil, fmt.Errorf("%w: audio stream %d is not in the file", ErrInvalidPlan, p.Index)
		}
		if _, dup := named[p.Index]; dup {
			return nil, nil, fmt.Errorf("%w: audio stream %d is named twice", ErrInvalidPlan, p.Index)
		}
		switch p.Action {
		case StreamCopy, StreamDrop:
		case StreamConvert:
			if _, ok := audioRates[p.Codec]; !ok {
				return nil, nil, fmt.Errorf("%w: audio codec %q is not aac, opus, ac3, eac3 or flac", ErrInvalidPlan, p.Codec)
			}
		default:
			return nil, nil, fmt.Errorf("%w: audio action %q on stream %d", ErrInvalidPlan, p.Action, p.Index)
		}
		named[p.Index] = p
	}

	var outs []audioOut
	var notes []string
	for _, t := range info.Audio {
		p := named[t.Index]
		if p.Action == StreamDrop {
			continue
		}
		if p.Action == StreamConvert {
			o := convertOut(p, t)
			if why := audioFits(plan.Container, o.codec); why != "" {
				fallback := fallbackOut(t, plan.Container)
				notes = append(notes, fmt.Sprintf("audio stream #%d (%s) is written as %s: %s", t.Index, o.codec, fallback.codec, why))
				o = fallback
			}
			outs = append(outs, o)
			continue
		}
		if why := audioFits(plan.Container, t.Codec); why != "" {
			o := fallbackOut(t, plan.Container)
			notes = append(notes, fmt.Sprintf("audio stream #%d (%s) is converted to %s: %s", t.Index, t.Codec, o.codec, why))
			outs = append(outs, o)
			continue
		}
		outs = append(outs, audioOut{track: t, action: StreamCopy, defaultFlag: p.Default})
	}
	return outs, notes, nil
}

// convertOut is one converted track: the channel count the plan allows, the bitrate it implies
// and the downmix that applies.
func convertOut(p AudioPlan, t media.AudioTrack) audioOut {
	channels := TargetChannels(p.Codec, t.Channels, p.Channels)
	o := audioOut{
		track: t, action: StreamConvert, codec: p.Codec, channels: channels,
		sampleRate: p.SampleRate, defaultFlag: p.Default,
	}
	o.bitrate = planBitrate(p, channels)
	o.downmix = downmixMode(t.Channels, channels, p.Downmix)
	if p.Loudnorm && p.Loudness != nil && p.Loudness.finite() {
		o.loudness = p.Loudness
	}
	return o
}

// fallbackOut is what a codec the container cannot hold becomes (L-AUD-8): WebM gets Opus, stereo
// at 128 kbps; MP4 gets E-AC-3 with the source's channels and the automatic rate. Neither grows
// the source.
func fallbackOut(t media.AudioTrack, container Container) audioOut {
	o := audioOut{track: t, action: StreamConvert}
	switch container {
	case ContainerWebM:
		o.codec = "opus"
		o.channels = TargetChannels("opus", t.Channels, 2)
		o.bitrate = 128
	case ContainerMP4:
		o.codec = "eac3"
		o.channels = TargetChannels("eac3", t.Channels, 0)
		o.bitrate = AutoBitrate("eac3", o.channels)
	default:
		// Audio fits every other container, so this is unreachable for a valid plan.
		o.action = StreamCopy
	}
	return o
}

// planBitrate is the output's bitrate: the person's number, per channel or for the whole track,
// else the codec's automatic rate. FLAC is lossless and takes none.
func planBitrate(p AudioPlan, channels int) int {
	if p.Codec == "flac" {
		return 0
	}
	if p.BitrateKbps > 0 {
		if p.BitratePer == "track" {
			return p.BitrateKbps
		}
		ch := channels
		if ch <= 0 {
			ch = 2
		}
		return p.BitrateKbps * ch
	}
	return AutoBitrate(p.Codec, channels)
}

// downmixMode is the mix that applies: one of K4's modes only when stereo is made from surround,
// since a mix of a track that already has the right channels is not a mix.
func downmixMode(source, out int, mode string) string {
	if source <= 0 || out <= 0 || out >= source {
		return ""
	}
	switch mode {
	case "speech":
		if source == 6 || source == 8 {
			return "speech"
		}
	case "dpl2":
		if source > 2 {
			return "dpl2"
		}
	}
	return ""
}

// audioArgs is one kept audio stream's part of the command, by its output position. The
// codecs stay on Build's global "-c:a copy" until a track converts, so a flow that copies every
// track writes exactly the command it wrote before audio conversion existed.
func audioArgs(i int, o audioOut) []string {
	if o.action != StreamConvert || o.codec == "" {
		return dispositionArgs(i, o.defaultFlag)
	}
	enc, _ := AudioEncoder(o.codec)
	argv := []string{fmt.Sprintf("-c:a:%d", i), enc}
	if o.bitrate > 0 {
		argv = append(argv, fmt.Sprintf("-b:a:%d", i), strconv.Itoa(o.bitrate)+"k")
	}
	if o.channels > 0 && o.track.Channels > 0 && o.channels != o.track.Channels {
		argv = append(argv, fmt.Sprintf("-ac:a:%d", i), strconv.Itoa(o.channels))
	}
	if o.sampleRate > 0 && o.sampleRate != o.track.SampleRate && o.loudness == nil {
		argv = append(argv, fmt.Sprintf("-ar:a:%d", i), strconv.Itoa(o.sampleRate))
	}
	if filter := audioFilter(o); filter != "" {
		argv = append(argv, fmt.Sprintf("-filter:a:%d", i), filter)
	}
	return append(argv, dispositionArgs(i, o.defaultFlag)...)
}

// dispositionArgs is the default flag: nil leaves the source's, true sets it and false clears it.
func dispositionArgs(i int, d *bool) []string {
	if d == nil {
		return nil
	}
	value := "0"
	if *d {
		value = "default"
	}
	return []string{fmt.Sprintf("-disposition:a:%d", i), value}
}

// audioFilter is the chain a converted track needs: the downmix (K4) and then loudness
// (L-AUD-15), in that order, because loudness is evened on the output's channels.
func audioFilter(o audioOut) string {
	var parts []string
	switch o.downmix {
	case "speech":
		layout := layoutName(o.track.Channels)
		parts = append(parts, "aformat=channel_layouts="+layout, speechPan(o.track.Channels))
	case "dpl2":
		parts = append(parts, "aresample=matrix_encoding=dplii")
	}
	if o.loudness != nil {
		if layout := layoutName(o.channels); layout != "" {
			parts = append(parts, "aformat=channel_layouts="+layout)
		}
		parts = append(parts, loudnormFilter(o.loudness))
		rate := o.sampleRate
		if rate <= 0 {
			rate = o.track.SampleRate
		}
		if rate > 0 {
			// loudnorm works at 192 kHz internally; the track goes back to its own rate.
			parts = append(parts, "aresample="+strconv.Itoa(rate))
		}
	}
	return strings.Join(parts, ",")
}

// speechPan is K4's "clear speech" mix: the centre channel is raised 3 dB and the surround
// channels are folded in below it, for the layouts whose channel names are the standard ones.
func speechPan(channels int) string {
	if channels == 8 {
		return "pan=stereo|FL=FL+1.0*FC+0.707*BL+0.707*SL|FR=FR+1.0*FC+0.707*BR+0.707*SR"
	}
	return "pan=stereo|FL=FL+1.0*FC+0.707*BL|FR=FR+1.0*FC+0.707*BR"
}

// layoutName is ffmpeg's name for a channel count, for aformat (L-AUD-11).
func layoutName(channels int) string {
	switch channels {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return ""
}

// LoudnessMeasureFilter measures a track's loudness without changing it.
const LoudnessMeasureFilter = "loudnorm=I=-24:TP=-2:LRA=7:print_format=json"

// LoudnessArgs is the analysis command for one audio stream: it reads the track and writes nothing
// (L-AUD-15).
func LoudnessArgs(path string, index int) []string {
	return []string{
		"ffmpeg", "-nostdin", "-hide_banner",
		"-i", path,
		"-map", "0:" + strconv.Itoa(index),
		"-af", LoudnessMeasureFilter,
		"-f", "null", "-",
	}
}

// loudnormFilter is the second pass: the measured numbers and the same target, applied linearly.
func loudnormFilter(l *Loudness) string {
	return fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=%g:measured_I=%s:measured_LRA=%s:measured_TP=%s:measured_thresh=%s:offset=%s:linear=true",
		LoudI, LoudTP, LoudLRA, dec(l.I), dec(l.LRA), dec(l.TP), dec(l.Threshold), dec(l.Offset))
}

func dec(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// loudnormReport is the JSON loudnorm prints at the end of the analysis. Every number is a string,
// including "-inf".
type loudnormReport struct {
	InputI       string `json:"input_i"`
	InputTP      string `json:"input_tp"`
	InputLRA     string `json:"input_lra"`
	InputThresh  string `json:"input_thresh"`
	TargetOffset string `json:"target_offset"`
}

// ParseLoudness reads the analysis report out of ffmpeg's stderr tail. A number that is missing or
// not finite (-inf is silence's loudness) is an error: the track is then left as it is.
func ParseLoudness(lines []string) (*Loudness, error) {
	text := strings.Join(lines, "\n")
	start := strings.LastIndex(text, "{")
	if start < 0 {
		return nil, fmt.Errorf("loudnorm printed no report")
	}
	end := strings.Index(text[start:], "}")
	if end < 0 {
		return nil, fmt.Errorf("loudnorm's report has no end")
	}
	var rep loudnormReport
	if err := json.Unmarshal([]byte(text[start:start+end+1]), &rep); err != nil {
		return nil, fmt.Errorf("loudnorm's report is not JSON: %v", err)
	}
	l := &Loudness{}
	for _, f := range []struct {
		raw string
		dst *float64
	}{
		{rep.InputI, &l.I}, {rep.InputTP, &l.TP}, {rep.InputLRA, &l.LRA},
		{rep.InputThresh, &l.Threshold}, {rep.TargetOffset, &l.Offset},
	} {
		v, err := strconv.ParseFloat(strings.TrimSpace(f.raw), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("loudness %q is not a finite number", f.raw)
		}
		*f.dst = v
	}
	return l, nil
}

// finite reports whether every measured number is usable.
func (l *Loudness) finite() bool {
	for _, v := range []float64{l.I, l.TP, l.LRA, l.Threshold, l.Offset} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func audioExists(tracks []media.AudioTrack, index int) bool {
	for _, t := range tracks {
		if t.Index == index {
			return true
		}
	}
	return false
}
