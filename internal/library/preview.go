// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"sort"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// PreviewUnknown is the skip reason for a file the scan has not probed yet: there is no stored probe to decide from.
const PreviewUnknown = "unknown"

// Preview is what a workflow would do to a library's files: WouldConvert of Total, and the rest by reason.
type Preview struct {
	WouldConvert int
	Total        int
	Skipped      []PreviewSkip
}

// PreviewSkip is one reason and how many files it holds, most first.
type PreviewSkip struct {
	Reason string
	Count  int
}

// PreviewOf counts the files the way QueueFiles would queue them, deciding from each file's stored probe, and queues
// nothing. Gone and still-changing rows are left out, as QueueFiles leaves them out. A row with no stored probe counts as
// unknown. decide is the workflow's decision for a probed file. A file that moved since the scan counted it is not
// checked here (no folder is read); the next scan settles it.
func PreviewOf(files []File, decide func(File, media.Info) (bool, string)) Preview {
	p := Preview{Skipped: []PreviewSkip{}}
	counts := map[string]int{}
	for _, f := range files {
		if f.State == Gone || f.State == StillChanging {
			continue
		}
		p.Total++
		if f.Probe == nil {
			counts[PreviewUnknown]++
			continue
		}
		queue, reason := decide(f, f.Probe.Info())
		if queue {
			p.WouldConvert++
			continue
		}
		if reason == "" {
			reason = "skipped"
		}
		counts[reason]++
	}
	for reason, n := range counts {
		p.Skipped = append(p.Skipped, PreviewSkip{Reason: reason, Count: n})
	}
	sort.Slice(p.Skipped, func(i, j int) bool {
		if p.Skipped[i].Count != p.Skipped[j].Count {
			return p.Skipped[i].Count > p.Skipped[j].Count
		}
		return p.Skipped[i].Reason < p.Skipped[j].Reason
	})
	return p
}
