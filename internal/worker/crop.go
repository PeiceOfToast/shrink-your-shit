// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// cropSampleTimeout bounds one sample: a seek and cropSampleFrames frames of decoding.
const cropSampleTimeout = 60 * time.Second

// cropTotalLimit bounds the whole detection, all the samples together: past it the file is encoded as it is.
// A variable so a test can make it short.
var cropTotalLimit = 3 * time.Minute

// cropSampleFrames is how many frames one sample reads; cropdetect settles within a few.
const cropSampleFrames = "30"

// detectCrop measures the black bars the main video has: one sample at each spot the rules name, and a crop
// only when the samples agree (rules.AgreedCrop). It returns the rectangle, or ok=false with the words the
// job log says. A sample that fails, or a detection that runs past cropTotalLimit, means no crop: the file is
// encoded as it is, and the log says why. A stop (ctx cancelled) returns ctx's error instead: the job ends.
func (w *Worker) detectCrop(ctx context.Context, src media.Info, srcPath string) (rect rules.CropRect, ok bool, words string, err error) {
	v := src.Video()
	if v == nil || v.Width <= 0 || v.Height <= 0 || src.DurationSec <= 0 {
		return rules.CropRect{}, false, "the black bars were not removed: the picture or its length is not known", nil
	}
	w0, h0 := v.Width, v.Height
	if v.Quarter() { // cropdetect sees the upright picture, as the encode does
		w0, h0 = h0, w0
	}
	tctx, cancelAll := context.WithTimeout(ctx, cropTotalLimit)
	defer cancelAll()
	var samples []rules.CropRect
	for _, at := range rules.CropSpots(src.DurationSec) {
		cctx, cancel := context.WithTimeout(tctx, cropSampleTimeout)
		_, stderr, cerr := w.Exec.Capture(cctx, w.FFmpeg, "-hide_banner", "-nostdin", "-v", "info",
			"-ss", strconv.FormatFloat(at, 'f', 3, 64), "-i", srcPath,
			"-map", "0:v:"+strconv.Itoa(src.MainVideoOrdinal()), "-an", "-sn",
			"-vf", "cropdetect=limit=24:round=2:reset=0", "-frames:v", cropSampleFrames, "-f", "null", "-")
		cancel()
		if err := ctx.Err(); err != nil { // stopped or shutting down: not a bars failure, the job goes no further
			return rules.CropRect{}, false, "", err
		}
		if tctx.Err() != nil {
			return rules.CropRect{}, false, fmt.Sprintf("the black bars were not removed: the picture took more than %s to sample", cropTotalLimit), nil
		}
		if cerr != nil {
			return rules.CropRect{}, false, fmt.Sprintf("the black bars were not removed: the picture could not be sampled at %s", rules.Clock(at)), nil
		}
		r, found := rules.ParseCropdetect(string(stderr))
		if !found {
			return rules.CropRect{}, false, fmt.Sprintf("the black bars were not removed: no reading at %s", rules.Clock(at)), nil
		}
		samples = append(samples, r)
	}
	r, agreed := rules.AgreedCrop(samples, w0, h0)
	if !agreed {
		return rules.CropRect{}, false, "the black bars were not removed: the samples did not agree on one picture, or there are none to remove", nil
	}
	return r, true, fmt.Sprintf("the black bars are removed: the picture is kept at %d x %d from %d, %d", r.W, r.H, r.X, r.Y), nil
}
