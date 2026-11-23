// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"math"
	"testing"
)

func TestParseCropdetectTakesTheLastLine(t *testing.T) {
	stderr := "[Parsed_cropdetect_0 @ 0x1] x1:0 crop=1920:798:0:141\nframe=1\n" +
		"[Parsed_cropdetect_0 @ 0x1] crop=1920:800:0:140 pts:5 t:0.1\n"
	got, ok := ParseCropdetect(stderr)
	if !ok || got != (CropRect{W: 1920, H: 800, X: 0, Y: 140}) {
		t.Errorf("last crop line: got %+v ok %v", got, ok)
	}
}

func TestParseCropdetectNoLineIsNotOK(t *testing.T) {
	if _, ok := ParseCropdetect("frame=1\nno crop here\n"); ok {
		t.Error("stderr with no crop line must not parse")
	}
}

func TestAgreedCropAllAgreeTakesTheMedian(t *testing.T) {
	samples := []CropRect{
		{W: 1920, H: 800, X: 0, Y: 140},
		{W: 1920, H: 800, X: 0, Y: 140},
		{W: 1920, H: 801, X: 0, Y: 139},
		{W: 1920, H: 800, X: 0, Y: 140},
		{W: 1920, H: 799, X: 0, Y: 140},
	}
	got, ok := AgreedCrop(samples, 1920, 1080)
	if !ok || got != (CropRect{W: 1920, H: 800, X: 0, Y: 140}) {
		t.Errorf("five samples that agree: got %+v ok %v", got, ok)
	}
}

func TestAgreedCropFourPixelsApartStillAgrees(t *testing.T) {
	samples := []CropRect{{W: 1920, H: 800, X: 0, Y: 140}, {W: 1920, H: 804, X: 0, Y: 140}}
	if _, ok := AgreedCrop(samples, 1920, 1080); !ok {
		t.Error("samples 4 px apart on a side must agree")
	}
}

// Every pair must be within the tolerance, not only each sample against the first: 804 and 796 are 8 px apart
// even though each is 4 px from 800.
func TestAgreedCropRejectsSamplesEightPxApart(t *testing.T) {
	samples := []CropRect{{W: 1920, H: 800, X: 0, Y: 140}, {W: 1920, H: 804, X: 0, Y: 140}, {W: 1920, H: 796, X: 0, Y: 140}}
	if got, ok := AgreedCrop(samples, 1920, 1080); ok {
		t.Errorf("800, 804 and 796 spread 8 px and must reject the crop, got %+v", got)
	}
}

func TestAgreedCropAcceptsSamplesWithinTolerancePairwise(t *testing.T) {
	samples := []CropRect{{W: 1920, H: 800, X: 0, Y: 140}, {W: 1920, H: 804, X: 0, Y: 140}, {W: 1920, H: 802, X: 0, Y: 140}}
	if _, ok := AgreedCrop(samples, 1920, 1080); !ok {
		t.Error("800, 804 and 802 spread 4 px and must agree")
	}
}

func TestAgreedCropOneSpotSixPxOffRejects(t *testing.T) {
	samples := []CropRect{
		{W: 1920, H: 800, X: 0, Y: 140},
		{W: 1920, H: 800, X: 0, Y: 140},
		{W: 1920, H: 806, X: 0, Y: 140},
		{W: 1920, H: 800, X: 0, Y: 140},
	}
	if got, ok := AgreedCrop(samples, 1920, 1080); ok {
		t.Errorf("one spot 6 px off must reject the crop, got %+v", got)
	}
}

func TestAgreedCropRoundsOddSizesDown(t *testing.T) {
	samples := []CropRect{{W: 1917, H: 797, X: 1, Y: 141}, {W: 1917, H: 797, X: 1, Y: 141}, {W: 1917, H: 797, X: 1, Y: 141}}
	got, ok := AgreedCrop(samples, 1920, 1080)
	if !ok || got != (CropRect{W: 1916, H: 796, X: 0, Y: 140}) {
		t.Errorf("odd sizes: got %+v ok %v, want even sides 1916x796 at 0,140", got, ok)
	}
}

func TestAgreedCropNoBarsIsNoCrop(t *testing.T) {
	samples := []CropRect{{W: 1920, H: 1080, X: 0, Y: 0}, {W: 1920, H: 1080, X: 0, Y: 0}}
	if _, ok := AgreedCrop(samples, 1920, 1080); ok {
		t.Error("a rectangle the size of the picture removes nothing and must not be applied")
	}
}

func TestAgreedCropOffThePictureRejects(t *testing.T) {
	samples := []CropRect{{W: 1920, H: 800, X: 0, Y: 400}}
	if _, ok := AgreedCrop(samples, 1920, 1080); ok {
		t.Error("a rectangle that runs past the picture's bottom must be rejected")
	}
	if _, ok := AgreedCrop(nil, 1920, 1080); ok {
		t.Error("no samples must give no crop")
	}
}

func TestCropSpotsSpreadAcrossTheFile(t *testing.T) {
	want := []float64{5, 15, 25, 35, 45, 55, 65, 75, 85, 95}
	got := CropSpots(100)
	if len(got) != len(want) {
		t.Fatalf("spots: got %v", got)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Errorf("spot %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

// The crop is a processor filter ahead of the scale, and the upload comes after both (a card encoder).
func TestCropFilterComesBeforeScaleAndUpload(t *testing.T) {
	crop := &CropRect{W: 1920, H: 800, X: 0, Y: 140}
	p := gpuPlan(22, func(v *VideoEncode) { v.Height = 720; v.CropRect = crop })
	args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_vaapi")
	if !ok || !has(args, "-filter:v:0", "crop=1920:800:0:140,scale=-2:720,format=nv12,hwupload") {
		t.Errorf("card encoder: ok %v, args %v", ok, args)
	}
	cpu, _ := p.FFmpegArgsWith("in.mkv", "out.mkv", "")
	if !has(cpu, "-filter:v:0", "crop=1920:800:0:140,scale=-2:720") {
		t.Errorf("processor encoder: %v", cpu)
	}
}

func TestCropWithoutScaleOnACardEncoder(t *testing.T) {
	crop := &CropRect{W: 1920, H: 800, X: 0, Y: 140}
	p := gpuPlan(22, func(v *VideoEncode) { v.CropRect = crop })
	args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_vaapi")
	if !ok || !has(args, "-filter:v:0", "crop=1920:800:0:140,format=nv12,hwupload") {
		t.Errorf("crop alone on a card encoder: ok %v, args %v", ok, args)
	}
}

// A crop means the card cannot take the whole picture alone: the full pipeline is refused. Without the crop the
// same plan is on the full pipeline, so the refusal is the crop's doing.
func TestCropRefusesTheFullGPUPipeline(t *testing.T) {
	plain := gpuPlan(22, func(v *VideoEncode) { v.Height = 720 })
	if _, ok := plain.FFmpegArgsFullGPU("in.mkv", "out.mkv", "hevc_vaapi"); !ok {
		t.Fatal("setup: the plan without a crop must be on the full pipeline")
	}
	cropped := gpuPlan(22, func(v *VideoEncode) {
		v.Height = 720
		v.CropRect = &CropRect{W: 1920, H: 800, X: 0, Y: 140}
	})
	if _, ok := cropped.FFmpegArgsFullGPU("in.mkv", "out.mkv", "hevc_vaapi"); ok {
		t.Error("a cropped plan must not take the full GPU pipeline")
	}
}

// The rules' own answer to "is this the full pipeline?" must agree with the arguments that run: a cropped hevc_qsv
// plan says no to both, and the uncropped plan says yes to both, so the no is the crop's doing (review 646 §1: a
// cropped job logged "full GPU pipeline" and passed full to its decoder line and step-down rules while running
// processor-decoded arguments).
func TestCroppedPlanAgreesWithTheFullGPUPipelineArguments(t *testing.T) {
	for _, enc := range []string{"hevc_qsv", "hevc_vaapi"} {
		plain := gpuPlan(22, func(v *VideoEncode) { v.Height = 720 })
		if !plain.HasFullPipeline(enc) {
			t.Fatalf("%s: setup: the plan without a crop must be on the full pipeline", enc)
		}
		if _, ok := plain.FFmpegArgsFullGPU("in.mkv", "out.mkv", enc); !ok {
			t.Fatalf("%s: setup: the plan without a crop must have the full-pipeline arguments", enc)
		}
		cropped := gpuPlan(22, func(v *VideoEncode) {
			v.Height = 720
			v.CropRect = &CropRect{W: 1920, H: 800, X: 0, Y: 140}
		})
		_, argsFull := cropped.FFmpegArgsFullGPU("in.mkv", "out.mkv", enc)
		if got := cropped.HasFullPipeline(enc); got != argsFull {
			t.Errorf("%s: HasFullPipeline %v, FFmpegArgsFullGPU ok %v: they must agree", enc, got, argsFull)
		}
		if cropped.HasFullPipeline(enc) {
			t.Errorf("%s: a cropped plan reports the full pipeline", enc)
		}
	}
}

// The measured rectangle is not a setting: the fingerprint is the same whatever the worker measured, and the
// person's choice to crop is part of it.
func TestFingerprintLeavesOutTheMeasuredCrop(t *testing.T) {
	base := gpuPlan(22, func(v *VideoEncode) { v.Crop = true })
	with := gpuPlan(22, func(v *VideoEncode) {
		v.Crop = true
		v.CropRect = &CropRect{W: 1920, H: 800, X: 0, Y: 140}
	})
	off := gpuPlan(22, func(v *VideoEncode) {})
	if (Snapshot{Plan: base}).Fingerprint() != (Snapshot{Plan: with}).Fingerprint() {
		t.Error("the measured rectangle must not change the fingerprint")
	}
	if (Snapshot{Plan: base}).Fingerprint() == (Snapshot{Plan: off}).Fingerprint() {
		t.Error("asking for the crop must change the fingerprint")
	}
}
