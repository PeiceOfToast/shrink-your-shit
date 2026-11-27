// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrFFprobeMissing means ffprobe is not on PATH.
var ErrFFprobeMissing = errors.New("ffprobe not found")

// ErrNotMedia means ffprobe could not read the file as media. It is kept apart from a missing
// ffprobe and from a cancelled call: a scanner skips such a file, while a worker fails the job.
var ErrNotMedia = errors.New("not a media file")

// Info is what one ffprobe run says about a file: the container and one entry per stream the app
// cares about, plus the chapters. Streams of other kinds (data, attachments) are left out.
type Info struct {
	Path      string
	Format    Format
	Video     []VideoTrack
	Audio     []AudioTrack
	Subtitles []SubtitleTrack
	Chapters  []Chapter
}

// Format is the container: its names, duration, size and overall bit rate.
type Format struct {
	Names    []string // format_name split on ","; the first is the short name
	LongName string
	Duration time.Duration
	Size     int64
	BitRate  int64
	Tags     map[string]string
}

// Rational is a frame rate as ffprobe reports it ("30000/1001").
type Rational struct {
	Num int
	Den int
}

// Float is the frame rate as a number; zero when the file does not say.
func (r Rational) Float() float64 {
	if r.Den == 0 {
		return 0
	}
	return float64(r.Num) / float64(r.Den)
}

func (r Rational) String() string {
	return strconv.Itoa(r.Num) + "/" + strconv.Itoa(r.Den)
}

// VideoTrack is one video stream. AttachedPicture marks cover art, which is a video stream in some
// containers but must never be mistaken for the main video.
type VideoTrack struct {
	Index           int
	Codec           string
	Profile         string
	Width, Height   int
	FrameRate       Rational
	PixelFormat     string
	BitDepth        int
	BitRate         int64
	ColorPrimaries  string
	ColorTransfer   string
	ColorSpace      string
	ColorRange      string
	HDR             HDR
	AttachedPicture bool
	Language        string
	Title           string
}

// HDR is the HDR side data found on a video stream. Metadata only: whether the picture really is
// HDR is for the eyes, not this parser.
type HDR struct {
	MasteringDisplay bool // "Mastering display metadata" side data (HDR10)
	ContentLight     bool // "Content light level metadata" side data
	DolbyVision      bool // "DOVI configuration record" side data
}

// HDRKind names what the stream carries, for the summary and later the pages: "SDR", "HDR10",
// "HLG" or "Dolby Vision".
func (v VideoTrack) HDRKind() string {
	switch {
	case v.HDR.DolbyVision:
		return "Dolby Vision"
	case v.ColorTransfer == "arib-std-b67":
		return "HLG"
	case v.ColorTransfer == "smpte2084":
		return "HDR10"
	}
	return "SDR"
}

// AudioTrack is one audio stream, with the dispositions the audio step needs.
type AudioTrack struct {
	Index         int
	Codec         string
	Profile       string
	Channels      int
	ChannelLayout string
	SampleRate    int
	BitRate       int64
	Language      string
	Title         string
	Default       bool
	Forced        bool
	Comment       bool
}

// SubtitleTrack is one subtitle stream. Text is false for bitmap subtitles (PGS, VobSub, DVB),
// which cannot be converted to text and need different handling.
type SubtitleTrack struct {
	Index    int
	Codec    string
	Language string
	Title    string
	Default  bool
	Forced   bool
	Text     bool
}

// Chapter is one chapter marker.
type Chapter struct {
	Start time.Duration
	End   time.Duration
	Title string
}

// Probe asks ffprobe about one file. Errors keep their cause: errors.Is can tell ErrNotMedia,
// ErrFFprobeMissing and context.Canceled / DeadlineExceeded apart.
func Probe(ctx context.Context, path string) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, fmt.Errorf("probe %s: %w", path, err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return Info{}, fmt.Errorf("probe %s: %w: %v", path, ErrFFprobeMissing, err)
	}
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-show_chapters",
		path,
	}
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Info{}, fmt.Errorf("probe %s: %w", path, ctxErr)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return Info{}, fmt.Errorf("%s: %w: %s", path, ErrNotMedia, lastLine(stderr.String()))
		}
		return Info{}, fmt.Errorf("run ffprobe on %s: %w", path, err)
	}
	info, err := ParseProbe(out.Bytes())
	if err != nil {
		return Info{}, fmt.Errorf("read ffprobe's answer for %s: %w", path, err)
	}
	info.Path = path
	return info, nil
}

// lastLine is the part of ffprobe's stderr worth putting in an error.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return "no message"
}

// The wire types match ffprobe's JSON. Most numbers arrive as strings ("10.000000"), so the flex
// types accept either form and "N/A".
type probeOutput struct {
	Format   probeFormat    `json:"format"`
	Streams  []probeStream  `json:"streams"`
	Chapters []probeChapter `json:"chapters"`
}

type probeFormat struct {
	FormatName string    `json:"format_name"`
	FormatLong string    `json:"format_long_name"`
	Duration   flexFloat `json:"duration"`
	Size       flexInt   `json:"size"`
	BitRate    flexInt   `json:"bit_rate"`
	Tags       wireTags  `json:"tags"`
}

type probeStream struct {
	Index            int              `json:"index"`
	CodecName        string           `json:"codec_name"`
	CodecType        string           `json:"codec_type"`
	Profile          string           `json:"profile"`
	Width            flexInt          `json:"width"`
	Height           flexInt          `json:"height"`
	AvgFrameRate     string           `json:"avg_frame_rate"`
	RFrameRate       string           `json:"r_frame_rate"`
	PixelFormat      string           `json:"pix_fmt"`
	BitsPerRawSample flexInt          `json:"bits_per_raw_sample"`
	BitRate          flexInt          `json:"bit_rate"`
	ColorPrimaries   string           `json:"color_primaries"`
	ColorTransfer    string           `json:"color_transfer"`
	ColorSpace       string           `json:"color_space"`
	ColorRange       string           `json:"color_range"`
	SampleRate       flexInt          `json:"sample_rate"`
	Channels         flexInt          `json:"channels"`
	ChannelLayout    string           `json:"channel_layout"`
	Disposition      probeDisposition `json:"disposition"`
	Tags             wireTags         `json:"tags"`
	SideDataList     []probeSideData  `json:"side_data_list"`
}

type probeDisposition struct {
	Default     flexInt `json:"default"`
	Forced      flexInt `json:"forced"`
	Comment     flexInt `json:"comment"`
	AttachedPic flexInt `json:"attached_pic"`
}

type probeSideData struct {
	Type string `json:"side_data_type"`
}

type probeChapter struct {
	StartTime flexFloat `json:"start_time"`
	EndTime   flexFloat `json:"end_time"`
	Tags      wireTags  `json:"tags"`
}

type wireTags map[string]any

// Strings renders a tag map as text; ffprobe gives most tags as strings but some as numbers.
func (t wireTags) Strings() map[string]string {
	if len(t) == 0 {
		return nil
	}
	out := make(map[string]string, len(t))
	for k, v := range t {
		out[k] = fmt.Sprint(v)
	}
	return out
}

type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" || s == "N/A" {
		*f = 0
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = flexInt(n)
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("expect a number, got %s", b)
	}
	*f = flexInt(math.Round(v))
	return nil
}

type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" || s == "N/A" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("expect a number, got %s", b)
	}
	*f = flexFloat(v)
	return nil
}

// ParseProbe turns one ffprobe JSON answer into Info. It is separate from Probe so the parser
// runs from fixtures without ffmpeg, and the fake's fixtures go through the same code.
func ParseProbe(data []byte) (Info, error) {
	var out probeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return Info{}, fmt.Errorf("parse ffprobe JSON: %w", err)
	}
	info := Info{
		Format: Format{
			LongName: out.Format.FormatLong,
			Duration: seconds(out.Format.Duration),
			Size:     int64(out.Format.Size),
			BitRate:  int64(out.Format.BitRate),
			Tags:     out.Format.Tags.Strings(),
		},
	}
	if out.Format.FormatName != "" {
		info.Format.Names = strings.Split(out.Format.FormatName, ",")
	}
	for i := range out.Streams {
		s := &out.Streams[i]
		switch s.CodecType {
		case "video":
			info.Video = append(info.Video, videoTrack(s))
		case "audio":
			info.Audio = append(info.Audio, audioTrack(s))
		case "subtitle":
			info.Subtitles = append(info.Subtitles, subtitleTrack(s))
		}
	}
	for _, c := range out.Chapters {
		info.Chapters = append(info.Chapters, Chapter{
			Start: seconds(c.StartTime),
			End:   seconds(c.EndTime),
			Title: c.Tags.Strings()["title"],
		})
	}
	return info, nil
}

func videoTrack(s *probeStream) VideoTrack {
	v := VideoTrack{
		Index:           s.Index,
		Codec:           s.CodecName,
		Profile:         s.Profile,
		Width:           int(s.Width),
		Height:          int(s.Height),
		FrameRate:       parseRational(s.AvgFrameRate, s.RFrameRate),
		PixelFormat:     s.PixelFormat,
		BitDepth:        bitDepth(int(s.BitsPerRawSample), s.PixelFormat),
		BitRate:         int64(s.BitRate),
		ColorPrimaries:  s.ColorPrimaries,
		ColorTransfer:   s.ColorTransfer,
		ColorSpace:      s.ColorSpace,
		ColorRange:      s.ColorRange,
		AttachedPicture: s.Disposition.AttachedPic != 0,
		Language:        s.Tags.Strings()["language"],
		Title:           s.Tags.Strings()["title"],
	}
	for _, sd := range s.SideDataList {
		switch sd.Type {
		case "Mastering display metadata":
			v.HDR.MasteringDisplay = true
		case "Content light level metadata":
			v.HDR.ContentLight = true
		case "DOVI configuration record":
			v.HDR.DolbyVision = true
		}
	}
	return v
}

func audioTrack(s *probeStream) AudioTrack {
	return AudioTrack{
		Index:         s.Index,
		Codec:         s.CodecName,
		Profile:       s.Profile,
		Channels:      int(s.Channels),
		ChannelLayout: s.ChannelLayout,
		SampleRate:    int(s.SampleRate),
		BitRate:       int64(s.BitRate),
		Language:      s.Tags.Strings()["language"],
		Title:         s.Tags.Strings()["title"],
		Default:       s.Disposition.Default != 0,
		Forced:        s.Disposition.Forced != 0,
		Comment:       s.Disposition.Comment != 0,
	}
}

func subtitleTrack(s *probeStream) SubtitleTrack {
	return SubtitleTrack{
		Index:    s.Index,
		Codec:    s.CodecName,
		Language: s.Tags.Strings()["language"],
		Title:    s.Tags.Strings()["title"],
		Default:  s.Disposition.Default != 0,
		Forced:   s.Disposition.Forced != 0,
		Text:     isTextSubtitle(s.CodecName),
	}
}

// isTextSubtitle knows the bitmap subtitle codecs; everything else (subrip, ass, mov_text,
// webvtt) is text.
func isTextSubtitle(codec string) bool {
	switch codec {
	case "dvd_subtitle", "dvdsub", "hdmv_pgs_subtitle", "pgssub", "dvb_subtitle", "xsub", "dvbsub":
		return false
	}
	return true
}

// parseRational prefers the average frame rate and falls back to the real one; "0/0" means the
// file does not say.
func parseRational(values ...string) Rational {
	for _, v := range values {
		numText, denText, ok := strings.Cut(v, "/")
		if !ok {
			continue
		}
		num, err1 := strconv.Atoi(numText)
		den, err2 := strconv.Atoi(denText)
		if err1 == nil && err2 == nil && den > 0 && num > 0 {
			return Rational{Num: num, Den: den}
		}
	}
	return Rational{}
}

// bitDepth trusts bits_per_raw_sample, then the pixel format's 10/12/16-le suffix, then 8.
func bitDepth(rawSample int, pixelFormat string) int {
	if rawSample > 0 {
		return rawSample
	}
	for _, bits := range []string{"16", "12", "10"} {
		if strings.HasSuffix(pixelFormat, bits+"le") || strings.HasSuffix(pixelFormat, bits+"be") {
			n, _ := strconv.Atoi(bits)
			return n
		}
	}
	return 8
}

func seconds(f flexFloat) time.Duration {
	return time.Duration(math.Round(float64(f) * float64(time.Second)))
}
