// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// W7: the Convert step's hardware choices. The defaults must compile to the same plan as before.
func hardwareTestInfo() media.Info {
	return media.Info{Container: "matroska,webm", VideoCodec: "h264", DurationSec: 60, Streams: []media.Stream{
		{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720},
		{Index: 1, Type: "audio", Codec: "aac", Language: "eng"},
	}}
}

func crfArg(args []string) string {
	for i, a := range args {
		if strings.HasPrefix(a, "-crf:") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestHardwareDefaultsCompileToTodaysPlan(t *testing.T) {
	info := hardwareTestInfo()
	base := DefaultSelectors(3).Template().Compile(info)
	sel := DefaultSelectors(3)
	sel.Hardware, sel.HardwareFail, sel.CPUQuality = "auto", "next", 0
	explicit := sel.Template().Compile(info)
	if !reflect.DeepEqual(base.Video, explicit.Video) {
		t.Errorf("explicit defaults change the plan: %+v vs %+v", explicit.Video, base.Video)
	}
	if base.Video.Hardware != "" || base.Video.HardwareFail != "" || base.Video.CPUCRF != 0 {
		t.Errorf("defaults not empty: %+v", base.Video)
	}
	if got, want := crfArg(base.FFmpegArgs("a.mkv", "b.mkv")), crfArg(explicit.FFmpegArgs("a.mkv", "b.mkv")); got != want || got == "" {
		t.Errorf("default CRF arg %q, explicit %q", got, want)
	}
}

func TestHardwareChoicesReachThePlan(t *testing.T) {
	sel := DefaultSelectors(3)
	sel.Hardware, sel.HardwareFail = "cpu", "fail"
	p := sel.Template().Compile(hardwareTestInfo())
	if p.Video.Hardware != "cpu" || p.Video.HardwareFail != "fail" {
		t.Errorf("choices not in the plan: %+v", p.Video)
	}
}

func TestCPUQualityOwnNumberReachesTheCPUEncodeArgs(t *testing.T) {
	info := hardwareTestInfo()
	same := DefaultSelectors(3).Template().Compile(info)
	sel := DefaultSelectors(3)
	sel.CPUQuality = 5 // a different slider position from Quality 3
	own := sel.Template().Compile(info)
	if own.Video.CPUCRF == 0 {
		t.Fatal("CPU quality 5 did not set the processor's CRF")
	}
	args := own.FFmpegArgs("a.mkv", "b.mkv")
	if got, want := crfArg(args), strconv.Itoa(own.Video.CPUCRF); got != want {
		t.Errorf("processor CRF arg %q, want %q", got, want)
	}
	if crfArg(args) == crfArg(same.FFmpegArgs("a.mkv", "b.mkv")) {
		t.Error("own CPU quality gave the same CRF as Quality")
	}
	sel.CPUQuality = 3 // the same as Quality: no own number
	if p := sel.Template().Compile(info); p.Video.CPUCRF != 0 {
		t.Errorf("CPU quality equal to Quality set CPUCRF %d", p.Video.CPUCRF)
	}
}

func TestHardwareChoicesValidate(t *testing.T) {
	ok := []func(*Selectors){
		func(s *Selectors) { s.Hardware = "only" },
		func(s *Selectors) { s.Hardware = "cpu" },
		func(s *Selectors) { s.HardwareFail = "fail" },
		func(s *Selectors) { s.CPUQuality = 1 },
		func(s *Selectors) { s.CPUQuality = 5 },
	}
	for i, set := range ok {
		s := DefaultSelectors(3)
		set(&s)
		if err := s.Validate(); err != nil {
			t.Errorf("case %d rejected: %v", i, err)
		}
	}
	bad := []func(*Selectors){
		func(s *Selectors) { s.Hardware = "gpu" },
		func(s *Selectors) { s.HardwareFail = "retry" },
		func(s *Selectors) { s.CPUQuality = 6 },
		func(s *Selectors) { s.CPUQuality = -1 },
	}
	for i, set := range bad {
		s := DefaultSelectors(3)
		set(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("bad case %d accepted", i)
		}
	}
}
