// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package vars is what a {{name}} in a workflow's text fields means (design-joblog-vars sections 2 and 3):
// one place that computes a file's values, used by the variable list the editor shows and by the run
// that names its files, and the reading of a template and making a value safe for a file name.
package vars

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// Source is everything a run's values come from. A field that is unknown for the file (no probe, no
// workflow) gives no value for its names: nothing is made up.
type Source struct {
	Path        string      // the file's path as the job has it
	Library     string      // the library's name
	LibraryRoot string      // the library's folder
	SizeBytes   int64       // the original's size (the probe's, when it has one)
	Info        *media.Info // its probe; nil when only the path is known
	Workflow    string      // the workflow's name
	Revision    int         // its newest published revision number (0: unknown)
	Worker      string      // the worker that runs the job
	Encoder     string      // the encoder it converts with ("" when not known yet)
	TargetCodec string      // the codec the new file's video has ("" when not known)
	OutputPath  string      // where the new file was saved ("" while it runs)
	NewSize     int64       // the new file's size (0 while it runs)
	Variables   []Own       // the workflow's own variables
}

// Own is one of a workflow's own variables as the run uses it. A secret never gives a value.
type Own struct {
	Name  string
	Type  string // text | number | boolean | choice | secret (workflow.Variable's words)
	Value any
}

// Values are the values a template's names take for this source: a name with no value is left out (it
// resolves to nothing). Words that name languages are the tags as the file has them.
func (s Source) Values() map[string]string {
	out := map[string]string{}
	put := func(name, v string) {
		if v != "" {
			out[name] = v
		}
	}
	base := filepath.Base(s.Path)
	put("file_name", base)
	put("title", strings.TrimSuffix(base, filepath.Ext(base)))
	put("ext", strings.ToLower(strings.TrimPrefix(filepath.Ext(base), ".")))
	put("folder", filepath.Dir(s.Path))
	put("path", s.Path)
	put("library", s.Library)
	put("library_root", s.LibraryRoot)
	put("size_bytes", bytesText(s.SizeBytes))
	in := s.Info
	before := s.SizeBytes
	if in != nil {
		put("size_bytes", bytesText(in.SizeBytes))
		if in.SizeBytes > 0 {
			before = in.SizeBytes // the probe's own size is the original's
		}
		put("container", strings.TrimPrefix(in.Ext, "."))
		if v := in.Video(); v != nil {
			put("video_codec", v.Codec)
			put("width", intText(v.Width))
			if v.Height > 0 {
				put("height", intText(v.Height))
				put("resolution", intText(v.Height)+"p")
			}
			put("hdr", map[bool]string{true: "yes", false: "no"}[in.IsHDR()])
			put("duration", durationText(in.DurationSec))
			put("bitrate_kbps", intText(in.BitrateKbps))
		}
		put("audio_languages", listText(distinct(in.Audio(), func(s media.Stream) string { return langTag(s.Language) })))
		put("audio_codecs", listText(distinct(in.Audio(), func(s media.Stream) string { return s.Codec })))
		put("audio_channels", listText(distinct(in.Audio(), func(s media.Stream) string { return intText(s.Channels) })))
		put("subtitle_languages", listText(distinct(in.Subtitles(), func(s media.Stream) string { return langTag(s.Language) })))
	}
	put("workflow", s.Workflow)
	if s.Revision > 0 {
		put("workflow_revision", strconv.Itoa(s.Revision))
	}
	put("worker", s.Worker)
	put("encoder", s.Encoder)
	put("target_codec", s.TargetCodec)
	put("output_path", s.OutputPath)
	put("new_size_bytes", bytesText(s.NewSize))
	if before > 0 && s.NewSize > 0 {
		put("saved_percent", percentText(float64(before-s.NewSize)*100/float64(before)))
	}
	for _, v := range s.Variables {
		if v.Type == "secret" || v.Value == nil {
			continue
		}
		put(v.Name, fmt.Sprint(v.Value))
	}
	return out
}

// EndValues are the values only a finished run knows (the Run group's last three): where the new file
// was saved and how much smaller it is. A name the run did not produce is left out. They are the same
// values Source.Values gives for a job, so the log's table and a template's resolution agree.
func EndValues(outputPath string, before, after int64) map[string]string {
	out := map[string]string{}
	if outputPath != "" {
		out["output_path"] = outputPath
	}
	if after > 0 {
		out["new_size_bytes"] = bytesText(after)
	}
	if before > 0 && after > 0 {
		out["saved_percent"] = percentText(float64(before-after) * 100 / float64(before))
	}
	return out
}

// Ref is one {{name}} place in a template: its name and the text span it takes (start of "{{" up to the
// end of "}}").
type Ref struct {
	Name       string
	Start, End int
}

// Refs reads a template's places in order. unclosed is true when a "{{" has no "}}" after it: that one is
// not a place (the whole rest of the text is literal, as the person typed it).
func Refs(t string) (refs []Ref, unclosed bool) {
	for i := 0; ; {
		open := strings.Index(t[i:], "{{")
		if open < 0 {
			return refs, false
		}
		start := i + open
		close := strings.Index(t[start+2:], "}}")
		if close < 0 {
			return refs, true
		}
		end := start + 2 + close + 2
		refs = append(refs, Ref{Name: strings.TrimSpace(t[start+2 : start+2+close]), Start: start, End: end})
		i = end
	}
}

// Resolve fills a template's places with their values: a name with no value (or no such name) becomes
// nothing. The names that came out empty are returned once each, in the order they appear.
func Resolve(t string, vals map[string]string) (string, []string) {
	return resolve(t, vals, nil)
}

// resolve is Resolve with an optional cleaning of each value (path pieces clean their values).
func resolve(t string, vals map[string]string, clean func(string) string) (string, []string) {
	refs, _ := Refs(t)
	if len(refs) == 0 {
		return t, nil
	}
	var b strings.Builder
	var empty []string
	seen := map[string]bool{}
	last := 0
	for _, r := range refs {
		b.WriteString(t[last:r.Start])
		v := vals[r.Name]
		if clean != nil {
			v = clean(v)
		}
		b.WriteString(v)
		if v == "" && !seen[r.Name] {
			seen[r.Name] = true
			empty = append(empty, r.Name)
		}
		last = r.End
	}
	b.WriteString(t[last:])
	return b.String(), empty
}

// CleanValue makes a value safe to put in a file or folder name: no separator ("/" or "\") and no
// character Windows refuses (< > : " | ? *), and no control characters. A value can never reach
// outside the name it is put in.
func CleanValue(v string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			return -1
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
}

// CleanPiece cleans a whole file or folder name as it is about to be used: the refused characters taken,
// then the spaces and dots at either end (Windows drops those silently), so nothing hides or breaks.
func CleanPiece(s string) string {
	return strings.Trim(CleanValue(s), " .")
}

// cleanValue is one value in a place of a name: the refused characters taken and the spaces and dots after
// it (a value that would end a name), but not its start: sub_kind's ".forced" is what it is.
func cleanValue(v string) string {
	return strings.TrimRight(CleanValue(v), " .")
}

// OutputName is the new file's name from its template: the values filled in, made safe for one path
// piece, with ext (".mkv") kept — a name that does not already end in it gets it, after whatever it
// typed itself (so "Movie.2019" keeps its 2019: only the ends of a name are the file's business).
// "" means today's name (the template is empty, or what it came out as is empty, "." or "..", or a name
// Windows refuses). The names that came out empty are returned with it, for the job log.
func OutputName(tpl, ext string, vals map[string]string) (string, []string) {
	if strings.TrimSpace(tpl) == "" {
		return "", nil
	}
	s, empty := resolve(tpl, vals, cleanValue) // each value is one piece: no separators, no dot at its end
	s = CleanPiece(s)
	if s == "" || s == "." || s == ".." {
		return "", empty
	}
	if !strings.EqualFold(filepath.Ext(s), ext) {
		s += ext
	}
	if base := strings.TrimSuffix(s, ext); base == "" || Reserved(strings.SplitN(base, ".", 2)[0]) {
		return "", empty // "CON.mkv" and "CON.2019.mkv": Windows refuses them, whatever their extension
	}
	return s, empty
}

// Folder is the move-to folder from its template: each value made safe for one path piece, the text
// between the places (the path itself) kept as it is. names that came out empty are returned too.
func Folder(tpl string, vals map[string]string) (string, []string) {
	if !strings.Contains(tpl, "{{") {
		return tpl, nil // today's plain folder, as saved
	}
	s, empty := resolve(tpl, vals, cleanValue)
	return strings.TrimSpace(s), empty
}

// Subtitle is one extracted subtitle, as the .srt name's template sees it: its language tag, its kind
// (".forced", ".sdh" or "", with its dot, so a name reads "Movie.eng.forced.srt"), and the default
// name's part (Part: "eng", "eng.forced", "eng.2").
type Subtitle struct {
	Part, Lang, Kind string
}

// SrtNames is where each extracted subtitle's file goes beside final, in order: the .srt name template
// filled in, or (an empty one) today's name, final's base plus "." plus the part plus ".srt". Each value
// is made safe for a file name, a name that does not end in .srt gets it, and two that come out the same
// get ".2", ".3" before it (as today's do). A name Windows refuses ("CON.srt", checked as OutputName
// checks it) and the names that came out empty keep today's name; the empty ones are returned too.
func SrtNames(final, tpl string, subs []Subtitle, vals map[string]string) ([]string, []string) {
	dir := filepath.Dir(final)
	base := strings.TrimSuffix(filepath.Base(final), filepath.Ext(final))
	out := make([]string, len(subs))
	var empty []string
	seen := map[string]bool{}
	report := func(names []string) {
		for _, n := range names {
			if n == "sub_lang" || n == "sub_kind" || seen[n] { // no language or kind is how most subtitles are
				continue
			}
			seen[n] = true
			empty = append(empty, n)
		}
	}
	used := map[string]int{}
	for k, s := range subs {
		name := defaultSrtName(base, s.Part)
		if strings.TrimSpace(tpl) != "" {
			v := make(map[string]string, len(vals)+2)
			for n, val := range vals {
				v[n] = val
			}
			v["sub_lang"], v["sub_kind"] = s.Lang, s.Kind
			r, e := resolve(tpl, v, cleanValue)
			report(e)
			if r = CleanPiece(r); r != "" && r != "." && r != ".." && !Reserved(strings.SplitN(r, ".", 2)[0]) {
				name = r
				if !strings.HasSuffix(strings.ToLower(name), ".srt") {
					name += ".srt"
				}
			}
		}
		used[name]++
		if n := used[name]; n > 1 {
			ext := filepath.Ext(name)
			name = strings.TrimSuffix(name, ext) + "." + strconv.Itoa(n) + ext
		}
		out[k] = filepath.Join(dir, name)
	}
	return out, empty
}

// defaultSrtName is today's name for an extracted subtitle: "Movie.srt", "Movie.eng.srt".
func defaultSrtName(base, part string) string {
	if part == "" {
		return base + ".srt"
	}
	return base + "." + part + ".srt"
}

// reservedNames are the names Windows refuses whatever their extension: CON.mkv is still CON.
var reservedNames = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true, "com1": true, "com2": true,
	"com3": true, "com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true,
	"lpt8": true, "lpt9": true}

// Reserved reports whether base is one of the names Windows refuses.
func Reserved(base string) bool { return reservedNames[strings.ToLower(base)] }

// distinct is one field of each stream, first seen first, without repeats or blanks.
func distinct(streams []media.Stream, f func(media.Stream) string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range streams {
		if v := f(s); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// langTag is a stream's language as the file tags it ("" when untagged, "und" when unknown: neither is
// shown).
func langTag(l string) string {
	if l == "und" {
		return ""
	}
	return l
}

func listText(items []string) string { return strings.Join(items, ", ") }

func intText(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func bytesText(n int64) string {
	if n <= 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

// durationText is a length as the design shows it: 1:45:12.
func durationText(sec float64) string {
	if sec <= 0 {
		return ""
	}
	s := int(math.Round(sec))
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}

// percentText is a percentage to one decimal, without a needless ".0" (62.5, 62, -4.2).
func percentText(p float64) string {
	s := strconv.FormatFloat(p, 'f', 1, 64)
	if strings.HasSuffix(s, ".0") {
		return strings.TrimSuffix(s, ".0")
	}
	return s
}
