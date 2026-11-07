// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// "shrink-your-shit restore <path>" asks the running service and puts the original back.
func TestRestoreCommand(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	access, _, _ := web.OpenAccess(t.TempDir(), false)
	srv := httptest.NewServer(web.Handler(st, access, web.Engine{}))
	defer srv.Close()
	t.Setenv("SYP_ADDR", strings.TrimPrefix(srv.URL, "http://"))

	dir := t.TempDir()
	path, trash := filepath.Join(dir, "a.mkv"), filepath.Join(dir, ".syp-trash", "a.1.mkv")
	os.MkdirAll(filepath.Dir(trash), 0o755)
	os.WriteFile(trash, []byte("original-bytes"), 0o644)
	os.WriteFile(path, []byte("new"), 0o644)
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), true)
	jid, _ := st.Enqueue(jobs.Job{Library: dir, LibraryID: l.ID, Path: path, SizeBefore: 14})
	st.Claim("t")
	st.Complete(jid, path, 3)
	h, _ := worker.SampleHash(trash)
	ost, _ := os.Stat(trash)
	oid, _ := st.BeginSwap(jobs.Original{JobID: jid, LibraryID: l.ID, Path: path, Trash: trash, Staged: "s", Bytes: 14, ModTime: ost.ModTime().UnixNano(), Hash: h, NewBytes: 3})
	st.FinishSwap(oid, time.Now().Add(7*24*time.Hour))

	var out, errOut bytes.Buffer
	if code := restoreCommand([]string{"nope.mkv"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "No kept original") {
		t.Errorf("unknown path: %d %q", code, errOut.String())
	}
	errOut.Reset()
	if code := restoreCommand([]string{path}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "The original is back") {
		t.Fatalf("%d %q %q", code, out.String(), errOut.String())
	}
	if b, _ := os.ReadFile(path); string(b) != "original-bytes" {
		t.Errorf("%q", b)
	}
	errOut.Reset()
	if code := restoreCommand([]string{"1"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "not kept any more") {
		t.Errorf("again: %d %q", code, errOut.String())
	}
	if code := restoreCommand(nil, &out, &errOut); code != 2 {
		t.Errorf("no argument: %d", code)
	}
}
