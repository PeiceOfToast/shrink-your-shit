// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"math"
	"time"
)

// Info rebuilds a probe from a summary, for a decision made from the summary the scan kept (the library preview). It is
// not the whole probe. A summary does not keep the stream indexes, the color primaries and space, the encoder tag, the
// Dolby Vision profile, the hearing-impaired flag or the chapters. The flow's Plan does not read those, and the library
// preview's test holds Plan's decision on a rebuilt probe to the decision on the real one.
func (t Tracks) Info() Info {
	info := Info{Format: Format{
		Duration: time.Duration(t.DurationMS) * time.Millisecond,
		Size:     t.SizeBytes,
		BitRate:  t.BitRate,
	}}
	if t.Container != "" {
		info.Format.Names = []string{t.Container}
	}
	for i, v := range t.Video {
		vt := VideoTrack{
			Index: i, Codec: v.Codec, Profile: v.Profile, Width: v.Width, Height: v.Height,
			FrameRate: rationalOf(v.FPS), PixelFormat: v.PixelFormat, BitDepth: v.BitDepth, BitRate: v.BitRate,
			AttachedPicture: v.AttachedPicture, Language: v.Language, Title: v.Title,
		}
		switch v.HDR {
		case "Dolby Vision":
			vt.HDR.DolbyVision = true
			vt.ColorTransfer = "smpte2084"
		case "HDR10":
			vt.ColorTransfer = "smpte2084"
		case "HLG":
			vt.ColorTransfer = "arib-std-b67"
		}
		info.Video = append(info.Video, vt)
	}
	for i, a := range t.Audio {
		info.Audio = append(info.Audio, AudioTrack{
			Index: i, Codec: a.Codec, Profile: a.Profile, Channels: a.Channels, ChannelLayout: a.ChannelLayout,
			SampleRate: a.SampleRate, BitRate: a.BitRate, Language: a.Language, Title: a.Title,
			Default: a.Default, Forced: a.Forced,
		})
	}
	for i, s := range t.Subtitles {
		info.Subtitles = append(info.Subtitles, SubtitleTrack{
			Index: i, Codec: s.Codec, Language: s.Language, Title: s.Title, Default: s.Default, Forced: s.Forced,
		})
	}
	return info
}

// rationalOf is a frame rate as a fraction with a millionth of a frame's precision, which is what a summary can hold.
func rationalOf(fps float64) Rational {
	if fps <= 0 {
		return Rational{}
	}
	return Rational{Num: int(math.Round(fps * 1_000_000)), Den: 1_000_000}
}
