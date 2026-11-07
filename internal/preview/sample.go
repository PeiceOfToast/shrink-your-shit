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
	plan := rules.Plan{Skip: "unreadable", SkipCode: rules.SkipUnreadable}
	var info media.Info
	// The file guards first, as the scanner and the library Preview decide (slice 23): a shared file or a
	// link out of the library is left as it is, and is not read.
	if lst, err := os.Lstat(path); err == nil {
		if code, _ := scanner.Guard(root, path, lst); code != "" {
			plan = rules.Plan{Skip: rules.SkipWords[code], SkipCode: code}
		} else if i, err := scanner.Probe(ctx, p.Store, p.Exec, p.FFprobe, path, st); err == nil {
			info, plan = i, t.Compile(i)
		}
	}
	if plan.Skip != "" {
		why := rules.SkipWords[plan.SkipCode]
		if why == "" {
			why = "this setting does not convert it"
		}
		return Sample{Lines: []string{"Left as it is: " + why + "."}, ExpectedBytesLow: size, ExpectedBytesHigh: size}
	}
	low, high := Saving(t, plan, info, size)
	s := Sample{ExpectedBytesLow: size - high, ExpectedBytesHigh: size - low}
	video := "Video: converted to the smaller format, same picture size and length"
	if plan.Video.Copy { // Basic's repackaging (S4)
		video = "Video: kept as it is (already in the smaller format); the file is only repackaged as an MKV file"
	}
	if plan.Video.Height > 0 {
		video = fmt.Sprintf("Video: converted to the smaller format and made %d lines high (from %d), same length", plan.Video.Height, info.Video().Height)
	}
	if plan.Video.MaxRateKbps > 0 {
		video += fmt.Sprintf(", at most %d Mbps", plan.Video.MaxRateKbps/1000)
	}
	s.Lines = append(s.Lines, video)
	s.Lines = append(s.Lines, audioLine(plan, len(info.Audio())))
	if n := len(info.Subtitles()); n > 0 {
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
		line := "Subtitles: " + strings.Join(parts, ", ")
		s.Lines = append(s.Lines, line)
		if t.Selectors != nil && t.Selectors.Subtitles == "srt" && n-dropped-files > 0 {
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
