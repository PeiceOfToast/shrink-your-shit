// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
