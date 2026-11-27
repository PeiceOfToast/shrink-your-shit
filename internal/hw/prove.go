// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package hw finds the graphics encoders this machine can really use. An encoder counts as available only when a
// real test encode through it writes a file whose ENCODER tag names that encoder (L-HW-1). Detection runs in the
// background, and the last answer is served while a new pass runs (L-HW-3).
package hw

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// TestTimeout bounds one test encode (L-HW-1). A variable so a test can shorten it.
var TestTimeout = 15 * time.Second

// Candidate is one encoder to prove: its ffmpeg name, its family, its codec, and for VAAPI the render node it uses.
type Candidate struct {
	Encoder string
	Family  encode.Family
	Codec   string
	Device  string
}

// Verdict is the answer for one candidate. Reason is plain words, set only when the encoder is not available.
type Verdict struct {
	Candidate
	Available bool
	Reason    string
}

// Inventory is one pass's answer: every candidate, in the order they were tested.
type Inventory struct {
	At       time.Time
	Verdicts []Verdict
}

// Prove runs one real test encode through c and reports whether the result counts. The output is the test's own
// file: it is removed whatever the answer, since a test keeps nothing (L-HW-1).
func Prove(ctx context.Context, m media.Media, dir string, c Candidate) Verdict {
	if c.Family == encode.FamilyVAAPI {
		if _, err := os.Stat(c.Device); err != nil {
			return no(c, "no render node "+c.Device)
		}
	}
	out := filepath.Join(dir, c.Encoder+".test.mkv")
	defer os.Remove(out)

	tctx, cancel := context.WithTimeout(ctx, TestTimeout)
	defer cancel()
	_, err := m.Run(tctx, append([]string{"ffmpeg"}, testArgs(c, out)...), nil)
	if err != nil {
		if ctx.Err() != nil {
			// The whole pass was cancelled: this is not the encode's own limit, and the caller drops the answer.
			return no(c, "the test was cancelled")
		}
		if errors.Is(tctx.Err(), context.DeadlineExceeded) {
			return no(c, fmt.Sprintf("the test encode did not finish in %d s", int(TestTimeout.Seconds())))
		}
		return no(c, plainReason(c, err))
	}
	info, err := m.Probe(ctx, out)
	if err != nil {
		return no(c, "the test output could not be read")
	}
	movie, ok := movieTrack(info)
	if !ok {
		return no(c, "the test output has no video stream")
	}
	if !namedBy(movie.Encoder, c.Encoder) {
		got := movie.Encoder
		if got == "" {
			got = "nothing"
		}
		return no(c, fmt.Sprintf("the test output was written by %s, not %s", got, c.Encoder))
	}
	return Verdict{Candidate: c, Available: true}
}

func no(c Candidate, reason string) Verdict {
	return Verdict{Candidate: c, Reason: reason}
}

// movieTrack is the first video stream that is not cover art.
func movieTrack(info media.Info) (media.VideoTrack, bool) {
	for _, v := range info.Video {
		if !v.AttachedPicture {
			return v, true
		}
	}
	return media.VideoTrack{}, false
}

// testArgs is the 0.1 s black 256×256 clip through one encoder, with the device setup its family needs (L-HW-1).
func testArgs(c Candidate, out string) []string {
	src := []string{"-f", "lavfi", "-i", "color=c=black:s=256x256:d=0.1"}
	switch c.Family {
	case encode.FamilyQSV:
		return append([]string{"-y", "-init_hw_device", "qsv=qsv:hw", "-filter_hw_device", "qsv"},
			append(src, "-vf", "format=nv12,hwupload=extra_hw_frames=64", "-c:v", c.Encoder, out)...)
	case encode.FamilyVAAPI:
		return append([]string{"-y", "-vaapi_device", c.Device}, append(src, "-vf", "format=nv12,hwupload", "-c:v", c.Encoder, out)...)
	}
	return append([]string{"-y"}, append(src, "-c:v", c.Encoder, out)...)
}

// plainReason turns a failed test encode into words a person can act on, for the card it was run on. The raw text
// comes from ffmpeg as it fails on real hardware (the proofs in slices/15-rebuild/s3-1b-hw-proofs.md). Anything not
// recognised keeps its own words, capped at 300 characters.
func plainReason(c Candidate, err error) string {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "error creating a mfx session"),
		strings.Contains(low, "error parsing global options") && c.Family == encode.FamilyQSV:
		return "no QSV runtime for this chip on this system"
	case strings.Contains(low, "conversion failed") && c.Codec == "av1":
		return "this card has no AV1 encoder"
	case strings.Contains(low, "no usable encoding entrypoint"):
		return fmt.Sprintf("this card's VA-API driver has no %s encoder", codecName(c.Codec))
	case strings.Contains(low, "_tramp_resolve"), strings.Contains(low, ".init.c:") && strings.Contains(low, "assertion"):
		return "the VA-API library on this system is too old for this ffmpeg"
	case strings.Contains(low, "vainitialize failed"), strings.Contains(low, "no va display"),
		strings.Contains(low, "failed to initialise vaapi"), strings.Contains(low, "failed to initialize vaapi"):
		return "the driver refused"
	case strings.Contains(low, "unknown encoder"), strings.Contains(low, "encoder not found"):
		return "this ffmpeg has no such encoder"
	}
	first := strings.TrimSpace(strings.SplitN(msg, "\n", 2)[0])
	reason := "the driver refused: " + first
	if len(reason) > 300 {
		reason = reason[:300]
	}
	return reason
}

// codecName is the name a person knows a codec by.
func codecName(codec string) string {
	switch codec {
	case "hevc":
		return "HEVC"
	case "h264":
		return "H.264"
	case "av1":
		return "AV1"
	}
	return strings.ToUpper(codec)
}

// namedBy reports whether an ENCODER tag names encoder. Real encoders write "Lavc<version> <encoder>" (for example
// "Lavc61.3.100 hevc_qsv"); a bare name is also accepted. The name must be the last word, so a longer encoder name
// does not match a shorter one.
func namedBy(tag, encoder string) bool {
	fields := strings.Fields(tag)
	if len(fields) == 0 || fields[len(fields)-1] != encoder {
		return false
	}
	return len(fields) == 1 || strings.HasPrefix(fields[0], "Lavc")
}
