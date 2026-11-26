// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The guided builder (slice 12, G1 and G1b): a new workflow from questions. Answers are one value per
// question; Build turns them into an ordinary workflow, and GuideOf reads any workflow back as answers
// with a coverage grade. Nothing is stored beside the workflow: the steps are the truth, the answers are
// a reading of them. The Basic flow asks Answers' named fields (the Shrink videos steps' answered
// settings, one "Check the file" pair in front, up to three cards after the save). The Advanced flow asks
// every setting of every step (More, driven by the step registry's schema) and the lists around the
// conversion (Before, AfterCards, ConvertFails, CheckFails, Variables; guide_cards.go). Basic's Skip and
// After are views of Before and AfterCards: GuideOf fills both, Apply takes the lists when they are given.

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

// Coverage says how far a workflow is what a flow's questions show.
const (
	CoverageFull    = "full"    // exactly the questions' steps
	CoveragePartial = "partial" // the questions' steps, plus others that are kept as they are (Basic only)
	CoverageNone    = "none"    // the questions can't show it
)

// Answers are the questions' answers. A zero value means the goal's default (Fill); a string outside
// its choices is refused by Check. The lists (Before, AfterCards) and Fails are the Advanced flow's: nil
// means "not answered" and the Basic views (Skip, After) decide; an empty list means "none".
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
	// Before are the conditions and actions in front of the conversion, in order; AfterCards the cards
	// after the save; ConvertFails and CheckFails what the conversion does when those steps don't work;
	// Variables the workflow's own variables (the Advanced flow, G1b).
	Before       []Card     `json:"before,omitempty"`
	AfterCards   []Card     `json:"afterCards,omitempty"`
	ConvertFails *Fails     `json:"convertFails,omitempty"`
	CheckFails   *Fails     `json:"checkFails,omitempty"`
	Variables    []Variable `json:"variables,omitempty"`
	// Flow says which questions are saving: "basic" writes the views (Skip, After) over the workflow and
	// leaves the lists as they are; "advanced" writes the lists and ignores the views. "" takes the lists
	// when they are given, else the views. GuideOf fills both, so a screen sends back what it got with its
	// own flow named and its edits win.
	Flow string `json:"flow,omitempty"`
	// LanguagesEverywhere also makes Languages the person's languages in Settings. The server does it;
	// it is not part of the workflow and never reads back.
	LanguagesEverywhere bool `json:"languagesEverywhere,omitempty"`
}

// The flows.
const (
	FlowBasic    = "basic"
	FlowAdvanced = "advanced"
)

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
	Coverage    string   `json:"coverage"`      // the Basic flow's
	Answers     *Answers `json:"answers"`       // nil when neither flow can show the workflow
	Extra       []string `json:"extra"`         // the steps Basic doesn't show, by name, kept as they are
	Why         string   `json:"why,omitempty"` // coverage none: what Basic can't show, so the workflow opens in Advanced
	Advanced    string   `json:"advanced"`      // the Advanced flow's coverage: full or none
	AdvancedWhy string   `json:"advancedWhy,omitempty"`
	// Unreadable names the steps whose stretches the questions can't show (Advanced): those stretches are
	// kept as they are, and the rest of the workflow is answered.
	Unreadable []string `json:"unreadable"`
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
	if a.Flow != "" && a.Flow != FlowBasic && a.Flow != FlowAdvanced {
		bad("flow", "'"+a.Flow+"' is not a flow.")
	}
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
	out = append(out, checkCards(a)...)
	slices.SortFunc(out, func(x, y Issue) int { return strings.Compare(x.Field, y.Field) })
	return out
}

// checkCards refuses a card the lists can't hold: a kind that can't sit there, a condition without its
// end, an end that isn't one. The cards' own settings are the validator's (every save runs it).
func checkCards(a Answers) []Issue {
	var out []Issue
	bad := func(field, msg string) {
		out = append(out, Issue{Code: "invalid_answer", Severity: "error", Field: field, Message: msg})
	}
	for i, c := range a.Before {
		field := "before." + strconv.Itoa(i)
		switch {
		case IsQuestion(c.Type) && slices.Contains(definitionPhase(c.Type), PhaseBefore):
			if c.Then == nil {
				bad(field+".then", "Say what happens to the files this condition turns away.")
			} else if _, ok := thenEnds[c.Then.End]; !ok {
				bad(field+".then.end", "'"+c.Then.End+"' is not a choice here.")
			} else {
				for j, ac := range c.Then.Actions {
					if !IsAction(ac.Type) || !slices.Contains(definitionPhase(ac.Type), PhaseBefore) {
						bad(field+".then.actions."+strconv.Itoa(j), "'"+nodeName(Node{Type: ac.Type})+"' can't run there.")
					}
				}
			}
		case IsAction(c.Type) && slices.Contains(definitionPhase(c.Type), PhaseBefore):
		default:
			bad(field, "'"+nodeName(Node{Type: c.Type})+"' can't sit before the steps that convert.")
		}
	}
	var after func(field string, cards []Card)
	after = func(field string, cards []Card) {
		for i, c := range cards {
			f := field + "." + strconv.Itoa(i)
			switch {
			case IsQuestion(c.Type) && slices.Contains(definitionPhase(c.Type), PhaseAfter):
				after(f+".yes", c.Yes)
				after(f+".no", c.No)
			case IsAction(c.Type) && slices.Contains(definitionPhase(c.Type), PhaseAfter):
				if def, _ := Definition(c.Type); len(c.OnError) > 0 && !hasPort(def.Outputs, "error") {
					bad(f+".onError", "'"+nodeName(Node{Type: c.Type})+"' has no 'didn't work' outcome.")
				}
				after(f+".onError", c.OnError)
			default:
				bad(f, "'"+nodeName(Node{Type: c.Type})+"' can't sit after the save.")
			}
		}
	}
	after("afterCards", a.AfterCards)
	for field, f := range map[string]*Fails{"convertFails": a.ConvertFails, "checkFails": a.CheckFails} {
		if f == nil {
			continue
		}
		switch f.End {
		case "none", "leave", "stop":
		case "retry":
			if f.Max < 1 || f.Max > 10 || f.BackoffSeconds < 0 || f.BackoffSeconds > 86400 || (f.Then != "skip" && f.Then != "stop") {
				bad(field, "Try 1 to 10 times, wait up to a day between tries, then stop or leave the file alone.")
			}
		default:
			bad(field+".end", "'"+f.End+"' is not a choice here.")
		}
	}
	return out
}

// Build is a new workflow from a: the preset with the answers applied.
func Build(a Answers) Doc { return Apply(Preset(), a) }

// Apply writes a into d's steps and returns the result. It changes only what the questions own: the
// Shrink videos steps' answered settings, the guide's own "Check the file" pair in front and its cards
// after the save (Basic), or the whole stretches in front and after the save when the lists are given
// (Advanced). Every other step, link and setting stays as it is. On a workflow neither flow can read, d
// is returned unchanged.
func Apply(d Doc, a Answers) Doc {
	a = Fill(a)
	w, ok := walk(d)
	lists := a.Flow == FlowAdvanced || (a.Flow == "" && (a.Before != nil || a.AfterCards != nil))
	// the main path can't be read, or the Basic flow can't and the Advanced lists weren't asked for: nothing
	// changes. Stretches the Advanced flow can't show are kept as they are (w.unread), the rest is written.
	if w.hard || (!ok && !lists && w.advancedWhy != "") {
		return d
	}
	orig := d
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
	if lists {
		if a.Before != nil {
			d = applyBefore(d, w, a.Before)
		}
		if a.AfterCards != nil && !w.afterSkip {
			d = applyAfterCards(d, w, a.AfterCards)
		}
		if !w.convertFailSkip { // an error link the questions can't show is kept as it is, even when sent
			d = applyFails(d, w.by[TypeConvert], a.ConvertFails)
		}
		if !w.checkFailSkip {
			d = applyFails(d, w.by[TypeVerify], a.CheckFails)
		}
		if a.Variables != nil {
			d.Variables = clone(a.Variables)
		}
	} else if ok {
		d = applySkip(d, w, a.Skip)
		if !w.afterSkip { // a stretch the questions can't show is kept as it is, Basic too
			d = applyAfter(d, w, a.After)
		}
	}
	return inOrder(orig, d)
}

// ChangesUnreadable says a Basic answer would change the stretch after Save that the questions can't show: its
// After is not the one the workflow reads back, so Apply would add or drop a card there. Such a save is refused
// (nothing is saved) rather than written without the change. Advanced answers and anything Apply can keep are not.
func ChangesUnreadable(d Doc, a Answers) bool {
	a = Fill(a)
	w, ok := walk(d)
	lists := a.Flow == FlowAdvanced || (a.Flow == "" && (a.Before != nil || a.AfterCards != nil))
	if !ok || w.hard || lists || !w.afterSkip {
		return false
	}
	g := GuideOf(d)
	return g.Answers != nil && afterKey(g.Answers.After) != afterKey(a.After)
}

// afterKey is the after-save answers as they change the stretch: a notify target counts only while notify is on,
// so a target left over from an unchecked box is not a change.
func afterKey(x After) After {
	if !x.Notify {
		x.NotifyTarget = ""
	}
	return x
}

// inOrder keeps the steps and links that were there in their old places, new ones after them, so
// re-applying unchanged answers changes no bytes (QA-716).
func inOrder(orig, d Doc) Doc {
	nodeAt, edgeAt := map[string]int{}, map[string]int{}
	for i, n := range orig.Nodes {
		nodeAt[n.ID] = i
	}
	for i, e := range orig.Edges {
		edgeAt[e.ID] = i
	}
	rank := func(at map[string]int, id string) int {
		if i, ok := at[id]; ok {
			return i
		}
		return len(at) + 1
	}
	slices.SortStableFunc(d.Nodes, func(x, y Node) int { return rank(nodeAt, x.ID) - rank(nodeAt, y.ID) })
	slices.SortStableFunc(d.Edges, func(x, y Edge) int { return rank(edgeAt, x.ID) - rank(edgeAt, y.ID) })
	return d
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

// applySkip puts the guide's "Check the file" pair in front of the conversion, or takes it out (Basic).
func applySkip(d Doc, w walked, s Skip) Doc {
	want := s.MinSizeGB > 0 || s.MinResolution > 0 || s.MinMinutes > 0
	config := map[string]any{"size": int(math.Round(s.MinSizeGB * 1024)), "resolution": s.MinResolution, "minMinutes": s.MinMinutes}
	kept := keep(d)
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
		d.Edges = append(d.Edges, kept.link(w.by[TypeInput], "success", next))
	}
	return d
}

// SkipReason is what the file's history says when the guide's conditions leave it alone.
const SkipReason = "Left alone by the workflow's conditions."

// afterTypes are the cards the Basic questions put after the save, in their order.
var afterTypes = []string{TypeMediaServer, TypeDownloader, TypeNotify}

// applyAfter rebuilds the guide's run of cards after the save (Basic): the ones asked for, in order, each
// keeping its node (and settings) when it was there, then whatever followed the run before.
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
	stay := map[string]bool{}
	for _, n := range run {
		stay[n.ID] = true
	}
	kept := keep(d)
	d.Nodes = slices.DeleteFunc(d.Nodes, func(n Node) bool { return owned[n.ID] && !stay[n.ID] })
	d.Edges = slices.DeleteFunc(d.Edges, func(e Edge) bool {
		return (owned[e.From] && !stay[e.From]) || (owned[e.To] && !stay[e.To]) ||
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
		d.Edges = append(d.Edges, kept.link(prev, "success", n.ID))
		prev = n.ID
	}
	if w.afterTail != "" {
		d.Edges = append(d.Edges, kept.link(prev, "success", w.afterTail))
	}
	return d
}

// walked is a workflow's shape as the guide reads it.
type walked struct {
	by          map[string]string // the Shrink videos steps' ids by type
	first       string            // the first converting step (Leave some files alone)
	before      []Card            // the conditions and actions in front, in order (Advanced)
	afterCards  []Card            // the cards after the save (Advanced)
	convertFail *Fails
	checkFail   *Fails
	// the error link of the conversion or the check can't be read: Apply keeps it as it is
	convertFailSkip, checkFailSkip bool
	advancedWhy                    string   // set when the Advanced flow can't show some stretch of the workflow
	hard                           bool     // set when neither flow can read the main path: nothing is changed
	afterSkip                      bool     // the stretch after the save can't be read: Apply keeps it as it is
	unread                         []string // the steps whose stretches the questions can't show, in order
	check                          string   // the guide's own "Check the file", or "" (Basic)
	checkEnd                       string   // its "Leave the file alone"
	after                          []Node   // the guide's cards after the save, in order (Basic)
	afterTail                      string   // what follows them (a step Basic doesn't show), or ""
	extra                          []string
	nodes                          []Node // the main path, in order
	why                            string // set when Basic can't read the workflow
}

// walk reads the workflow once: the stretch in front, the Shrink videos steps, the stretch after the
// save. ok is the Basic flow's verdict; w.advancedWhy is the Advanced flow's.
func walk(d Doc) (walked, bool) {
	w := walked{by: map[string]string{}}
	l := linksOf(d)
	var input string
	for _, n := range d.Nodes {
		if n.Type == TypeLegacy {
			return w.fail("it keeps an earlier install's settings")
		}
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
	w.nodes = append(w.nodes, l.byID[input])
	// in front: the conditions and actions (Advanced); Basic shows its own pair and carries actions
	before, first, why := readBefore(l, input)
	if why != "" {
		return w.fail(why)
	}
	w.before, w.first = before, first
	for i, c := range before {
		n := l.byID[c.ID]
		w.nodes = append(w.nodes, n)
		switch {
		// Basic's own pair: first, only its conditions as numbers, its No leaving the file alone, and nothing
		// else linking into the check or its End (review2-715 P2-2: a hand-made link into the pair would go with it)
		case IsQuestion(c.Type) && i == 0 && c.Type == TypeCheck && n.Enabled && ownedCheck(n) && c.Then.End == "leave" && len(c.Then.Actions) == 0 && c.Then.Side != "yes" &&
			l.in[c.ID] == 1 && l.in[l.out[[2]string{c.ID, "else"}]] == 1:
			w.check, w.checkEnd = c.ID, l.out[[2]string{c.ID, "else"}]
		case IsQuestion(c.Type):
			if w.why == "" {
				w.why = "'" + nodeName(n) + "' sits where the questions can't show it"
			}
		case !n.Enabled:
		default:
			w.extra = append(w.extra, nodeName(n))
		}
	}
	// the Shrink videos steps, in their order
	next := 1
	cur := first
	seen := map[string]bool{}
	for cur != "" && next < len(PresetOrder) {
		n, ok := l.byID[cur]
		if !ok || seen[cur] {
			return w.fail("its links don't lead from Library files to Save the result")
		}
		seen[cur] = true
		w.nodes = append(w.nodes, n)
		switch {
		case n.Type == PresetOrder[next]:
			if !n.Enabled {
				return w.fail("'" + nodeName(n) + "' is turned off")
			}
			w.by[n.Type] = n.ID
			next++
		case slices.Contains(PresetOrder, n.Type):
			return w.fail("its conversion steps are not in the usual order")
		default:
			return w.fail("'" + nodeName(n) + "' sits among the steps that convert")
		}
		cur = l.out[[2]string{cur, "success"}]
	}
	if next < len(PresetOrder) {
		def, _ := Definition(PresetOrder[next])
		return w.fail("it has no '" + def.Name + "' step")
	}
	// a Basic answer taken from a variable ({{name}}) can't be shown as a choice; the folder is a template by design
	for typ, id := range w.by {
		for k, v := range l.byID[id].Config {
			if s, ok := v.(string); ok && basicKeys[typ][k] && k != "folder" && strings.Contains(s, "{{") {
				return w.fail("'" + nodeName(l.byID[id]) + "' takes a setting from a variable")
			}
		}
	}
	// what the conversion does when it doesn't work
	for _, typ := range []string{TypeConvert, TypeVerify} {
		f, why := readFails(l, l.byID[w.by[typ]])
		if why != "" {
			w.skip(nodeName(l.byID[w.by[typ]]), why)
			if typ == TypeConvert {
				w.convertFailSkip = true
			} else {
				w.checkFailSkip = true
			}
		} else if typ == TypeConvert {
			w.convertFail = f
		} else {
			w.checkFail = f
		}
	}
	// after the save: every card (Advanced); Basic owns one of each of its kinds, without links off other
	// ports (review-715 P2-2), then anything else
	afterCards, why := readAfter(l, cur, map[string]bool{})
	if why != "" { // the whole stretch from the save on is kept as it is
		w.afterSkip = true
		name := "the steps after Save"
		if n, ok := l.byID[cur]; ok {
			name = nodeName(n)
		}
		w.skip(name, why)
	} else {
		w.afterCards = afterCards
	}
	ownedKinds := map[string]bool{}
	for id := cur; id != ""; {
		n, ok := l.byID[id]
		if !ok || seen[id] {
			break
		}
		seen[id] = true
		if slices.Contains(afterTypes, n.Type) && n.Enabled && w.afterTail == "" && !ownedKinds[n.Type] && !linkedElsewhere(d, n.ID) {
			ownedKinds[n.Type] = true
			w.after = append(w.after, n)
			w.nodes = append(w.nodes, n)
			id = l.out[[2]string{id, "success"}]
			continue
		}
		if w.afterTail == "" {
			w.afterTail = n.ID
		}
		w.extra = append(w.extra, nodeName(n))
		w.nodes = append(w.nodes, n)
		id = l.out[[2]string{id, "success"}]
	}
	return w, w.why == ""
}

// linkedElsewhere says a step has a link off a port other than success (W9's error link): hand-made.
func linkedElsewhere(d Doc, id string) bool {
	for _, e := range d.Edges {
		if e.From == id && e.FromPort != "success" {
			return true
		}
	}
	return false
}

// fail is a verdict both flows share: neither can read the workflow.
func (w walked) fail(why string) (walked, bool) {
	w.why, w.advancedWhy, w.hard = why, why, true
	return w, false
}

// skip records a stretch the questions can't show: the workflow still opens and the rest is answered; the
// stretch is kept as it is. name is the step it starts at, or "" when there is none.
func (w *walked) skip(name, why string) {
	if w.advancedWhy == "" {
		w.advancedWhy = why
	}
	if name != "" {
		w.unread = append(w.unread, name)
	}
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

// GuideOf reads d as answers. Coverage is the Basic flow's: full (d is what its questions build),
// partial (that, plus steps and settings they don't show, kept as they are by Apply) or none (Why says
// what stops it). Advanced is the Advanced flow's: full, or none with AdvancedWhy. Answers is nil only
// when neither can show the workflow.
func GuideOf(d Doc) Guide {
	w, ok := walk(d)
	if w.hard {
		return Guide{Coverage: CoverageNone, Extra: []string{}, Unreadable: []string{}, Why: "Basic can't show this workflow: " + w.why + ".",
			Advanced: CoverageNone, AdvancedWhy: "The questions can't show this workflow: " + w.why + "."}
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
	// the Advanced lists hold the readable parts; a stretch they can't show is left out (nil) and kept by Apply
	g := Guide{Coverage: CoverageFull, Extra: orEmpty(w.extra), Advanced: CoverageFull, Unreadable: orEmpty(w.unread)}
	a.Before = orEmptyCards(w.before)
	if !w.afterSkip {
		a.AfterCards = orEmptyCards(w.afterCards)
	}
	a.ConvertFails, a.CheckFails = w.convertFail, w.checkFail
	a.Variables = orEmptyVars(d.Variables)
	if w.advancedWhy != "" {
		g.Advanced, g.AdvancedWhy = CoverageNone, "The questions can't show part of this workflow: "+w.advancedWhy+"."
	}
	g.Answers = ptr(a)
	switch {
	case !ok:
		g.Coverage, g.Why = CoverageNone, "Basic can't show this workflow: "+w.why+". Open it in Advanced."
	case len(w.extra) > 0:
		g.Coverage = CoveragePartial
	}
	return g
}

func orEmptyCards(c []Card) []Card {
	if c == nil {
		return []Card{}
	}
	return c
}

func orEmptyVars(v []Variable) []Variable {
	if v == nil {
		return []Variable{}
	}
	return v
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
	w, _ := walk(d)
	if w.hard {
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
// The Advanced lists' cards say what the step itself says (its summary in the editor).
func Summary(a Answers) []string {
	a = Fill(a)
	var out []string
	for _, c := range a.Before {
		out = append(out, cardSentence(c))
	}
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
	if a.Before == nil {
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
	}
	for _, f := range []struct {
		name string
		f    *Fails
	}{{"converting", a.ConvertFails}, {"checking the result", a.CheckFails}} {
		if f.f == nil {
			continue
		}
		switch f.f.End {
		case "leave":
			out = append(out, "If "+f.name+" doesn't work, the file is left alone.")
		case "stop":
			out = append(out, "If "+f.name+" doesn't work, the job stops as failed.")
		case "retry":
			out = append(out, "If "+f.name+" doesn't work, it is tried "+strconv.Itoa(f.f.Max)+" times.")
		}
	}
	if a.AfterCards != nil {
		for _, c := range a.AfterCards {
			out = append(out, "Afterwards, "+lower(cardSentence(c)))
		}
		return out
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

// cardSentence is what a card says: the step's own summary (the editor's), with what hangs off it.
func cardSentence(c Card) string {
	n := Node{ID: c.ID, Type: c.Type, TypeVersion: 1, Name: c.Name, Config: c.Config, Enabled: !c.Off}
	s := nodeName(n)
	if sum := summary(n); sum != "" {
		s += ": " + sum
	}
	if c.Then != nil {
		side := "the files that don't match"
		if c.Then.Side == "yes" {
			side = "the files that match"
		}
		what := map[string]string{"leave": "are left alone", "stop": "stop as failed", "later": "are tried again later", "handoff": "go to another workflow"}[c.Then.End]
		if len(c.Then.Actions) > 0 {
			what = "first " + lower(strings.Join(cardNames(c.Then.Actions), ", ")) + ", then " + what
		}
		s += "; " + side + " " + what
	}
	if len(c.Yes) > 0 || len(c.No) > 0 {
		s += "; on Yes " + lower(strings.Join(cardNames(c.Yes), ", ")) + "; on No " + lower(strings.Join(cardNames(c.No), ", "))
	}
	if len(c.OnError) > 0 {
		s += "; if it doesn't work, " + lower(strings.Join(cardNames(c.OnError), ", "))
	}
	return s + "."
}

func lower(s string) string {
	if s == "" {
		return "nothing more"
	}
	return strings.ToLower(s[:1]) + s[1:]
}
