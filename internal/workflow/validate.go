// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Result is a Doc checked: its issues, whether the stack can show it without loss, each node's
// summary in plain words, and what the editor may do with each step's place and each step type.
type Result struct {
	Issues        []Issue              `json:"issues"`
	Stackable     bool                 `json:"stackable"`
	StepSummaries map[string]string    `json:"stepSummaries"`
	StepPlaces    map[string]StepPlace `json:"stepPlaces"` // one entry per node in the doc
	AddSteps      map[string]AddStep   `json:"addSteps"`   // one entry per type in Definitions()
}

// StepPlace is what the editor may do with a step's place. W11 sends it; W10 (the step editor) makes
// moving and deleting real.
type StepPlace struct {
	FixedPlace  bool   `json:"fixedPlace"`
	FixedReason string `json:"fixedReason"` // why, when FixedPlace; "" when the step may move or be removed
}

// AddStep is what the add-step menu offers for a step type in this workflow.
type AddStep struct {
	Addable bool   `json:"addable"`
	Reason  string `json:"reason"` // why not, when !Addable
	After   string `json:"after"`  // the node it would be added after, when Addable
}

// stepPlace is the editor's rule for a step: the steps that convert keep their order until W10; a
// question, an action or an end (FB1b) can be moved and removed.
func stepPlace(n Node) StepPlace {
	if IsQuestion(n.Type) || IsAction(n.Type) || IsEnd(n.Type) {
		return StepPlace{}
	}
	return StepPlace{FixedPlace: true, FixedReason: "The steps that convert run in a set order for now, so they can't be moved or deleted. To skip this step, turn it off."}
}

// addSteps is the add-step menu's rule: a question or an action (FB1b) goes before the steps that
// convert, after the last such step already there (or the input); an action on the saved result (FB2a)
// goes after the save, behind the last such action already there; an end is placed in the graph only;
// the steps that convert can't be added until W10. after is the node the editor inserts behind.
func addSteps(d Doc) map[string]AddStep {
	after, afterSave := "", ""
	if order, ok := mainPath(d); ok {
		for _, n := range order {
			switch {
			case n.Type == TypeCommit:
				afterSave = n.ID
			case afterSave != "" && (IsAction(n.Type) || IsQuestion(n.Type)):
				afterSave = n.ID
			case n.Type == TypeInput || IsQuestion(n.Type) || IsAction(n.Type):
				after = n.ID
			}
		}
	}
	out := map[string]AddStep{}
	for _, def := range Definitions() {
		afterOnly := (IsAction(def.Type) || IsQuestion(def.Type)) && !slices.Contains(def.Phase, PhaseBefore)
		switch {
		case def.ReadOnly:
			out[def.Type] = AddStep{Addable: false, Reason: "Only an earlier install's workflow has this step."}
		case IsEnd(def.Type): // an end has no Next, so the steps view can't chain it; the graph puts it on a No or a Didn't work
			out[def.Type] = AddStep{Addable: false, Reason: "Goes on a question's No, or on a step's Didn't work, in the graph."}
		case afterOnly:
			if afterSave == "" {
				out[def.Type] = AddStep{Addable: false, Reason: "This workflow has no 'Save the result' step to add it after."}
			} else {
				out[def.Type] = AddStep{Addable: true, After: afterSave}
			}
		case IsQuestion(def.Type) || IsAction(def.Type):
			if after == "" {
				out[def.Type] = AddStep{Addable: false, Reason: "This workflow has no Library files step to add it after."}
			} else {
				out[def.Type] = AddStep{Addable: true, After: after}
			}
		default:
			out[def.Type] = AddStep{Addable: false, Reason: "Can't run among the steps that convert yet."}
		}
	}
	return out
}

// stackable says whether the steps view can show d without loss (FB1d): a straight chain, or the main
// path (Next and Yes ports) with, off each question's No, one end and nothing else. Any other edge (a
// second converting path, an action chain off a No, a link from a Didn't work port) needs the graph.
func stackable(d Doc) bool {
	if _, ok := straightChain(d); ok {
		return true
	}
	path, ok := mainPath(d)
	if !ok {
		return false
	}
	on := map[string]bool{}
	for _, n := range path {
		on[n.ID] = true
	}
	byID := map[string]Node{}
	for _, n := range d.Nodes {
		byID[n.ID] = n
	}
	hanging := map[string]bool{} // the ends off a No
	for _, e := range d.Edges {
		from, to := byID[e.From], byID[e.To]
		switch {
		case on[e.From] && (e.FromPort == "success" || e.FromPort == "match") && on[e.To]:
			continue // the main path itself
		case on[e.From] && e.FromPort == "else" && IsQuestion(from.Type) && IsEnd(to.Type):
			hanging[e.To] = true
		default:
			return false
		}
	}
	for _, n := range d.Nodes {
		if !on[n.ID] && !hanging[n.ID] {
			return false
		}
	}
	return true
}

// mainPath is the chain from the input along Next and Yes ports (the steps view's rows; FB1b): ok is
// false when there is no input or the chain loops.
func mainPath(d Doc) ([]Node, bool) {
	byID := map[string]Node{}
	for _, n := range d.Nodes {
		byID[n.ID] = n
	}
	next := map[string]string{}
	for _, e := range d.Edges {
		if e.FromPort == "success" || e.FromPort == "match" {
			next[e.From] = e.To
		}
	}
	start := ""
	for _, n := range d.Nodes {
		if n.Type == TypeInput {
			start = n.ID
		}
	}
	if start == "" {
		return nil, false
	}
	var order []Node
	seen := map[string]bool{}
	for id := start; id != ""; id = next[id] {
		n, ok := byID[id]
		if !ok || seen[id] {
			return nil, false
		}
		seen[id] = true
		order = append(order, n)
	}
	return order, true
}

// Errors reports whether any issue blocks Publish.
func (r Result) Errors() bool {
	return slices.ContainsFunc(r.Issues, func(i Issue) bool { return i.Severity == "error" })
}

var hhmmRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Policy is what this install allows a workflow to do (FB2b): the Safety switch for Run a command, and
// what it knows of the other workflows (FB4b, "Hand to another workflow"). Validate alone assumes
// everything is allowed and checks a hand-over's target not at all; the server passes the live policy
// in Scopes.
type Policy struct {
	CommandsAllowed bool
	// Workflows is another workflow by id (nil: no lookup, so a target is not checked); SelfID is the
	// workflow being validated (0: unknown), so a hand-over that leads back to it is refused.
	Workflows func(id int64) (OtherWorkflow, bool)
	SelfID    int64
}

// OtherWorkflow is what validation knows of a workflow a step may hand files to: its name, whether it
// has been published, and the workflows its own published paths hand files to.
type OtherWorkflow struct {
	Name      string
	Published bool
	HandsTo   []int64
	// FileValues: a typed setting of its names one of the file's own values (file.*, source.*), which a
	// hand-over can't fill (review-579), so it can't be a target.
	FileValues bool
}

// handoffIssue checks one "Hand to another workflow" step against the other workflows (FB4b): the
// target must exist, be published, not be this workflow, and not lead back to it through its own
// hand-overs. It walks up to MaxHandoffs workflows deep, the same bound the run keeps. With no issue, the
// step's summary names the workflow.
func handoffIssue(n Node, p Policy) (*Issue, string) {
	id, err := strconv.ParseInt(strOf(n.Config, "workflow", ""), 10, 64)
	if err != nil || id <= 0 { // the config check already said invalid_config
		return nil, ""
	}
	bad := func(code, msg string) (*Issue, string) {
		return &Issue{Code: code, Severity: "error", NodeID: n.ID, Field: "workflow", Message: "'" + nodeName(n) + "' " + msg}, ""
	}
	if id == p.SelfID {
		return bad("handoff_loop", "hands the file back to this workflow. Choose another one.")
	}
	w, ok := p.Workflows(id)
	if !ok {
		return bad("invalid_config", "hands the file to a workflow that isn't there any more. Choose another one.")
	}
	if !w.Published {
		return bad("handoff_unpublished", "hands the file to '"+w.Name+"', which hasn't been published yet. Publish it first.")
	}
	if w.FileValues {
		return bad("handoff_file_values", "hands the file to '"+w.Name+"', whose settings use the file's own values ({{file.…}} or {{source.…}}), which a hand-over can't fill. Choose another workflow, or give that setting a plain value.")
	}
	// the other workflow's own hand-overs, breadth first: one that leads back here is a loop
	seen := map[int64]bool{id: true}
	queue := []int64{id}
	for depth := 0; len(queue) > 0 && depth < rules.MaxHandoffs; depth++ {
		var next []int64
		for _, from := range queue {
			f, ok := p.Workflows(from)
			if !ok {
				continue
			}
			for _, to := range f.HandsTo {
				if to == p.SelfID && p.SelfID != 0 {
					return bad("handoff_loop", "leads back to this workflow through '"+w.Name+"'. The file would go round in a loop.")
				}
				if !seen[to] {
					seen[to] = true
					next = append(next, to)
				}
			}
		}
		queue = next
	}
	return nil, "Hands the file to " + w.Name
}

// Validate checks d (spec 5.6): one trigger, every node reachable, no cycle (no loop nodes yet), known
// node types, ports and config values, edges between existing ports, always-on steps on, and whether
// this version can run it. A node's unwired "error" port means stop: the job fails and the original
// stays, as the engine has always done.
func Validate(d Doc) Result {
	return ValidateWith(d, Scopes{Policy: Policy{CommandsAllowed: true}})
}

// ValidateWith is Validate in the live scopes (W2): the workflow's own variables, then its library's,
// then the server's global ones; file.* and source.* stay references with s.LeaveFile, so a published
// workflow may name a file it does not have yet. Under the install's policy (FB2b), a card it forbids
// is an error naming the switch, so the draft is kept but can't be published until the switch is on.
func ValidateWith(d Doc, s Scopes) Result {
	r := Result{Issues: []Issue{}, StepSummaries: map[string]string{}, StepPlaces: map[string]StepPlace{}, AddSteps: map[string]AddStep{}}
	add := func(i Issue) {
		if i.Severity == "" {
			i.Severity = "error"
		}
		r.Issues = append(r.Issues, i)
	}
	if d.SchemaVersion != SchemaVersion {
		msg := fmt.Sprintf("This workflow is in format version %d, which is older than this version can upgrade. It is kept as it was stored.", d.SchemaVersion)
		if d.SchemaVersion > SchemaVersion {
			msg = fmt.Sprintf("This workflow is in format version %d, which is newer than this version reads (%d). Update shrink-your-shit to open it.", d.SchemaVersion, SchemaVersion)
		}
		add(Issue{Code: "unknown_schema_version", Message: msg})
		return r
	}
	// W1: a setting that takes a typed value may hold one whole variable. It is resolved here, so the
	// checks below read what the run will use; a setting whose reference Resolve already reported is
	// not checked a second time as a literal (that would say the same thing twice, in other words).
	resolved, refIssues, _ := resolveOnce(d, s)
	for _, i := range refIssues {
		add(i)
	}
	badRef := map[[2]string]bool{}
	for _, i := range refIssues {
		if i.NodeID != "" && i.Field != "" {
			badRef[[2]string{i.NodeID, i.Field}] = true
		}
	}
	d = resolved
	r.AddSteps = addSteps(d) // FB1b's menu, from the resolved doc
	for _, i := range CheckVariables(d) {
		add(i)
	}
	for _, i := range CheckTemplatesWith(d, s) { // W2: the globals are names a template may use
		add(i)
	}
	for _, i := range CheckPublished(d) {
		add(i)
	}
	defs := map[string]StepDefinition{}
	for _, def := range Definitions() {
		defs[def.Type] = def
	}
	byID := map[string]Node{}
	triggers := 0
	for _, n := range d.Nodes {
		if n.ID == "" || byID[n.ID].ID != "" {
			add(Issue{Code: "duplicate_id", NodeID: n.ID, Message: "Two steps have the same id."})
			continue
		}
		byID[n.ID] = n
		r.StepPlaces[n.ID] = stepPlace(n)
		def, ok := defs[n.Type]
		if !ok {
			add(Issue{Code: "unknown_node_type", NodeID: n.ID, Message: fmt.Sprintf("%q is not a step this version has.", n.Type)})
			continue
		}
		if n.TypeVersion != def.TypeVersion {
			add(Issue{Code: "unknown_type_version", NodeID: n.ID, Message: fmt.Sprintf("This %s step was made by another version (%d).", def.Name, n.TypeVersion)})
		}
		if n.Type == TypeInput {
			triggers++
		}
		if def.AlwaysOn && !n.Enabled {
			add(Issue{Code: "invalid_config", NodeID: n.ID, Message: def.Name + " can't be turned off."})
		}
		if !def.Available { // a card this build can't run yet (FB1b): a draft may hold it, Publish refuses it
			add(Issue{Code: "not_runnable_yet", NodeID: n.ID, Message: "'" + nodeName(n) + "' can't run in this version. Remove it, or keep the workflow as a draft."})
		}
		for _, i := range checkConfig(n, def, badRef, s.LeaveFile) {
			add(i)
		}
		if n.Type == TypeTag && len(rules.CleanTags(strOf(n.Config, "add", ""))) == 0 && len(rules.CleanTags(strOf(n.Config, "remove", ""))) == 0 { // review2-542 P2: an empty card would fail every job
			add(Issue{Code: "invalid_config", NodeID: n.ID, ConfigPath: "/add", Field: "add", Message: "In '" + nodeName(n) + "', name a tag to add or one to remove."})
		}
		if n.Type == TypeWait { // FB4: one of the two, and a time it can read
			secs, until := intOf(n.Config, "seconds", 0), strOf(n.Config, "until", "")
			if secs <= 0 && until == "" {
				add(Issue{Code: "invalid_config", NodeID: n.ID, ConfigPath: "/seconds", Field: "seconds", Message: "In '" + nodeName(n) + "', set seconds to wait, or a time of day to wait until."})
			}
			if until != "" && !hhmmRe.MatchString(until) {
				add(Issue{Code: "invalid_config", NodeID: n.ID, ConfigPath: "/until", Field: "until", Message: "In '" + nodeName(n) + "', the time must read like 02:00."})
			}
		}
		if n.Type == TypeCheckResult { // FB4b: the percent tests need their percent; the running-time test its seconds
			switch test := strOf(n.Config, "test", "saved_at_least"); {
			case test == "runtime_within" && questionOf(n).Seconds <= 0:
				add(Issue{Code: "invalid_config", NodeID: n.ID, ConfigPath: "/seconds", Field: "seconds", Message: "In '" + nodeName(n) + "', set how many seconds the running time may be off by."})
			case test != "bigger" && test != "runtime_within" && intOf(n.Config, "percent", 0) <= 0:
				add(Issue{Code: "invalid_config", NodeID: n.ID, ConfigPath: "/percent", Field: "percent", Message: "In '" + nodeName(n) + "', set the percent of the original's size to compare with."})
			}
		}
		if n.Type == TypeGoto { // FB4b: a workflow to hand to, by id. A missing key is checkConfig's "needs a value"; an empty or unreadable one (review2-579: the picker's placeholder picked again) is refused here, so it never fails every file at run time.
			if v, present := n.Config["workflow"]; present {
				if id, err := strconv.ParseInt(strOf(n.Config, "workflow", ""), 10, 64); v == "" || err != nil || id <= 0 {
					add(Issue{Code: "invalid_config", NodeID: n.ID, ConfigPath: "/workflow", Field: "workflow", Message: "In '" + nodeName(n) + "', choose the workflow to hand the file to."})
				}
			}
		}
		if n.Type == TypeCheckName && strOf(n.Config, "match", "contains") == "regex" { // review2-531 P3: a pattern that can't compile would answer No for every file
			if _, err := regexp.Compile(strOf(n.Config, "pattern", "")); err != nil {
				add(Issue{Code: "invalid_config", NodeID: n.ID, ConfigPath: "/pattern", Field: "pattern",
					Message: fmt.Sprintf("In '%s', the regular expression can't be read: %v.", nodeName(n), err)})
			}
		}
		r.StepSummaries[n.ID] = summary(n)
	}
	switch {
	case triggers == 0:
		add(Issue{Code: "no_trigger", Message: "A workflow starts with Library files."})
	case triggers > 1:
		add(Issue{Code: "multiple_triggers", Message: "A workflow has one Library files step."})
	}
	afterSide := afterTheSave(d) // FB4b: the steps reached from Save the result, where only after cards may follow
	out := map[string][]string{}
	for _, e := range d.Edges {
		from, okF := byID[e.From]
		to, okT := byID[e.To]
		if !okF || !okT {
			add(Issue{Code: "dangling_edge", EdgeID: e.ID, Message: "A connection leads to a step that isn't there."})
			continue
		}
		_, knownFrom := defs[from.Type]
		_, knownTo := defs[to.Type]
		if !knownFrom || !knownTo {
			// unknown_node_type already names the step. Its ports are unknown too, so checking them would
			// name a step with no name. Keep the link, so the steps after it aren't reported as unreachable.
			out[e.From] = append(out[e.From], e.To)
			continue
		}
		if !hasPort(defs[from.Type].Outputs, e.FromPort) {
			add(Issue{Code: "unknown_port", EdgeID: e.ID, NodeID: from.ID, Port: e.FromPort, Message: fmt.Sprintf("%s has no %q outcome.", defs[from.Type].Name, e.FromPort)})
			continue
		}
		if !hasPort(defs[to.Type].Inputs, e.ToPort) {
			add(Issue{Code: "unknown_port", EdgeID: e.ID, NodeID: to.ID, Port: e.ToPort, Message: fmt.Sprintf("%s can't be connected to.", defs[to.Type].Name)})
			continue
		}
		out[e.From] = append(out[e.From], e.To)
		for _, i := range phaseIssue(from, to, e, defs, afterSide[from.ID]) {
			add(i)
		}
	}
	// reachable from the trigger, and no cycle (loop nodes come later)
	for _, n := range d.Nodes {
		if n.Type != TypeInput {
			continue
		}
		seen := map[string]bool{}
		var walk func(string, map[string]bool) bool
		walk = func(id string, onPath map[string]bool) bool {
			if onPath[id] {
				return false
			}
			if seen[id] {
				return true
			}
			seen[id], onPath[id] = true, true
			for _, to := range out[id] {
				if !walk(to, onPath) {
					return false
				}
			}
			delete(onPath, id)
			return true
		}
		if !walk(n.ID, map[string]bool{}) {
			add(Issue{Code: "cycle_without_loop", Message: "Steps lead back to an earlier step. Loops come in a later update."})
		}
		for _, m := range d.Nodes {
			if !seen[m.ID] {
				add(Issue{Code: "unreachable_node", Severity: "warning", NodeID: m.ID, Message: "Nothing leads to this step, so it never runs."})
			}
		}
	}
	r.Stackable = stackable(d)
	if !r.Errors() {
		_, issues := ToTemplateWith(d, rules.Template{}, s)
		for _, i := range issues {
			add(i)
		}
	}
	if !s.Policy.CommandsAllowed { // FB2b: the Safety switch counts in what the editor shows and in what may be published
		for _, n := range d.Nodes {
			if n.Type == TypeCommand {
				add(Issue{Code: "commands_off", Severity: "error", NodeID: n.ID,
					Message: "'" + nodeName(n) + "' can't run until \"Workflows may run commands\" is on in Settings > Safety."})
			}
		}
	}
	if s.Policy.Workflows != nil { // FB4b: a hand-over must lead to another, published workflow and never back here
		for _, n := range d.Nodes {
			if n.Type != TypeGoto {
				continue
			}
			if issue, summary := handoffIssue(n, s.Policy); issue != nil {
				add(*issue)
			} else if summary != "" {
				r.StepSummaries[n.ID] = summary
			}
		}
	}
	return r
}

// afterTheSave is the set of steps reached from a Save the result step along any port (FB4b): a card there
// runs on the saved file, whatever phases its kind may also sit in.
func afterTheSave(d Doc) map[string]bool {
	next := map[string][]string{}
	for _, e := range d.Edges {
		next[e.From] = append(next[e.From], e.To)
	}
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		for _, to := range next[id] {
			if !seen[to] {
				seen[to] = true
				walk(to)
			}
		}
	}
	for _, n := range d.Nodes {
		if n.Type == TypeCommit {
			walk(n.ID)
		}
	}
	return seen
}

// phaseIssue says when an edge would run a step in a phase it can't sit in (FB1b, FB2a): a step that belongs
// before the conversion after a step that converts, checks or saves; a step that belongs after the save before
// the conversion or inside it. A failure port (error, skipped) may still lead to an end (V6b). The compile
// refuses the same shapes; this names the step while editing, before a compile is tried.
func phaseIssue(from, to Node, e Edge, defs map[string]StepDefinition, afterSave bool) []Issue {
	fd, td := defs[from.Type], defs[to.Type]
	if fd.RunsAs == "" || td.RunsAs == "" || e.FromPort == "error" || e.FromPort == "skipped" {
		return nil
	}
	bad := func(msg string) []Issue {
		return []Issue{{Code: "wrong_phase", Severity: "error", NodeID: to.ID, EdgeID: e.ID, Message: "'" + nodeName(to) + "' " + msg}}
	}
	card := func(d StepDefinition) bool { return d.RunsAs == RunsAction || d.RunsAs == RunsQuestion } // FB4b: a question may sit after the save too
	afterOnly := func(d StepDefinition) bool {
		return card(d) && !slices.Contains(d.Phase, PhaseBefore)
	}
	afterOK := func(d StepDefinition) bool { // may sit after the save
		return card(d) && slices.Contains(d.Phase, PhaseAfter)
	}
	checkOK := func(d StepDefinition) bool { // may sit between the check and the save (FB4b-3)
		return d.RunsAs == RunsQuestion && slices.Contains(d.Phase, PhaseCheck)
	}
	// where an after-only step belongs, in words: one that may also sit after the check says so
	belongs := func(d StepDefinition) string {
		if checkOK(d) {
			return "belongs after the file is checked. Put it between 'Check the result' and 'Save the result', or after the save."
		}
		return "belongs after the file is saved. Put it after 'Save the result'."
	}
	switch fd.RunsAs {
	case RunsPlan, RunsVerify: // inside the conversion: nothing but its own steps follow on Next
		if fd.RunsAs == RunsVerify && checkOK(td) { // FB4b-3: a question about the checked result, before the save
			return nil
		}
		if td.RunsAs == RunsQuestion || td.RunsAs == RunsAction || td.RunsAs == RunsEnd {
			if afterOnly(td) {
				return bad("comes after '" + nodeName(from) + "', but it " + belongs(td))
			}
			return bad("comes after '" + nodeName(from) + "', but it belongs before the steps that convert. Move it up, or remove the link.")
		}
	case RunsCommit: // after the save: only the steps that act on the saved file
		if td.RunsAs != RunsEnd && !afterOK(td) {
			return bad("can't follow 'Save the result'. After the save come only steps that act on the saved file.")
		}
	case RunsAction, RunsQuestion: // after the save, only after steps follow; before it, an after-only step never does
		if !afterSave && checkOK(fd) && (afterOnly(fd) || e.FromPort == "else") { // FB4b-3: between the check and the save
			if td.RunsAs == RunsCommit || to.Type == TypeEnd || to.Type == TypeFail || checkOK(td) {
				return nil
			}
			return bad("can't come between 'Check the result' and 'Save the result'. There come only 'Compare the result', the save, 'Leave the file alone' or 'Stop as failed'.")
		}
		if afterSave || afterOnly(fd) {
			if !afterOK(td) {
				return bad("can't follow '" + nodeName(from) + "'. After the save come only steps that act on the saved file.")
			}
		} else if afterOnly(td) {
			return bad(belongs(td))
		}
	case RunsInput:
		if afterOnly(td) {
			return bad(belongs(td))
		}
	}
	return nil
}

// checkConfig checks n's config against its definition's schema, the one the registry serves: types
// (integer, number, string, boolean, array, object), enum values, minimum and maximum, maxLength, an
// array's items, and required keys without a default (ui-shop 12:32Z: a minimum of 0 wasn't enforced).
// Each problem names the step and the setting. A key the schema doesn't know is kept and warned about
// (research: never drop it silently).
func checkConfig(n Node, def StepDefinition, badRef map[[2]string]bool, leaveFile bool) []Issue {
	var out []Issue
	if def.ReadOnly { // kept as it is: nothing to check against a form
		return nil
	}
	step := nodeName(n)
	bad := func(k, msg string) {
		out = append(out, Issue{Code: "invalid_config", Severity: "error", NodeID: n.ID, ConfigPath: "/" + k, Field: k,
			Message: fmt.Sprintf("In '%s', %s", step, msg)})
	}
	props, _ := def.ConfigSchema["properties"].(map[string]any)
	if req, ok := def.ConfigSchema["required"].([]string); ok {
		for _, k := range req {
			p, _ := props[k].(map[string]any)
			if _, has := n.Config[k]; !has && p != nil && p["default"] == nil {
				bad(k, fmt.Sprintf("“%s” needs a value.", titleOf(p, k)))
			}
		}
	}
	keys := make([]string, 0, len(n.Config))
	for k := range n.Config {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		v := n.Config[k]
		p, ok := props[k].(map[string]any)
		if !ok {
			out = append(out, Issue{Code: "unknown_config_key", Severity: "warning", NodeID: n.ID, ConfigPath: "/" + k, Field: k,
				Message: fmt.Sprintf("In '%s', %q is not a setting of this step; it is kept but not used.", step, k)})
			continue
		}
		if badRef[[2]string{n.ID, k}] {
			continue // Resolve said what is wrong with the reference this setting holds
		}
		if leaveFile {
			if str, ok := v.(string); ok {
				if name, whole := wholeRef(str); whole {
					if _, isFile := ReservedNames[name]; isFile {
						continue // W2: the job fills this from the file; Resolve checked its kind
					}
				}
			}
		}
		if msg := valueProblem(p, v, titleOf(p, k)); msg != "" {
			bad(k, msg)
		}
	}
	return out
}

// valueProblem says what is wrong with v for schema p (title names the setting), "" when nothing is.
func valueProblem(p map[string]any, v any, title string) string {
	if enum, ok := p["enum"].([]any); ok {
		if !slices.ContainsFunc(enum, func(e any) bool { return sameValue(e, v) }) {
			return fmt.Sprintf("%v is not a choice for “%s”.", v, title)
		}
		return ""
	}
	if t, _ := p["type"].(string); t != "" && !typeMatches(t, v) {
		return fmt.Sprintf("“%s” has the wrong kind of value (%s).", title, kindWords(t))
	}
	if x, ok := num(v); ok {
		if min, ok := num(p["minimum"]); ok && x < min {
			return fmt.Sprintf("“%s” can't be less than %v.", title, p["minimum"])
		}
		if max, ok := num(p["maximum"]); ok && x > max {
			return fmt.Sprintf("“%s” can't be more than %v.", title, p["maximum"])
		}
	}
	if str, ok := v.(string); ok {
		if max, ok := num(p["maxLength"]); ok && float64(len([]rune(str))) > max {
			return fmt.Sprintf("“%s” can be at most %v characters.", title, p["maxLength"])
		}
	}
	if items, ok := p["items"].(map[string]any); ok {
		var list []any
		switch a := v.(type) {
		case []any:
			list = a
		case []string:
			for _, e := range a {
				list = append(list, e)
			}
		}
		for _, e := range list {
			if msg := valueProblem(items, e, title); msg != "" {
				return msg
			}
		}
	}
	return ""
}

// kindWords is a schema type in plain words.
func kindWords(t string) string {
	return map[string]string{"integer": "a whole number", "number": "a number", "string": "text", "boolean": "yes or no",
		"array": "a list", "object": "a group of settings"}[t]
}

func sameValue(a, b any) bool {
	if fa, ok := num(a); ok {
		fb, ok := num(b)
		return ok && fa == fb
	}
	return a == b
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func typeMatches(t string, v any) bool {
	switch t {
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "integer":
		x, ok := num(v)
		return ok && x == math.Trunc(x)
	case "number":
		_, ok := num(v)
		return ok
	case "array":
		switch v.(type) {
		case []any, []string:
			return true
		}
		return false
	case "object":
		_, ok := v.(map[string]any)
		return ok
	}
	return true
}

func titleOf(p map[string]any, k string) string {
	if t, ok := p["title"].(string); ok {
		return t
	}
	return k
}

func hasPort(ports []Port, name string) bool {
	return slices.ContainsFunc(ports, func(p Port) bool { return p.Name == name })
}

// summary is a node's line in plain words (design: server-rendered, like the old "Your files become").
func summary(n Node) string {
	c := n.Config
	if !n.Enabled { // ui-shop's words per step (registry words pass)
		switch n.Type {
		case TypeAudio:
			return "Off: the original's tracks are kept"
		case TypeSubtitles:
			return "Off: the original's subtitles are kept"
		case TypeContainer:
			return "Off: keeps the original's type"
		}
		return "Off"
	}
	switch n.Type {
	case TypeInput:
		return "Every video in the libraries that use this workflow"
	case TypeSkips:
		if boolOf(c, "includeDolbyVision", false) {
			return "Skips files already in this format and AV1 or VP9 videos; Dolby Vision files become HDR10"
		}
		return "Skips Dolby Vision files, files already in this format, and AV1 or VP9 videos"
	case TypeConvert:
		name := "HEVC"
		if f, ok := rules.VideoFormats[strOf(c, "format", "hevc")]; ok {
			name = f.Name
		}
		quality := ""
		if p, ok := rules.SliderAt(intOf(c, "quality", 3)); ok {
			quality = qualityWords(p.Name)
		}
		size := "same resolution"
		if r := strOf(c, "resolution", "keep"); r != "keep" {
			size = "at most " + r + "p"
		}
		parts := []string{name, quality, size}
		if d := strOf(c, "bitDepth", "keep"); d == "8" || d == "10" { // a non-default depth is named; keep adds nothing
			parts = append(parts, d+"-bit")
		}
		if f := strOf(c, "maxFrameRate", "keep"); f != "keep" && f != "" { // FB5b: the limit is named; keep adds nothing
			parts = append(parts, "at most "+f+" fps")
		}
		if b := strOf(c, "maxBitrate", "none"); b != "none" {
			parts = append(parts, strings.ToUpper(b[:1])+b[1:]+" bitrate limit")
		}
		return strings.Join(parts, " · ")
	case TypeAudio:
		words := rules.AudioWords[strOf(c, "audio", "stereo_aac")]
		if boolOf(c, "onlyMyLanguages", true) {
			words += ", in your languages"
		}
		return words
	case TypeSubtitles:
		return map[string]string{"keep_all": "Keep all", "my_languages": "Only my languages", "text_only": "Text only", "srt": "As SRT files"}[strOf(c, "subtitles", "keep_all")]
	case TypeContainer:
		return map[string]string{"mkv": "MKV", "mp4": "MP4", "webm": "WebM", "keep": "Keep as is"}[strOf(c, "fileType", "mkv")]
	case TypeVerify:
		return "The new file must play through, look like the original and hold what you chose"
	case TypeCommit:
		switch strOf(c, "policy", "separate_folder") {
		case "replace":
			return "Replace the original (kept for a while, then deleted)"
		case "beside":
			return "Next to the original · both stay"
		}
		if strOf(c, "folderMode", "next_to_library") == "one_folder" {
			return "Save to " + strOf(c, "folder", "a folder") + " · originals untouched"
		}
		return "Save next to each library · originals untouched"
	case TypeLegacy:
		return "Settings from an earlier install, run exactly as before"
	case TypeCheck:
		return "Only if " + questionWords(n)
	case TypeCheckName:
		how := map[string]string{"contains": "contains", "starts": "starts with", "ends": "ends with", "glob": "matches", "regex": "matches the expression"}[strOf(c, "match", "contains")]
		what := map[string]string{"name": "the file name", "path": "the path"}[strOf(c, "where", "name")]
		return "Only if " + what + " " + how + " “" + strOf(c, "pattern", "") + "”"
	case TypeCheckAge:
		o, y := intOf(c, "olderDays", 0), intOf(c, "youngerDays", 0)
		switch {
		case o > 0 && y > 0:
			return "Only if last changed " + strconv.Itoa(o) + " to " + strconv.Itoa(y) + " days ago"
		case o > 0:
			return "Only if last changed at least " + strconv.Itoa(o) + " days ago"
		case y > 0:
			return "Only if last changed in the last " + strconv.Itoa(y) + " days"
		}
		return "Only if (no age set)"
	case TypeCheckVariable:
		op := map[string]string{"set": "has a value", "unset": "has no value", "equals": "is “" + strOf(c, "value", "") + "”", "contains": "contains “" + strOf(c, "value", "") + "”",
			"more": "is more than " + strOf(c, "value", ""), "less": "is less than " + strOf(c, "value", "")}[strOf(c, "op", "equals")]
		return "Only if {{" + strOf(c, "variable", "") + "}} " + op
	case TypeCheckLibrary:
		return "Only if the file is from one of " + strconv.Itoa(len(listOf(c, "libraries"))) + " libraries"
	case TypeCheckHardLinks:
		return "Only if the file shares its data with another name"
	case TypeCheckTag:
		return "Only if the file has " + map[string]string{"all": "all of", "none": "none of"}[strOf(c, "tagOp", "any")] + map[bool]string{true: "any of", false: ""}[strOf(c, "tagOp", "any") == "any" || strOf(c, "tagOp", "") == ""] + " the tags " + strOf(c, "tags", "")
	case TypeTag:
		add, remove := strings.Join(rules.CleanTags(strOf(c, "add", "")), ", "), strings.Join(rules.CleanTags(strOf(c, "remove", "")), ", ")
		switch {
		case add != "" && remove != "":
			return "Adds " + add + "; removes " + remove
		case remove != "":
			return "Removes " + remove
		case add != "":
			return "Adds " + add
		}
		return "Nothing to add or remove yet"
	case TypeSetVariable:
		return "Set {{" + strOf(c, "name", "") + "}} = “" + strOf(c, "value", "") + "”"
	case TypeNote:
		return "Note: “" + strOf(c, "text", "") + "”"
	case TypeEnd:
		return strOf(c, "reason", "Left alone by this workflow.")
	case TypeFail:
		return "Failed: " + strOf(c, "reason", "Stopped by this workflow.")
	case TypeRequeue:
		return "Back in the queue for " + minutesWords(intOf(c, "afterMin", 60)) + ", at most " + strconv.Itoa(intOf(c, "maxTries", 5)) + " tries"
	case TypeGoto:
		return "Hands the file to another workflow"
	case TypeCheckRandom:
		return "Only for about " + strconv.Itoa(intOf(c, "share", 10)) + "% of files"
	case TypeCheckResult:
		switch strOf(c, "test", "saved_at_least") {
		case "saved_less_than":
			return "Only if less than " + strconv.Itoa(intOf(c, "percent", 10)) + "% was saved"
		case "bigger":
			return "Only if the new file is bigger"
		case "runtime_within":
			secs := questionOf(n).Seconds
			if secs <= 0 { // unset: the schema's default, as percent's summary does
				secs = 2
			}
			return "Only if the running time is within " + strconv.FormatFloat(secs, 'f', -1, 64) + " seconds of the original's"
		}
		return "Only if at least " + strconv.Itoa(intOf(c, "percent", 10)) + "% was saved"
	case TypeWait:
		if u := strOf(c, "until", ""); u != "" {
			return "Waits in the queue until " + u
		}
		return "Waits " + strconv.Itoa(intOf(c, "seconds", 0)) + " seconds"
	// FB2a: the cards after the save
	case TypeMediaServer:
		return "Tells " + map[string]string{"plex": "Plex", "jellyfin": "Jellyfin"}[strOf(c, "which", "all")] + map[bool]string{true: "every connected media server", false: ""}[strOf(c, "which", "all") == "all"]
	case TypeDownloader:
		return "Tells " + map[string]string{"sonarr": "Sonarr", "radarr": "Radarr"}[strOf(c, "which", "all")] + map[bool]string{true: "every connected downloader", false: ""}[strOf(c, "which", "all") == "all"]
	case TypeNotify:
		to := strOf(c, "target", "")
		if to == "" {
			to = "every target that is on"
		}
		return "To " + to + ": “" + strOf(c, "text", "") + "”"
	case TypeSidecar:
		return "Writes " + strOf(c, "name", "") + " beside the new file"
	case TypeChecksum:
		return strings.ToUpper(strOf(c, "kind", "sha256")) + " of the new file, as {{checksum}}"
	case TypeCopy:
		return "A copy in " + strOf(c, "folder", "") + ", as {{copy_path}}"
	case TypeCommand:
		return "Runs " + strOf(c, "program", "") + " " + strOf(c, "arguments", "")
	case TypeLookupDownloader:
		return "Sets {{title_clean}}, {{year}}, {{season}}, {{episode}}, {{original_language}}"
	}
	return ""
}

// questionWords are a "Check the file" step's conditions in plain words, for its summary.
func questionWords(n Node) string {
	c := n.Config
	var parts []string
	if l := listOf(c, "codec"); len(l) > 0 {
		parts = append(parts, "the video is "+strings.Join(l, " or "))
	}
	if v := intOf(c, "minBitrateKbps", 0); v > 0 {
		parts = append(parts, "the bitrate is at least "+strconv.Itoa(v)+" kbps")
	}
	if v := intOf(c, "resolution", 0); v > 0 {
		parts = append(parts, "the resolution is at least "+strconv.Itoa(v)+"p")
	}
	if v := strOf(c, "hdr", ""); v != "" {
		parts = append(parts, map[string]string{"yes": "it is HDR", "no": "it is not HDR"}[v])
	}
	if v := strOf(c, "dolbyVision", ""); v != "" {
		parts = append(parts, map[string]string{"yes": "it is Dolby Vision", "no": "it is not Dolby Vision"}[v])
	}
	if v := intOf(c, "fps", 0); v > 0 {
		parts = append(parts, "the frame rate is at least "+strconv.Itoa(v))
	}
	if v := intOf(c, "size", 0); v > 0 {
		parts = append(parts, "the file is at least "+strconv.Itoa(v)+" MB")
	}
	if l := listOf(c, "container"); len(l) > 0 {
		parts = append(parts, "the file type is "+strings.Join(upper(l), " or "))
	}
	if v := intOf(c, "bitDepth", 0); v > 0 {
		parts = append(parts, "the picture is "+strconv.Itoa(v)+"-bit")
	}
	if l := listOf(c, "audioCodec"); len(l) > 0 {
		parts = append(parts, "some audio is "+strings.Join(upper(l), " or "))
	}
	if v := intOf(c, "minChannels", 0); v > 0 {
		parts = append(parts, "some audio has at least "+strconv.Itoa(v)+" channels")
	}
	if l := listOf(c, "audioLanguage"); len(l) > 0 {
		parts = append(parts, "some audio is in "+strings.Join(l, " or "))
	}
	if l := listOf(c, "subtitleLanguage"); len(l) > 0 {
		parts = append(parts, "some subtitle is in "+strings.Join(l, " or "))
	}
	if v := intOf(c, "minMinutes", 0); v > 0 {
		parts = append(parts, "it runs at least "+strconv.Itoa(v)+" min")
	}
	if v := strOf(c, "chapters", ""); v != "" {
		parts = append(parts, map[string]string{"yes": "it has chapters", "no": "it has no chapters"}[v])
	}
	if len(parts) == 0 {
		return "(no condition set)"
	}
	return strings.Join(parts, " and ")
}

// upper is each of l in upper case (codec and file type names).
func upper(l []string) []string {
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = strings.ToUpper(s)
	}
	return out
}

// listOf is a config list as strings (a Doc made here holds []string; one read from JSON holds []any).
func listOf(m map[string]any, k string) []string {
	switch v := m[k].(type) {
	case []string:
		return v
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// qualityWords is a slider position in the step summary: "Balanced quality" (design SCREENS V3), but a
// name that already says quality or size stands as it is ("Better quality", not "Better quality
// quality"; "Smaller", which is about the file, not "Smaller quality").
func qualityWords(name string) string {
	n := strings.ToLower(name)
	if strings.Contains(n, "quality") || strings.Contains(n, "small") {
		return name
	}
	return name + " quality"
}

// minutesWords says a number of minutes in the unit that reads best.
func minutesWords(m int) string {
	switch {
	case m >= 1440 && m%1440 == 0:
		return plural(m/1440, "day")
	case m >= 60 && m%60 == 0:
		return plural(m/60, "hour")
	}
	return plural(m, "minute")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}
