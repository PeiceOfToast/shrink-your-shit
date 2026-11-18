// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"encoding/json"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// probeSummary is a file's probe in the words the job's log shows (user review 11-13 item E): its video (codec, size,
// frame rate, bit depth, HDR), its audio and subtitle tracks, the bitrate and the length. It is stored as JSON.
func probeSummary(info media.Info) string {
	s := struct {
		Codec        string  `json:"codec"`
		Width        int     `json:"width"`
		Height       int     `json:"height"`
		FrameRate    float64 `json:"frameRate"`
		BitDepth     int     `json:"bitDepth"`
		HDR          bool    `json:"hdr"`
		BitrateKbps  int     `json:"bitrateKbps"`
		DurationSec  float64 `json:"durationSec"`
		AudioTracks  int     `json:"audioTracks"`
		SubtitleTrks int     `json:"subtitleTracks"`
	}{BitrateKbps: info.BitrateKbps, DurationSec: info.DurationSec, AudioTracks: len(info.Audio()), SubtitleTrks: len(info.Subtitles())}
	if v := info.Video(); v != nil {
		s.Codec, s.Width, s.Height, s.FrameRate, s.BitDepth, s.HDR = v.Codec, v.Width, v.Height, v.FrameRate, v.BitDepth, v.IsHDR()
	}
	b, _ := json.Marshal(s)
	return string(b)
}
