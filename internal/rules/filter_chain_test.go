// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"reflect"
	"testing"
)

var testUpload = []string{"format=nv12", "hwupload"}

func TestFilterChainCPUOnlyGetsNoUpload(t *testing.T) {
	got := filterChain([]Filter{{Text: "scale=-2:1080"}, {Text: "setsar=1"}}, false, testUpload, false)
	want := []string{"scale=-2:1080", "setsar=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("processor encoder, processor filters: got %v, want %v", got, want)
	}
}

func TestFilterChainCardAfterCPUGetsOneUploadBeforeIt(t *testing.T) {
	entries := []Filter{{Text: "a"}, {Text: "b"}, {Text: "c", Card: true}, {Text: "d", Card: true}}
	got := filterChain(entries, false, testUpload, true)
	want := []string{"a", "b", "format=nv12", "hwupload", "c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("card filters after processor filters: got %v, want %v", got, want)
	}
}

func TestFilterChainCardEncoderOnProcessorFramesUploadsAtTheEnd(t *testing.T) {
	got := filterChain([]Filter{{Text: "scale=-2:720"}}, false, testUpload, true)
	want := []string{"scale=-2:720", "format=nv12", "hwupload"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("card encoder, no card filters: got %v, want %v", got, want)
	}
	if got := filterChain(nil, false, testUpload, true); !reflect.DeepEqual(got, testUpload) {
		t.Errorf("card encoder, empty list: got %v, want the upload alone", got)
	}
}

func TestFilterChainFullPipelineStartsOnTheCard(t *testing.T) {
	got := filterChain([]Filter{{Text: "scale_vaapi=w=-2:h=1080:format=nv12", Card: true}}, true, testUpload, true)
	want := []string{"scale_vaapi=w=-2:h=1080:format=nv12"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("full pipeline: got %v, want no upload", got)
	}
}

func TestFilterChainFamilyWithoutUploadAddsNothing(t *testing.T) {
	got := filterChain([]Filter{{Text: "scale=-2:1080"}}, false, nil, true)
	if !reflect.DeepEqual(got, []string{"scale=-2:1080"}) {
		t.Errorf("no upload set: got %v", got)
	}
}

func TestFilterChainProcessorAfterCardPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a processor filter after card frames must panic: no download is built")
		}
	}()
	filterChain([]Filter{{Text: "scale_vaapi", Card: true}, {Text: "setsar=1"}}, true, testUpload, true)
}

// TestVAAPISquarePixelsUploadAfterSAR pins the square-pixel case the golden matrix does not cover: the SAR fix
// comes before the upload, as it did when the tail was hard-coded.
func TestVAAPISquarePixelsUploadAfterSAR(t *testing.T) {
	p := gpuPlan(22, func(v *VideoEncode) { v.Height = 1080; v.SquarePixels = true })
	args, ok := p.FFmpegArgsWith("in.mkv", "out.mkv", "hevc_vaapi")
	if !ok || !has(args, "-filter:v:0", "scale=-2:1080,setsar=1,format=nv12,hwupload") {
		t.Errorf("vaapi square pixels: ok %v, args %v", ok, args)
	}
	cpu, _ := p.FFmpegArgsWith("in.mkv", "out.mkv", "")
	if !has(cpu, "-filter:v:0", "scale=-2:1080,setsar=1") {
		t.Errorf("processor square pixels: %v", cpu)
	}
}
