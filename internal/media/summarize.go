// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

// Tracks is the compact, JSON-friendly summary of a file that goes into jobs.media (backend-plan
// §7 step 3). It is deliberately plain: the api turns two of these into the job page's MediaRows
// later, and nothing here is ffprobe's own shape.
type Tracks struct {
	Container string `json:"container"`
	// DurationMS and SizeBytes are milliseconds and bytes, the units the screens take.
	DurationMS int64           `json:"durationMs"`
	SizeBytes  int64           `json:"sizeBytes"`
	BitRate    int64           `json:"bitRate"`
	Video      []TrackVideo    `json:"video,omitempty"`
	Audio      []TrackAudio    `json:"audio,omitempty"`
	Subtitles  []TrackSubtitle `json:"subtitles,omitempty"`
}

// TrackVideo is one video track as the pages show it.
type TrackVideo struct {
	Codec           string  `json:"codec"`
	Profile         string  `json:"profile,omitempty"`
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	FPS             float64 `json:"fps"`
	BitDepth        int     `json:"bitDepth"`
	HDR             string  `json:"hdr"`
	PixelFormat     string  `json:"pixelFormat"`
	BitRate         int64   `json:"bitRate,omitempty"`
	AttachedPicture bool    `json:"attachedPicture,omitempty"`
	Language        string  `json:"language,omitempty"`
	Title           string  `json:"title,omitempty"`
}

// TrackAudio is one audio track: language, codec and channels are what the page names.
type TrackAudio struct {
	Codec         string `json:"codec"`
	Profile       string `json:"profile,omitempty"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channelLayout,omitempty"`
	SampleRate    int    `json:"sampleRate,omitempty"`
	BitRate       int64  `json:"bitRate,omitempty"`
	Language      string `json:"language,omitempty"`
	Title         string `json:"title,omitempty"`
	Default       bool   `json:"default,omitempty"`
	Forced        bool   `json:"forced,omitempty"`
}

// TrackSubtitle is one subtitle track; Text says whether it can become an .srt sidecar.
type TrackSubtitle struct {
	Codec    string `json:"codec"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Text     bool   `json:"text"`
	Default  bool   `json:"default,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
}

// Summarize turns a probe into the summary kept with a job. It never fails: a file with no
// streams gives a summary with the container and sizes only.
func Summarize(info Info) Tracks {
	t := Tracks{
		Container:  containerName(info.Format.Names),
		DurationMS: info.Format.Duration.Milliseconds(),
		SizeBytes:  info.Format.Size,
		BitRate:    info.Format.BitRate,
	}
	for _, v := range info.Video {
		t.Video = append(t.Video, TrackVideo{
			Codec:           v.Codec,
			Profile:         v.Profile,
			Width:           v.Width,
			Height:          v.Height,
			FPS:             v.FrameRate.Float(),
			BitDepth:        v.BitDepth,
			HDR:             v.HDRKind(),
			PixelFormat:     v.PixelFormat,
			BitRate:         v.BitRate,
			AttachedPicture: v.AttachedPicture,
			Language:        v.Language,
			Title:           v.Title,
		})
	}
	for _, a := range info.Audio {
		t.Audio = append(t.Audio, TrackAudio{
			Codec:         a.Codec,
			Profile:       a.Profile,
			Channels:      a.Channels,
			ChannelLayout: a.ChannelLayout,
			SampleRate:    a.SampleRate,
			BitRate:       a.BitRate,
			Language:      a.Language,
			Title:         a.Title,
			Default:       a.Default,
			Forced:        a.Forced,
		})
	}
	for _, s := range info.Subtitles {
		t.Subtitles = append(t.Subtitles, TrackSubtitle{
			Codec:    s.Codec,
			Language: s.Language,
			Title:    s.Title,
			Text:     s.Text,
			Default:  s.Default,
			Forced:   s.Forced,
		})
	}
	return t
}

// containerName is the short container name: the first entry of ffprobe's format_name list.
func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
