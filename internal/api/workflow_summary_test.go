// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// starterWithFormat is the shrink-videos starter with its video format set, so a draft can differ from what is published.
func starterWithFormat(t *testing.T, format string) flow.Flow {
	t.Helper()
	s, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	f := s.Flow
	for i, ref := range f.Steps {
		if ref.Type != "video.convert" {
			continue
		}
		var settings map[string]any
		if err := json.Unmarshal(ref.Settings, &settings); err != nil {
			t.Fatal(err)
		}
		settings["format"] = format
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		f.Steps[i].Settings = raw
	}
	return f
}

type summaryBody struct {
	Summary struct {
		Converts    string `json:"converts"`
		LeavesAlone string `json:"leavesAlone"`
		Original    string `json:"original"`
	} `json:"summary"`
}

func getSummary(t *testing.T, e *env, cookie *http.Cookie, id int64) summaryBody {
	t.Helper()
	rec := e.do(http.MethodGet, "/api/v2/workflows/"+strconv.FormatInt(id, 10), "", withSession(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET workflow = %d: %s", rec.Code, rec.Body)
	}
	var out summaryBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The lines describe what the library runs: the published revision. A draft edited after publishing does not change them.
func TestSummaryKeepsThePublishedWords(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	ctx := context.Background()
	id, _, err := e.flows.Create(ctx, starterWithFormat(t, "hevc"), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.flows.Publish(ctx, id, nil, flow.ValidateContext{Env: flow.Env{}}); err != nil {
		t.Fatal(err)
	}
	draft, err := e.flows.Draft(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.flows.SaveDraft(ctx, id, starterWithFormat(t, "h264"), draft.Rev); err != nil {
		t.Fatal(err)
	}
	got := getSummary(t, e, cookie, id)
	if !strings.Contains(got.Summary.Converts, "HEVC") || strings.Contains(got.Summary.Converts, "H.264") {
		t.Errorf("converts = %q, want the published HEVC, not the draft's H.264", got.Summary.Converts)
	}
}

// A workflow with nothing published gets empty lines, never sentences about its draft.
func TestSummaryEmptyWithoutAPublishedRevision(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	id, _, err := e.flows.Create(context.Background(), starterWithFormat(t, "hevc"), "test")
	if err != nil {
		t.Fatal(err)
	}
	got := getSummary(t, e, cookie, id)
	if got.Summary.Converts != "" || got.Summary.LeavesAlone != "" || got.Summary.Original != "" {
		t.Errorf("summary = %+v, want empty lines with nothing published", got.Summary)
	}
}

// A settings read that fails gives the store's default, 7 days, never 0.
func TestKeepDaysNeverZeroWhenTheReadFails(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// Not migrated: there is no settings table to read, so the read fails.
	s := &Server{}
	if got := s.keepDaysNow(&Deps{DB: db}); got != 7 {
		t.Errorf("keepDaysNow after a failed read = %d, want 7", got)
	}
}

// A read that fails is reported as unread, so the summary's replace line can be left empty.
func TestKeepDaysReadReportsAFailure(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := &Server{}
	if _, ok := s.keepDaysRead(&Deps{DB: db}); ok {
		t.Error("a read with no settings table reported as read")
	}
}

// The replace line reads processing.keepOriginalsDays each time the workflow is read: the first setting and then a second
// one each name their own days, so a fixed number could not pass both.
func TestReplaceLineReadsTheSettingEachTime(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	ctx := context.Background()
	f := starterWithFormat(t, "hevc")
	for i, ref := range f.Steps {
		if ref.Type != "file.save" {
			continue
		}
		var settings map[string]any
		if err := json.Unmarshal(ref.Settings, &settings); err != nil {
			t.Fatal(err)
		}
		settings["where"] = "replace"
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		f.Steps[i].Settings = raw
	}
	id, _, err := e.flows.Create(ctx, f, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.flows.Publish(ctx, id, nil, flow.ValidateContext{Env: flow.Env{}}); err != nil {
		t.Fatal(err)
	}
	for _, days := range []int{10, 20} {
		rec := e.do(http.MethodPatch, "/api/v2/settings", `{"processing":{"keepOriginalsDays":`+strconv.Itoa(days)+`}}`, write(withSession(cookie)), false)
		if rec.Code != http.StatusOK {
			t.Fatalf("set keep = %d: %s", rec.Code, rec.Body)
		}
		want := "The original goes to the trash for " + strconv.Itoa(days) + " days."
		if got := getSummary(t, e, cookie, id).Summary.Original; got != want {
			t.Errorf("original with %d days = %q, want %q", days, got, want)
		}
	}
}
