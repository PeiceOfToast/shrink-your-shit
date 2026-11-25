// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The guided builder (slice 12, G1): a new workflow from a few questions. Answers are one value per
// question; Build turns them into an ordinary workflow (the Shrink videos steps, at most one "Check the
// file" pair in front and up to three cards after the save), and GuideOf reads a workflow back as answers
// with a coverage grade, so the questions can reopen any workflow. Nothing is stored beside the
// workflow: the steps are the truth, the answers are a reading of them. The Basic flow asks Answers'
// named fields; the Advanced flow asks every setting of every step (More, and the cards in Extra), driven
// by the step registry's schema, so a workflow Basic can't show (coverage none) opens in Advanced.

// Goals, the first question. Each one chooses the defaults of the rest.
const (
	GoalSmaller     = "smaller"      // small files that look the same
	GoalEveryDevice = "every_device" // files that play on every device
	GoalBoth        = "both"         // small, and plays on most devices
)

// What happens to the original (the Save the result step).
const (
	OriginalBesideLibrary  = "beside_library"   // a folder beside each library; the original stays
	OriginalOneFolder      = "one_folder"       // one folder for every new file; the original stays
	OriginalNextToOriginal = "next_to_original" // the new file next to the original
	OriginalReplace        = "replace"          // the original goes to the trash
)

// Coverage says how far a workflow is what the questions build.
const (
	CoverageFull    = "full"    // exactly the questions' steps
	CoveragePartial = "partial" // the questions' steps, plus others that are kept as they are
	CoverageNone    = "none"    // made in the steps in a way the questions can't show
)

// Answers are the questions' answers. A zero value means the goal's default (Fill); a string outside
// its choices is refused by Check.
type Answers struct {
	Goal               string   `json:"goal"`
	Quality            int      `json:"quality"`
	Slower             bool     `json:"slower"` // takes longer for smaller files
	Format             string   `json:"format"`
	Resolution         string   `json:"resolution"`
	Audio              string   `json:"audio"`
	AllLanguages       bool     `json:"allLanguages"` // keep every language; off keeps only my languages
	Languages          []string `json:"languages"`    // the step's own languages (G2); empty = my languages in Settings
	Subtitles          string   `json:"subtitles"`
	SubtitleLanguages  []string `json:"subtitleLanguages"` // empty = the same as the sound
	FileType           string   `json:"fileType"`
	Original           string   `json:"original"`
	Folder             string   `json:"folder"` // with one_folder
	IfBigger           string   `json:"ifBigger"`
	Skip               Skip     `json:"skip"`
	IncludeDolbyVision bool     `json:"includeDolbyVision"`
	After              After    `json:"after"`
	// More are the steps' settings the Basic questions don't ask (the Advanced flow's), by step type, as the
	// step stores them: Convert video's rate control, hardware, black bars, bit depth, frame rate; Audio's even
	// loudness; Save the result's file names; Library files' ignore list. Basic reads them back and writes them
	// unchanged, so switching between the flows loses nothing. A nil value takes a setting away.
	More map[string]map[string]any `json:"more,omitempty"`
	// LanguagesEverywhere also makes Languages the person's languages in Settings. The server does it;
	// it is not part of the workflow and never reads back.
	LanguagesEverywhere bool `json:"languagesEverywhere,omitempty"`
}

// Skip are the optional conditions a file must meet, or it is left alone. 0 is "any".
type Skip struct {
	MinSizeGB     float64 `json:"minSizeGB"`
	MinResolution int     `json:"minResolution"` // 0, 720, 1080, 2160
	MinMinutes    int     `json:"minMinutes"`
}

// After are the cards after the save.
type After struct {
	MediaServer  bool   `json:"mediaServer"`
	Downloader   bool   `json:"downloader"`
	Notify       bool   `json:"notify"`
	NotifyTarget string `json:"notifyTarget"` // a target's name, as the card has it; "" = every target that is on
}

// Guide is a workflow read as answers.
type Guide struct {
	Coverage string   `json:"coverage"`
	Answers  *Answers `json:"answers"`       // nil when the coverage is none
	Extra    []string `json:"extra"`         // the steps the questions don't show, by name, kept as they are
	Why      string   `json:"why,omitempty"` // coverage none: what Basic can\'t show, so the workflow opens in Advanced
}

// DefaultAnswers are the answers a goal chooses. Every default keeps the original and asks for nothing
// afterwards; "Replace the original" is never a default.
func DefaultAnswers(goal string) Answers {
	a := Answers{Goal: GoalSmaller, Quality: 3, Format: "hevc", Resolution: "keep", Audio: "stereo_aac_surround",
		Subtitles: "keep_all", FileType: "mkv", Original: OriginalBesideLibrary, IfBigger: "keep"}
	switch goal {
	case GoalEveryDevice:
		a.Goal, a.Format, a.Audio, a.Subtitles, a.FileType = goal, "h264", "stereo_aac", "text_only", "mp4"
	case GoalBoth:
		a.Goal, a.Audio, a.Subtitles, a.FileType = goal, "stereo_aac", "text_only", "mp4"
	}
	return a
}

// Fill is a with every unset value taken from its goal's defaults.
func Fill(a Answers) Answers {
	if a.Goal == "" {
		a.Goal = GoalSmaller
	}
	d := DefaultAnswers(a.Goal)
	pick := func(v *string, def string) {
		if *v == "" {
			*v = def
		}
	}
	if a.Quality == 0 {
		a.Quality = d.Quality
	}
	pick(&a.Format, d.Format)
	pick(&a.Resolution, d.Resolution)
	pick(&a.Audio, d.Audio)
	pick(&a.Subtitles, d.Subtitles)
	pick(&a.FileType, d.FileType)
	pick(&a.Original, d.Original)
	pick(&a.IfBigger, d.IfBigger)
	a.Languages, a.SubtitleLanguages = orEmpty(a.Languages), orEmpty(a.SubtitleLanguages)
	return a
}

// Check refuses an answer outside its choices. Each issue names the answer (Field, the JSON key) so the
// screen shows it on that question.
func Check(a Answers) []Issue {
	a = Fill(a)
	var out []Issue
	bad := func(field, msg string) {
		out = append(out, Issue{Code: "invalid_answer", Severity: "error", Field: field, Message: msg})
	}
	in := func(field, v string, choices ...string) {
		if !slices.Contains(choices, v) {
			bad(field, "'"+v+"' is not a choice here.")
		}
	}
	in("goal", a.Goal, GoalSmaller, GoalEveryDevice, GoalBoth)
	if a.Quality < 1 || a.Quality > 5 {
		bad("quality", "Quality is a position from 1 to 5.")
	}
	in("format", a.Format, "hevc", "h264", "av1", "vp9")
	in("resolution", a.Resolution, "keep", "1080", "720", "480")
	in("audio", a.Audio, "stereo_aac", "stereo_aac_surround", "eac3_surround", "ac3_surround", "stereo_opus", "as_is")
	in("subtitles", a.Subtitles, "keep_all", "my_languages", "text_only", "srt")
	in("fileType", a.FileType, "mkv", "mp4", "webm", "keep")
	in("original", a.Original, OriginalBesideLibrary, OriginalOneFolder, OriginalNextToOriginal, OriginalReplace)
	if a.Original == OriginalOneFolder && strings.TrimSpace(a.Folder) == "" {
		bad("folder", "Name the folder the new files go to.")
	}
	in("ifBigger", a.IfBigger, "keep", "replace")
	if a.Skip.MinSizeGB < 0 || a.Skip.MinSizeGB > 100000 {
		bad("skip.minSizeGB", "A size in GB, 0 for any.")
	}
	if !slices.Contains([]int{0, 720, 1080, 2160}, a.Skip.MinResolution) {
		bad("skip.minResolution", "Any, 720p, 1080p or 4K.")
	}
	if a.Skip.MinMinutes < 0 || a.Skip.MinMinutes > 100000 {
		bad("skip.minMinutes", "A running time in minutes, 0 for any.")
	}
	// until G2 gives the steps their own languages key, a choice here can only be the person's languages in
	// Settings: audio languages need "also use these everywhere", subtitle languages wait (review-715 P2-1)
	if !hasConfigKey(TypeAudio, "languages") && len(a.Languages) > 0 && !a.LanguagesEverywhere {
		bad("languages", "For now the languages you choose apply everywhere: tick 'Also use these as my languages everywhere', or leave them to Settings.")
	}
	if !hasConfigKey(TypeSubtitles, "languages") && len(a.SubtitleLanguages) > 0 {
		bad("subtitleLanguages", "Subtitles' own languages come in a later update. For now they follow the sound's.")
	}
	for field, codes := range map[string][]string{"languages": a.Languages, "subtitleLanguages": a.SubtitleLanguages} {
		if len(codes) > 20 {
			bad(field, "At most 20 languages.")
		}
		for _, c := range codes {
			if rules.CanonLang(c) == "" { // the same check Settings and a step's own list use
				bad(field, "'"+c+"' is not a language code (use codes like eng, fre, ger, or en, fr, de).")
				break
			}
		}
	}
	slices.SortFunc(out, func(x, y Issue) int { return strings.Compare(x.Field, y.Field) })
	return out
}

// Build is a new workflow from a: the preset with the answers applied.
func Build(a Answers) Doc { return Apply(Preset(), a) }

// Apply writes a into d's steps and returns the result. It changes only what the questions own: the
// Shrink videos steps' answered settings, the guide's own "Check the file" pair in front and its cards
// after the save. Every other step, link and setting stays as it is. d must read as full or partial
// (GuideOf); on a workflow that reads as none, d is returned unchanged.
func Apply(d Doc, a Answers) Doc {
	a = Fill(a)
	w, ok := walk(d)
	if !ok {
		return d
	}
	d = clone(d)
	set := func(id string, f func(c map[string]any)) {
		for i := range d.Nodes {
			if d.Nodes[i].ID == id {
				if d.Nodes[i].Config == nil {
					d.Nodes[i].Config = map[string]any{}
				}
				f(d.Nodes[i].Config)
			}
		}
	}
	set(w.by[TypeSkips], func(c map[string]any) { c["includeDolbyVision"] = a.IncludeDolbyVision })
	set(w.by[TypeConvert], func(c map[string]any) {
		c["quality"], c["format"], c["resolution"] = a.Quality, a.Format, a.Resolution
		switch {
		case a.Slower:
			c["speed"] = "slower"
		case strOf(c, "speed", "") != "faster": // faster is the steps' own choice; the questions don't take it away
			c["speed"] = "normal"
		}
	})
	set(w.by[TypeAudio], func(c map[string]any) {
		c["audio"], c["onlyMyLanguages"] = a.Audio, !a.AllLanguages
		setLanguages(TypeAudio, c, a.Languages)
	})
	set(w.by[TypeSubtitles], func(c map[string]any) {
		c["subtitles"] = a.Subtitles
		setLanguages(TypeSubtitles, c, a.SubtitleLanguages)
	})
	set(w.by[TypeContainer], func(c map[string]any) { c["fileType"] = a.FileType })
	set(w.by[TypeCommit], func(c map[string]any) {
		delete(c, "folderMode")
		delete(c, "folder")
		switch a.Original {
		case OriginalBesideLibrary:
			c["policy"], c["folderMode"] = "separate_folder", "next_to_library"
		case OriginalOneFolder:
			c["policy"], c["folderMode"], c["folder"] = "separate_folder", "one_folder", a.Folder
		case OriginalNextToOriginal:
			c["policy"] = "beside"
		case OriginalReplace:
			c["policy"] = "replace"
		}
		c["ifBigger"] = a.IfBigger
	})
	for typ, more := range a.More {
		if id := w.by[typ]; id != "" {
			set(id, func(c map[string]any) {
				for k, v := range more {
					if basicKeys[typ][k] { // a Basic answer is written by its question, never through More
						continue
					}
					if v == nil {
						delete(c, k)
					} else {
						c[k] = v
					}
				}
			})
		}
	}
	d = applySkip(d, w, a.Skip)
	return applyAfter(d, w, a.After)
}

// basicKeys are the settings the Basic questions own, by step type; everything else on these steps is More.
var basicKeys = map[string]map[string]bool{
	TypeSkips:     {"includeDolbyVision": true},
	TypeConvert:   {"quality": true, "format": true, "resolution": true, "speed": true, "kind": true},
	TypeAudio:     {"audio": true, "onlyMyLanguages": true, "languages": true},
	TypeSubtitles: {"subtitles": true, "languages": true},
	TypeContainer: {"fileType": true},
	TypeCommit:    {"policy": true, "folderMode": true, "folder": true, "ifBigger": true},
	TypeInput:     {},
}

// moreOf are a step's settings outside the Basic questions that differ from their defaults, or nil when
// there are none: what the Advanced flow asks, and what Basic's notice names.
func moreOf(typ string, c map[string]any) map[string]any {
	var out map[string]any
	def, _ := Definition(typ)
	props, _ := def.ConfigSchema["properties"].(map[string]any)
	for k, v := range c {
		if basicKeys[typ][k] {
			continue
		}
		switch x := v.(type) {
		case []string: // an empty list is the same as none
			if len(x) == 0 {
				continue
			}
		case []any:
			if len(x) == 0 {
				continue
			}
		case map[string]any:
		default:
			if field, ok := props[k].(map[string]any); ok && sameValue(field["default"], v) {
				continue
			}
		}
		if out == nil {
			out = map[string]any{}
		}
		out[k] = v
	}
	return out
}

// setLanguages writes the step's own languages (G2's key) when the step has it, and never a key the step
// doesn't know: a workflow saved before the key existed stays clean of unknown settings.
func setLanguages(typ string, c map[string]any, codes []string) {
	if !hasConfigKey(typ, "languages") {
		return
	}
	if len(codes) == 0 {
		delete(c, "languages")
		return
	}
	c["languages"] = slices.Clone(codes)
}

func hasConfigKey(typ, key string) bool {
	def, ok := Definition(typ)
	if !ok {
		return false
	}
	props, _ := def.ConfigSchema["properties"].(map[string]any)
	_, has := props[key]
	return has
}

// applySkip puts the guide's "Check the file" pair in front of the conversion, or takes it out.
func applySkip(d Doc, w walked, s Skip) Doc {
	want := s.MinSizeGB > 0 || s.MinResolution > 0 || s.MinMinutes > 0
	config := map[string]any{"size": int(math.Round(s.MinSizeGB * 1024)), "resolution": s.MinResolution, "minMinutes": s.MinMinutes}
	switch {
	case want && w.check != "":
		for i := range d.Nodes {
			if d.Nodes[i].ID == w.check {
				d.Nodes[i].Config = config
			}
		}
	case want:
		check := Node{ID: NewID(), Type: TypeCheck, TypeVersion: 1, Enabled: true, Config: config}
		end := Node{ID: NewID(), Type: TypeEnd, TypeVersion: 1, Enabled: true, Config: map[string]any{"reason": SkipReason}}
		for i := range d.Edges { // the input's link now goes to the check; the check's Yes goes on to the old target
			if d.Edges[i].From == w.by[TypeInput] && d.Edges[i].FromPort == "success" {
				d.Edges = append(d.Edges, Edge{ID: NewID(), From: check.ID, FromPort: "match", To: d.Edges[i].To, ToPort: "in"})
				d.Edges[i].To = check.ID
			}
		}
		d.Edges = append(d.Edges, Edge{ID: NewID(), From: check.ID, FromPort: "else", To: end.ID, ToPort: "in"})
		d.Nodes = append(d.Nodes, check, end)
	case w.check != "":
		var next string
		for _, e := range d.Edges {
			if e.From == w.check && e.FromPort == "match" {
				next = e.To
			}
		}
		d.Nodes = slices.DeleteFunc(d.Nodes, func(n Node) bool { return n.ID == w.check || n.ID == w.checkEnd })
		d.Edges = slices.DeleteFunc(d.Edges, func(e Edge) bool { return e.From == w.check || e.To == w.check || e.To == w.checkEnd })
		d.Edges = append(d.Edges, Edge{ID: NewID(), From: w.by[TypeInput], FromPort: "success", To: next, ToPort: "in"})
	}
	return d
}

// SkipReason is what the file's history says when the guide's conditions leave it alone.
const SkipReason = "Left alone by the workflow's conditions."

// afterTypes are the cards the questions put after the save, in their order.
var afterTypes = []string{TypeMediaServer, TypeDownloader, TypeNotify}

// applyAfter rebuilds the guide's run of cards after the save: the ones asked for, in order, each keeping
// its node (and settings) when it was there, then whatever followed the run before.
func applyAfter(d Doc, w walked, a After) Doc {
	want := map[string]bool{TypeMediaServer: a.MediaServer, TypeDownloader: a.Downloader, TypeNotify: a.Notify}
	have := map[string]Node{}
	for _, n := range w.after {
		have[n.Type] = n
	}
	owned := map[string]bool{}
	for _, n := range w.after {
		owned[n.ID] = true
	}
	var run []Node
	for _, typ := range afterTypes {
		if !want[typ] {
			continue
		}
		n, ok := have[typ]
		if !ok {
			n = Node{ID: NewID(), Type: typ, TypeVersion: 1, Enabled: true, Config: map[string]any{"which": "all"}}
			if typ == TypeNotify {
				n.Config = map[string]any{"kind": "good", "text": "{{title}} is done: {{saved_percent}}% smaller"}
			}
		}
		if typ == TypeNotify {
			n.Config["target"] = a.NotifyTarget
		}
		run = append(run, n)
	}
	keep := map[string]bool{}
	for _, n := range run {
		keep[n.ID] = true
	}
	d.Nodes = slices.DeleteFunc(d.Nodes, func(n Node) bool { return owned[n.ID] && !keep[n.ID] })
	d.Edges = slices.DeleteFunc(d.Edges, func(e Edge) bool {
		return (owned[e.From] && !keep[e.From]) || (owned[e.To] && !keep[e.To]) ||
			(e.From == w.by[TypeCommit] && e.FromPort == "success") || (owned[e.From] && e.FromPort == "success")
	})
	for _, n := range run {
		if !slices.ContainsFunc(d.Nodes, func(x Node) bool { return x.ID == n.ID }) {
			d.Nodes = append(d.Nodes, n)
		} else {
			for i := range d.Nodes {
				if d.Nodes[i].ID == n.ID {
					d.Nodes[i].Config = n.Config
				}
			}
		}
	}
	prev := w.by[TypeCommit]
	for _, n := range run {
		d.Edges = append(d.Edges, Edge{ID: NewID(), From: prev, FromPort: "success", To: n.ID, ToPort: "in"})
		prev = n.ID
	}
	if w.afterTail != "" {
		d.Edges = append(d.Edges, Edge{ID: NewID(), From: prev, FromPort: "success", To: w.afterTail, ToPort: "in"})
	}
	return d
}

// walked is a workflow's shape as the guide reads it.
type walked struct {
	by        map[string]string // the Shrink videos steps' ids by type
	check     string            // the guide's own "Check the file", or ""
	checkEnd  string            // its "Leave the file alone"
	after     []Node            // the guide's cards after the save, in order
	afterTail string            // what follows them (a step the questions don't show), or ""
	extra     []string          // the steps the questions don't show, by name
	nodes     []Node            // the main path, in order
	why       string            // set when the workflow can't be read
}

// walk follows the main path (the success links) from Library files to Save the result and on.
func walk(d Doc) (walked, bool) {
	w := walked{by: map[string]string{}}
	byID := map[string]Node{}
	succ, match, elseTo := map[string]string{}, map[string]string{}, map[string]string{}
	for _, n := range d.Nodes {
		byID[n.ID] = n
		if n.Type == TypeLegacy {
			return w.fail("it keeps an earlier install's settings")
		}
	}
	linkedElsewhere := map[string]bool{} // a card with a link off another port (W9's error link) is hand-made: not the guide's
	for _, e := range d.Edges {
		if e.FromPort != "success" {
			linkedElsewhere[e.From] = true
		}
		if e.ToPort != "in" {
			continue
		}
		switch e.FromPort {
		case "success":
			succ[e.From] = e.To
		case "match":
			match[e.From] = e.To
		case "else":
			elseTo[e.From] = e.To
		}
	}
	var input string
	for _, n := range d.Nodes {
		if n.Type == TypeInput {
			if input != "" {
				return w.fail("it has more than one Library files step")
			}
			input = n.ID
		}
	}
	if input == "" {
		return w.fail("it has no Library files step")
	}
	w.by[TypeInput] = input
	w.nodes = append(w.nodes, byID[input])
	cur := succ[input]
	// the guide's own pair: a check right after the input, with only the guide's conditions, whose No leaves the file alone
	if n, ok := byID[cur]; ok && n.Type == TypeCheck && ownedCheck(n) && incoming(d, n.ID) == 1 {
		// its End is the guide's only when nothing else leads into it (review2-715 P2-2): a hand-made link
		// into the pair would go with it
		if end, ok := byID[elseTo[n.ID]]; ok && end.Type == TypeEnd && succ[end.ID] == "" && incoming(d, end.ID) == 1 {
			w.check, w.checkEnd = n.ID, end.ID
			w.nodes = append(w.nodes, n)
			cur = match[n.ID]
		}
	}
	next := 1 // the next Shrink videos step expected, in PresetOrder
	seen := map[string]bool{input: true}
	for cur != "" && next < len(PresetOrder) {
		n, ok := byID[cur]
		if !ok || seen[cur] {
			return w.fail("its links don't lead from Library files to Save the result")
		}
		seen[cur] = true
		w.nodes = append(w.nodes, n)
		def, _ := Definition(n.Type)
		switch {
		case n.Type == PresetOrder[next]:
			if !n.Enabled {
				return w.fail("'" + nodeName(n) + "' is turned off")
			}
			w.by[n.Type] = n.ID
			next++
		case slices.Contains(PresetOrder, n.Type):
			return w.fail("its conversion steps are not in the usual order")
		case def.RunsAs == RunsAction:
			w.extra = append(w.extra, nodeName(n))
		default:
			return w.fail("'" + nodeName(n) + "' sits where the questions can't show it")
		}
		cur = succ[cur]
	}
	if next < len(PresetOrder) {
		d, _ := Definition(PresetOrder[next])
		return w.fail("it has no '" + d.Name + "' step")
	}
	// a Basic answer taken from a variable ({{name}}) can't be shown as a choice; the folder is a template by design
	for typ, id := range w.by {
		for k, v := range byID[id].Config {
			if s, ok := v.(string); ok && basicKeys[typ][k] && k != "folder" && strings.Contains(s, "{{") {
				return w.fail("'" + nodeName(byID[id]) + "' takes a setting from a variable")
			}
		}
	}
	// after the save: the guide's cards (one of each kind), then anything else
	ownedKinds := map[string]bool{}
	for cur != "" {
		n, ok := byID[cur]
		if !ok || seen[cur] {
			break
		}
		seen[cur] = true
		if slices.Contains(afterTypes, n.Type) && n.Enabled && w.afterTail == "" && !ownedKinds[n.Type] && !linkedElsewhere[n.ID] {
			ownedKinds[n.Type] = true
			w.after = append(w.after, n)
			w.nodes = append(w.nodes, n)
			cur = succ[cur]
			continue
		}
		if w.afterTail == "" {
			w.afterTail = n.ID
		}
		w.extra = append(w.extra, nodeName(n))
		w.nodes = append(w.nodes, n)
		cur = succ[cur]
	}
	return w, true
}

func (w walked) fail(why string) (walked, bool) {
	w.why = why
	return w, false
}

// incoming is how many links lead into id.
func incoming(d Doc, id string) int {
	n := 0
	for _, e := range d.Edges {
		if e.To == id {
			n++
		}
	}
	return n
}

// ownedCheck says n has only the guide's conditions set, as numbers: a {{variable}} there is the steps'
// own (review2-715 P2-1), never read as 0.
func ownedCheck(n Node) bool {
	for k, v := range n.Config {
		switch k {
		case "size", "resolution", "minMinutes":
			switch v.(type) {
			case float64, int, nil:
				continue
			}
			return false
		}
		switch x := v.(type) {
		case string:
			if x != "" {
				return false
			}
		case bool:
			if x {
				return false
			}
		case float64:
			if x != 0 {
				return false
			}
		case int:
			if x != 0 {
				return false
			}
		case []any:
			if len(x) > 0 {
				return false
			}
		case []string:
			if len(x) > 0 {
				return false
			}
		case nil:
		default:
			return false
		}
	}
	return true
}

// GuideOf reads d as answers. Coverage full: d is what the questions build; partial: that, plus steps
// and settings they don't show (Extra), kept as they are by Apply; none: the questions can't show it,
// and Why says what stops them.
func GuideOf(d Doc) Guide {
	w, ok := walk(d)
	if !ok {
		return Guide{Coverage: CoverageNone, Extra: []string{}, Why: "Basic can't show this workflow: " + w.why + ". Open it in Advanced."}
	}
	byID := map[string]Node{}
	for _, n := range d.Nodes {
		byID[n.ID] = n
	}
	cv, au, su, co, cm, sk := byID[w.by[TypeConvert]].Config, byID[w.by[TypeAudio]].Config, byID[w.by[TypeSubtitles]].Config,
		byID[w.by[TypeContainer]].Config, byID[w.by[TypeCommit]].Config, byID[w.by[TypeSkips]].Config
	a := Answers{Quality: intOf(cv, "quality", 3), Slower: strOf(cv, "speed", "normal") == "slower", Format: strOf(cv, "format", "hevc"),
		Resolution: strOf(cv, "resolution", "keep"), Audio: strOf(au, "audio", "stereo_aac"), AllLanguages: !boolOf(au, "onlyMyLanguages", true),
		Languages: guideCodes(au["languages"]), Subtitles: strOf(su, "subtitles", "keep_all"), SubtitleLanguages: guideCodes(su["languages"]),
		FileType: strOf(co, "fileType", "mkv"), IfBigger: strOf(cm, "ifBigger", "replace"), IncludeDolbyVision: boolOf(sk, "includeDolbyVision", false)}
	switch strOf(cm, "policy", "separate_folder") {
	case "replace":
		a.Original = OriginalReplace
	case "beside":
		a.Original = OriginalNextToOriginal
	default:
		if strOf(cm, "folderMode", "next_to_library") == "one_folder" {
			a.Original, a.Folder = OriginalOneFolder, strOf(cm, "folder", "")
		} else {
			a.Original = OriginalBesideLibrary
		}
	}
	// the goal isn't stored: it is read from what it chose
	a.Goal = GoalSmaller
	if a.Format == "h264" {
		a.Goal = GoalEveryDevice
	} else if a.FileType == "mp4" {
		a.Goal = GoalBoth
	}
	if w.check != "" {
		c := byID[w.check].Config
		a.Skip = Skip{MinSizeGB: float64(intOf(c, "size", 0)) / 1024, MinResolution: intOf(c, "resolution", 0), MinMinutes: intOf(c, "minMinutes", 0)}
	}
	for _, n := range w.after {
		switch n.Type {
		case TypeMediaServer:
			a.After.MediaServer = true
		case TypeDownloader:
			a.After.Downloader = true
		case TypeNotify:
			a.After.Notify, a.After.NotifyTarget = true, strOf(n.Config, "target", "")
		}
	}
	for typ, id := range w.by {
		if more := moreOf(typ, byID[id].Config); more != nil {
			if a.More == nil {
				a.More = map[string]map[string]any{}
			}
			a.More[typ] = more
		}
	}
	g := Guide{Coverage: CoverageFull, Answers: ptr(a), Extra: orEmpty(w.extra)}
	if len(w.extra) > 0 {
		g.Coverage = CoveragePartial
	}
	return g
}

func guideCodes(v any) []string {
	out := []string{}
	switch x := v.(type) {
	case []string:
		out = append(out, x...)
	case []any:
		for _, c := range x {
			if s, ok := c.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// Steps are the main path's step names, in order, for the preview.
func Steps(d Doc) []string {
	w, ok := walk(d)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(w.nodes))
	for _, n := range w.nodes {
		out = append(out, nodeName(n))
	}
	return out
}

var formatNames = map[string]string{"hevc": "HEVC", "h264": "H.264", "av1": "AV1", "vp9": "VP9"}

// Summary says in sentences what the answers build: the last page of the questions, and the live summary.
func Summary(a Answers) []string {
	a = Fill(a)
	var out []string
	s := "Shrink videos to " + formatNames[a.Format] + " at quality " + strconv.Itoa(a.Quality)
	if a.Slower {
		s += ", taking longer for smaller files"
	}
	if a.Resolution == "keep" {
		s += ", keeping the picture size."
	} else {
		s += ", at most " + a.Resolution + "p."
	}
	out = append(out, s)
	langs := func(codes []string) string {
		if a.AllLanguages {
			return "in every language"
		}
		if len(codes) == 0 {
			return "in your languages from Settings"
		}
		return "in " + strings.Join(codes, ", ")
	}
	switch a.Audio {
	case "stereo_aac":
		s = "Make a stereo track that plays everywhere; surround becomes stereo"
	case "stereo_aac_surround":
		s = "Keep a stereo track and the surround track as it is"
	case "eac3_surround", "ac3_surround":
		s = "Make surround sound for sound bars"
	case "stereo_opus":
		s = "Make the smallest stereo track"
	default:
		s = "Leave the sound as it is"
	}
	out = append(out, s+", "+langs(a.Languages)+"; tracks with no language set are kept.")
	subs := a.SubtitleLanguages
	if len(subs) == 0 {
		subs = a.Languages
	}
	switch a.Subtitles {
	case "my_languages":
		s = "Keep only the subtitles " + langs(subs) + "."
	case "text_only":
		s = "Keep text subtitles only; picture subtitles are left out."
	case "srt":
		s = "Save the subtitles " + langs(subs) + " as separate .srt files."
	default:
		s = "Keep every subtitle."
	}
	out = append(out, s)
	ft := map[string]string{"mkv": "MKV", "mp4": "MP4", "webm": "WebM", "keep": "its current type"}[a.FileType]
	switch a.Original {
	case OriginalOneFolder:
		s = "Save as " + ft + " in " + a.Folder + "; the original stays where it is"
	case OriginalNextToOriginal:
		s = "Save as " + ft + " next to the original, which stays where it is"
	case OriginalReplace:
		s = "Save as " + ft + " and replace the original; the original is kept in the trash for the days set in Settings > Safety"
	default:
		s = "Save as " + ft + " in a folder beside each library; the original stays where it is"
	}
	if a.IfBigger == "keep" {
		s += ". If the new file is bigger, the original is kept."
	} else {
		s += ". The new file is used even if it is bigger."
	}
	out = append(out, s)
	s = "Files already " + formatNames[a.Format] + ", AV1 or VP9 are left alone"
	if a.IncludeDolbyVision {
		s += "; Dolby Vision files become HDR10."
	} else {
		s += ", and so are Dolby Vision files."
	}
	out = append(out, s)
	var conds []string
	if a.Skip.MinSizeGB > 0 {
		conds = append(conds, "smaller than "+strconv.FormatFloat(a.Skip.MinSizeGB, 'f', -1, 64)+" GB")
	}
	if a.Skip.MinResolution > 0 {
		conds = append(conds, "under "+map[int]string{720: "720p", 1080: "1080p", 2160: "4K"}[a.Skip.MinResolution])
	}
	if a.Skip.MinMinutes > 0 {
		conds = append(conds, "shorter than "+strconv.Itoa(a.Skip.MinMinutes)+" minutes")
	}
	if len(conds) > 0 {
		out = append(out, "Files "+strings.Join(conds, ", or ")+" are left alone.")
	}
	var tells []string
	if a.After.MediaServer {
		tells = append(tells, "tell the media server")
	}
	if a.After.Downloader {
		tells = append(tells, "tell the downloader")
	}
	if a.After.Notify {
		if a.After.NotifyTarget != "" {
			tells = append(tells, "send a message to "+a.After.NotifyTarget)
		} else {
			tells = append(tells, "send a notification")
		}
	}
	if len(tells) > 0 {
		out = append(out, "Afterwards, "+strings.Join(tells, ", ")+".")
	}
	return out
}
