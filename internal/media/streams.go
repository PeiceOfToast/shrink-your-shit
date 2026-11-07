// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Stream describes one stream of a media file, as reported by ffprobe. Fields that do not apply to
// the stream's type, or that ffprobe did not report, are zero. Which fields to trust for what:
// see the comments on the individual fields.
type Stream struct {
	Index    int    `json:"index"`
	Type     string `json:"type"` // video, audio, subtitle, attachment, data (anything else as ffprobe says)
	Codec    string `json:"codec"`
	Language string `json:"language,omitempty"` // as tagged (usually ISO 639-2, "und" when unknown); "" when untagged
	Title    string `json:"title,omitempty"`

	// StartTime is when the stream's first frame is shown, in seconds (0 when not reported). Encoders
	// often delay the first frame slightly, so the same picture sits at a slightly different time in
	// the original and in a converted file.
	StartTime float64 `json:"startTime,omitempty"`

	Default bool `json:"default,omitempty"`
	Forced  bool `json:"forced,omitempty"`
	// AttachedPic is true for cover art stored as a video stream (typical in mp4/mp3): it is not
	// the movie, and must not be mistaken for it.
	AttachedPic bool `json:"attachedPic,omitempty"`

	// Video
	Width, Height int
	// FrameRate is the average frame rate in frames per second.
	FrameRate float64 `json:"frameRate,omitempty"`
	// VFR is true when the average frame rate clearly differs from the nominal one. That is
	// reliable for mp4/mov; for mkv ffprobe reports the nominal rate as the average, so a variable
	// frame rate mkv is NOT detected here (it needs a scan of packet timestamps). Untested on real
	// interlaced broadcast captures, where the nominal rate may be the field rate: before using VFR
	// for a decision, ignore it when FieldOrder is not progressive or check such a file.
	VFR bool `json:"vfr,omitempty"`
	// PixFmt is e.g. yuv420p10le; BitDepth is derived from it (0 when unknown).
	PixFmt         string `json:"pixFmt,omitempty"`
	BitDepth       int    `json:"bitDepth,omitempty"`
	ColorSpace     string `json:"colorSpace,omitempty"`
	ColorTransfer  string `json:"colorTransfer,omitempty"` // smpte2084 = HDR10/PQ, arib-std-b67 = HLG
	ColorPrimaries string `json:"colorPrimaries,omitempty"`
	FieldOrder     string `json:"fieldOrder,omitempty"` // progressive, tt, bb, tb, bt, or "" when not reported
	// DolbyVision and HDR10Plus come from stream-level side data. HDR10+ is often carried per
	// frame, which ffprobe's stream data may not show, so a false HDR10Plus is not proof of absence.
	DolbyVision bool `json:"dolbyVision,omitempty"`
	HDR10Plus   bool `json:"hdr10plus,omitempty"`
	// DVProfile is the Dolby Vision profile from the DOVI configuration record (0: not known, e.g. a
	// dvh1 tag or RPU data alone). Profile 5 has no HDR10 base and must never be re-encoded (S3).
	DVProfile int `json:"dvProfile,omitempty"`
	// DVCompat is the record's dv_bl_signal_compatibility_id, nil when not given: 0 means the base layer
	// has no HDR10/SDR/HLG fallback (like profile 5), so re-encoding it would show wrong colours.
	DVCompat *int `json:"dvCompat,omitempty"`
	// MasteringDisplay is the HDR10 mastering display in x265's master-display form,
	// G(x,y)B(x,y)R(x,y)WP(x,y)L(max,min); CLL is "MaxCLL,MaxFALL" (x265's max-cll). Empty when absent.
	MasteringDisplay string `json:"masteringDisplay,omitempty"`
	CLL              string `json:"cll,omitempty"`

	// Audio
	Channels      int    `json:"channels,omitempty"`
	ChannelLayout string `json:"channelLayout,omitempty"`
	SampleRate    int    `json:"sampleRate,omitempty"`
	// BitrateKbps is the stream's own bitrate when ffprobe reports one (many mkv streams do not).
	BitrateKbps int `json:"bitrateKbps,omitempty"`
	// Profile is the codec profile ffprobe names, e.g. "DTS-HD MA" (lossless) or "DTS" (lossy).
	Profile string `json:"profile,omitempty"`

	// Subtitle: "text" (subrip, ass, mov_text, webvtt...), "bitmap" (pgs, vobsub, dvb), or "" if unknown.
	SubKind string `json:"subKind,omitempty"`

	// Attachment (fonts, cover images in mkv)
	Filename string `json:"filename,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// addSideData reads the HDR side data in a stream's (or its first frame's) side_data_list: Dolby Vision
// (configuration record or RPU), HDR10+, the mastering display and the content light level.
func (s *Stream) addSideData(m map[string]any) {
	list, _ := m["side_data_list"].([]any)
	for _, e := range list {
		sd, _ := e.(map[string]any)
		t := strings.ToLower(str(sd, "side_data_type"))
		switch {
		case strings.Contains(t, "dovi") || strings.Contains(t, "dolby vision"):
			s.DolbyVision = true
			if p := int(num(sd, "dv_profile")); p > 0 {
				s.DVProfile = p
			}
			if _, ok := sd["dv_bl_signal_compatibility_id"]; ok {
				c := int(num(sd, "dv_bl_signal_compatibility_id"))
				s.DVCompat = &c
			}
		case strings.Contains(t, "hdr10+") || strings.Contains(t, "2094-40"):
			s.HDR10Plus = true
		case strings.Contains(t, "mastering display"):
			if md := masterDisplay(sd); md != "" {
				s.MasteringDisplay = md
			}
		case strings.Contains(t, "content light level"):
			s.CLL = fmt.Sprintf("%d,%d", int(num(sd, "max_content")), int(num(sd, "max_average")))
		}
	}
}

// masterDisplay is ffprobe's mastering display side data in x265's units: chromaticity in 1/50000,
// luminance in 1/10000 cd/m². "" when a value is missing (an incomplete record is not written out).
func masterDisplay(sd map[string]any) string {
	var v [10]int64
	for i, k := range []string{"green_x", "green_y", "blue_x", "blue_y", "red_x", "red_y", "white_point_x", "white_point_y", "max_luminance", "min_luminance"} {
		f, ok := rational(sd[k])
		if !ok {
			return ""
		}
		unit := 50000.0
		if i >= 8 {
			unit = 10000
		}
		v[i] = int64(math.Round(f * unit))
	}
	if v[8] <= 0 {
		return ""
	}
	return fmt.Sprintf("G(%d,%d)B(%d,%d)R(%d,%d)WP(%d,%d)L(%d,%d)", v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7], v[8], v[9])
}

// rational reads ffprobe's "num/den" (or a plain number).
func rational(x any) (float64, bool) {
	switch v := x.(type) {
	case float64:
		return v, true
	case string:
		n, d, found := strings.Cut(strings.TrimSpace(v), "/")
		a, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, false
		}
		if !found {
			return a, true
		}
		b, err := strconv.ParseFloat(d, 64)
		if err != nil || b == 0 {
			return 0, false
		}
		return a / b, true
	}
	return 0, false
}

// IsHDR reports whether the video stream carries HDR: PQ (HDR10), HLG, Dolby Vision or HDR10+.
func (s Stream) IsHDR() bool {
	return s.ColorTransfer == "smpte2084" || s.ColorTransfer == "arib-std-b67" || s.DolbyVision || s.HDR10Plus
}

// textSubCodecs are the subtitle codecs ffmpeg can read as text (and so save as .srt). TTML is text
// but has no ffmpeg decoder: it has no kind, so it is copied, never converted.
var textSubCodecs = map[string]bool{
	"subrip": true, "srt": true, "ass": true, "ssa": true, "mov_text": true, "webvtt": true,
	"text": true, "subviewer": true, "subviewer1": true, "microdvd": true, "jacosub": true,
	"sami": true, "realtext": true, "stl": true, "vplayer": true, "mpl2": true, "pjs": true,
	"eia_608": true,
}

var bitmapSubCodecs = map[string]bool{
	"hdmv_pgs_subtitle": true, "dvd_subtitle": true, "dvb_subtitle": true, "xsub": true,
	"dvb_teletext": true,
}

// Video returns the main video stream: the first video stream that is not cover art. It is nil
// when there is none. (Info.VideoCodec keeps its older meaning, "the first video stream".)
func (i Info) Video() *Stream {
	for k := range i.Streams {
		if i.Streams[k].Type == "video" && !i.Streams[k].AttachedPic {
			return &i.Streams[k]
		}
	}
	return nil
}

// ByType returns the streams of one type, in file order.
func (i Info) ByType(t string) []Stream {
	var out []Stream
	for _, s := range i.Streams {
		if s.Type == t {
			out = append(out, s)
		}
	}
	return out
}

// Audio returns the audio streams in file order.
func (i Info) Audio() []Stream { return i.ByType("audio") }

// Subtitles returns the subtitle streams in file order.
func (i Info) Subtitles() []Stream { return i.ByType("subtitle") }

// Attachments returns the attachment streams (fonts, images) in file order.
func (i Info) Attachments() []Stream { return i.ByType("attachment") }

// IsHDR reports whether the main video stream is HDR (HDR10/PQ, HLG, Dolby Vision, HDR10+).
func (i Info) IsHDR() bool {
	v := i.Video()
	return v != nil && v.IsHDR()
}

// HasDolbyVision reports whether the main video stream carries Dolby Vision metadata.
func (i Info) HasDolbyVision() bool {
	v := i.Video()
	return v != nil && v.DolbyVision
}

// HasBitmapSubs reports whether any subtitle stream is a picture format (PGS, VobSub, DVB).
func (i Info) HasBitmapSubs() bool {
	for _, s := range i.Subtitles() {
		if s.SubKind == "bitmap" {
			return true
		}
	}
	return false
}

// ---- parsing helpers: ffprobe's JSON mixes numbers and numeric strings, so read leniently ----

func str(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func num(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			return f
		}
	}
	return 0
}

// tag reads a tag case-insensitively (mkv uses "language", mp4 sometimes "LANGUAGE").
func tag(m map[string]any, key string) string {
	tags, ok := m["tags"].(map[string]any)
	if !ok {
		return ""
	}
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func disposition(m map[string]any, key string) bool {
	d, ok := m["disposition"].(map[string]any)
	if !ok {
		return false
	}
	f, _ := d[key].(float64)
	return f != 0
}

// fraction parses ffprobe's "30000/1001" (and plain numbers); 0 for "0/0" or junk.
func fraction(s string) float64 {
	a, b, ok := strings.Cut(s, "/")
	n, err := strconv.ParseFloat(strings.TrimSpace(a), 64)
	if err != nil {
		return 0
	}
	if !ok {
		return n
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err != nil || d == 0 {
		return 0
	}
	return n / d
}

var depthRe = regexp.MustCompile(`(\d+)(le|be)$`)

// bitDepth gives the bits per colour component: the stream's own report if there is one, else
// what the pixel format name says (yuv420p10le -> 10, yuv420p -> 8), else 0.
func bitDepth(raw, pixFmt string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 && n <= 32 {
		return n
	}
	if m := depthRe.FindStringSubmatch(pixFmt); m != nil {
		if n, _ := strconv.Atoi(m[1]); n > 0 && n <= 32 {
			return n
		}
		return 0 // a number that is not a bit depth (y210le, rgb48le): unknown, not "8"
	}
	if pixFmt != "" {
		return 8
	}
	return 0
}

func parseStream(m map[string]any, position int) Stream {
	s := Stream{
		Index:       position,
		Type:        str(m, "codec_type"),
		Codec:       str(m, "codec_name"),
		Language:    tag(m, "language"),
		Title:       tag(m, "title"),
		Default:     disposition(m, "default"),
		Forced:      disposition(m, "forced"),
		AttachedPic: disposition(m, "attached_pic"),
		BitrateKbps: int(num(m, "bit_rate") / 1000),
		Profile:     str(m, "profile"),
		StartTime:   num(m, "start_time"),
	}
	if _, ok := m["index"]; ok {
		s.Index = int(num(m, "index"))
	}
	switch s.Type {
	case "video":
		s.Width, s.Height = int(num(m, "width")), int(num(m, "height"))
		avg, nominal := fraction(str(m, "avg_frame_rate")), fraction(str(m, "r_frame_rate"))
		s.FrameRate = avg
		if s.FrameRate == 0 {
			s.FrameRate = nominal
		}
		s.VFR = avg > 0 && nominal > 0 && !s.AttachedPic && math.Abs(avg-nominal)/nominal > 0.03
		s.PixFmt = str(m, "pix_fmt")
		s.BitDepth = bitDepth(str(m, "bits_per_raw_sample"), s.PixFmt)
		s.ColorSpace = str(m, "color_space")
		s.ColorTransfer = str(m, "color_transfer")
		s.ColorPrimaries = str(m, "color_primaries")
		s.FieldOrder = str(m, "field_order")
		switch strings.ToLower(str(m, "codec_tag_string")) {
		case "dvh1", "dvhe", "dav1": // Dolby Vision signalled by the MP4/MOV sample entry
			s.DolbyVision = true
		}
		s.addSideData(m)
	case "audio":
		s.Channels = int(num(m, "channels"))
		s.ChannelLayout = str(m, "channel_layout")
		s.SampleRate = int(num(m, "sample_rate"))
	case "subtitle":
		switch {
		case textSubCodecs[s.Codec]:
			s.SubKind = "text"
		case bitmapSubCodecs[s.Codec]:
			s.SubKind = "bitmap"
		}
	case "attachment":
		s.Filename = tag(m, "filename")
		s.MimeType = tag(m, "mimetype")
	}
	return s
}

// MainVideoOrdinal is the position of the main video stream among the file's video streams (the N
// in ffmpeg's "0:v:N"), or -1 if there is none.
func (i Info) MainVideoOrdinal() int {
	n := 0
	for k := range i.Streams {
		if i.Streams[k].Type != "video" {
			continue
		}
		if !i.Streams[k].AttachedPic {
			return n
		}
		n++
	}
	return -1
}

// Lossless reports whether an audio stream is stored without loss (TrueHD/MLP, FLAC, ALAC, PCM, DTS-HD
// Master Audio): converting it to a lossy format always makes it much smaller.
func (s Stream) Lossless() bool {
	switch {
	case s.Codec == "truehd" || s.Codec == "mlp" || s.Codec == "flac" || s.Codec == "alac":
		return true
	case strings.HasPrefix(s.Codec, "pcm_"):
		return true
	case s.Codec == "dts" && strings.Contains(s.Profile, "MA"):
		return true
	}
	return false
}
