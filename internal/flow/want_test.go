// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// wantInfo is a source with a picture; callers set its colour description.
func wantInfo() media.Info {
	return media.Info{
		Format: media.Format{Duration: 60 * time.Second},
		Video: []media.VideoTrack{{
			Index: 0, Codec: "h264", Width: 1920, Height: 1080,
			FrameRate: media.Rational{Num: 25, Den: 1},
		}},
	}
}

// TestWantedVideoIsThePictureThePlanWrites: the result check compares the new file's probe with
// the size and colour the plan's output should carry (L-SAFE-3, K4).
func TestWantedVideoIsThePictureThePlanWrites(t *testing.T) {
	sdr := wantInfo()
	sdr.Video[0].ColorPrimaries, sdr.Video[0].ColorTransfer, sdr.Video[0].ColorSpace = "bt709", "bt709", "bt709"

	t.Run("a copied video has no picture to compare", func(t *testing.T) {
		plan := encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecCopy}}
		if got := wantedVideo(plan, wantInfo()); got.Compare || got.Width != 0 {
			t.Errorf("want = %+v; a copied video is not compared", got)
		}
	})
	t.Run("a file without video", func(t *testing.T) {
		info := wantInfo()
		info.Video = nil
		plan := encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC}}
		if got := wantedVideo(plan, info); got.Compare {
			t.Errorf("want = %+v; there is no picture", got)
		}
	})
	t.Run("the source's own size and description", func(t *testing.T) {
		plan := encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC}}
		got := wantedVideo(plan, sdr)
		if !got.Compare || got.Width != 1920 || got.Height != 1080 {
			t.Errorf("want = %+v; want the 1920x1080 the plan keeps", got)
		}
		if got.ColorPrimaries != "bt709" || got.ColorTransfer != "bt709" || got.ColorSpace != "bt709" {
			t.Errorf("colour = %+v; want the source's SDR description", got)
		}
	})
	t.Run("the box the plan scales to", func(t *testing.T) {
		plan := encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC, Scale: encode.Scale{Height: 720}}}
		got := wantedVideo(plan, sdr)
		if got.Width != 1280 || got.Height != 720 {
			t.Errorf("want = %+v; want the 1280x720 the plan writes", got)
		}
	})
	t.Run("tone-mapping is described as BT.709", func(t *testing.T) {
		hdr := wantInfo()
		hdr.Video[0].ColorPrimaries, hdr.Video[0].ColorTransfer, hdr.Video[0].ColorSpace = "bt2020", "smpte2084", "bt2020nc"
		plan := encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC, HDR: encode.HDRTonemap}}
		got := wantedVideo(plan, hdr)
		if got.ColorPrimaries != "bt709" || got.ColorTransfer != "bt709" || got.ColorSpace != "bt709" {
			t.Errorf("colour = %+v; a tone-mapped result is BT.709", got)
		}
	})
	t.Run("keeping HDR writes the source's description", func(t *testing.T) {
		hdr := wantInfo()
		hdr.Video[0].ColorPrimaries, hdr.Video[0].ColorTransfer, hdr.Video[0].ColorSpace = "bt2020", "smpte2084", "bt2020nc"
		plan := encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC, HDR: encode.HDRKeep}}
		got := wantedVideo(plan, hdr)
		if got.ColorPrimaries != "bt2020" || got.ColorTransfer != "smpte2084" || got.ColorSpace != "bt2020nc" {
			t.Errorf("colour = %+v; want the source's HDR description", got)
		}
	})
	t.Run("Dolby Vision without a transfer line is HDR10", func(t *testing.T) {
		dv := wantInfo()
		dv.Video[0].HDR.DolbyVision = true
		plan := encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC}}
		got := wantedVideo(plan, dv)
		if got.ColorPrimaries != "bt2020" || got.ColorTransfer != "smpte2084" || got.ColorSpace != "bt2020nc" {
			t.Errorf("colour = %+v; want Build's HDR10 defaults", got)
		}
	})
	t.Run("an SDR file with no description compares nothing", func(t *testing.T) {
		plan := encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC}}
		got := wantedVideo(plan, wantInfo())
		if got.ColorPrimaries != "" || got.ColorTransfer != "" || got.ColorSpace != "" {
			t.Errorf("colour = %+v; an unknown description is not compared", got)
		}
	})
}
