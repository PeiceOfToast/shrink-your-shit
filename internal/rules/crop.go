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

// cropSpotFractions are where through the file the worker samples the picture: spread out, so one dark
// scene or one bright title card does not decide the bars for the whole file.
var cropSpotFractions = []float64{0.1, 0.3, 0.5, 0.7, 0.9}

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
// when a sample is off the picture, when the samples disagree by more than cropTolerance on any side (a
// mid-file change of shape is never cropped), or when the rectangle would remove nothing. Sides are made even,
// as the encoders need, and the agreed rectangle is the median of the samples.
func AgreedCrop(samples []CropRect, srcW, srcH int) (CropRect, bool) {
	if len(samples) == 0 || srcW <= 0 || srcH <= 0 {
		return CropRect{}, false
	}
	first := samples[0]
	var ws, hs, xs, ys []int
	for _, s := range samples {
		if s.W <= 0 || s.H <= 0 || s.X < 0 || s.Y < 0 || s.X+s.W > srcW || s.Y+s.H > srcH {
			return CropRect{}, false
		}
		if absInt(s.W-first.W) > cropTolerance || absInt(s.H-first.H) > cropTolerance ||
			absInt(s.X-first.X) > cropTolerance || absInt(s.Y-first.Y) > cropTolerance {
			return CropRect{}, false
		}
		ws, hs, xs, ys = append(ws, s.W), append(hs, s.H), append(xs, s.X), append(ys, s.Y)
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

// even rounds down to an even number.
func even(n int) int { return n &^ 1 }

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
