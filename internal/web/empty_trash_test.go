// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// emptyTrashKept makes a finished job in library l whose original (data) is kept in .syp-trash.
func emptyTrashKept(t *testing.T, st *jobs.Store, l jobs.Library, name, data string) string {
	t.Helper()
	path := filepath.Join(l.Path, name)
	os.WriteFile(path, []byte("new"), 0o644)
	trash := filepath.Join(l.Path, ".syp-trash", name)
	os.MkdirAll(filepath.Dir(trash), 0o755)
	if err := os.WriteFile(trash, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(trash)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := worker.SampleHash(trash)
	if err != nil {
		t.Fatal(err)
	}
	jid, err := st.Enqueue(jobs.Job{Library: l.Path, LibraryID: l.ID, Path: path, SizeBefore: fi.Size()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := st.Complete(jid, path, 3); err != nil {
		t.Fatal(err)
	}
	id, err := st.BeginSwap(jobs.Original{JobID: jid, LibraryID: l.ID, Path: path, Trash: trash, Bytes: fi.Size(), ModTime: fi.ModTime().UnixNano(), Hash: hash, NewBytes: 3, NewHash: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishSwap(id, time.Now()); err != nil {
		t.Fatal(err)
	}
	return trash
}

func TestEmptyTrashSkipsAnUnmountedShare(t *testing.T) {
	if runtime.GOOS != "linux" { // shares are watched on Linux and Windows; the mount path here is a Linux one
		t.Skip("shares are watched on Linux")
	}
	oldAnswers := shareAnswers
	shareAnswers = func(string) bool { return false }
	t.Cleanup(func() { shareAnswers = oldAnswers })

	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.SetPaused(false)
	// keepOriginalsDays 0: a kept original is due at once
	st.SetMeta("settings", `{"firstRunDone":true,"firstRunStep":5,"keepOriginalsDays":0,"parallelJobs":1,"lowPriority":true,"scanSchedule":"02:00","queueOrder":"newest","languages":[]}`)

	mnt := filepath.Join(t.TempDir(), "mnt")
	if err := os.MkdirAll(filepath.Join(mnt, "movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh, err := st.SaveShare(jobs.Share{Type: "smb", Address: "//nas/movies", Name: "Movies"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetShareMountPoint(sh.ID, mnt); err != nil {
		t.Fatal(err)
	}
	shared, err := st.AddLibrary("Movies", filepath.Join(mnt, "movies"), rules.SliderTemplate(4), true)
	if err != nil {
		t.Fatal(err)
	}
	local, err := st.AddLibrary("Local", t.TempDir(), rules.SliderTemplate(4), true)
	if err != nil {
		t.Fatal(err)
	}
	sharedTrash := emptyTrashKept(t, st, shared, "a.mkv", "aaaa")
	localTrash := emptyTrashKept(t, st, local, "b.mkv", "bbbbbb")

	api := http.NewServeMux()
	v2Workflows(api, st, Engine{})
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/trash/purge", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, req)
		return rec
	}

	rec := post("")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST all: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		DeletedFiles int64 `json:"deletedFiles"`
		FreedBytes   int64 `json:"freedBytes"`
		Skipped      []struct {
			Library string `json:"library"`
			Reason  string `json:"reason"`
		} `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DeletedFiles != 1 || got.FreedBytes != 6 {
		t.Errorf("deleted %d freed %d, want 1 and 6 (the local library only)", got.DeletedFiles, got.FreedBytes)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].Library != "Movies" || got.Skipped[0].Reason == "" {
		t.Errorf("skipped %+v, want the Movies share", got.Skipped)
	}
	if _, err := os.Stat(sharedTrash); err != nil {
		t.Error("the unmounted share's kept original was touched")
	}
	if _, err := os.Stat(localTrash); !os.IsNotExist(err) {
		t.Error("the local library's kept original was not deleted")
	}

	if rec := post(`{"libraryId":999}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d %s", rec.Code, rec.Body)
	}
}
