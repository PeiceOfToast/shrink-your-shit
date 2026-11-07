// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// done makes a finished job: original size before, new size after, output at out.
func done(t *testing.T, st *jobs.Store, libID int64, path, out string, before, after int64) int64 {
	t.Helper()
	id, err := st.Enqueue(jobs.Job{Library: "/m", LibraryID: libID, Path: path, SizeBefore: before})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("t"); err != nil {
		t.Fatal(err)
	}
	if err := st.Complete(id, out, after); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDoneAndKeptLists(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	kept := done(t, st, l.ID, "/m/kept.mkv", "/m/kept.mkv", 1000, 400)
	oid, _ := st.BeginSwap(jobs.Original{JobID: kept, LibraryID: l.ID, Path: "/m/kept.mkv", Trash: "/m/.syp-trash/kept.1.mkv", Staged: "s", Bytes: 1000, Hash: "h", NewBytes: 400})
	st.FinishSwap(oid, time.Now().Add(7*24*time.Hour-time.Minute))
	done(t, st, l.ID, "/m/gone.mkv", "/m/gone.mkv", 500, 300)          // replaced, nothing kept (0 days)
	done(t, st, l.ID, "/m/beside.mkv", "/m/beside.hevc.mkv", 800, 200) // beside: original untouched

	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/done"})
	var d struct {
		Items []map[string]any
		Total int
	}
	json.Unmarshal([]byte(body), &d)
	if d.Total != 3 || len(d.Items) != 3 {
		t.Fatalf("%s", body)
	}
	byPath := map[string]map[string]any{}
	for _, i := range d.Items {
		byPath[i["path"].(string)] = i
	}
	if k := byPath["/m/kept.mkv"]; k["originalState"] != "kept" || k["keptDaysLeft"] != 7.0 || k["keptId"] != fmt.Sprint(oid) || k["library"] != "Movies" || k["beforeBytes"] != 1000.0 || k["afterBytes"] != 400.0 {
		t.Errorf("kept: %v", k)
	}
	if g := byPath["/m/gone.mkv"]; g["originalState"] != "deleted" || g["keptId"] != nil || g["keptDaysLeft"] != nil {
		t.Errorf("gone: %v", g)
	}
	if b := byPath["/m/beside.mkv"]; b["originalState"] != "kept" || b["keptId"] != nil {
		t.Errorf("beside: %v", b)
	}

	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/kept"})
	var k struct {
		KeptBytes, Count, Days int
		Items                  []map[string]any
	}
	json.Unmarshal([]byte(body), &k)
	if k.KeptBytes != 1000 || k.Count != 1 || k.Days != 7 || k.Items[0]["path"] != "/m/kept.mkv" || k.Items[0]["nowBytes"] != 400.0 || k.Items[0]["library"] != "Movies" {
		t.Errorf("%s", body)
	}

	// F1: freed = originals gone (500-300); waiting = originals kept (1000-400); beside saves nothing
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/libraries"})
	if !strings.Contains(body, `"freedBytes":200`) || !strings.Contains(body, `"waitingBytes":600`) {
		t.Errorf("%s", body)
	}

	code, body := post(t, srv, "POST", fmt.Sprintf("/api/kept/%d/restore", oid), `{}`)
	if code != 501 || !strings.Contains(body, "not_available") {
		t.Errorf("%d %s", code, body)
	}
}
