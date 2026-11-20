// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/vars"
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
	// FB1b: seven more facts a file can be asked about.
	BitDepth          int      `json:"bitDepth,omitempty"`         // the picture's bit depth is this (8, 10, 12)
	AudioCodecs       []string `json:"audioCodec,omitempty"`       // some audio track is one of these (aac, ac3, eac3, dts, truehd, flac, opus, ...)
	MinChannels       int      `json:"minChannels,omitempty"`      // some audio track has at least this many channels
	AudioLanguages    []string `json:"audioLanguage,omitempty"`    // some audio track is in one of these languages (ISO 639-2)
	SubtitleLanguages []string `json:"subtitleLanguage,omitempty"` // some subtitle track is in one of these languages
	MinMinutes        int      `json:"minMinutes,omitempty"`       // the running time is at least this many minutes
	Chapters          string   `json:"chapters,omitempty"`         // "yes" | "no": the file has chapters
}

// Empty reports whether no condition is set (the step would answer Yes for every file).
func (c VideoCheck) Empty() bool {
	return len(c.Codecs) == 0 && c.MinBitrateKbps == 0 && c.Resolution == 0 && c.HDR == "" && c.DolbyVision == "" &&
		c.MinFPS == 0 && c.MinSizeMB == 0 && len(c.Containers) == 0 && c.BitDepth == 0 && len(c.AudioCodecs) == 0 &&
		c.MinChannels == 0 && len(c.AudioLanguages) == 0 && len(c.SubtitleLanguages) == 0 && c.MinMinutes == 0 && c.Chapters == ""
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
	depth := 0
	if v != nil {
		depth = v.BitDepth
		if depth == 0 {
			depth = 8
		}
	}
	var audioCodecs, audioLangs, subLangs []string
	channels := 0
	for _, st := range info.Streams {
		switch st.Type {
		case "audio":
			audioCodecs, audioLangs, channels = append(audioCodecs, st.Codec), append(audioLangs, st.Language), max(channels, st.Channels)
		case "subtitle":
			subLangs = append(subLangs, st.Language)
		}
	}
	anyOf := func(have, want []string) bool {
		return slices.ContainsFunc(have, func(h string) bool { return slices.Contains(want, h) })
	}
	minutes := int(info.DurationSec / 60)
	conds := []cond{
		{len(c.Codecs) > 0, slices.Contains(c.Codecs, info.VideoCodec), "video is " + codecWords(info.VideoCodec)},
		{c.MinBitrateKbps > 0, info.BitrateKbps >= c.MinBitrateKbps, "bitrate is " + strconv.Itoa(info.BitrateKbps) + " kbps"},
		{c.Resolution > 0, lines >= c.Resolution, "resolution is " + strconv.Itoa(lines) + "p"},
		{c.HDR != "", hdr == (c.HDR == "yes"), "picture is " + yesNo(hdr, "HDR", "not HDR")},
		{c.DolbyVision != "", dv == (c.DolbyVision == "yes"), "picture is " + yesNo(dv, "Dolby Vision", "not Dolby Vision")},
		{c.MinFPS > 0, fps >= c.MinFPS, "frame rate is " + strconv.FormatFloat(fps, 'f', -1, 64) + " fps"},
		{c.MinSizeMB > 0, info.SizeBytes >= c.MinSizeMB<<20, "size is " + HumanBytes(info.SizeBytes)},
		{len(c.Containers) > 0, slices.Contains(c.Containers, ext), "file type is " + strings.ToUpper(cmpOr(ext, "unknown"))},
		{c.BitDepth > 0, depth == c.BitDepth, "picture is " + strconv.Itoa(depth) + "-bit"},
		{len(c.AudioCodecs) > 0, anyOf(audioCodecs, c.AudioCodecs), "audio is " + strings.ToUpper(cmpOr(strings.Join(audioCodecs, ", "), "none"))},
		{c.MinChannels > 0, channels >= c.MinChannels, "audio has " + strconv.Itoa(channels) + " channels"},
		{len(c.AudioLanguages) > 0, anyOf(audioLangs, c.AudioLanguages), "audio languages are " + cmpOr(strings.Join(audioLangs, ", "), "none")},
		{len(c.SubtitleLanguages) > 0, anyOf(subLangs, c.SubtitleLanguages), "subtitle languages are " + cmpOr(strings.Join(subLangs, ", "), "none")},
		{c.MinMinutes > 0, minutes >= c.MinMinutes, "running time is " + strconv.Itoa(minutes) + " min"},
		{c.Chapters != "", (info.Chapters > 0) == (c.Chapters == "yes"), "file has " + yesNo(info.Chapters > 0, "chapters", "no chapters")},
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

// Facts are what a question may ask beyond the probe (FB1b): the run's variables and the file's record.
// The worker fills them at job time; an empty Facts answers every variable question No ("no value").
type Facts struct {
	Vars      map[string]string `json:"vars,omitempty"`
	Path      string            `json:"path,omitempty"`      // the original's full path
	Library   string            `json:"library,omitempty"`   // the library's name
	LibraryID string            `json:"libraryId,omitempty"` // the library's id, as the question's config stores it
	HardLinks bool              `json:"hardLinks,omitempty"` // the original shares its data with another name
	ModTime   time.Time         `json:"modTime,omitempty"`   // when the original was last changed
	Now       time.Time         `json:"-"`                   // the time to measure age from (zero: time.Now)
}

// Question is what a question step asks (FB1b): a "Check the file" question (Kind "", VideoCheck's
// fields, as before) or one of the other kinds. A stored BranchCheck from before FB1b reads as Kind "".
type Question struct {
	VideoCheck
	Kind string `json:"kind,omitempty"` // "" (the file's facts) | name | age | variable | library | hardlinks
	// name: the file's name or path matches a pattern
	Pattern       string `json:"pattern,omitempty"`
	Match         string `json:"match,omitempty"` // contains | starts | ends | glob | regex
	Where         string `json:"where,omitempty"` // name | path (default name)
	CaseSensitive bool   `json:"caseSensitive,omitempty"`
	// age: the original was last changed at least / at most this many days ago
	OlderDays   int `json:"olderDays,omitempty"`
	YoungerDays int `json:"youngerDays,omitempty"`
	// variable: a {{name}} is set / equals / contains / is more / is less than Value
	Variable string `json:"variable,omitempty"`
	Op       string `json:"op,omitempty"` // set | equals | contains | more | less
	Value    string `json:"value,omitempty"`
	// library: the file comes from one of these libraries (ids)
	Libraries []string `json:"libraries,omitempty"`
}

// Ask is the question's answer for a file, with what decided it in words.
func (q Question) Ask(info media.Info, f Facts) (bool, string) {
	switch q.Kind {
	case "", "file":
		return q.VideoCheck.Ask(info)
	case "name":
		subject := filepath.Base(f.Path)
		if q.Where == "path" {
			subject = f.Path
		}
		have, want := subject, q.Pattern
		if !q.CaseSensitive {
			have, want = strings.ToLower(have), strings.ToLower(want)
		}
		ok := false
		switch q.Match {
		case "starts":
			ok = strings.HasPrefix(have, want)
		case "ends":
			ok = strings.HasSuffix(have, want)
		case "glob":
			ok, _ = filepath.Match(want, have)
		case "regex": // the pattern keeps its case: lowering it would turn \S into \s (review2-531 P2)
			pat := q.Pattern
			if !q.CaseSensitive {
				pat = "(?i)" + pat
			}
			if re, err := regexp.Compile(pat); err == nil {
				ok = re.MatchString(subject)
			}
		default:
			ok = strings.Contains(have, want)
		}
		return ok, "the " + cmpOr(q.Where, "name") + " is " + strconv.Quote(subject)
	case "age":
		now := f.Now
		if now.IsZero() {
			now = time.Now()
		}
		days := 0
		if !f.ModTime.IsZero() {
			days = int(now.Sub(f.ModTime).Hours() / 24)
		}
		says := "last changed " + strconv.Itoa(days) + " days ago"
		if f.ModTime.IsZero() {
			return false, "the file's date is not known"
		}
		if q.OlderDays > 0 && days < q.OlderDays {
			return false, says
		}
		if q.YoungerDays > 0 && days > q.YoungerDays {
			return false, says
		}
		return true, says
	case "variable":
		v, set := f.Vars[q.Variable]
		if !set || v == "" {
			return q.Op == "unset", "{{" + q.Variable + "}} has no value"
		}
		says := "{{" + q.Variable + "}} is " + strconv.Quote(v)
		want, _ := vars.Resolve(q.Value, f.Vars) // the value may name variables ({{library}}); review2-531 P2
		q.Value = want
		switch q.Op {
		case "set":
			return true, says
		case "unset":
			return false, says
		case "equals":
			return strings.EqualFold(v, q.Value), says
		case "contains":
			return strings.Contains(strings.ToLower(v), strings.ToLower(q.Value)), says
		case "more", "less":
			a, errA := strconv.ParseFloat(strings.TrimSpace(v), 64)
			b, errB := strconv.ParseFloat(strings.TrimSpace(q.Value), 64)
			if errA != nil || errB != nil {
				return false, says + " (not a number)"
			}
			if q.Op == "more" {
				return a > b, says
			}
			return a < b, says
		}
		return false, says
	case "library":
		return slices.Contains(q.Libraries, f.LibraryID), "the library is " + cmpOr(f.Library, "unknown")
	case "hardlinks":
		return f.HardLinks, yesNo(f.HardLinks, "the file shares its data with another name", "the file has one name")
	}
	return false, "a question this version can't ask"
}

// Empty reports whether the question has nothing to ask (it would answer Yes for every file).
func (q Question) Empty() bool {
	switch q.Kind {
	case "", "file":
		return q.VideoCheck.Empty()
	case "name":
		return q.Pattern == ""
	case "age":
		return q.OlderDays == 0 && q.YoungerDays == 0
	case "variable":
		return q.Variable == ""
	case "library":
		return len(q.Libraries) == 0
	}
	return false
}

// Card is a step carried to the run as it was saved (FB1b): a before- or after-action the run engine
// executes, with its config and its own name for the job log.
type Card struct {
	NodeID string         `json:"nodeId"`
	Type   string         `json:"type"`
	Name   string         `json:"name,omitempty"`
	Config map[string]any `json:"config,omitempty"`
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
	NodeID string   `json:"nodeId"`
	Port   string   `json:"port"`
	Check  Question `json:"check"`
}

// Branch is one path of a workflow with checks.
type Branch struct {
	Checks []BranchCheck `json:"checks"`
	Then   *Template     `json:"then,omitempty"` // the steps that convert, as a straight workflow
	End    string        `json:"end,omitempty"`  // "Leave the file alone": its words (Then is nil)
	Path   []string      `json:"path"`           // the node ids along it, input to the end
	// OnFailure: what the path does when its conversion doesn't work (V6b).
	OnFailure *Failures `json:"onFailure,omitempty"`
	// FB1b: the actions on the path before the conversion, in order; Fail: the path ends the job as
	// failed with End's words ("Stop as failed"), the original untouched.
	Before []Card `json:"before,omitempty"`
	Fail   bool   `json:"fail,omitempty"`
}

// SkipLeftByWorkflow is a file a workflow's "Leave the file alone" step ended (V6a).
const SkipLeftByWorkflow = "left_by_workflow"

// route is the first branch the file's answers lead down, with the decisions on the way (false: none;
// validation makes every answer lead somewhere, so only a broken Template gets there).
func route(branches []Branch, info media.Info, f Facts) (Branch, []Decision, bool) {
	for _, b := range branches {
		var ds []Decision
		ok := true
		for _, c := range b.Checks {
			yes, why := c.Check.Ask(info, f)
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
	switch {
	case b.Then == nil && b.Fail:
		p = Plan{Kind: KindBranches, Skip: skipWords(b.End), SkipCode: SkipLeftByWorkflow, Fail: true}
	case b.Then == nil:
		p = Plan{Kind: KindBranches, Skip: skipWords(b.End), SkipCode: SkipLeftByWorkflow}
	default:
		then := *b.Then
		then.Languages = languages
		p = then.Compile(info)
	}
	p.Path, p.OnFailure, p.Before = b.Path, b.OnFailure, b.Before
	return p
}
