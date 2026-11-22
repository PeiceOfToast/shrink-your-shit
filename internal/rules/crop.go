// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"sort"
	"strconv"
	"strings"
)

// CropRect is the picture that remains once the black bars are cut away: the rectangle a crop filter keeps,
// in the upright picture's pixels (a phone video's rotation is already applied).
type CropRect struct {
	W int `json:"w"`
	H int `json:"h"`
	X int `json:"x"`
	Y int `json:"y"`
}

// Filter is the crop filter's text.
func (r CropRect) Filter() string {
	return "crop=" + strconv.Itoa(r.W) + ":" + strconv.Itoa(r.H) + ":" + strconv.Itoa(r.X) + ":" + strconv.Itoa(r.Y)
}

// cropTolerance is how far, in pixels, two samples may differ on a side and still agree.
const cropTolerance = 4

// cropSpotFractions are where through the file the worker samples the picture: ten spots 10% apart, so one dark
// scene or one bright title card does not decide the bars for the whole file, and a stretch of a tenth of the
// film or more with another shape cannot fall between two of them.
var cropSpotFractions = []float64{0.05, 0.15, 0.25, 0.35, 0.45, 0.55, 0.65, 0.75, 0.85, 0.95}

// CropSpots are the seconds to sample for a file of the given duration.
func CropSpots(duration float64) []float64 {
	out := make([]float64, len(cropSpotFractions))
	for i, f := range cropSpotFractions {
		out[i] = duration * f
	}
	return out
}

// ParseCropdetect reads the crop line ffmpeg's cropdetect prints ("... crop=W:H:X:Y"). cropdetect refines its
// guess as it goes, so the last line wins. ok is false when no well-formed line is there.
func ParseCropdetect(stderr string) (CropRect, bool) {
	var r CropRect
	ok := false
	for _, line := range strings.Split(stderr, "\n") {
		i := strings.LastIndex(line, "crop=")
		if i < 0 {
			continue
		}
		fields := strings.Fields(line[i+len("crop="):])
		if len(fields) == 0 {
			continue
		}
		parts := strings.Split(fields[0], ":")
		if len(parts) != 4 {
			continue
		}
		var v [4]int
		good := true
		for k, s := range parts {
			n, err := strconv.Atoi(s)
			if err != nil {
				good = false
				break
			}
			v[k] = n
		}
		if good {
			r, ok = CropRect{W: v[0], H: v[1], X: v[2], Y: v[3]}, true
		}
	}
	return r, ok
}

// AgreedCrop turns the samples of one file into the crop to apply. It returns ok=false, and nothing is cropped,
// when a sample is off the picture, when on any side the samples spread over more than cropTolerance (every
// pair of samples is within it, not only each one against the first; a mid-file change of shape is never
// cropped), or when the rectangle would remove nothing. Sides are made even, as the encoders need, and the
// agreed rectangle is the median of the samples.
func AgreedCrop(samples []CropRect, srcW, srcH int) (CropRect, bool) {
	if len(samples) == 0 || srcW <= 0 || srcH <= 0 {
		return CropRect{}, false
	}
	var ws, hs, xs, ys []int
	for _, s := range samples {
		if s.W <= 0 || s.H <= 0 || s.X < 0 || s.Y < 0 || s.X+s.W > srcW || s.Y+s.H > srcH {
			return CropRect{}, false
		}
		ws, hs, xs, ys = append(ws, s.W), append(hs, s.H), append(xs, s.X), append(ys, s.Y)
	}
	for _, side := range [][]int{ws, hs, xs, ys} {
		if spread(side) > cropTolerance {
			return CropRect{}, false
		}
	}
	r := CropRect{W: even(median(ws)), H: even(median(hs)), X: even(median(xs)), Y: even(median(ys))}
	switch {
	case r.W <= 0 || r.H <= 0:
		return CropRect{}, false
	case r.X+r.W > srcW || r.Y+r.H > srcH: // medians of different samples can meet past the edge by a pixel
		return CropRect{}, false
	case r.X == 0 && r.Y == 0 && r.W == srcW && r.H == srcH: // no bars: nothing to remove
		return CropRect{}, false
	}
	return r, true
}

// median is the middle value (the lower of the two middles for an even count).
func median(v []int) int {
	c := append([]int(nil), v...)
	sort.Ints(c)
	return c[(len(c)-1)/2]
}

// spread is the distance from the smallest value to the largest.
func spread(v []int) int {
	lo, hi := v[0], v[0]
	for _, n := range v[1:] {
		if n < lo {
			lo = n
		}
		if n > hi {
			hi = n
		}
	}
	return hi - lo
}

// even rounds down to an even number.
func even(n int) int { return n &^ 1 }
