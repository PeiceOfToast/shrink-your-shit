// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"testing"
	"time"
)

// GET /api/v2/workers' live.gpu is this computer's busiest GPU reading (the overview's gpuPercent): one entry
// when a GPU was read, none when nothing could be read. A remote worker doesn't report its GPU: none.
func TestWorkersLiveGPUIsTheBusiestReading(t *testing.T) {
	resetGPU(t)
	srv, st, a, _ := libServer(t, true)
	person := a.Token()
	code, _, _ := st.NewPairing(time.Now())
	rw, _, err := st.Pair(code, "Desktop", "1", "", "", WorkerAPILevel, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	type gpu struct {
		Index         int
		UtilPercent   float64
		VramUsedBytes *int64
	}
	live := func() map[string][]gpu {
		t.Helper()
		_, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers", bearer: person})
		var list struct {
			Items []struct {
				ID, Kind string
				Live     struct{ GPU []gpu }
			}
		}
		if err := json.Unmarshal([]byte(b), &list); err != nil {
			t.Fatal(err)
		}
		out := map[string][]gpu{}
		for _, w := range list.Items {
			if w.ID == rw.ID {
				out["remote"] = w.Live.GPU
			} else {
				out[w.Kind] = w.Live.GPU
			}
		}
		return out
	}

	pct := 37.0
	readNvidiaSMI = func() *float64 { return &pct }
	got := live()
	if l := got["local"]; len(l) != 1 || l[0].Index != 0 || l[0].UtilPercent != 37 || l[0].VramUsedBytes != nil {
		t.Errorf("local live.gpu with a 37%% reading = %+v, want one entry index 0, 37, vram null", l)
	}
	if r, ok := got["remote"]; !ok || len(r) != 0 {
		t.Errorf("remote live.gpu = %+v (listed %v), want []", r, ok)
	}

	readNvidiaSMI = func() *float64 { return nil }
	gpuUse.mu.Lock()
	gpuUse.ok = false
	gpuUse.mu.Unlock()
	if l := live()["local"]; len(l) != 0 {
		t.Errorf("local live.gpu with nothing readable = %+v, want []", l)
	}
}
