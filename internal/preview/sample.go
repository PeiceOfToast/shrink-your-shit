// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package preview

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
)

// Sample is one file's breakdown under a setting (UI-API-CONTRACT.md addendum 5): plain lines, no
// numbers from the encoder's settings (COPY-1), and the new file's expected size as a range.
type Sample struct {
	Lines             []string `json:"lines"`
	ExpectedBytesLow  int64    `json:"expectedBytesLow"`
	ExpectedBytesHigh int64    `json:"expectedBytesHigh"`
}

// Sample probes one file (read only) and says what the setting would do with it.
func (p *Previewer) Sample(ctx context.Context, root, path string, st fs.FileInfo, t rules.Template) Sample {
	size := st.Size()
	info, plan := p.plan(ctx, root, path, st, t)
	if plan.Skip != "" {
		return Sample{Lines: []string{"Left as it is: " + skipWhy(plan) + "."}, ExpectedBytesLow: size, ExpectedBytesHigh: size}
	}
	low, high := Saving(t, plan, info, size)
	s := Sample{ExpectedBytesLow: size - high, ExpectedBytesHigh: size - low}
	s.Lines = append(s.Lines, videoLine(plan, info))
	if hdr := hdrLine(plan, info); hdr != "" { // S3
		s.Lines = append(s.Lines, hdr)
	}
	s.Lines = append(s.Lines, audioLine(plan, len(info.Audio())))
	if line, stays := subtitleLine(t, plan, info); line != "" {
		s.Lines = append(s.Lines, line)
		if stays {
			s.Lines = append(s.Lines, rules.SubtitlesStayInside)
		}
	}
	if plan.Output == "beside" {
		kind := "file"
		if plan.Container != "" {
			kind = strings.ToUpper(plan.Container) + " file"
		}
		s.Lines = append(s.Lines, "The new "+kind+" is written next to the original, which stays as it is")
	}
	return s
}

// plan reads one file (read only) and compiles the setting for it. The file guards come first, as the
// scanner and the library Preview decide (slice 23): a shared file or a link out of the library is left as
// it is, and is not read. A file that cannot be probed comes back with plan.Skip set.
func (p *Previewer) plan(ctx context.Context, root, path string, st fs.FileInfo, t rules.Template) (media.Info, rules.Plan) {
	var info media.Info
	plan := rules.Plan{Skip: "unreadable", SkipCode: rules.SkipUnreadable}
	if lst, err := os.Lstat(path); err == nil {
		if code, _ := scanner.Guard(root, path, lst); code != "" {
			plan = rules.Plan{Skip: rules.SkipWords[code], SkipCode: code}
		} else if i, err := scanner.Probe(ctx, p.Store, p.Exec, p.FFprobe, path, st); err == nil {
			info, plan = i, t.Compile(i)
		}
	}
	return info, plan
}

// skipWhy is why a skipped file is left as it is, in plain words.
func skipWhy(plan rules.Plan) string {
	if why := rules.SkipWords[plan.SkipCode]; why != "" {
		return why
	}
	return "this setting does not convert it"
}

// videoLine says what happens to the picture.
func videoLine(plan rules.Plan, info media.Info) string {
	video := "Video: converted to the smaller format, same picture size and length"
	if plan.Video.Copy { // Basic's repackaging (S4)
		video = "Video: kept as it is (already in the smaller format); the file is only repackaged as an MKV file"
	}
	if plan.Video.Height > 0 {
		video = fmt.Sprintf("Video: converted to the smaller format and made %d lines high (from %d), same length", plan.Video.Height, info.Video().Height)
	}
	if plan.Video.MaxRateKbps > 0 && !plan.Video.IsVP9Target() {
		video += fmt.Sprintf(", at most %d Mbps", plan.Video.MaxRateKbps/1000)
	}
	return video
}

// hdrLine says what happens to HDR (S3), or "" when the file has none to speak of.
func hdrLine(plan rules.Plan, info media.Info) string {
	switch {
	case plan.HDRNote != "":
		return "HDR: " + plan.HDRNote
	case plan.Video.HDR != nil || (plan.Video.Copy && info.IsHDR()):
		return "HDR: stays HDR"
	}
	return ""
}

// subtitleLine says what happens to the subtitles, or "" when there are none. stays is true when the
// subtitles that are kept stay inside the file's own folder (rules.SubtitlesStayInside applies).
func subtitleLine(t rules.Template, plan rules.Plan, info media.Info) (line string, stays bool) {
	n := len(info.Subtitles())
	if n == 0 {
		return "", false
	}
	dropped, files := 0, 0
	for _, op := range plan.Tracks {
		switch {
		case op.ToFile:
			files++
		case op.Type == "subtitle" && op.Drop:
			dropped++
		}
	}
	var parts []string
	if kept := n - dropped - files; kept > 0 || dropped+files == 0 {
		parts = append(parts, tracks(kept, "subtitle"))
	}
	if files > 0 {
		parts = append(parts, fmt.Sprintf("%d saved as separate .srt files", files))
	}
	if dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", dropped))
	}
	stays = t.Selectors != nil && t.Selectors.Subtitles == "srt" && n-dropped-files > 0
	return "Subtitles: " + strings.Join(parts, ", "), stays
}

func tracks(n int, what string) string {
	switch n {
	case 0:
		return "none"
	case 1:
		return "1 " + what + " kept"
	}
	return fmt.Sprintf("%d %ss kept", n, what)
}

// audioLine says what happens to the audio in plain words (slice 25b).
func audioLine(plan rules.Plan, n int) string {
	if plan.AllAudioKept {
		return "Audio: " + rules.NoAudioInMyLanguages
	}
	dropped, converted, to := 0, 0, ""
	for _, op := range plan.Tracks {
		if op.Type != "audio" {
			continue
		}
		switch {
		case op.Drop:
			dropped++
		case op.Encoder != "":
			converted++
			to = map[string]string{"aac": "AAC", "eac3": "E-AC-3", "opus": "Opus"}[op.Codec]
		}
	}
	var parts []string
	if kept := n - dropped - converted; kept > 0 || converted+dropped == 0 {
		parts = append(parts, tracks(kept, "track"))
	}
	if converted > 0 {
		parts = append(parts, fmt.Sprintf("%d converted to %s", converted, to))
	}
	if dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d removed (not in your languages)", dropped))
	}
	return "Audio: " + strings.Join(parts, ", ")
}
