// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// Set a variable makes a name the cards after it read; Add a note writes the resolved line.
func TestBeforeRunsTheCardsInOrder(t *testing.T) {
	var logged []string
	st := &State{Vars: map[string]string{"title": "Harrow Lane (2019)", "resolution": "1080p"}}
	notes, err := Before([]rules.Card{
		{NodeID: "a", Type: workflow.TypeSetVariable, Config: map[string]any{"name": "profile", "value": "remux of {{title}}"}},
		{NodeID: "b", Type: workflow.TypeNote, Name: "Say so", Config: map[string]any{"text": "Converting {{title}} as {{profile}} at {{resolution}}"}},
	}, st, Env{Log: func(l string) { logged = append(logged, l) }})
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["profile"] != "remux of Harrow Lane (2019)" {
		t.Errorf("profile = %q", st.Vars["profile"])
	}
	if len(logged) != 1 || logged[0] != "Converting Harrow Lane (2019) as remux of Harrow Lane (2019) at 1080p" {
		t.Errorf("logged %q", logged)
	}
	if len(notes) != 2 || notes[0].Name != "Set a variable" || !strings.Contains(notes[0].Words, "{{profile}}") || notes[1].Name != "Say so" || len(notes[1].Empty) != 0 {
		t.Errorf("notes %+v", notes)
	}
	// a name with no value is reported, not invented
	notes, err = Before([]rules.Card{{NodeID: "c", Type: workflow.TypeNote, Config: map[string]any{"text": "{{nothing}} here"}}}, st, Env{})
	if err != nil || len(notes) != 1 || len(notes[0].Empty) != 1 || notes[0].Empty[0] != "nothing" {
		t.Errorf("%v %+v", err, notes)
	}
}

// A card the engine can't run, or a name a step may not set, stops the run with the card's name.
func TestBeforeStopsAtTheFirstFailure(t *testing.T) {
	st := &State{}
	notes, err := Before([]rules.Card{
		{NodeID: "a", Type: workflow.TypeSetVariable, Config: map[string]any{"name": "ok", "value": "1"}},
		{NodeID: "b", Type: workflow.TypeSetVariable, Name: "Bad one", Config: map[string]any{"name": "title", "value": "x"}},
		{NodeID: "c", Type: workflow.TypeNote, Config: map[string]any{"text": "never"}},
	}, st, Env{})
	if err == nil || !strings.HasPrefix(err.Error(), "Bad one didn't work: ") || len(notes) != 1 || st.Vars["ok"] != "1" {
		t.Errorf("%v %+v %v", err, notes, st.Vars)
	}
	if _, err := Before([]rules.Card{{NodeID: "z", Type: "action.later"}}, st, Env{}); err == nil || !strings.Contains(err.Error(), "can't run") {
		t.Errorf("%v", err)
	}
}

// FB2a: the after-save cards act on the saved file. A checksum is published for the cards after it, a sidecar
// is written beside the file with the variables filled in, a copy keeps the library's sub-folders, the
// connections and the targets are told through the env and answer in words, and one card that doesn't
// work is a warning while every card after it still runs.
func TestAfterCardsActOnTheSavedFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Films", "Harrow Lane (2019)")
	os.MkdirAll(dir, 0o755)
	final := filepath.Join(dir, "Harrow Lane (2019).mkv")
	os.WriteFile(final, []byte("hello"), 0o644)
	backup := filepath.Join(t.TempDir(), "backup")
	var told, sent []string
	env := Env{
		TellMediaServer: func(which string) (string, error) { told = append(told, "media:"+which); return "Told Plex", nil },
		TellDownloader:  func(string) (string, error) { return "", errors.New("none of these is connected and on") },
		Notify: func(target, kind, text string) (string, error) {
			sent = append(sent, target+"|"+kind+"|"+text)
			return "Sent to phone", nil
		},
	}
	cards := []rules.Card{
		{NodeID: "sum", Type: workflow.TypeChecksum, Name: "Checksum the result", Config: map[string]any{"kind": "sha256"}},
		{NodeID: "side", Type: workflow.TypeSidecar, Name: "Write a file beside the result", Config: map[string]any{"name": "{{title}}.sha256", "text": "{{checksum}}  {{title}}.mkv"}},
		{NodeID: "dl", Type: workflow.TypeDownloader, Name: "Tell the downloader", Config: map[string]any{}},
		{NodeID: "copy", Type: workflow.TypeCopy, Name: "Copy the result to a folder", Config: map[string]any{"folder": backup, "name": "{{title}} ({{checksum_kind}})"}},
		{NodeID: "ms", Type: workflow.TypeMediaServer, Name: "Tell the media server", Config: map[string]any{"which": "plex"}},
		{NodeID: "say", Type: workflow.TypeNotify, Name: "Send a notification", Config: map[string]any{"target": "t1", "kind": "good", "text": "{{title}} done, copy at {{copy_path}}"}},
	}
	st := &State{Vars: map[string]string{"title": "Harrow Lane (2019)"}, Final: final, Original: final, LibraryRoot: filepath.Join(root, "Films")}
	notes, warnings := After(cards, st, env)
	if len(notes) != 6 {
		t.Fatalf("%d notes, want one per card: %+v", len(notes), notes)
	}
	const sum = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" // sha256("hello")
	if st.Vars["checksum"] != sum || st.Vars["checksum_kind"] != "sha256" || notes[0].Words != "SHA256 "+sum {
		t.Errorf("checksum: %q %q %q", st.Vars["checksum"], st.Vars["checksum_kind"], notes[0].Words)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "Harrow Lane (2019).sha256")); err != nil || string(b) != sum+"  Harrow Lane (2019).mkv" {
		t.Errorf("sidecar: %q %v", b, err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Tell the downloader didn't work: none of these is connected") || !notes[2].Failed {
		t.Errorf("the downloader card should be the one warning: %v %+v", warnings, notes[2])
	}
	want := filepath.Join(backup, "Harrow Lane (2019)", "Harrow Lane (2019) (sha256).mkv")
	if b, err := os.ReadFile(want); err != nil || string(b) != "hello" || st.Vars["copy_path"] != want {
		t.Errorf("copy: %q %v, copy_path %q", b, err, st.Vars["copy_path"])
	}
	if fmt.Sprint(told) != "[media:plex]" || notes[4].Words != "Told Plex" {
		t.Errorf("media server: %v %+v", told, notes[4])
	}
	if fmt.Sprint(sent) != "[t1|good|Harrow Lane (2019) done, copy at "+want+"]" || notes[5].Words != "Sent to phone" {
		t.Errorf("notify: %v %+v", sent, notes[5])
	}
	for i, n := range notes {
		if i != 2 && n.Failed {
			t.Errorf("note %d failed: %+v", i, n)
		}
	}

	// a second run: the sidecar and the copy are left as they are (ifExists keep), and say so; replace writes over
	os.WriteFile(final, []byte("hello again"), 0o644)
	notes, warnings = After(cards[:2], st, env)
	if len(warnings) != 0 || !strings.HasPrefix(notes[1].Words, "Left ") {
		t.Errorf("keep: %v %+v", warnings, notes[1])
	}
	replace := rules.Card{NodeID: "side", Type: workflow.TypeSidecar, Name: "Write", Config: map[string]any{"name": "{{title}}.sha256", "text": "{{checksum}}", "ifExists": "replace"}}
	After([]rules.Card{replace}, st, env)
	if b, _ := os.ReadFile(filepath.Join(dir, "Harrow Lane (2019).sha256")); string(b) != st.Vars["checksum"] || string(b) == sum {
		t.Errorf("replace: %q", b)
	}

	// review2-538 P2: with "Replace it", a sidecar or a copy aimed at the saved file or the original is refused
	// and both files keep their bytes; a name that resolves to the original's name is the common case
	os.WriteFile(final, []byte("THE ENCODE"), 0o644)
	original := filepath.Join(dir, "Harrow Lane (2019).original.mkv")
	os.WriteFile(original, []byte("THE ORIGINAL"), 0o644)
	st.Original, st.Vars["file_name"] = original, filepath.Base(original)
	for _, c := range []rules.Card{
		{NodeID: "s1", Type: workflow.TypeSidecar, Name: "Sidecar", Config: map[string]any{"name": "{{file_name}}", "text": "a note", "ifExists": "replace"}},
		{NodeID: "s2", Type: workflow.TypeSidecar, Name: "Sidecar", Config: map[string]any{"name": "{{title}}.mkv", "text": "a note", "ifExists": "replace"}},
		{NodeID: "c1", Type: workflow.TypeCopy, Name: "Copy", Config: map[string]any{"folder": st.LibraryRoot, "name": "{{file_name}}", "ifExists": "replace"}}, // the library's sub-folder lands it beside the original
		{NodeID: "c2", Type: workflow.TypeCopy, Name: "Copy", Config: map[string]any{"folder": st.LibraryRoot, "name": "{{title}}", "ifExists": "replace"}},
	} {
		notes, warnings := After([]rules.Card{c}, st, env)
		if len(warnings) != 1 || !strings.Contains(warnings[0], "write over the") || !notes[0].Failed {
			t.Errorf("%s onto a protected file: %v %+v", c.NodeID, warnings, notes)
		}
	}
	if b, _ := os.ReadFile(final); string(b) != "THE ENCODE" {
		t.Errorf("the saved file was written over: %q", b)
	}
	if b, _ := os.ReadFile(original); string(b) != "THE ORIGINAL" {
		t.Errorf("the original was written over: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "Harrow Lane (2019).mkv.part")); err == nil {
		t.Error("a .part file was left behind")
	}
	// without a saved file, an env or an absolute folder every card says so and nothing is written
	notes, warnings = After(cards, &State{}, Env{})
	if len(warnings) != 6 {
		t.Errorf("no file, no env: %v", warnings)
	}
	_, warnings = After([]rules.Card{{NodeID: "c", Type: workflow.TypeCopy, Name: "Copy", Config: map[string]any{"folder": "relative/{{missing}}"}}}, st, env)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "relative") {
		t.Errorf("relative folder: %v", warnings)
	}
	_ = notes
}

func TestSplitArgs(t *testing.T) {
	got, err := SplitArgs(`"/m/My Film.mkv" 42  a\"b "" x`)
	if err != nil || fmt.Sprint(got) != `[/m/My Film.mkv 42 a"b  x]` || len(got) != 5 {
		t.Errorf("%q %v", got, err)
	}
	if _, err := SplitArgs(`"open`); err == nil {
		t.Error("an unclosed quote: want an error")
	}
	if got, _ := SplitArgs("  "); len(got) != 0 {
		t.Errorf("blank: %q", got)
	}
}

// FB2b: Run a command resolves its arguments, runs in the saved file's folder with the timeout from its config,
// publishes what the program printed (cut to MaxOutput), logs each line, and reports an exit code as a warning;
// with no Exec (the Safety switch off) it says so. Ask the downloader sets every name, empty when unknown.
func TestCommandAndLookupCards(t *testing.T) {
	final := filepath.Join(t.TempDir(), "Film.mkv")
	os.WriteFile(final, []byte("x"), 0o644)
	var got struct {
		program, dir string
		args         []string
		timeout      time.Duration
	}
	var logged []string
	env := Env{
		Log: func(l string) { logged = append(logged, l) },
		Exec: func(program string, args []string, dir string, timeout time.Duration, onLine func(string)) (string, error) {
			got.program, got.args, got.dir, got.timeout = program, args, dir, timeout
			onLine("line one")
			if program == "fails" {
				return "bad\n", errors.New("fails exited with code 3")
			}
			return "line one\n" + strings.Repeat("y", MaxOutput+50), nil
		},
		Lookup: func(which string) (map[string]string, string, error) {
			if which == "radarr" {
				return map[string]string{"title_clean": "", "year": "", "season": "", "episode": "", "original_language": ""}, "No connected downloader knows this file", nil
			}
			return map[string]string{"title_clean": "The Show", "year": "2019", "season": "1", "episode": "2", "original_language": "English"}, "Sonarr: The Show (2019) S01E02", nil
		},
	}
	st := &State{Vars: map[string]string{"title": "My Film", "output_path": final}, Final: final}
	cmd := rules.Card{NodeID: "c", Type: workflow.TypeCommand, Name: "Run a command", Config: map[string]any{"program": "/usr/local/bin/after", "arguments": `"{{output_path}}" {{title}} --n=2`, "timeoutSec": float64(5)}}
	notes, warnings := After([]rules.Card{cmd}, st, env)
	if len(warnings) != 0 || got.program != "/usr/local/bin/after" || fmt.Sprint(got.args) != "["+final+" My Film --n=2]" || got.dir != filepath.Dir(final) || got.timeout != 5*time.Second {
		t.Errorf("run: %+v %v %+v", got, warnings, notes)
	}
	if out := st.Vars["command_output"]; !strings.HasPrefix(out, "line one\n") || len(out) > MaxOutput || notes[0].Words != "Ran /usr/local/bin/after (exit 0)" {
		t.Errorf("output: %d %q", len(out), notes[0].Words)
	}
	if fmt.Sprint(logged) != "[/usr/local/bin/after: line one]" {
		t.Errorf("log: %v", logged)
	}
	bad := rules.Card{NodeID: "c", Type: workflow.TypeCommand, Name: "Run a command", Config: map[string]any{"program": "fails"}}
	if _, warnings := After([]rules.Card{bad}, st, env); len(warnings) != 1 || !strings.Contains(warnings[0], "Run a command didn't work: fails exited with code 3") || st.Vars["command_output"] != "bad" {
		t.Errorf("exit code: %v %q", warnings, st.Vars["command_output"])
	}
	if _, warnings := After([]rules.Card{cmd}, st, Env{}); len(warnings) != 1 || !strings.Contains(warnings[0], "Settings > Safety") {
		t.Errorf("switch off: %v", warnings)
	}
	if _, warnings := After([]rules.Card{{NodeID: "c", Type: workflow.TypeCommand, Name: "Run", Config: map[string]any{"program": "x", "arguments": `"open`}}}, st, env); len(warnings) != 1 || !strings.Contains(warnings[0], "never closed") {
		t.Errorf("bad quoting: %v", warnings)
	}

	look := rules.Card{NodeID: "l", Type: workflow.TypeLookupDownloader, Name: "Ask the downloader for details", Config: map[string]any{}}
	st2 := &State{Vars: map[string]string{}}
	notes, err := Before([]rules.Card{look}, st2, env)
	if err != nil || st2.Vars["title_clean"] != "The Show" || st2.Vars["episode"] != "2" || notes[0].Words != "Sonarr: The Show (2019) S01E02" {
		t.Errorf("lookup: %v %+v %v", st2.Vars, notes, err)
	}
	look.Config = map[string]any{"which": "radarr"}
	if _, err := Before([]rules.Card{look}, st2, env); err != nil || st2.Vars["title_clean"] != "" || st2.Vars["year"] != "" {
		t.Errorf("unknown file: %v %v", st2.Vars, err)
	}
	if _, err := Before([]rules.Card{look}, st2, Env{}); err == nil {
		t.Error("no lookup from here: want an error")
	}
}

// RunProgram with a real program: this test binary as a helper (see TestMain), its output line by line, the exit
// code as an error, a program that isn't there, and the timeout.
func TestRunProgram(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	var lines []string
	out, err := RunProgram(self, []string{"-flow.helper", "echo", "one", "two words"}, t.TempDir(), 10*time.Second, func(l string) { lines = append(lines, l) })
	if err != nil || out != "one\ntwo words\n" || fmt.Sprint(lines) != "[one two words]" {
		t.Errorf("echo: %q %v %v", out, err, lines)
	}
	out, err = RunProgram(self, []string{"-flow.helper", "exit", "3"}, "", 10*time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "exited with code 3") || out != "exiting 3\n" {
		t.Errorf("exit: %q %v", out, err)
	}
	if _, err := RunProgram(filepath.Join(t.TempDir(), "no-such-program"), nil, "", time.Second, nil); err == nil || !strings.Contains(err.Error(), "could not start") {
		t.Errorf("missing: %v", err)
	}
	if _, err := RunProgram(self, []string{"-flow.helper", "sleep"}, "", 300*time.Millisecond, nil); err == nil || !strings.Contains(err.Error(), "stopped after") {
		t.Errorf("timeout: %v", err)
	}
	// review2-539 P2a: a program that exits 0 but leaves a child holding its output is finished, near the timeout,
	// never "stopped"; the worker's loop is not held for as long as the child lives
	began := time.Now()
	out, err = RunProgram(self, []string{"-flow.helper", "spawn-sleep"}, "", 500*time.Millisecond, nil)
	if took := time.Since(began); err != nil || !strings.Contains(out, "spawned") || took > 5*time.Second {
		t.Errorf("a child left behind: %q %v after %s", out, err, took)
	}
	// and one that exits 3 the same way keeps its exit code
	if _, err := RunProgram(self, []string{"-flow.helper", "spawn-sleep", "3"}, "", 500*time.Millisecond, nil); err == nil || !strings.Contains(err.Error(), "exited with code 3") {
		t.Errorf("a child left behind, exit 3: %v", err)
	}
	// review2-539 P3a: the log takes the first MaxLogLines lines and says so once; the output value is still capped
	var logged []string
	out, err = RunProgram(self, []string{"-flow.helper", "many", "300"}, "", 10*time.Second, func(l string) { logged = append(logged, l) })
	if err != nil || len(logged) != MaxLogLines+1 || logged[MaxLogLines] != "… more output not shown in the log" || len(out) > MaxOutput+2048 {
		t.Errorf("many lines: %d logged, last %q, %d bytes, %v", len(logged), logged[len(logged)-1], len(out), err)
	}
}

// review2-539 P2b: a file name with a double quote, a space, $(…) or a backtick stays one argument, exactly as
// the author quoted the placeholder; no value can add or split arguments.
func TestCommandArgumentsFromHostileNames(t *testing.T) {
	var got []string
	env := Env{Exec: func(program string, args []string, dir string, timeout time.Duration, onLine func(string)) (string, error) {
		got = args
		return "", nil
	}}
	for _, name := range []string{`Movie" --output=/etc/passwd --quiet=".mkv`, "My Film (2019).mkv", "$(id).mkv", "a`b`.mkv", `C:\\Films\\Movie.mkv`} {
		st := &State{Vars: map[string]string{"output_path": "/m/" + name, "title": "x y"}, Final: "/m/" + name}
		card := rules.Card{NodeID: "c", Type: workflow.TypeCommand, Name: "Run", Config: map[string]any{"program": "p", "arguments": `"{{output_path}}" --title={{title}} {{output_path}}`}}
		if _, warnings := After([]rules.Card{card}, st, env); len(warnings) != 0 || len(got) != 3 || got[0] != "/m/"+name || got[1] != "--title=x y" || got[2] != "/m/"+name {
			t.Errorf("%q: args %q %v", name, got, warnings)
		}
	}
}

// TestMain lets the test binary stand in for a program (-flow.helper echo|exit|sleep ...).
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "-flow.helper" {
		switch os.Args[2] {
		case "echo":
			for _, a := range os.Args[3:] {
				fmt.Println(a)
			}
		case "exit":
			code, _ := strconv.Atoi(os.Args[3])
			fmt.Println("exiting", code)
			os.Exit(code)
		case "sleep":
			time.Sleep(10 * time.Second)
		case "spawn-sleep": // a child that inherits this program's output and outlives it
			child := exec.Command(os.Args[0], "-flow.helper", "sleep")
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
			child.Start()
			fmt.Println("spawned")
			if len(os.Args) > 3 {
				code, _ := strconv.Atoi(os.Args[3])
				os.Exit(code)
			}
		case "many":
			n, _ := strconv.Atoi(os.Args[3])
			for i := 0; i < n; i++ {
				fmt.Println("line", i, strings.Repeat("x", 40))
			}
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// FB2c: Tag the file resolves its lists, hands them to the store through the env, publishes {{tags}} as the record
// has them after, and says so; without a store it says it can't tag from here; nothing to do is an error too.
func TestTagCard(t *testing.T) {
	have := []string{"needs review"}
	env := Env{Tag: func(add, remove []string) ([]string, error) {
		have = rules.ApplyTags(have, add, remove)
		return have, nil
	}}
	st := &State{Vars: map[string]string{"profile": "remux"}}
	card := rules.Card{NodeID: "t", Type: workflow.TypeTag, Name: "Tag the file", Config: map[string]any{"add": "converted, {{profile}}", "remove": "needs review"}}
	notes, err := Before([]rules.Card{card}, st, env)
	if err != nil || st.Vars["tags"] != "converted, remux" || notes[0].Words != "Tags: “converted, remux”" {
		t.Errorf("%v %+v %q", err, notes, st.Vars["tags"])
	}
	var logged []string
	notes, err = Before([]rules.Card{card}, st, Env{Log: func(l string) { logged = append(logged, l) }})
	if err != nil || !strings.Contains(notes[0].Words, "Tags not kept") || len(logged) != 1 { // review-542: no store yet: say so, go on
		t.Errorf("no store: %v %+v %v", err, notes, logged)
	}
	empty := rules.Card{NodeID: "t", Type: workflow.TypeTag, Name: "Tag", Config: map[string]any{"add": " , "}}
	if _, err := Before([]rules.Card{empty}, st, env); err == nil || !strings.Contains(err.Error(), "nothing to add or remove") {
		t.Errorf("nothing: %v", err)
	}
	onlyMissing := rules.Card{NodeID: "t", Type: workflow.TypeTag, Name: "Tag", Config: map[string]any{"add": "{{missing}}"}}
	if _, err := Before([]rules.Card{onlyMissing}, st, env); err == nil || !strings.Contains(err.Error(), "{{missing}} had no value") { // review2-542 P3d
		t.Errorf("only a missing variable: %v", err)
	}
	if _, warnings := After([]rules.Card{card}, st, env); len(warnings) != 0 || st.Vars["tags"] != "converted, remux" {
		t.Errorf("after the save too: %v", warnings)
	}
}

// FB4: Wait pauses for its seconds through the env's sleep (cut short with the sleep's error), or asks for the
// file back at a time of day: today when that is still ahead, else tomorrow; a time it can't read is an error.
func TestWaitCard(t *testing.T) {
	var slept time.Duration
	now := time.Date(2026, 11, 20, 22, 0, 0, 0, time.Local)
	env := Env{Sleep: func(d time.Duration) error { slept = d; return nil }, Now: func() time.Time { return now }}
	st := &State{Vars: map[string]string{}}
	notes, err := Before([]rules.Card{{NodeID: "w", Type: workflow.TypeWait, Name: "Wait", Config: map[string]any{"seconds": float64(90)}}}, st, env)
	if err != nil || slept != 90*time.Second || notes[0].Words != "Waited 90 seconds" {
		t.Errorf("seconds: %v %s %+v", err, slept, notes)
	}
	_, err = Before([]rules.Card{{NodeID: "w", Type: workflow.TypeWait, Name: "Wait", Config: map[string]any{"until": "23:30"}}}, st, env)
	var later *Later
	if !errors.As(err, &later) || !later.At.Equal(time.Date(2026, 11, 20, 23, 30, 0, 0, time.Local)) {
		t.Errorf("until later today: %v", err)
	}
	_, err = Before([]rules.Card{{NodeID: "w", Type: workflow.TypeWait, Name: "Wait", Config: map[string]any{"until": "02:00"}}}, st, env)
	if !errors.As(err, &later) || !later.At.Equal(time.Date(2026, 11, 21, 2, 0, 0, 0, time.Local)) {
		t.Errorf("until tomorrow: %v", err)
	}
	wait := rules.Card{NodeID: "w", Type: workflow.TypeWait, Name: "Wait", Config: map[string]any{"until": "02:00"}}
	own := env
	own.ReturnedAt = time.Date(2026, 11, 21, 2, 0, 0, 0, time.Local) // the file is back because this wait's time came: through it goes (review2-548 P1)
	if notes, err := Before([]rules.Card{wait}, st, own); err != nil || !strings.Contains(notes[0].Words, "came back as asked") {
		t.Errorf("after the wait: %v %+v", err, notes)
	}
	other := env
	other.ReturnedAt = time.Date(2026, 11, 21, 1, 30, 0, 0, time.Local) // back from Convert again later: not this wait's time, so it still holds (review-548 item 5)
	if _, err := Before([]rules.Card{wait}, st, other); !errors.As(err, &later) {
		t.Errorf("another return time: %v", err)
	}
	retried := env
	retried.Retried = true // a person pressed Retry on the waiting file: wanted now (review-548 item 6)
	if notes, err := Before([]rules.Card{wait}, st, retried); err != nil || !strings.Contains(notes[0].Words, "retried by hand") {
		t.Errorf("retried: %v %+v", err, notes)
	}
	if _, err := Before([]rules.Card{{NodeID: "w", Type: workflow.TypeWait, Name: "Wait", Config: map[string]any{"until": "25:99"}}}, st, env); err == nil || errors.As(err, &later) {
		t.Errorf("a bad time: %v", err)
	}
	if _, err := Before([]rules.Card{{NodeID: "w", Type: workflow.TypeWait, Name: "Wait", Config: map[string]any{}}}, st, env); err == nil {
		t.Error("nothing set: want an error")
	}
	cut := Env{Sleep: func(time.Duration) error { return errors.New("the wait was cut short") }}
	if _, err := Before([]rules.Card{{NodeID: "w", Type: workflow.TypeWait, Name: "Wait", Config: map[string]any{"seconds": float64(5)}}}, st, cut); err == nil || !strings.Contains(err.Error(), "cut short") {
		t.Errorf("cut short: %v", err)
	}
}

// review-548 item 4: the next time of day is by calendar date in the location, so a clock change keeps the
// wall-clock time: across the spring and autumn changes in Europe/London, "until 02:30" is 02:30 local the next
// day, not an hour early or late.
func TestNextTimeOfDayAcrossClockChanges(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skip(err)
	}
	for _, eve := range []time.Time{time.Date(2026, 3, 28, 22, 0, 0, 0, london), time.Date(2026, 10, 24, 22, 0, 0, 0, london)} {
		at := nextTimeOfDay(eve, 2, 30)
		if at.Hour() != 2 || at.Minute() != 30 || at.Day() != eve.Day()+1 || at.Location() != london {
			t.Errorf("from %s: %s", eve, at)
		}
		env := Env{Now: func() time.Time { return eve }}
		_, err := Before([]rules.Card{{NodeID: "w", Type: workflow.TypeWait, Name: "Wait", Config: map[string]any{"until": "02:30"}}}, &State{Vars: map[string]string{}}, env)
		var later *Later
		if !errors.As(err, &later) || later.At.In(london).Format("2006-01-02 15:04") != eve.AddDate(0, 0, 1).Format("2006-01-02")+" 02:30" {
			t.Errorf("the card from %s: %v", eve, err)
		}
	}
	// still ahead today: today's
	now := time.Date(2026, 3, 29, 1, 0, 0, 0, london)
	if at := nextTimeOfDay(now, 2, 30); at.Day() != 29 || at.Hour() != 2 || at.Minute() != 30 {
		t.Errorf("later today: %s", at)
	}
}

// review2-548 P3a: a Wait whose time falls inside the spring-forward gap (02:00 in Berlin on 2026-03-29) is queued
// to the clock's normalised time (03:00) and recognises that return as its own, so the file is not held another day.
func TestWaitInsideTheSpringGapRecognisesItsReturn(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip(err)
	}
	eve := time.Date(2026, 3, 28, 22, 0, 0, 0, berlin)
	card := rules.Card{NodeID: "w", Type: workflow.TypeWait, Name: "Wait", Config: map[string]any{"until": "02:00"}}
	_, err = Before([]rules.Card{card}, &State{Vars: map[string]string{}}, Env{Now: func() time.Time { return eve }})
	var later *Later
	if !errors.As(err, &later) || later.At.In(berlin).Format("2006-01-02 15:04") != "2026-03-29 03:00" {
		t.Fatalf("queued to the normalised clock: %v", err)
	}
	back := later.At
	notes, err := Before([]rules.Card{card}, &State{Vars: map[string]string{}}, Env{Now: func() time.Time { return back.Add(time.Second) }, ReturnedAt: back})
	if err != nil || !strings.Contains(notes[0].Words, "came back as asked") {
		t.Errorf("its own return on that night: %v %+v", err, notes)
	}
	if isOwnTime(time.Date(2026, 3, 29, 3, 30, 0, 0, berlin), 2, 0) || !isOwnTime(time.Date(2026, 3, 30, 2, 0, 0, 0, berlin), 2, 0) {
		t.Error("isOwnTime")
	}
}

// FB4b: a question after the save branches the chain: the cards on the port its answer picks run, the
// others don't, and the timeline gets the answer in words. The sizes come from the run's values.
func TestCompareTheResultBranchesTheAfterCards(t *testing.T) {
	var tags [][]string
	env := Env{Tag: func(add, remove []string) ([]string, error) { tags = append(tags, add); return add, nil }, Log: func(string) {}}
	q := rules.Question{Kind: "result", Test: "saved_at_least", Percent: 30}
	cards := []rules.Card{{NodeID: "q", Type: workflow.TypeCheckResult, Name: "Worth it?", Check: &q,
		Yes: []rules.Card{{NodeID: "t1", Type: workflow.TypeTag, Config: map[string]any{"add": "big-win"}}},
		No: []rules.Card{{NodeID: "n1", Type: workflow.TypeNote, Config: map[string]any{"text": "Only {{saved_percent}} saved"}},
			{NodeID: "q2", Type: workflow.TypeCheckResult, Check: &rules.Question{Kind: "result", Test: "bigger"}, Yes: []rules.Card{{NodeID: "t2", Type: workflow.TypeTag, Config: map[string]any{"add": "grew"}}}}}}}
	st := &State{Vars: map[string]string{"size_bytes": "1000", "new_size_bytes": "600", "saved_percent": "40"}}
	notes, warnings := After(cards, st, env)
	if len(warnings) != 0 || len(notes) != 2 || notes[0].NodeID != "q" || notes[0].Words != "Yes (40.0% saved, asked for at least 30%)" || notes[1].NodeID != "t1" {
		t.Fatalf("yes: %+v %v", notes, warnings)
	}
	st.Vars["new_size_bytes"] = "1200"
	notes, _ = After(cards, st, env)
	var ids []string
	for _, n := range notes {
		ids = append(ids, n.NodeID)
	}
	if strings.Join(ids, ",") != "q,n1,q2,t2" || notes[2].Words != "Yes (20.0% bigger)" {
		t.Errorf("no, then bigger: %v %+v", ids, notes)
	}
	if len(tags) != 2 || tags[0][0] != "big-win" || tags[1][0] != "grew" {
		t.Errorf("tags: %v", tags)
	}
	delete(st.Vars, "size_bytes")
	if notes, _ := After(cards, st, env); notes[0].Words != "No (the sizes aren't known)" {
		t.Errorf("unknown sizes: %+v", notes[0])
	}
}

// FB4b-3: the questions between the check and the save decide whether the path goes on to the save, keeps
// the original ("Leave the file alone", or a port with nothing on it) or fails; each answer is a note.
func TestBeforeSaveDecides(t *testing.T) {
	q := rules.Question{Kind: "result", Test: "saved_at_least", Percent: 30}
	within := rules.Question{Kind: "result", Test: "runtime_within", Seconds: 1}
	cards := []rules.Card{{NodeID: "q", Type: workflow.TypeCheckResult, Name: "Worth it?", Check: &q,
		Yes: []rules.Card{{NodeID: "save", Type: workflow.TypeCommit, Name: "Save the result"}},
		No: []rules.Card{{NodeID: "q2", Type: workflow.TypeCheckResult, Name: "Same length?", Check: &within,
			Yes: []rules.Card{{NodeID: "leave", Type: workflow.TypeEnd, Name: "Leave the file alone", Config: map[string]any{"reason": "Not worth it"}}}}}}}
	st := &State{Result: rules.Result{SizeBefore: 1000, SizeAfter: 600, DurationBefore: 100, DurationAfter: 100.4}}
	v, notes := BeforeSave(cards, st)
	if !v.Save || v.NodeID != "save" || len(notes) != 1 || notes[0].Words != "Yes (40.0% saved, asked for at least 30%)" {
		t.Fatalf("saves: %+v %+v", v, notes)
	}
	st.Result.SizeAfter = 900
	v, notes = BeforeSave(cards, st)
	if v.Save || v.Fail || v.NodeID != "leave" || v.Words != "Not worth it" || len(notes) != 2 || notes[1].Words != "Yes (running time 0:01:40 against the original's 0:01:40, 0.4 seconds off, asked for within 1 second)" {
		t.Fatalf("kept by the end: %+v %+v", v, notes)
	}
	st.Result.DurationAfter = 103
	if v, notes = BeforeSave(cards, st); v.Save || v.Fail || v.NodeID != "" || len(notes) != 2 || !strings.HasPrefix(notes[1].Words, "No (") {
		t.Fatalf("kept by nothing on the port: %+v %+v", v, notes)
	}
	cards[0].No = []rules.Card{{NodeID: "stop", Type: workflow.TypeFail, Name: "Stop as failed", Config: map[string]any{"reason": "Too little saved"}}}
	if v, _ = BeforeSave(cards, st); !v.Fail || v.Save || v.Words != "Too little saved" || v.NodeID != "stop" {
		t.Fatalf("fails: %+v", v)
	}
	// the sizes come from the values when the worker left them 0 (the after phase sets both)
	st2 := &State{Vars: map[string]string{"size_bytes": "1000", "new_size_bytes": "500"}}
	if v, _ := BeforeSave(cards, st2); !v.Save {
		t.Errorf("sizes from the values: %+v", v)
	}
}

// W9: a failing after-save action with an error path runs that path instead of the rest of its own path; with
// no error path (unwired) the failure is a warning and the rest runs, as before.
func TestAfterErrorPathRunsInsteadOfTheRest(t *testing.T) {
	var sent []string
	env := Env{Notify: func(target, kind, text string) (string, error) {
		sent = append(sent, target)
		if target == "t1" {
			return "", errors.New("the phone is off")
		}
		return "Sent", nil
	}}
	note := func(id, target string, onErr ...rules.Card) rules.Card {
		return rules.Card{NodeID: id, Type: workflow.TypeNotify, Name: "Send a notification", Config: map[string]any{"target": target, "kind": "good", "text": "x"}, OnError: onErr}
	}
	st := &State{Vars: map[string]string{}}
	notes, warnings := After([]rules.Card{note("a", "t1", note("e", "t2")), note("b", "t3")}, st, env)
	if fmt.Sprint(sent) != "[t1 t2]" || len(notes) != 2 || !notes[0].Failed || len(warnings) != 1 {
		t.Errorf("wired: sent %v, notes %+v, warnings %v", sent, notes, warnings)
	}
	sent = nil
	notes, warnings = After([]rules.Card{note("a", "t1"), note("b", "t3")}, st, env)
	if fmt.Sprint(sent) != "[t1 t3]" || len(notes) != 2 || len(warnings) != 1 {
		t.Errorf("unwired: sent %v, notes %+v, warnings %v", sent, notes, warnings)
	}
}

// W9 pin: a failing card inside a Check's Yes branch runs its error path, skips the rest of that Yes branch,
// and never takes the No branch.
func TestErrorPathInsideAYesBranchStaysInIt(t *testing.T) {
	var sent, tags []string
	env := Env{
		Notify: func(target, kind, text string) (string, error) {
			sent = append(sent, target)
			if target == "t1" {
				return "", errors.New("the phone is off")
			}
			return "Sent", nil
		},
		Tag: func(add, remove []string) ([]string, error) { tags = append(tags, add...); return add, nil },
		Log: func(string) {},
	}
	notify := func(id, target string, onErr ...rules.Card) rules.Card {
		return rules.Card{NodeID: id, Type: workflow.TypeNotify, Name: "Send a notification", Config: map[string]any{"target": target, "kind": "good", "text": "x"}, OnError: onErr}
	}
	q := rules.Question{Kind: "result", Test: "saved_at_least", Percent: 30}
	cards := []rules.Card{{NodeID: "q", Type: workflow.TypeCheckResult, Name: "Worth it?", Check: &q,
		Yes: []rules.Card{notify("a", "t1", notify("e", "t2")), {NodeID: "tg", Type: workflow.TypeTag, Name: "Tag the file", Config: map[string]any{"add": "after-fail"}}},
		No:  []rules.Card{notify("n", "t9")}}}
	st := &State{Vars: map[string]string{"size_bytes": "1000", "new_size_bytes": "600", "saved_percent": "40"}}
	notes, warnings := After(cards, st, env)
	if fmt.Sprint(sent) != "[t1 t2]" || len(tags) != 0 || len(warnings) != 1 {
		t.Errorf("sent %v, tags %v, warnings %v, notes %+v", sent, tags, warnings, notes)
	}
}
