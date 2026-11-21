// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package flow runs the cards of a workflow path that sit outside the conversion (docs/architecture/
// flow-builder.md, section 6): the actions before it (FB1c) and, later, the actions after the save
// (FB2). It has no storage and no HTTP; the worker gives it the run's state and what the actions may
// touch, and records each card's words as a step of the job log.
package flow

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/vars"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// State is one run's state as the cards see it.
type State struct {
	Vars map[string]string // the built-ins, the workflow's own variables, and what cards set (vars.Resolve's values)
	// After the save (FB2a): the new file in place, the original's path, and the library's root folder (a
	// copy keeps the file's sub-folders under it).
	Final       string
	Original    string
	LibraryRoot string
}

// Env is what actions may touch; the worker fills it. A nil function means the action can't run here.
type Env struct {
	Log func(line string) // a line into the job log (Add a note)
	// FB2a: the connections and the notification targets. Each answers in words, or with an error.
	TellMediaServer func(which string) (string, error) // "all" | plex | jellyfin
	TellDownloader  func(which string) (string, error) // "all" | sonarr | radarr
	Notify          func(target, kind, text string) (string, error)
	// FB2b: a program run on the saved result (nil when "Workflows may run commands" is off in Settings > Safety),
	// and the downloaders' answer about a file (the values to publish, and words for the log).
	Exec   func(program string, args []string, dir string, timeout time.Duration, onLine func(string)) (output string, err error)
	Lookup func(which string) (vals map[string]string, words string, err error)
	// FB2c: tags on the job record; answers with the record's tags after the change.
	Tag func(add, remove []string) ([]string, error)
}

// MaxOutput is how much of a program's output {{command_output}} keeps.
const MaxOutput = 4000

// Note is what one card did, in words, for the job log's timeline.
type Note struct {
	NodeID string
	Name   string
	Words  string
	Empty  []string // the {{name}} references that had no value
	Failed bool     // the card didn't work; Words says why (After only: Before stops at the first one)
}

// Before runs the path's actions before the conversion, in order. The first action that doesn't work
// stops the run: the worker fails the job with the error's words, the original untouched (section 1.5).
func Before(cards []rules.Card, st *State, env Env) ([]Note, error) {
	if st.Vars == nil {
		st.Vars = map[string]string{}
	}
	var out []Note
	for _, c := range cards {
		n, err := run(c, st, env)
		if err != nil {
			return out, fmt.Errorf("%s didn't work: %w", nameOf(c), err)
		}
		out = append(out, n)
	}
	return out, nil
}

// After runs the path's actions on the saved result, in order. One that doesn't work is a warning on the
// job, never a failure: the save is done and stays done, and every card still runs (section 1.5).
func After(cards []rules.Card, st *State, env Env) (notes []Note, warnings []string) {
	if st.Vars == nil {
		st.Vars = map[string]string{}
	}
	for _, c := range cards {
		n, err := run(c, st, env)
		if err != nil {
			n.Words, n.Failed = nameOf(c)+" didn't work: "+err.Error(), true
			warnings = append(warnings, n.Words)
		}
		notes = append(notes, n)
	}
	return notes, warnings
}

// run does one card.
func run(c rules.Card, st *State, env Env) (Note, error) {
	n := Note{NodeID: c.NodeID, Name: nameOf(c)}
	str := func(k, def string) string {
		if v, ok := c.Config[k].(string); ok && v != "" {
			return v
		}
		return def
	}
	switch c.Type {
	case workflow.TypeTag:
		addText, empty := vars.Resolve(str("add", ""), st.Vars)
		removeText, more := vars.Resolve(str("remove", ""), st.Vars)
		add, remove := rules.CleanTags(addText), rules.CleanTags(removeText)
		n.Empty = append(empty, more...)
		if len(add) == 0 && len(remove) == 0 {
			if len(n.Empty) > 0 { // review2-542 P3d: say which name was empty
				return n, errors.New("nothing to add or remove: {{" + strings.Join(n.Empty, "}}, {{") + "}} had no value")
			}
			return n, errors.New("nothing to add or remove")
		}
		if env.Tag == nil { // review-542: a store without tags yet: say so and go on, the run is not the card's to stop
			n.Words = "Tags not kept: this install's records have no tags yet, so nothing changed"
			if env.Log != nil {
				env.Log(nameOf(c) + ": " + n.Words)
			}
			return n, nil
		}
		tags, err := env.Tag(add, remove)
		if err != nil {
			return n, err
		}
		st.Vars["tags"] = strings.Join(tags, ", ")
		n.Words = "Tags: " + quoteOr(strings.Join(tags, ", "), "none")
		if _, leftOff := rules.ApplyTagsCounted(tags, add, nil); leftOff > 0 { // what the store could not take
			n.Words += " (" + strconv.Itoa(leftOff) + " left off: a record carries at most " + strconv.Itoa(rules.MaxTags) + ")"
		}
		return n, nil
	case workflow.TypeLookupDownloader:
		if env.Lookup == nil {
			return n, errors.New("no downloader can be asked from here")
		}
		vals, words, err := env.Lookup(str("which", "all"))
		if err != nil {
			return n, err
		}
		for _, k := range []string{"title_clean", "year", "season", "episode", "original_language"} {
			st.Vars[k] = vals[k] // every name is set, empty when the downloader has no such thing
		}
		n.Words = words
		return n, nil
	case workflow.TypeCommand:
		if env.Exec == nil {
			return n, errors.New("running commands is off: turn on \"Workflows may run commands\" in Settings > Safety")
		}
		program := strings.TrimSpace(str("program", ""))
		if program == "" {
			return n, errors.New("no program is named")
		}
		parts, err := SplitArgs(str("arguments", "")) // review2-539 P2b: split as written, then fill each in: a value never breaks out of its argument
		if err != nil {
			return n, err
		}
		var args, empty []string
		for _, part := range parts {
			v, more := vars.Resolve(part, st.Vars)
			args, empty = append(args, v), append(empty, more...)
		}
		timeout := 60 * time.Second
		if v, ok := c.Config["timeoutSec"].(float64); ok && v >= 1 && v <= 3600 {
			timeout = time.Duration(v) * time.Second
		} else if v, ok := c.Config["timeoutSec"].(int); ok && v >= 1 && v <= 3600 {
			timeout = time.Duration(v) * time.Second
		}
		dir := ""
		if st.Final != "" {
			dir = filepath.Dir(st.Final)
		}
		onLine := func(string) {}
		if env.Log != nil {
			onLine = func(l string) { env.Log(program + ": " + l) }
		}
		out, err := env.Exec(program, args, dir, timeout, onLine)
		if len(out) > MaxOutput {
			out = out[:MaxOutput]
			for !utf8.ValidString(out) { // never a cut rune
				out = out[:len(out)-1]
			}
		}
		st.Vars["command_output"] = strings.TrimSpace(out)
		n.Empty = empty
		if err != nil {
			return n, err
		}
		n.Words = "Ran " + program + " (exit 0)"
		return n, nil
	case workflow.TypeMediaServer:
		if env.TellMediaServer == nil {
			return n, errors.New("no media server can be told from here")
		}
		words, err := env.TellMediaServer(str("which", "all"))
		n.Words = words
		return n, err
	case workflow.TypeDownloader:
		if env.TellDownloader == nil {
			return n, errors.New("no downloader can be told from here")
		}
		words, err := env.TellDownloader(str("which", "all"))
		n.Words = words
		return n, err
	case workflow.TypeNotify:
		if env.Notify == nil {
			return n, errors.New("no notification target can be reached from here")
		}
		text, empty := vars.Resolve(str("text", ""), st.Vars)
		words, err := env.Notify(str("target", ""), str("kind", "good"), text)
		n.Words, n.Empty = words, empty
		return n, err
	case workflow.TypeSidecar:
		if st.Final == "" {
			return n, errors.New("there is no saved file yet")
		}
		name, empty := vars.Resolve(str("name", ""), st.Vars)
		name = vars.CleanPiece(strings.TrimSpace(name))
		if name == "" || name == "." || name == ".." {
			return n, errors.New("the file name came out empty")
		}
		path := filepath.Join(filepath.Dir(st.Final), name)
		if what := protected(path, st); what != "" { // review2-538 P2: beside the result, never over it
			return n, errors.New("it would write over " + what)
		}
		if _, err := os.Lstat(path); err == nil && str("ifExists", "keep") == "keep" {
			n.Words = "Left " + path + " as it is: a file with that name is there"
			return n, nil
		}
		text, more := vars.Resolve(str("text", ""), st.Vars)
		if err := writeWhole(path, []byte(text)); err != nil {
			return n, err
		}
		n.Words, n.Empty = "Wrote "+path, append(empty, more...)
		return n, nil
	case workflow.TypeChecksum:
		if st.Final == "" {
			return n, errors.New("there is no saved file yet")
		}
		kind := str("kind", "sha256")
		var h hash.Hash
		switch kind {
		case "sha1":
			h = sha1.New()
		case "md5":
			h = md5.New()
		default:
			kind, h = "sha256", sha256.New()
		}
		f, err := os.Open(st.Final)
		if err != nil {
			return n, err
		}
		defer f.Close()
		if _, err := io.Copy(h, f); err != nil {
			return n, err
		}
		sum := hex.EncodeToString(h.Sum(nil))
		st.Vars["checksum"], st.Vars["checksum_kind"] = sum, kind
		n.Words = strings.ToUpper(kind) + " " + sum
		return n, nil
	case workflow.TypeCopy:
		if st.Final == "" {
			return n, errors.New("there is no saved file yet")
		}
		folder, empty := vars.Resolve(str("folder", ""), st.Vars)
		folder = strings.TrimSpace(folder)
		if !filepath.IsAbs(folder) {
			return n, errors.New("the folder came out relative: " + quote(folder))
		}
		sub := "" // the file's sub-folders inside its library, kept under the copy's folder
		if st.LibraryRoot != "" {
			if rel, err := filepath.Rel(st.LibraryRoot, filepath.Dir(st.Final)); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				sub = rel
			}
		}
		name := filepath.Base(st.Final)
		if t := str("name", ""); t != "" {
			resolved, more := vars.Resolve(t, st.Vars)
			empty = append(empty, more...)
			if nm := vars.CleanPiece(strings.TrimSpace(resolved)); nm != "" {
				name = nm
				if filepath.Ext(name) == "" {
					name += filepath.Ext(st.Final)
				}
			}
		}
		dst := filepath.Join(folder, sub, name)
		if what := protected(dst, st); what != "" { // review2-538 P2: a copy never lands on the saved file or the original
			return n, errors.New("the copy would write over " + what)
		}
		if _, err := os.Lstat(dst); err == nil && str("ifExists", "keep") == "keep" {
			st.Vars["copy_path"] = dst
			n.Words = "Left " + dst + " as it is: a file with that name is there"
			return n, nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return n, err
		}
		if err := copyFile(st.Final, dst); err != nil {
			return n, err
		}
		st.Vars["copy_path"] = dst
		n.Words, n.Empty = "Copied to "+dst, empty
		return n, nil
	case workflow.TypeSetVariable:
		name, _ := c.Config["name"].(string)
		if !workflow.NamePattern.MatchString(name) || workflow.BuiltinName(name) {
			return n, fmt.Errorf("%q is not a name a step may set", name)
		}
		tpl, _ := c.Config["value"].(string)
		val, empty := vars.Resolve(tpl, st.Vars)
		st.Vars[name] = val
		n.Words, n.Empty = "Set {{"+name+"}} = "+quote(val), empty
		return n, nil
	case workflow.TypeNote:
		tpl, _ := c.Config["text"].(string)
		text, empty := vars.Resolve(tpl, st.Vars)
		if env.Log != nil {
			env.Log(text)
		}
		n.Words, n.Empty = "Note: "+quote(text), empty
		return n, nil
	}
	return n, fmt.Errorf("a step this version can't run (%s)", c.Type)
}

func nameOf(c rules.Card) string {
	if c.Name != "" {
		return c.Name
	}
	if d, ok := workflow.Definition(c.Type); ok {
		return d.Name
	}
	return c.Type
}

func quote(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 120 {
		s = string(r[:117]) + "..."
	}
	return "“" + s + "”"
}

// copyFile writes src's bytes to a temporary name beside dst and renames it into place, so a reader never
// sees a half-written copy.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// protected names what a card must never write over: the saved file ("the new file") or the original
// ("the original"), by path or, when both exist, by identity. "" when path is neither.
func protected(path string, st *State) string {
	same := func(a, b string) bool {
		if b == "" {
			return false
		}
		if filepath.Clean(a) == filepath.Clean(b) {
			return true
		}
		fa, errA := os.Stat(a)
		fb, errB := os.Stat(b)
		return errA == nil && errB == nil && os.SameFile(fa, fb)
	}
	switch {
	case same(path, st.Final):
		return "the new file"
	case same(path, st.Original):
		return "the original"
	}
	return ""
}

// writeWhole writes b to a temporary name beside path and renames it into place, so a reader never sees a
// half-written file (review2-538 P3a).
func writeWhole(path string, b []byte) error {
	tmp := path + ".part"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// SplitArgs splits a card's arguments on spaces, keeping what is inside double quotes as one argument
// (a backslash before a quote keeps the quote). No shell is involved, so nothing else is special.
func SplitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in, has := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '\\' && i+1 < len(s) && s[i+1] == '"':
			cur.WriteByte('"')
			i++
			has = true
		case ch == '"':
			in, has = !in, true
		case !in && (ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'):
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteByte(ch)
			has = true
		}
	}
	if in {
		return nil, errors.New("the arguments have a double quote that is never closed")
	}
	if has {
		out = append(out, cur.String())
	}
	return out, nil
}

// quoteOr quotes s, or says what an empty one is.
func quoteOr(s, empty string) string {
	if s == "" {
		return empty
	}
	return quote(s)
}
