// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/sysload"
)

// resetGPU empties gpuPercent's cache, and the AMD and Intel readings read as not measured (this machine's
// real GPUs must not reach the expectations), and restores the real readings when the test ends.
func resetGPU(t *testing.T) {
	t.Helper()
	real, realOther := readNvidiaSMI, readOtherGPUs
	readOtherGPUs = func() *float64 { return nil }
	t.Cleanup(func() {
		readNvidiaSMI, readOtherGPUs = real, realOther
		gpuUse.mu.Lock()
		gpuUse.ok = false
		gpuUse.mu.Unlock()
	})
	gpuUse.mu.Lock()
	gpuUse.ok = false
	gpuUse.mu.Unlock()
}

type overviewWorker struct {
	ID, Kind   string
	CPUPercent *float64 `json:"cpuPercent"`
	GPUPercent *float64 `json:"gpuPercent"`
}

func overviewWorkers(t *testing.T, body string) []overviewWorker {
	t.Helper()
	var o struct{ Workers []overviewWorker }
	if err := json.Unmarshal([]byte(body), &o); err != nil {
		t.Fatal(err)
	}
	return o.Workers
}

// The overview's workers carry the live cpuPercent that GET /api/v2/workers reports: a remote's is its
// last reported load; one that never reported one is null.
func TestOverviewWorkerCPUIsTheLiveFigure(t *testing.T) {
	resetGPU(t)
	srv, st, a, _ := libServer(t, true)
	person := a.Token()
	code, _, _ := st.NewPairing(time.Now())
	rw, _, err := st.Pair(code, "Desktop", "1", "", "", WorkerAPILevel, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	code, _, _ = st.NewPairing(time.Now())
	silent, _, err := st.Pair(code, "Laptop", "1", "", "", WorkerAPILevel, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pct := 42.5
	workerLoads.Store(rw.ID, sysload.Load{CPUPercent: &pct})
	defer workerLoads.Delete(rw.ID)

	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview", bearer: person})
	got := map[string]overviewWorker{}
	for _, w := range overviewWorkers(t, body) {
		got[w.ID] = w
	}
	if w := got[rw.ID]; w.CPUPercent == nil || *w.CPUPercent != pct || w.GPUPercent != nil {
		t.Errorf("reporting remote: %+v, want cpuPercent %v and gpuPercent null", w, pct)
	}
	if w := got[silent.ID]; w.CPUPercent != nil {
		t.Errorf("silent remote: cpuPercent %v, want null", *w.CPUPercent)
	}
	if !strings.Contains(body, `"savedBytes":0`) || !strings.Contains(body, `"waitingBytes":0`) {
		t.Errorf("daily days carry no savedBytes and waitingBytes: %s", body)
	}
}

// gpuPercent is NVIDIA's busiest GPU from nvidia-smi on this computer; without nvidia-smi it is null.
func TestOverviewGPUFromNvidiaSMI(t *testing.T) {
	resetGPU(t)
	srv, st, a, _ := libServer(t, true)
	person := a.Token()
	_ = st

	readNvidiaSMI = func() *float64 { v := 37.0; return &v }
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview", bearer: person})
	local := localOverviewWorker(t, body)
	if local.GPUPercent == nil || *local.GPUPercent != 37 {
		t.Errorf("local gpuPercent %v, want 37", local.GPUPercent)
	}

	// no nvidia-smi: null, and the cache holds that answer for a while
	resetGPU(t)
	readNvidiaSMI = func() *float64 { return nil }
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/overview", bearer: person})
	if local := localOverviewWorker(t, body); local.GPUPercent != nil {
		t.Errorf("gpuPercent %v without nvidia-smi, want null", *local.GPUPercent)
	}
}

// Without nvidia-smi on PATH the real reading is null, and no command is run.
func TestGPUReadingNeedsNvidiaSMIOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if v := readNvidiaSMI(); v != nil {
		t.Errorf("gpu %v with no nvidia-smi on PATH, want null", *v)
	}
}

func localOverviewWorker(t *testing.T, body string) overviewWorker {
	t.Helper()
	for _, w := range overviewWorkers(t, body) {
		if w.Kind == "local" {
			return w
		}
	}
	t.Fatalf("no local worker in %s", body)
	return overviewWorker{}
}
