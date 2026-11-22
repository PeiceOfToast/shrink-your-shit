// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"strconv"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// widthTol is how far the new picture's width may be from the one the plan predicts: an encoder rounds
// the odd width an aspect makes (1920x1080 at 480 lines is 853⅓, made 852 or 854).
const widthTol = 2

// MainVideo is how the plan's main video is judged once the encoder that runs is known: the format the
// plan names at the height it plans, a width within widthTol of the one the plan's shape predicts (and
// even), and the depth that encoder makes (OutputDepth). An exact prediction was wrong whenever an
// encoder or a filter rounded differently (foundation review cause 1: job 48's 852 for a planned 854;
// the 10-bit SDR cases in #498 and #507).
type MainVideo struct {
	Index  int    // the main video among the video streams (Plan.Video.Index)
	Format string // the plan's format: hevc, h264, av1 or vp9
	Height int    // the height it must have, exactly
	Width  int    // the width that height predicts; a picture within widthTol px of it, and even, passes
	Depth  int    // 8 or 10: OutputDepth of the encoder that ran
	// Encoder is the encoder that ran, for the refusal's words ("" is the processor).
	Encoder string
}

// MainVideoRules is the main video's rules for this plan and the encoder that runs: gpu is a graphics
// encoder's ffmpeg name, or "" for the processor. Nil for a copied video (same bytes: the exact
// comparison stands), or when the plan names no main video of the source.
func (p Plan) MainVideoRules(src media.Info, gpu string) *MainVideo {
	if p.Video.Copy {
		return nil
	}
	w, h, _ := p.plannedPicture(src)
	if w <= 0 || h <= 0 {
		return nil
	}
	format := p.Video.Format
	if format == "" { // a Plan from before S6
		format = "hevc"
	}
	return &MainVideo{Index: p.Video.Index, Format: format, Height: h, Width: w, Depth: OutputDepth(p, &src, gpu), Encoder: gpu}
}

// plannedPicture is the main video's planned size: the source's, a ±90° phone video's swapped (ffmpeg
// encodes the upright picture), then scaled to p.Video.Height when the plan scales. scaled says whether
// the height is the plan's rather than the source's.
func (p Plan) plannedPicture(src media.Info) (w, h int, scaled bool) {
	vs := src.ByType("video")
	if p.Video.Index < 0 || p.Video.Index >= len(vs) {
		return 0, 0, false
	}
	v := vs[p.Video.Index]
	w, h = v.Width, v.Height
	if !p.Video.Copy && v.Quarter() {
		w, h = h, w
	}
	if p.Video.Height > 0 {
		w, h, scaled = ScaledWidth(w, h, p.Video.Height), p.Video.Height, true
	}
	return w, h, scaled
}

// GraphicsEncoderIn is the graphics encoder an ffmpeg command runs for the plan's main video, or "" for
// the processor (a software encoder, or a copy). A remote worker reports the command it ran; the server
// judges the uploaded file by the encoder in it.
func GraphicsEncoderIn(args []string, index int) string {
	enc := VideoEncoderIn(args, index)
	if !graphicsEncoders[enc] { // a software encoder (libx265), a copy, or nothing
		return ""
	}
	return enc
}

// KnownVideoEncoder reports whether enc is a video encoder this program's own commands name: one of the
// graphics encoders it builds arguments for, or a software one a format uses. A remote worker's report names
// the encoder its command ran; the server keeps that name, so it takes only the names it would write itself.
func KnownVideoEncoder(enc string) bool {
	if graphicsEncoders[enc] {
		return true
	}
	for _, f := range VideoFormats {
		if f.Encoder == enc {
			return true
		}
	}
	return false
}

// VideoEncoderIn is the encoder an ffmpeg command names for the plan's main video: a graphics encoder, a
// software one (libx265), or "copy"; "" when the command names none.
func VideoEncoderIn(args []string, index int) string {
	key := "-c:v:" + strconv.Itoa(index)
	enc := ""
	for i, a := range args {
		if a == key && i+1 < len(args) {
			enc = args[i+1]
		}
	}
	return enc
}

// compareMainVideo judges the plan's main video by its rules instead of against the original: a refusal
// names the rule that was broken, not "in the original".
func compareMainVideo(m MainVideo, g media.Stream, n int) []string {
	var p []string
	name := fmt.Sprintf("video #%d", n)
	if m.Format != "" && g.Codec != m.Format {
		p = append(p, fmt.Sprintf("%s: the video format is %s, but the plan makes %s", name, g.Codec, m.Format))
	}
	// The height is exact (a picture of another height is another picture); the width has to be even and
	// within widthTol of the shape the height predicts.
	if g.Height != m.Height || g.Width%2 != 0 || abs(g.Width-m.Width) > widthTol {
		shape := fmt.Sprintf("%dx%d, ±%d", m.Width, m.Height, widthTol)
		if g.Width%2 != 0 {
			shape += ", an even width"
		}
		p = append(p, fmt.Sprintf("%s: the picture size changed to %dx%d, but the plan makes it %d lines high with the original's shape (%s)",
			name, g.Width, g.Height, m.Height, shape))
	}
	if m.Depth > 0 && g.BitDepth != m.Depth {
		p = append(p, fmt.Sprintf("%s: the colour depth is %d-bit, but %s makes %d-bit for this file",
			name, g.BitDepth, ranEncoderWords(m.Encoder), m.Depth))
	}
	return p
}

// ranEncoderWords names the encoder that ran, for a refusal: the card's ffmpeg name, or the processor.
func ranEncoderWords(gpu string) string {
	if gpu == "" {
		return "the processor"
	}
	return gpu
}
