// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// previewProbes are three stored probes: one the starter converts, one it leaves alone, and a file never probed.
func previewProbes() []media.Info {
	return []media.Info{
		{
			Format: media.Format{Names: []string{"mov", "mp4"}, Duration: 2 * time.Hour, Size: 3_000_000_000, BitRate: 3_000_000},
			Video:  []media.VideoTrack{{Codec: "h264", Profile: "High", Width: 1920, Height: 1080, FrameRate: media.Rational{Num: 24000, Den: 1001}, PixelFormat: "yuv420p", BitDepth: 8}},
			Audio:  []media.AudioTrack{{Codec: "aac", Channels: 2, SampleRate: 48000, Language: "eng"}},
		},
		{
			Format: media.Format{Names: []string{"matroska", "webm"}, Duration: time.Hour, Size: 900_000_000, BitRate: 2_000_000},
			Video:  []media.VideoTrack{{Codec: "hevc", Profile: "Main", Width: 1920, Height: 1080, FrameRate: media.Rational{Num: 25, Den: 1}, PixelFormat: "yuv420p", BitDepth: 8}},
			Audio:  []media.AudioTrack{{Codec: "aac", Channels: 2, SampleRate: 48000, Language: "eng"}},
		},
	}
}

// The preview counts the stored probes with the queue's decision, and changes nothing: the file rows and the queue are the
// same after it as before.
func TestLibraryPreviewCountsTheStoredProbes(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	ctx := context.Background()
	wf, _, err := e.flows.CreateFromStarter(ctx, "shrink-videos", "Preview")
	if err != nil {
		t.Fatal(err)
	}
	lib := mustAddLibrary(t, e, cookie, "Films", t.TempDir(), "daily")
	libID, err := strconv.ParseInt(lib.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	probes := previewProbes()
	for i, info := range probes {
		tracks := media.Summarize(info)
		f := library.File{LibraryID: libID, RelPath: "f" + strconv.Itoa(i) + ".mp4", Size: info.Format.Size, MTime: time.Unix(1_700_000_000, 0), FileID: "id" + strconv.Itoa(i)}
		if _, err := e.catalog.SaveFile(ctx, f, library.Skipped, "", &tracks); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.catalog.SaveFile(ctx, library.File{LibraryID: libID, RelPath: "never.mp4", Size: 1, MTime: time.Unix(1_700_000_000, 0), FileID: "id-never"}, library.Skipped, "", nil); err != nil {
		t.Fatal(err)
	}
	rev, err := e.flows.Revision(ctx, wf, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := library.Preview{Total: 3}
	for _, info := range probes {
		if ok, _ := flow.DecideFile(rev, info); ok {
			want.WouldConvert++
		}
	}
	if want.WouldConvert != 1 {
		t.Fatalf("the fixtures decide %d to convert; the test needs one to convert and one to leave alone", want.WouldConvert)
	}
	filesBefore, err := e.catalog.Files(ctx, libID)
	if err != nil {
		t.Fatal(err)
	}

	rec := e.do(http.MethodGet, "/api/v2/libraries/"+lib.ID+"/preview?workflowId="+strconv.FormatInt(wf, 10), "", withSession(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		WouldConvert int `json:"wouldConvert"`
		Total        int `json:"total"`
		Skipped      []struct {
			Reason string `json:"reason"`
			Count  int    `json:"count"`
		} `json:"skipped"`
	}
	decodeInto(t, rec, &got)
	if got.Total != 3 || got.WouldConvert != want.WouldConvert {
		t.Errorf("preview = %d of %d, want %d of 3", got.WouldConvert, got.Total, want.WouldConvert)
	}
	unknown := 0
	for _, s := range got.Skipped {
		if s.Reason == library.PreviewUnknown {
			unknown = s.Count
		}
	}
	if unknown != 1 {
		t.Errorf("skipped = %+v, want the never-probed file as unknown", got.Skipped)
	}

	filesAfter, err := e.catalog.Files(ctx, libID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filesAfter) != len(filesBefore) {
		t.Fatalf("the preview changed the file rows: %d before, %d after", len(filesBefore), len(filesAfter))
	}
	for i := range filesAfter {
		if filesAfter[i].State != filesBefore[i].State || filesAfter[i].RelPath != filesBefore[i].RelPath {
			t.Errorf("file %s changed: %s -> %s", filesBefore[i].RelPath, filesBefore[i].State, filesAfter[i].State)
		}
	}
}

// The preview's refusals: a library that is not there, a missing or unknown workflow.
func TestLibraryPreviewRefusals(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	ctx := context.Background()
	wf, _, err := e.flows.CreateFromStarter(ctx, "shrink-videos", "Preview")
	if err != nil {
		t.Fatal(err)
	}
	lib := mustAddLibrary(t, e, cookie, "Films", t.TempDir(), "daily")

	if rec := e.do(http.MethodGet, "/api/v2/libraries/999999/preview?workflowId="+strconv.FormatInt(wf, 10), "", withSession(cookie), false); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library = %d, want 404", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/api/v2/libraries/"+lib.ID+"/preview", "", withSession(cookie), false); rec.Code != http.StatusBadRequest {
		t.Errorf("no workflowId = %d, want 400", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/api/v2/libraries/"+lib.ID+"/preview?workflowId=x", "", withSession(cookie), false); rec.Code != http.StatusBadRequest {
		t.Errorf("workflowId=x = %d, want 400", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/api/v2/libraries/"+lib.ID+"/preview?workflowId=999999", "", withSession(cookie), false); rec.Code != http.StatusNotFound {
		t.Errorf("unknown workflow = %d, want 404", rec.Code)
	}
}

// An empty library gives zeros and an empty list.
func TestLibraryPreviewOfAnEmptyLibrary(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	ctx := context.Background()
	wf, _, err := e.flows.CreateFromStarter(ctx, "shrink-videos", "Preview")
	if err != nil {
		t.Fatal(err)
	}
	lib := mustAddLibrary(t, e, cookie, "Empty", t.TempDir(), "daily")
	rec := e.do(http.MethodGet, "/api/v2/libraries/"+lib.ID+"/preview?workflowId="+strconv.FormatInt(wf, 10), "", withSession(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Body.String(); got != `{"wouldConvert":0,"total":0,"skipped":[]}`+"\n" {
		t.Errorf("empty preview body = %s", got)
	}
}
