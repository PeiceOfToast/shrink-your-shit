// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCanTransition(t *testing.T) {
	ok := [][2]Status{{Queued, Running}, {Running, Done}, {Running, Failed}, {Running, Queued}, {Failed, Queued}, {Running, Kept}, {Kept, Queued}}
	for _, c := range ok {
		if !CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should be allowed", c[0], c[1])
		}
	}
	bad := [][2]Status{{Queued, Done}, {Queued, Failed}, {Done, Queued}, {Done, Running}, {Failed, Done}, {Failed, Running}, {Queued, Kept}, {Kept, Done}, {Kept, Running}, {Done, Kept}}
	for _, c := range bad {
		if CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should be rejected", c[0], c[1])
		}
	}
}

func TestLifecycle(t *testing.T) {
	s := open(t)
	if _, err := s.Claim("w"); !errors.Is(err, ErrNoJob) {
		t.Fatalf("empty queue: %v", err)
	}
	id, err := s.Enqueue(Job{Library: "/lib", Path: "/lib/a.mkv", Reason: "r", SizeBefore: 100})
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.Claim("builtin")
	if err != nil || j.ID != id || j.Status != Running || j.Worker != "builtin" {
		t.Fatalf("claim: %+v %v", j, err)
	}
	if _, err := s.Claim("other"); !errors.Is(err, ErrNoJob) {
		t.Fatal("running job must not be claimable twice")
	}
	s.Progress(id, 42)
	if j, _ := s.Get(id); j.Progress != 42 {
		t.Errorf("progress %v", j.Progress)
	}
	if err := s.Complete(id, "/lib/a.mkv", 40); err != nil {
		t.Fatal(err)
	}
	j, _ = s.Get(id)
	if j.Status != Done || j.Progress != 100 || j.SizeAfter != 40 {
		t.Errorf("after complete: %+v", j)
	}
	if err := s.Fail(id, "x"); err == nil {
		t.Error("done -> failed must be rejected")
	}
	if err := s.Retry(id); err == nil {
		t.Error("done -> queued must be rejected")
	}
}

func TestFailAndRetry(t *testing.T) {
	s := open(t)
	id, _ := s.Enqueue(Job{Path: "/a"})
	if err := s.Fail(id, "no"); err == nil {
		t.Error("queued -> failed must be rejected")
	}
	s.Claim("w")
	if err := s.Fail(id, "ffmpeg exploded"); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Get(id); j.Status != Failed || j.Error != "ffmpeg exploded" {
		t.Errorf("%+v", j)
	}
	if err := s.Retry(id); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Get(id); j.Status != Queued || j.Error != "" {
		t.Errorf("%+v", j)
	}
}

func TestRequeueRunning(t *testing.T) {
	s := open(t)
	s.Enqueue(Job{Path: "/a"})
	s.Claim("w")
	n, err := s.RequeueRunning()
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := s.Claim("w"); err != nil {
		t.Errorf("should be claimable again: %v", err)
	}
}

func TestFilesAndList(t *testing.T) {
	s := open(t)
	if f, err := s.GetFile("/x"); f != nil || err != nil {
		t.Fatalf("%v %v", f, err)
	}
	s.PutFile(File{Path: "/x", Size: 1, ModTime: 2, Codec: "h264", Decision: "queued"})
	s.PutFile(File{Path: "/x", Size: 3, ModTime: 4, Codec: "hevc", Decision: "skip"})
	f, _ := s.GetFile("/x")
	if f.Size != 3 || f.Codec != "hevc" {
		t.Errorf("%+v", f)
	}
	s.Enqueue(Job{Path: "/1"})
	s.Enqueue(Job{Path: "/2"})
	l, _ := s.List(10)
	if len(l) != 2 || l[0].Path != "/2" {
		t.Errorf("newest first: %+v", l)
	}
}

func TestKeepAndRetry(t *testing.T) {
	s := open(t)
	id, _ := s.Enqueue(Job{Path: "/a"})
	if err := s.Keep(id, "no"); err == nil {
		t.Error("queued -> kept must be rejected")
	}
	s.Claim("w")
	if err := s.Keep(id, "the output has 1 audio stream, the source has 2"); err != nil {
		t.Fatal(err)
	}
	j, _ := s.Get(id)
	if j.Status != Kept || !strings.Contains(j.Error, "2") {
		t.Errorf("%+v", j)
	}
	if err := s.Complete(id, "/a", 1); err == nil {
		t.Error("a kept job must not become done")
	}
	if err := s.Retry(id); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Get(id); j.Status != Queued || j.Error != "" {
		t.Errorf("%+v", j)
	}
}
