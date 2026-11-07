// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"math"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// StreamExpect says what one stream of the new file must look like. A zero value is not checked,
// except Language, which must match exactly (including "no language"; "und", "undetermined", is the same
// as none: MP4 writes it where MKV writes nothing).
type StreamExpect struct {
	Type     string // video, audio, subtitle, attachment
	Codec    string
	Language string
	Channels int    // audio
	SubKind  string // subtitle: text or bitmap
	Forced   bool   // subtitle
	Width    int    // video
	Height   int    // video
	BitDepth int    // video
	Filename string // attachment
}

// Expect is what the new file must look like before it may replace the original. verify compares the
// output against an Expect and nothing else, so the way an Expect is made can change (later slices
// derive it from a Plan: a different codec, dropped tracks...) without touching the checks.
type Expect struct {
	// Streams, in the order they must appear in the new file.
	Streams  []StreamExpect
	Chapters int
	// DurationSec is the length the new file must have, within DurationTol (a fraction); 0 = unchecked.
	DurationSec float64
	DurationTol float64
	// VideoPackets is how many frames the main video stream must have, within PacketsTol (a fraction);
	// 0 = unknown, unchecked.
	VideoPackets int64
	PacketsTol   float64
	// MinSavings is the fraction of the original's size that must be saved: 0 means "just smaller".
	MinSavings float64
	// MinSSIM is the picture threshold the flow asks for; 0 = the program's -min-ssim.
	MinSSIM float64
	// Container is the file type the new file must be, as ffprobe names it ("matroska,webm"): the
	// original's, or the flow's new type; "" = unchecked (unknown).
	Container string `json:",omitempty"`
}

// DefaultExpect is the expectation for what the worker does today: every stream of the original is
// kept, in order, except data streams (dropped on purpose); the first video stream becomes HEVC at the
// same size and bit depth; everything else is copied unchanged. packets is the source's main video
// frame count, 0 if unknown.
func DefaultExpect(src media.Info, packets int64) Expect {
	e := Expect{
		Container:    src.Container,
		Chapters:     src.Chapters,
		DurationSec:  src.DurationSec,
		DurationTol:  0.02,
		VideoPackets: packets,
		PacketsTol:   0.01,
	}
	firstVideo := true
	for _, s := range src.Streams {
		if s.Type == "data" {
			continue
		}
		x := StreamExpect{Type: s.Type, Codec: s.Codec, Language: s.Language}
		switch s.Type {
		case "video":
			x.Width, x.Height, x.BitDepth = s.Width, s.Height, s.BitDepth
			x.Language = "" // not checked for video (see below)
			if firstVideo {
				x.Codec = "hevc"
				firstVideo = false
			}
		case "audio":
			x.Channels = s.Channels
		case "subtitle":
			x.SubKind, x.Forced = s.SubKind, s.Forced
		case "attachment":
			x.Filename = s.Filename
			x.Language = ""
		}
		e.Streams = append(e.Streams, x)
	}
	return e
}

var checkedTypes = []string{"video", "audio", "subtitle", "attachment"}

func typeCount(streams []StreamExpect, t string) int {
	n := 0
	for _, s := range streams {
		if s.Type == t {
			n++
		}
	}
	return n
}

func ofType(streams []media.Stream, t string) []media.Stream {
	var out []media.Stream
	for _, s := range streams {
		if s.Type == t {
			out = append(out, s)
		}
	}
	return out
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s stream", word)
	}
	return fmt.Sprintf("%d %s streams", n, word)
}

func describe(s StreamExpect, ordinal int) string {
	d := fmt.Sprintf("%s #%d", s.Type, ordinal)
	var bits []string
	if s.Language != "" {
		bits = append(bits, s.Language)
	}
	if s.Channels > 0 {
		bits = append(bits, fmt.Sprintf("%d channels", s.Channels))
	}
	if s.Filename != "" {
		bits = append(bits, s.Filename)
	}
	if len(bits) > 0 {
		d += " (" + strings.Join(bits, ", ") + ")"
	}
	return d
}

// Compare checks a probed output against the expectation and returns every difference in plain words
// (empty = it matches). outPackets is the new file's main video frame count, or -1 if not counted.
func (e Expect) Compare(out media.Info, outPackets int64) []string {
	var problems []string

	if e.Container != "" && out.Container != e.Container {
		problems = append(problems, fmt.Sprintf("the new file's type is %s, not %s as it should be", containerWords(out.Container), containerWords(e.Container)))
	}

	// Streams: counts per type first; positions only where the counts agree.
	for _, t := range checkedTypes {
		want, got := typeCount(e.Streams, t), len(ofType(out.Streams, t))
		if want == got {
			continue
		}
		msg := fmt.Sprintf("the original has %s but the new file has %s", plural(want, t), plural(got, t))
		if got < want {
			var missing []string
			n := 0
			for _, s := range e.Streams {
				if s.Type != t {
					continue
				}
				n++
				if n > got {
					missing = append(missing, describe(s, n))
				}
			}
			msg += " (missing: " + strings.Join(missing, ", ") + ")"
		}
		problems = append(problems, msg)
	}
	for _, t := range checkedTypes {
		var want []StreamExpect
		for _, s := range e.Streams {
			if s.Type == t {
				want = append(want, s)
			}
		}
		got := ofType(out.Streams, t)
		if len(want) != len(got) {
			continue
		}
		for i, w := range want {
			problems = append(problems, compareStream(w, got[i], i+1)...)
		}
	}
	// Anything that is not one of the four known types must not appear from nowhere.
	for _, s := range out.Streams {
		known := false
		for _, t := range checkedTypes {
			known = known || s.Type == t
		}
		if !known && s.Type != "data" {
			problems = append(problems, fmt.Sprintf("the new file has an unexpected %q stream", s.Type))
		}
	}

	if out.Chapters != e.Chapters {
		problems = append(problems, fmt.Sprintf("the original has %d chapters but the new file has %d", e.Chapters, out.Chapters))
	}
	if e.DurationSec > 0 {
		tol := e.DurationTol
		if out.DurationSec <= 0 {
			problems = append(problems, "the new file has no readable duration")
		} else if math.Abs(out.DurationSec-e.DurationSec) > e.DurationSec*tol {
			problems = append(problems, fmt.Sprintf("the new file is %s long but the original is %s (more than %.0f%% apart)",
				Clock(out.DurationSec), Clock(e.DurationSec), tol*100))
		}
	}
	if e.VideoPackets > 0 && outPackets >= 0 {
		if math.Abs(float64(outPackets-e.VideoPackets)) > float64(e.VideoPackets)*e.PacketsTol {
			problems = append(problems, fmt.Sprintf("the new file has %d video frames but the original has %d (more than %.0f%% apart)",
				outPackets, e.VideoPackets, e.PacketsTol*100))
		}
	}
	return problems
}

func compareStream(w StreamExpect, g media.Stream, n int) []string {
	var p []string
	name := describe(w, n)
	differ := func(what, was, now string) {
		p = append(p, fmt.Sprintf("%s: %s is %s in the original but %s in the new file", name, what, was, now))
	}
	if w.Codec != "" && w.Codec != g.Codec {
		differ("the format", w.Codec, g.Codec)
	}
	switch w.Type {
	case "video":
		if w.Width > 0 && (g.Width != w.Width || g.Height != w.Height) {
			differ("the picture size", fmt.Sprintf("%dx%d", w.Width, w.Height), fmt.Sprintf("%dx%d", g.Width, g.Height))
		}
		if w.BitDepth > 0 && g.BitDepth != w.BitDepth {
			differ("the colour depth", fmt.Sprintf("%d-bit", w.BitDepth), fmt.Sprintf("%d-bit", g.BitDepth))
		}
	case "audio":
		if !sameLanguage(w.Language, g.Language) {
			differ("the language", quoteLang(w.Language), quoteLang(g.Language))
		}
		if w.Channels > 0 && g.Channels != w.Channels {
			differ("the channel count", fmt.Sprint(w.Channels), fmt.Sprint(g.Channels))
		}
	case "subtitle":
		if !sameLanguage(w.Language, g.Language) {
			differ("the language", quoteLang(w.Language), quoteLang(g.Language))
		}
		if w.SubKind != "" && w.SubKind != g.SubKind {
			differ("the kind", w.SubKind, g.SubKind)
		}
		if w.Forced != g.Forced {
			differ("the 'forced' flag", fmt.Sprint(w.Forced), fmt.Sprint(g.Forced))
		}
	case "attachment":
		if w.Filename != "" && w.Filename != g.Filename {
			differ("the file name", w.Filename, g.Filename)
		}
	}
	return p
}

func quoteLang(l string) string {
	if l == "" {
		return "not set"
	}
	return "'" + l + "'"
}

// Clock formats seconds as h:mm:ss.
func Clock(sec float64) string {
	t := int(sec + 0.5)
	return fmt.Sprintf("%d:%02d:%02d", t/3600, t%3600/60, t%60)
}

// HumanBytes formats a size for messages (1.5 GB).
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func sameLanguage(a, b string) bool {
	norm := func(s string) string {
		if strings.EqualFold(s, "und") {
			return ""
		}
		return strings.ToLower(s)
	}
	return norm(a) == norm(b)
}

// Containers are the file types a flow can make (fileType), as ffprobe names them.
var Containers = map[string]string{"mkv": "matroska,webm", "mp4": "mov,mp4,m4a,3gp,3g2,mj2"}

func containerWords(name string) string {
	switch {
	case name == "":
		return "unknown"
	case strings.HasPrefix(name, "matroska"):
		return "MKV"
	case strings.Contains(name, "mp4"):
		return "MP4"
	}
	return strings.ToUpper(strings.SplitN(name, ",", 2)[0])
}
