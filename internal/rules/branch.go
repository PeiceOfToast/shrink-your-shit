// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"slices"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// Branches (V6a; slices/v2-engine/v6-plan.md). A workflow with "Check the file" steps (filter.video)
// compiles to a Template whose Branches are its paths, in order: each path's checks (with the answer the
// path needs at each), and what it ends in: the steps that convert (a Template of their own, compiled as
// a straight workflow is) or "Leave the file alone" (flow.end). A file takes the first path whose checks
// all answer as the path needs. A straight workflow has no Branches, so its Template and Plans are what
// they were.

// VideoCheck is a "Check the file" step's question. Every condition set must hold for "Yes".
type VideoCheck struct {
	Name           string   `json:"name"`                     // the step's name, for the timeline ("Is it 4K?")
	Codecs         []string `json:"codec,omitempty"`          // its video is one of these (ffprobe's names: hevc, h264, ...)
	MinBitrateKbps int      `json:"minBitrateKbps,omitempty"` // at least this bitrate
	Resolution     int      `json:"resolution,omitempty"`     // at least this many lines (720, 1080, 2160: the shorter side)
	HDR            string   `json:"hdr,omitempty"`            // "yes" | "no"
	DolbyVision    string   `json:"dolbyVision,omitempty"`    // "yes" | "no"
	MinFPS         float64  `json:"fps,omitempty"`            // at least this frame rate
	MinSizeMB      int64    `json:"size,omitempty"`           // at least this size, in MB
	Containers     []string `json:"container,omitempty"`      // its file type is one of these (mkv, mp4, avi, ...)
}

// Empty reports whether no condition is set (the step would answer Yes for every file).
func (c VideoCheck) Empty() bool {
	return len(c.Codecs) == 0 && c.MinBitrateKbps == 0 && c.Resolution == 0 && c.HDR == "" && c.DolbyVision == "" &&
		c.MinFPS == 0 && c.MinSizeMB == 0 && len(c.Containers) == 0
}

// Ask is the check's answer for a file, with what decided it in words ("resolution is 2160p"): for Yes
// the first condition, for No the one that didn't hold.
func (c VideoCheck) Ask(info media.Info) (bool, string) {
	v := info.Video()
	lines, fps := 0, 0.0
	if v != nil {
		lines, fps = min(v.Width, v.Height), v.FrameRate
	}
	type cond struct {
		set  bool
		ok   bool
		says string
	}
	ext := strings.TrimPrefix(strings.ToLower(info.Ext), ".")
	hdr, dv := info.IsHDR(), info.HasDolbyVision()
	conds := []cond{
		{len(c.Codecs) > 0, slices.Contains(c.Codecs, info.VideoCodec), "video is " + codecWords(info.VideoCodec)},
		{c.MinBitrateKbps > 0, info.BitrateKbps >= c.MinBitrateKbps, "bitrate is " + strconv.Itoa(info.BitrateKbps) + " kbps"},
		{c.Resolution > 0, lines >= c.Resolution, "resolution is " + strconv.Itoa(lines) + "p"},
		{c.HDR != "", hdr == (c.HDR == "yes"), "picture is " + yesNo(hdr, "HDR", "not HDR")},
		{c.DolbyVision != "", dv == (c.DolbyVision == "yes"), "picture is " + yesNo(dv, "Dolby Vision", "not Dolby Vision")},
		{c.MinFPS > 0, fps >= c.MinFPS, "frame rate is " + strconv.FormatFloat(fps, 'f', -1, 64) + " fps"},
		{c.MinSizeMB > 0, info.SizeBytes >= c.MinSizeMB<<20, "size is " + HumanBytes(info.SizeBytes)},
		{len(c.Containers) > 0, slices.Contains(c.Containers, ext), "file type is " + strings.ToUpper(cmpOr(ext, "unknown"))},
	}
	first := ""
	for _, k := range conds {
		if !k.set {
			continue
		}
		if !k.ok {
			return false, k.says
		}
		if first == "" {
			first = k.says
		}
	}
	return true, first
}

// Decision is how a file went through a "Check the file" step: the port it left by and design's words,
// "Is it 4K? → Yes (resolution is 2160p)".
type Decision struct {
	NodeID string `json:"nodeId"`
	Port   string `json:"port"` // match | else
	Words  string `json:"words"`
}

// BranchCheck is one check on a path, and the answer the path needs (Port match: Yes; else: No).
type BranchCheck struct {
	NodeID string     `json:"nodeId"`
	Port   string     `json:"port"`
	Check  VideoCheck `json:"check"`
}

// Branch is one path of a workflow with checks.
type Branch struct {
	Checks []BranchCheck `json:"checks"`
	Then   *Template     `json:"then,omitempty"` // the steps that convert, as a straight workflow
	End    string        `json:"end,omitempty"`  // "Leave the file alone": its words (Then is nil)
	Path   []string      `json:"path"`           // the node ids along it, input to the end
	// OnFailure: what the path does when its conversion doesn't work (V6b).
	OnFailure *Failures `json:"onFailure,omitempty"`
}

// SkipLeftByWorkflow is a file a workflow's "Leave the file alone" step ended (V6a).
const SkipLeftByWorkflow = "left_by_workflow"

// route is the first branch the file's answers lead down, with the decisions on the way (false: none;
// validation makes every answer lead somewhere, so only a broken Template gets there).
func route(branches []Branch, info media.Info) (Branch, []Decision, bool) {
	for _, b := range branches {
		var ds []Decision
		ok := true
		for _, c := range b.Checks {
			yes, why := c.Check.Ask(info)
			if yes != (c.Port == "match") {
				ok = false
				break
			}
			ds = append(ds, Decision{NodeID: c.NodeID, Port: c.Port, Words: c.Check.Name + " → " + yesNo(yes, "Yes", "No") + " (" + why + ")"})
		}
		if ok {
			return b, ds, true
		}
	}
	return Branch{}, nil, false
}

func yesNo(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func cmpOr(s, or string) string {
	if s == "" {
		return or
	}
	return s
}

// codecWords is a video codec as people know it.
func codecWords(c string) string {
	if w, ok := map[string]string{"hevc": "HEVC", "h264": "H.264", "av1": "AV1", "vp9": "VP9", "vp8": "VP8", "mpeg4": "MPEG-4",
		"mpeg2video": "MPEG-2", "mpeg1video": "MPEG-1", "wmv3": "WMV", "vc1": "VC-1", "prores": "ProRes"}[c]; ok {
		return w
	}
	if c == "" {
		return "unknown"
	}
	return strings.ToUpper(c)
}

// lowerFirst is s with its first letter in lower case (skip words follow "skip: ").
// skipWords are a "Leave the file alone" reason as skip words, which follow "skip: ": without the
// reason's own full stop, as every place that shows them adds one (V6 review: "left alone..").
func skipWords(s string) string { return lowerFirst(strings.TrimRight(strings.TrimSpace(s), ".")) }

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// Parts are the straight Templates a Template runs: itself, or each path's steps that convert (V6a).
// Checks about where new files go look at every part.
func (t Template) Parts() []Template {
	if t.Branches == nil {
		return []Template{t}
	}
	var out []Template
	for _, b := range *t.Branches {
		if b.Then != nil {
			out = append(out, *b.Then)
		}
	}
	return out
}

// Failure is what a path does when a step of its conversion doesn't work (V6b): retry, skip, stop or
// fall back to another path. Set on "Convert video" (the conversion run) and "Check the result" (the
// checks said no); none: today's behaviour (failures that may pass are tried again, then it fails).
type Failure struct {
	NodeID         string  `json:"nodeId"` // the step whose failure this is
	Name           string  `json:"name"`   // its name, for the words
	Action         string  `json:"action"` // retry | skip | stop | fallback
	Max            int     `json:"max,omitempty"`
	BackoffSeconds int     `json:"backoffSeconds,omitempty"`
	Then           string  `json:"then,omitempty"`      // after the last retry: skip | stop
	SkipWords      string  `json:"skipWords,omitempty"` // skip: the reason ("Leave the file alone" on the error port)
	Fallback       *Branch `json:"fallback,omitempty"`  // fallback: the other path, converted from the original
}

// Failures are a path's policies: for the conversion run, and for the checks of the result.
type Failures struct {
	Convert *Failure `json:"convert,omitempty"`
	Check   *Failure `json:"check,omitempty"`
}

// For is the policy for a failure of the conversion run (check false) or of its checks (true): the
// check's own, else the conversion's.
func (f *Failures) For(check bool) *Failure {
	switch {
	case f == nil:
		return nil
	case check && f.Check != nil:
		return f.Check
	}
	return f.Convert
}

// CompileBranch is the Plan of path b for a file (a fallback path run on its own): its conversion, or
// "Leave the file alone".
func CompileBranch(b Branch, info media.Info, languages string) Plan {
	var p Plan
	if b.Then == nil {
		p = Plan{Kind: KindBranches, Skip: skipWords(b.End), SkipCode: SkipLeftByWorkflow}
	} else {
		then := *b.Then
		then.Languages = languages
		p = then.Compile(info)
	}
	p.Path, p.OnFailure = b.Path, b.OnFailure
	return p
}
