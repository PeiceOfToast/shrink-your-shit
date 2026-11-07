// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package preview

import (
	"context"
	"fmt"
	"io/fs"
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
func (p *Previewer) Sample(ctx context.Context, path string, st fs.FileInfo, t rules.Template) Sample {
	size := st.Size()
	info, err := scanner.Probe(ctx, p.Store, p.Exec, p.FFprobe, path, st)
	plan := rules.Plan{Skip: "unreadable", SkipCode: rules.SkipUnreadable}
	if err == nil {
		plan = t.Compile(info)
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
	if high == 0 {
		s.Lines = append(s.Lines, fmt.Sprintf("Probably left as it is: the new file is unlikely to be at least %d%% smaller.", plan.MinSavingPct))
		return s
	}
	video := "Video: converted to the smaller format, same picture size and length"
	if plan.Video.Height > 0 {
		video = fmt.Sprintf("Video: converted to the smaller format and made %d lines high (from %d), same length", plan.Video.Height, info.Video().Height)
	}
	if plan.Video.MaxRateKbps > 0 {
		video += fmt.Sprintf(", at most %d Mbps", plan.Video.MaxRateKbps/1000)
	}
	s.Lines = append(s.Lines, video)
	s.Lines = append(s.Lines, "Audio: "+tracks(len(info.Audio()), "track"))
	if n := len(info.Subtitles()); n > 0 {
		s.Lines = append(s.Lines, "Subtitles: "+tracks(n, "subtitle"))
	}
	if plan.MinSavingPct > 0 {
		s.Lines = append(s.Lines, fmt.Sprintf("Kept as it is if the new file is not at least %d%% smaller", plan.MinSavingPct))
	}
	if plan.Output == "beside" {
		kind := "file"
		if plan.Container != "" {
			kind = strings.ToUpper(plan.Container) + " file"
		}
		s.Lines = append(s.Lines, "The new "+kind+" is written next to the original, which stays as it is")
	}
	if media.LinkCount(path, st) > 1 {
		s.Lines = append(s.Lines, "Shared with another folder: converting it frees no space, because the other folder keeps its copy")
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
