// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// StepDecision is what one workflow step decided about this file, in the job log's words (log-spec R3, R4):
// the step's name, whether it ran or left the file alone, and one sentence saying what it decided and why.
// The job log writes one section per step; a step that did nothing still appears (silence is never "not
// applicable").
type StepDecision struct {
	Label    string `json:"label"`    // the step's name as the workflow shows it ("Convert video", "Audio")
	Outcome  string `json:"outcome"`  // ran | skipped
	Decision string `json:"decision"` // one sentence: what it decided, and why
}

// The names the workflow's own steps have (workflow/registry.go), as the log shows them.
const (
	StepConvertVideo = "Convert video"
	StepAudio        = "Audio"
	StepSubtitles    = "Subtitles"
	StepFileType     = "File type"
	StepCheck        = "Check the file"
	StepLeftAlone    = "Leave the file alone"
)

// checkSteps are a workflow's check answers for this file, each its own decision on the same step (the
// log numbers the repeats, R12).
func checkSteps(ds []Decision) []StepDecision {
	out := make([]StepDecision, 0, len(ds))
	for _, d := range ds {
		out = append(out, StepDecision{Label: StepCheck, Outcome: "ran", Decision: sentence(d.Words)})
	}
	return out
}

// withStepDecisions fills a Plan's Steps when it has none: one decision per workflow step that ran, or the
// one step that left the file alone. A flow whose step names or ids a Plan can't know (the Template carries
// no node ids yet) still names its steps the workflow's own way.
func withStepDecisions(p Plan, info media.Info) Plan {
	if len(p.Steps) > 0 {
		return p
	}
	if p.Skip != "" {
		return planWith(p, StepDecision{Label: StepLeftAlone, Outcome: "skipped", Decision: sentence(p.Skip)})
	}
	if p.Video.Codec == "" { // a flow that cannot run: the worker fails the job, not a decision
		return p
	}
	p.Steps = append(p.Steps, videoDecision(p, info))
	if p.Kind != KindLegacy { // a legacy flow has one step: its video settings
		p.Steps = append(p.Steps, audioDecision(p, info), subtitleDecision(p, info), fileTypeDecision(p, info))
	}
	return p
}

func planWith(p Plan, d StepDecision) Plan {
	p.Steps = []StepDecision{d}
	return p
}

// videoDecision is the video step's sentence: what becomes what, with the numbers that decided it.
func videoDecision(p Plan, info media.Info) StepDecision {
	src := "its video"
	if v := info.Video(); v != nil {
		src = codecWords(v.Codec)
	}
	if p.Video.Copy {
		return StepDecision{Label: StepConvertVideo, Outcome: "ran",
			Decision: "Copy the video: it is already " + src + ", at this size and with no bitrate limit."}
	}
	target := p.Video.Format
	if target == "" {
		target = "hevc"
	}
	d := "Convert video: " + src
	if v := info.Video(); v != nil && v.BitrateKbps > 0 {
		d += " at " + strconv.Itoa(v.BitrateKbps) + " kbps"
	}
	d += " becomes " + VideoFormats[target].Name
	if p.Video.TargetKbps > 0 { // level: the encode aims at a bitrate (-b:v), so the sentence names it, not a CRF
		d += " at about " + strconv.Itoa(p.Video.TargetKbps) + " kbps"
		if p.Video.MaxRateKbps > 0 {
			d += ", peaks up to " + strconv.Itoa(p.Video.MaxRateKbps) + " kbps"
		}
	} else {
		d += " at CRF " + strconv.Itoa(p.Video.CRF)
	}
	if p.Video.Height > 0 {
		d += fmt.Sprintf(", scaled down to %dp", p.Video.Height)
	}
	switch {
	case p.Video.PeakLimitKbps > 0 && p.Video.TargetKbps == 0: // VBR: the person's max is named, not only the figure it aims at
		d += fmt.Sprintf(", VBR, aims for about %d kbps, never above %d kbps", p.Video.MaxRateKbps, p.Video.PeakLimitKbps)
	case p.Video.MaxRateKbps > 0 && p.Video.TargetKbps == 0: // quality with a cap; a level has already named its peak
		d += fmt.Sprintf(", and never above %d kbps", p.Video.MaxRateKbps)
	}
	return StepDecision{Label: StepConvertVideo, Outcome: "ran", Decision: d + "."}
}

// audioDecision is the audio step's sentence: how many tracks are kept, converted, added or left out.
func audioDecision(p Plan, info media.Info) StepDecision {
	audio := info.Audio()
	var converted, dropped, added, inPlan int
	codecs := map[string]bool{}
	for _, o := range p.Tracks {
		if o.Type != "audio" {
			continue
		}
		inPlan++
		switch {
		case o.Drop:
			dropped++
		case o.Add:
			added++
		case o.Encoder != "":
			converted++
			codecs[codecWords(o.Codec)] = true
		}
	}
	if len(audio) == 0 {
		return StepDecision{Label: StepAudio, Outcome: "ran", Decision: "There is no audio."}
	}
	var parts []string
	if kept := len(audio) - inPlan + countCopiedAudio(p); kept > 0 {
		parts = append(parts, fmt.Sprintf("keep %d as it is", kept))
	}
	if converted > 0 {
		parts = append(parts, fmt.Sprintf("convert %d to %s", converted, joinWords(sortedKeys(codecs))))
	}
	if added > 0 {
		parts = append(parts, fmt.Sprintf("add %d stereo track", added))
	}
	if dropped > 0 {
		parts = append(parts, fmt.Sprintf("leave out %d (not in your languages)", dropped))
	}
	if len(parts) == 0 {
		parts = append(parts, "keep every track as it is")
	}
	return StepDecision{Label: StepAudio, Outcome: "ran", Decision: sentence(joinParts(parts))}
}

// countCopiedAudio is how many of the plan's audio tracks an op converts or adds (the rest are copied).
func countCopiedAudio(p Plan) int {
	n := 0
	for _, o := range p.Tracks {
		if o.Type == "audio" && !o.Drop && !o.Add && o.Encoder == "" {
			n++
		}
	}
	return n
}

// subtitleDecision is the subtitles step's sentence: kept inside, saved as .srt files, or left out.
func subtitleDecision(p Plan, info media.Info) StepDecision {
	subs := info.Subtitles()
	if len(subs) == 0 {
		return StepDecision{Label: StepSubtitles, Outcome: "ran", Decision: "There are no subtitle tracks."}
	}
	var inPlan, saved, dropped, converted int
	for _, o := range p.Tracks {
		if o.Type != "subtitle" {
			continue
		}
		inPlan++
		switch {
		case o.Drop:
			dropped++
		case o.ToFile || o.Extract != "":
			saved++
		case o.Encoder != "":
			converted++
		}
	}
	var parts []string
	if kept := len(subs) - inPlan; kept > 0 {
		parts = append(parts, fmt.Sprintf("keep %d inside the new file as it is", kept))
	}
	if converted > 0 {
		parts = append(parts, fmt.Sprintf("convert %d to plain text", converted))
	}
	if saved > 0 {
		parts = append(parts, fmt.Sprintf("save %d as .srt files", saved))
	}
	if dropped > 0 {
		parts = append(parts, fmt.Sprintf("leave out %d", dropped))
	}
	if len(parts) == 0 {
		parts = append(parts, "keep every subtitle track as it is")
	}
	return StepDecision{Label: StepSubtitles, Outcome: "ran", Decision: sentence(joinParts(parts))}
}

// fileTypeDecision is the file type step's sentence: the new file's type, and why it changed when it did.
func fileTypeDecision(p Plan, info media.Info) StepDecision {
	if p.TypeNote != "" {
		return StepDecision{Label: StepFileType, Outcome: "ran", Decision: p.TypeNote}
	}
	if p.Container != "" {
		return StepDecision{Label: StepFileType, Outcome: "ran", Decision: "Save the new file as " + strings.ToUpper(p.Container) + "."}
	}
	return StepDecision{Label: StepFileType, Outcome: "ran", Decision: "Keep the original's file type."}
}

// sentence is words as one sentence: first letter capitalised, one full stop.
func sentence(s string) string {
	s = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), "."))
	if s == "" {
		return s
	}
	return upperFirst(s) + "."
}

// joinParts joins a decision's parts as one sentence: "a, b and c".
func joinParts(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinWords(ss []string) string {
	if len(ss) == 1 {
		return ss[0]
	}
	return joinParts(ss)
}
