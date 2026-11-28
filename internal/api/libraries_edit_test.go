// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// pageLibrary is the part of page 09's row these tests read.
type pageLibrary struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Watch    string  `json:"watch"`
	Paused   bool    `json:"paused"`
	Status   string  `json:"status"`
	NextScan *string `json:"nextScanAt"`
	Workflow *struct{ ID, Name string }
}

func decodeLibrary(t *testing.T, rec *httptest.ResponseRecorder) pageLibrary {
	t.Helper()
	var row pageLibrary
	decodeInto(t, rec, &row)
	return row
}

func patchLib(t *testing.T, e *env, cookie *http.Cookie, id string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(http.MethodPatch, "/api/v2/libraries/"+id, mustJSON(t, body), write(withSession(cookie)), false)
}

// mustAddLibrary adds one through the API and answers its row.
func mustAddLibrary(t *testing.T, e *env, cookie *http.Cookie, name, path, watch string) pageLibrary {
	t.Helper()
	body := map[string]any{"name": name, "path": path}
	if watch != "" {
		body["watch"] = watch
	}
	rec := e.do(http.MethodPost, "/api/v2/libraries", mustJSON(t, body), write(withSession(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add %s: %d %s", name, rec.Code, rec.Body)
	}
	return decodeLibrary(t, rec)
}

// expectLibraryChanged reads the one library.changed a save or a removal put on the stream.
func expectLibraryChanged(t *testing.T, ch <-chan events.Event, id int64) {
	t.Helper()
	select {
	case ev := <-ch:
		if ev.Kind != events.KindLibraryChanged || ev.LibraryID != strconv.FormatInt(id, 10) || ev.Message == "" {
			t.Fatalf("event = %+v, want library.changed for library %d", ev, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no library.changed for library %d", id)
	}
}

// TestPatchLibrarySavesEachField: the panel's Save changes only what it sends, moves the next scan
// when the schedule changes, and says library.changed on the stream (page 09).
func TestPatchLibrarySavesEachField(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	clips := t.TempDir()
	added := mustAddLibrary(t, e, cookie, "Clips", clips, "daily")
	if added.NextScan == nil {
		t.Fatal("a new library is due at once, so it needs a next scan")
	}
	changed, stop := e.bus.Subscribe(events.Filter{Kind: events.KindLibraryChanged})
	defer stop()

	// The name alone: every other field, and the schedule, stays as it was.
	rec := patchLib(t, e, cookie, "1", map[string]any{"name": "Movies"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	row := decodeLibrary(t, rec)
	if row.ID != "1" || row.Name != "Movies" || row.Path != clips || row.Watch != "daily" || row.Paused {
		t.Fatalf("renamed row = %+v", row)
	}
	if row.NextScan == nil || *row.NextScan != *added.NextScan {
		t.Fatalf("rename moved the next scan: %v, want %v", row.NextScan, *added.NextScan)
	}
	expectLibraryChanged(t, changed, 1)

	// A new watch moves the next scan at once: weekly from the fixed clock, plus the row's minutes.
	rec = patchLib(t, e, cookie, "1", map[string]any{"watch": "weekly"})
	if rec.Code != http.StatusOK {
		t.Fatalf("watch: %d %s", rec.Code, rec.Body)
	}
	row = decodeLibrary(t, rec)
	if row.Watch != "weekly" {
		t.Fatalf("watch = %q, want weekly", row.Watch)
	}
	if row.NextScan == nil || *row.NextScan != "2026-12-04T04:01:00Z" {
		t.Fatalf("next scan = %v, want 2026-12-04T04:01:00Z", row.NextScan)
	}
	expectLibraryChanged(t, changed, 1)

	// Paused: no next scan while paused. A paused library is not scanned, even by hand.
	rec = patchLib(t, e, cookie, "1", map[string]any{"paused": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", rec.Code, rec.Body)
	}
	row = decodeLibrary(t, rec)
	if !row.Paused || row.Status != "paused" || row.NextScan != nil {
		t.Fatalf("paused row = %+v; want paused with no next scan", row)
	}
	expectLibraryChanged(t, changed, 1)

	// Unpaused: watching again, with the next scan armed.
	rec = patchLib(t, e, cookie, "1", map[string]any{"paused": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("unpause: %d %s", rec.Code, rec.Body)
	}
	row = decodeLibrary(t, rec)
	// The watch is still weekly, so the next scan is a week out from the fixed clock.
	if row.Paused || row.Status != "needs_workflow" || row.NextScan == nil || *row.NextScan != "2026-12-04T04:01:00Z" {
		t.Fatalf("unpaused row = %+v; want watching again with a next scan", row)
	}
	expectLibraryChanged(t, changed, 1)

	// The saved values are what the page reads back.
	var read pageLibrary
	get(t, e, cookie, "/api/v2/libraries/1", &read)
	if read.Name != "Movies" || read.Watch != "weekly" || read.Paused || read.NextScan == nil {
		t.Fatalf("read back %+v", read)
	}
}

// TestPatchLibraryRefusesABadWatch: a Save names hourly, daily or weekly; anything else is refused
// before it is saved.
func TestPatchLibraryRefusesABadWatch(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	mustAddLibrary(t, e, cookie, "Clips", t.TempDir(), "daily")

	rec := patchLib(t, e, cookie, "1", map[string]any{"watch": "sometimes"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad watch: %d %s, want 400", rec.Code, rec.Body)
	}
	code, _, env := errorBody(t, rec)
	if code != "invalid_setting" || env["field"] != "watch" {
		t.Fatalf("bad watch: code %q field %v", code, env["field"])
	}

	var read pageLibrary
	get(t, e, cookie, "/api/v2/libraries/1", &read)
	if read.Watch != "daily" {
		t.Fatalf("watch = %q after a refused save, want daily", read.Watch)
	}
}

// TestPatchLibraryRefusesAWatchedPath: a path change runs Add's own check, so two libraries cannot
// watch one folder — not under its own path, and not through a link to it (S2-0).
func TestPatchLibraryRefusesAWatchedPath(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	first := t.TempDir()
	mustAddLibrary(t, e, cookie, "One", first, "")
	second := t.TempDir()
	mustAddLibrary(t, e, cookie, "Two", second, "")

	// Add's answer for the same folder, to show the Save gives the same one.
	rec := e.do(http.MethodPost, "/api/v2/libraries", mustJSON(t, map[string]any{"name": "Three", "path": first}), write(withSession(cookie)), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("add over a watched folder: %d %s, want 400", rec.Code, rec.Body)
	}
	addCode, addMessage, addEnv := errorBody(t, rec)

	rec = patchLib(t, e, cookie, "2", map[string]any{"path": first})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("path change over a watched folder: %d %s, want 400", rec.Code, rec.Body)
	}
	code, message, env := errorBody(t, rec)
	if code != addCode || message != addMessage || env["field"] != addEnv["field"] {
		t.Fatalf("path change refused with %q/%q, Add with %q/%q", code, message, addCode, addMessage)
	}

	// The same folder through a link is the same folder (S2-0).
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(first, link); err != nil {
			t.Fatal(err)
		}
		rec = patchLib(t, e, cookie, "2", map[string]any{"path": link})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path change to a link: %d %s, want 400", rec.Code, rec.Body)
		}
		if code, _, _ := errorBody(t, rec); code != addCode || code != "invalid_request" {
			t.Fatalf("link refusal code %q, want %q", code, addCode)
		}

		// A folder inside another library's folder overlaps it.
		nested := filepath.Join(first, "inside")
		if err := os.Mkdir(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		rec = patchLib(t, e, cookie, "2", map[string]any{"path": nested})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path change into another library: %d %s, want 400", rec.Code, rec.Body)
		}
		if code, _, env := errorBody(t, rec); code != "invalid_request" || env["field"] != "path" {
			t.Fatalf("nested refusal %q field %v", code, env["field"])
		}
	}

	// Nothing was saved.
	var read pageLibrary
	get(t, e, cookie, "/api/v2/libraries/2", &read)
	if read.Path != second {
		t.Fatalf("path = %q after refused saves, want %q", read.Path, second)
	}

	// A working folder of its own is accepted.
	third := t.TempDir()
	rec = patchLib(t, e, cookie, "2", map[string]any{"path": third})
	if rec.Code != http.StatusOK {
		t.Fatalf("own folder: %d %s", rec.Code, rec.Body)
	}
	if row := decodeLibrary(t, rec); row.Path != third {
		t.Fatalf("path = %q, want %q", row.Path, third)
	}
}

// TestPatchLibrarySettingTheWorkflowQueuesTheCountedFiles: a library that was waiting for a workflow
// starts queueing the files it already counted, with no rescan (page 09). The wired scanner is the
// real one: the catalog's rows become jobs.
func TestPatchLibrarySettingTheWorkflowQueuesTheCountedFiles(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, true)
	cookie := e.setup()
	clips := t.TempDir()
	added := mustAddLibrary(t, e, cookie, "Clips", clips, "")
	if added.Status != "needs_workflow" || added.Workflow != nil {
		t.Fatalf("added row = %+v; want needs_workflow", added)
	}
	if err := os.WriteFile(filepath.Join(clips, "clip.mkv"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The row as a scan would have saved it: the file's own size and mtime, so QueueFiles sees it
	// unchanged since the count.
	info, err := os.Stat(filepath.Join(clips, "clip.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	libID, _ := strconv.ParseInt(added.ID, 10, 64)
	if _, err := e.catalog.SaveFile(ctx, library.File{
		LibraryID: libID, RelPath: "clip.mkv", Size: info.Size(), MTime: info.ModTime(),
	}, library.Skipped, "needs_workflow", nil); err != nil {
		t.Fatal(err)
	}
	e.scans.queue = func(ctx context.Context, lib library.Library) (int, error) {
		return newTestQueueingScanner(e).QueueFiles(ctx, lib)
	}

	rec := patchLib(t, e, cookie, "1", map[string]any{"workflowId": "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("set workflow: %d %s", rec.Code, rec.Body)
	}
	row := decodeLibrary(t, rec)
	if row.Workflow == nil || row.Workflow.ID != "1" || row.Status == "needs_workflow" {
		t.Fatalf("row = %+v; want the workflow set", row)
	}
	if asks := e.scans.queueRequests(); len(asks) != 1 || asks[0].ID != libID {
		t.Fatalf("queue requests = %+v, want one for library %d", asks, libID)
	}
	// No rescan: the files came from the catalog, not a walk.
	if scans := e.scans.scanned(); len(scans) != 0 {
		t.Fatalf("scan started %v; setting a workflow is not a scan", scans)
	}

	list, err := e.queue.List(ctx, jobs.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("jobs = %d rows, want the counted file queued", len(list))
	}
	if list[0].LibraryID != libID || list[0].WorkflowID != 1 || list[0].State != jobs.Waiting {
		t.Fatalf("job = %+v; want library %d queued on workflow 1", list[0], libID)
	}
}

// TestPatchLibraryPathChangeQueuesNothingFromTheOldFolder: the stored rows describe the old folder,
// so a path change drops them; a workflow set in the same Save queues nothing until the next scan
// has counted the new folder (review-890 item 3).
func TestPatchLibraryPathChangeQueuesNothingFromTheOldFolder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, true)
	cookie := e.setup()
	old := t.TempDir()
	added := mustAddLibrary(t, e, cookie, "Clips", old, "")
	libID, _ := strconv.ParseInt(added.ID, 10, 64)
	if _, err := e.catalog.SaveFile(ctx, library.File{
		LibraryID: libID, RelPath: "old.mkv", Size: 5, MTime: e.clock.Now(),
	}, library.Skipped, "needs_workflow", nil); err != nil {
		t.Fatal(err)
	}
	e.scans.queue = func(ctx context.Context, lib library.Library) (int, error) {
		return newTestQueueingScanner(e).QueueFiles(ctx, lib)
	}

	next := t.TempDir()
	rec := patchLib(t, e, cookie, added.ID, map[string]any{"path": next, "workflowId": "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("path and workflow together: %d %s", rec.Code, rec.Body)
	}
	if row := decodeLibrary(t, rec); row.Path != next || row.Workflow == nil {
		t.Fatalf("row = %+v; want the new path with the workflow set", row)
	}
	if rows, err := e.catalog.Files(ctx, libID); err != nil || len(rows) != 0 {
		t.Fatalf("files rows = %d (%v); a path change clears the old folder's rows", len(rows), err)
	}
	list, err := e.queue.List(ctx, jobs.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("jobs = %d; the old folder's files are not the new folder's", len(list))
	}
}

// TestPatchLibraryPathChangeCancelsWaitingJobs: a path change takes the old folder's rows with it,
// so the library's waiting jobs are cancelled in the same transaction, with a History line that says
// why. A running job still finishes (review-890 at 82d8316b).
func TestPatchLibraryPathChangeCancelsWaitingJobs(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, true)
	cookie := e.setup()
	old := t.TempDir()
	lib := mustAddLibraryWithWorkflow(t, e, 1, 1, old)
	ids := mustEnqueueWaiting(t, e, lib, "a.mkv", "b.mkv")
	// The oldest waits first, so a.mkv's job is the running one.
	claimed, err := e.queue.Claim(ctx, "worker-1", jobs.ClaimOptions{Order: jobs.OrderOldest})
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != ids[0] {
		t.Fatalf("claimed %+v, want job %d", claimed, ids[0])
	}
	cancelled, stop := e.bus.Subscribe(events.Filter{Kind: events.KindCancelled})
	defer stop()

	next := t.TempDir()
	rec := patchLib(t, e, cookie, "1", map[string]any{"path": next})
	if rec.Code != http.StatusOK {
		t.Fatalf("path change: %d %s", rec.Code, rec.Body)
	}

	if got, _ := e.queue.Get(ctx, ids[1]); got.State != jobs.Cancelled {
		t.Fatalf("waiting job after a path change = %s, want cancelled", got.State)
	}
	if got, _ := e.queue.Get(ctx, ids[0]); got.State != jobs.Running {
		t.Fatalf("running job after a path change = %s, want running", got.State)
	}
	select {
	case ev := <-cancelled:
		if ev.JobID != ids[1] || !strings.Contains(ev.Message, "folder changed") {
			t.Fatalf("cancelled event = %+v; want job %d with the folder-changed line", ev, ids[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled job has no History line")
	}

	// The running job is still the worker's, and it finishes.
	if err := e.queue.Finish(ctx, claimed.ID, claimed.Attempt, jobs.Result{Verdict: jobs.VerdictFail, Reason: "the folder moved under it"}); err != nil {
		t.Fatalf("finish the running job: %v", err)
	}
	if got, _ := e.queue.Get(ctx, claimed.ID); got.State != jobs.Failed {
		t.Fatalf("finished job state = %s, want failed", got.State)
	}
}

// newTestQueueingScanner is the real scanner wired the way cmd wires it, with a fake prober and a
// Decide that wants everything.
func newTestQueueingScanner(e *env) *library.Scanner {
	return &library.Scanner{
		Catalog: e.catalog,
		Prober: media.MediaFuncs{ProbeFunc: func(context.Context, string) (media.Info, error) {
			return media.Info{
				Format: media.Format{Names: []string{"matroska"}, Size: 5, Duration: time.Second},
				Video:  []media.VideoTrack{{Codec: "h264", Width: 320, Height: 240}},
			}, nil
		}},
		Decide: func(library.File, media.Info) (bool, string) { return true, "" },
		Enqueue: func(ctx context.Context, lib library.Library, f library.File) error {
			if lib.WorkflowID == nil {
				return errors.New("the library has no workflow")
			}
			fileID, err := e.catalog.SaveFile(ctx, f, library.Queued, "", f.Probe)
			if err != nil {
				return err
			}
			wf, err := e.flows.Get(ctx, *lib.WorkflowID)
			if err != nil {
				return err
			}
			_, open, err := e.queue.Enqueue(ctx, fileID, lib.ID, *lib.WorkflowID, wf.CurrentRev,
				filepath.Join(lib.Path, filepath.FromSlash(f.RelPath)), "scan")
			if err != nil {
				return err
			}
			if open {
				return library.ErrAlreadyQueued
			}
			return nil
		},
	}
}

// mustAddLibraryWithWorkflow adds a library already working on workflow id.
func mustAddLibraryWithWorkflow(t *testing.T, e *env, id, workflowID int64, path string) library.Library {
	t.Helper()
	wid := workflowID
	lib, err := e.catalog.AddLibrary(context.Background(), library.NewLibrary{Name: "Clips", Path: path, WorkflowID: &wid})
	if err != nil {
		t.Fatal(err)
	}
	if lib.ID != id {
		t.Fatalf("library id %d, want %d", lib.ID, id)
	}
	return lib
}

// mustEnqueueWaitings puts one waiting job on each file of a library.
func mustEnqueueWaiting(t *testing.T, e *env, lib library.Library, rels ...string) []int64 {
	t.Helper()
	ctx := context.Background()
	var ids []int64
	for _, rel := range rels {
		fileID, err := e.catalog.SaveFile(ctx, library.File{
			LibraryID: lib.ID, RelPath: rel, Size: 5, MTime: e.clock.Now(),
		}, library.Queued, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		id, open, err := e.queue.Enqueue(ctx, fileID, lib.ID, *lib.WorkflowID, 1,
			filepath.Join(lib.Path, rel), "test")
		if err != nil {
			t.Fatal(err)
		}
		if open {
			t.Fatalf("file %s already has an open job", rel)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestPatchLibraryUnsettingTheWorkflowKeepsWaitingJobs: null goes back to needs_workflow, the files
// queued before it stay queued, and nothing is re-decided (page 09).
func TestPatchLibraryUnsettingTheWorkflowKeepsWaitingJobs(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, true)
	cookie := e.setup()
	clips := t.TempDir()
	lib := mustAddLibraryWithWorkflow(t, e, 1, 1, clips)
	waiting := mustEnqueueWaiting(t, e, lib, "a.mkv")

	rec := patchLib(t, e, cookie, "1", map[string]any{"workflowId": nil})
	if rec.Code != http.StatusOK {
		t.Fatalf("unset workflow: %d %s", rec.Code, rec.Body)
	}
	row := decodeLibrary(t, rec)
	if row.Workflow != nil || row.Status != "needs_workflow" {
		t.Fatalf("row = %+v; want needs_workflow", row)
	}
	if asks := e.scans.queueRequests(); len(asks) != 0 {
		t.Fatalf("unsetting queued files: %+v", asks)
	}
	job, err := e.queue.Get(ctx, waiting[0])
	if err != nil {
		t.Fatal(err)
	}
	if job.State != jobs.Waiting || job.WorkflowID != 1 {
		t.Fatalf("waiting job after unsetting = %+v; want waiting on workflow 1", job)
	}
}

// TestPatchLibraryChangingTheWorkflowKeepsWaitingJobs: a new workflow applies to the files queued
// from then on; a job already waiting keeps the workflow it was queued with (page 09).
func TestPatchLibraryChangingTheWorkflowKeepsWaitingJobs(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, true)
	cookie := e.setup()
	other, _, err := e.flows.CreateFromStarter(ctx, "shrink-videos", "Other")
	if err != nil {
		t.Fatal(err)
	}
	clips := t.TempDir()
	lib := mustAddLibraryWithWorkflow(t, e, 1, 1, clips)
	waiting := mustEnqueueWaiting(t, e, lib, "a.mkv")

	rec := patchLib(t, e, cookie, "1", map[string]any{"workflowId": strconv.FormatInt(other, 10)})
	if rec.Code != http.StatusOK {
		t.Fatalf("change workflow: %d %s", rec.Code, rec.Body)
	}
	row := decodeLibrary(t, rec)
	if row.Workflow == nil || row.Workflow.ID != strconv.FormatInt(other, 10) {
		t.Fatalf("row = %+v; want the new workflow", row)
	}
	// Changing a working library's workflow is not setting a first one: no files are re-decided.
	if asks := e.scans.queueRequests(); len(asks) != 0 {
		t.Fatalf("changing the workflow queued files: %+v", asks)
	}
	job, err := e.queue.Get(ctx, waiting[0])
	if err != nil {
		t.Fatal(err)
	}
	if job.WorkflowID != 1 {
		t.Fatalf("waiting job workflow = %d, want the one it was queued with (1)", job.WorkflowID)
	}
}

// TestDeleteLibraryCancelsWaitingAndLeavesTheRunning: Remove cancels what has not started, lets the
// running file finish, and touches no file on disk (page 09).
func TestDeleteLibraryCancelsWaitingAndLeavesTheRunning(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, true)
	cookie := e.setup()
	clips := t.TempDir()
	lib := mustAddLibraryWithWorkflow(t, e, 1, 1, clips)
	keep := filepath.Join(clips, "a.mkv")
	if err := os.WriteFile(keep, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	ids := mustEnqueueWaiting(t, e, lib, "a.mkv", "b.mkv")
	// The oldest waits first, so a.mkv's job is the running one.
	claimed, err := e.queue.Claim(ctx, "worker-1", jobs.ClaimOptions{Order: jobs.OrderOldest})
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("no job to claim")
	}
	changed, stop := e.bus.Subscribe(events.Filter{Kind: events.KindLibraryChanged})
	defer stop()

	rec := e.do(http.MethodDelete, "/api/v2/libraries/1", "", write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body)
	}
	var done struct{ OK bool }
	decodeInto(t, rec, &done)
	if !done.OK {
		t.Fatalf("remove body %s; want ok", rec.Body)
	}
	expectLibraryChanged(t, changed, 1)

	running, err := e.queue.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.State != jobs.Running {
		t.Fatalf("running job after remove = %s, want running", running.State)
	}
	for _, id := range ids {
		if id == claimed.ID {
			continue
		}
		if got, _ := e.queue.Get(ctx, id); got.State != jobs.Cancelled {
			t.Fatalf("waiting job %d after remove = %s, want cancelled", id, got.State)
		}
	}

	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("remove touched a file on disk: %v", err)
	}
	if rows, err := e.catalog.Files(ctx, 1); err != nil || len(rows) != 0 {
		t.Fatalf("catalog rows after remove: %v, %v", rows, err)
	}
	if rec := e.do(http.MethodGet, "/api/v2/libraries/1", "", withSession(cookie), false); rec.Code != http.StatusNotFound {
		t.Fatalf("get removed library: %d, want 404", rec.Code)
	}
	var list []pageLibrary
	get(t, e, cookie, "/api/v2/libraries", &list)
	if len(list) != 0 {
		t.Fatalf("libraries after remove: %+v", list)
	}
}

// TestPatchAndDeleteUnknownLibraryAnswer404: a number no library has is reported as not found.
func TestPatchAndDeleteUnknownLibraryAnswer404(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	for _, r := range []struct{ method, path, body string }{
		{http.MethodPatch, "/api/v2/libraries/99", `{"name":"x"}`},
		{http.MethodDelete, "/api/v2/libraries/99", ""},
		{http.MethodPatch, "/api/v2/libraries/abc", `{"name":"x"}`},
		{http.MethodDelete, "/api/v2/libraries/abc", ""},
	} {
		rec := e.do(r.method, r.path, r.body, write(withSession(cookie)), false)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: %d, want 404", r.method, r.path, rec.Code)
		}
		if code, _, _ := errorBody(t, rec); code != "not_found" {
			t.Fatalf("%s %s: code %q, want not_found", r.method, r.path, code)
		}
	}
}
