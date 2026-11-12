// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// V4a (UI-API-CONTRACT-v2.md, Workers; slices/v2-engine/v4-plan.md): the workers in the contract's
// shape. There is one today, this program's pool ("local"); remote workers come in V7. What it can do
// comes from the doctor's encoder test (a test frame per encoder, never a guess from a list); its
// processor slots are Settings' parallelJobs, its graphics slots its own setting (default 1 when a
// hardware encoder works, else 0: they are used once hardware encoding comes, V4c).
type workerView struct {
	store *jobs.Store
	ss    *settingsStore
	eng   Engine
	host  string

	mu     sync.Mutex
	status string
	since  time.Time // when this program first saw the status (an approximation: it is read on demand)
}

var processStart = time.Now()

func newWorkerView(store *jobs.Store, ss *settingsStore, eng Engine) *workerView {
	host, _ := os.Hostname()
	return &workerView{store: store, ss: ss, eng: eng, host: host, since: processStart}
}

// tools is the last encoder test (ok false: none yet; it starts in the background).
func (v *workerView) tools() (doctor.Tools, bool) {
	if v.eng.Doctor == nil {
		return doctor.Tools{}, false
	}
	return v.eng.Doctor.ToolsNow()
}

// slots are the local worker's slots: processor (parallelJobs) and graphics.
func (v *workerView) slots(cfg jobs.WorkerConfig, s Settings, working int, t doctor.Tools) map[string]any {
	gpu := 0
	if cfg.GPUSlots != nil {
		gpu = *cfg.GPUSlots
	} else if len(t.Hardware) > 0 {
		gpu = 1
	}
	return map[string]any{"cpu": map[string]int{"used": working, "total": s.ParallelJobs}, "gpu": map[string]int{"used": 0, "total": gpu}}
}

// local is the local worker as the contract's Worker.
func (v *workerView) local(ctx context.Context) (map[string]any, error) {
	cfg, err := v.store.WorkerConfigOf(jobs.LocalWorker)
	if err != nil {
		return nil, err
	}
	s, err := v.ss.load()
	if err != nil {
		return nil, err
	}
	working, err := v.store.WorkingJobs()
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, j := range working {
		ids = append(ids, strconv.FormatInt(j.ID, 10))
	}
	status := "online"
	switch paused := v.store.Paused(); {
	case paused && len(working) > 0:
		status = "draining" // the running files finish, nothing new is taken
	case paused:
		status = "paused"
	case len(working) > 0:
		status = "busy"
	}
	v.mu.Lock()
	if status != v.status {
		if v.status != "" {
			v.since = time.Now()
		}
		v.status = status
	}
	since := v.since
	v.mu.Unlock()
	t, ok := v.tools()
	var ff any
	gpus := []map[string]any{}
	if ok && t.FFmpeg != "" {
		ff = map[string]string{"path": t.FFmpeg, "version": t.FFmpegVersion}
		if found, vendor := doctor.GPU(t); found {
			gpus = append(gpus, map[string]any{"index": 0, "vendor": gpuVendor(vendor), "model": "", "vramBytes": nil})
		}
	}
	var schedule any
	if s.QuietHours.From != "" {
		schedule = map[string]string{"from": s.QuietHours.From, "to": s.QuietHours.To}
	}
	return map[string]any{
		"id": jobs.LocalWorker, "name": cfg.Name, "kind": "local", "host": v.host, "os": runtime.GOOS, "arch": runtime.GOARCH,
		"appVersion": v.eng.Version, "status": status, "statusSince": since.UTC().Format(time.RFC3339),
		"lastHeartbeatAt": time.Now().UTC().Format(time.RFC3339), // in this program: answering is its heartbeat
		"slots":           v.slots(cfg, s, len(working), t),
		"live":            map[string]any{"cpuPercent": nil, "memUsedBytes": nil, "memTotalBytes": nil, "gpu": []any{}}, // CAN'T YET
		"gpus":            gpus, "ffmpeg": ff, "capabilities": capabilities(t, ok), "currentJobIds": ids,
		"tags": cfg.Tags, "pool": cfg.Pool, "schedule": schedule, "pathMappings": []any{},
	}, nil
}

// remoteOfflineAfter: a remote worker that hasn't called for this long shows offline (it heartbeats
// every 10 s).
const remoteOfflineAfter = 30 * time.Second

// remote is a paired worker on another computer in the contract's shape (V7b1): what it reported, its
// running jobs, and offline once it stops calling.
func (v *workerView) remote(rw jobs.RemoteWorker) (map[string]any, error) {
	working, err := v.store.WorkingJobs()
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, j := range working {
		if j.Worker == rw.ID {
			ids = append(ids, strconv.FormatInt(j.ID, 10))
		}
	}
	seen, _ := time.Parse(time.RFC3339, rw.LastSeen)
	status := "online"
	switch {
	case seen.IsZero() || time.Since(seen) > remoteOfflineAfter:
		status = "offline"
	case len(ids) > 0:
		status = "busy"
	case v.store.Paused():
		status = "paused"
	}
	var caps any = []any{}
	if c := json.RawMessage(rw.Capabilities); json.Valid(c) && string(c) != "{}" {
		caps = c
	}
	return map[string]any{
		"id": rw.ID, "name": rw.Name, "kind": "remote", "host": nil, "os": nil, "arch": nil,
		"appVersion": rw.Version, "status": status, "statusSince": nil, "lastHeartbeatAt": nilIfEmpty(rw.LastSeen),
		"slots": map[string]any{"cpu": map[string]any{"total": 1, "used": len(ids)}, "gpu": nil},
		"live":  map[string]any{"cpuPercent": nil, "memUsedBytes": nil, "memTotalBytes": nil, "gpu": []any{}},
		"gpus":  []any{}, "ffmpeg": nil, "capabilities": caps, "currentJobIds": ids,
		"tags": []string{}, "pool": nil, "schedule": nil, "pathMappings": []any{},
	}, nil
}

// gpuVendor is the contract's vendor word for doctor.GPU's.
func gpuVendor(v string) string {
	switch v {
	case "NVIDIA":
		return "nvidia"
	case "Intel":
		return "intel"
	case "AMD":
		return "amd"
	}
	return "other"
}

var encoderLabels = map[string]string{
	"libx265": "x265, HEVC (processor)", "libx264": "x264, H.264 (processor)", "libsvtav1": "SVT-AV1 (processor)", "libvpx-vp9": "VP9 (processor)",
	"hevc_nvenc": "NVIDIA NVENC, HEVC (graphics card)", "hevc_qsv": "Intel Quick Sync, HEVC (graphics)", "hevc_vaapi": "VA-API, HEVC (graphics)",
	"hevc_amf": "AMD AMF, HEVC (graphics card)", "hevc_videotoolbox": "VideoToolbox, HEVC (Apple)", "hevc_mf": "Media Foundation, HEVC (graphics)",
	"hevc_v4l2m2m": "V4L2, HEVC (hardware)",
}

// capabilities are the encoder test's findings as the contract's Capability list ([] before the first
// test): each encoder tested with a real frame, passed or failed; ffprobe and the picture check.
func capabilities(t doctor.Tools, ok bool) []map[string]any {
	out := []map[string]any{}
	if !ok {
		return out
	}
	at := any(nil)
	if !t.TestedAt.IsZero() {
		at = t.TestedAt.UTC().Format(time.RFC3339)
	}
	add := func(kind, name, label, slot, test, detail string) {
		c := map[string]any{"key": kind + ":" + name, "kind": kind, "label": label, "slot": slot, "test": test, "testedAt": at}
		if detail != "" {
			c["detail"] = detail
		}
		out = append(out, c)
	}
	const failed = "ffmpeg lists it, but a test frame failed on this computer."
	for _, e := range t.Software {
		add("encoder", e, encoderLabels[e], "cpu", "passed", "")
	}
	for _, e := range t.SoftwareListed {
		add("encoder", e, encoderLabels[e], "cpu", "failed", failed)
	}
	for _, e := range t.Hardware {
		detail := "8-bit only: HDR files are converted on the processor."
		if slices.Contains(t.Hardware10, e) {
			detail = "10-bit too (HDR)."
		}
		add("encoder", e, encoderLabels[e], "gpu", "passed", detail)
	}
	for _, e := range t.HardwareListed {
		add("encoder", e, encoderLabels[e], "gpu", "failed", failed)
	}
	if t.FFprobe != "" {
		add("tool", "ffprobe", "ffprobe "+t.FFprobeVersion, "cpu", "passed", "")
	}
	if t.SSIM {
		add("filter", "ssim", "Picture check (SSIM)", "cpu", "passed", "")
	}
	return out
}

func v2Workers(api *http.ServeMux, v *workerView) {
	found := func(w http.ResponseWriter, r *http.Request) bool {
		if r.PathValue("id") != jobs.LocalWorker {
			apiError(w, http.StatusNotFound, "not_found", "There is no such worker. Nothing was changed.")
			return false
		}
		return true
	}
	one := func(w http.ResponseWriter, r *http.Request) {
		wk, err := v.local(r.Context())
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, wk)
	}
	list := func(w http.ResponseWriter, r *http.Request) {
		wk, err := v.local(r.Context())
		if err != nil {
			serverError(w, err)
			return
		}
		items := []any{wk}
		remotes, err := v.store.RemoteWorkers()
		if err != nil {
			serverError(w, err)
			return
		}
		for _, rw := range remotes {
			one, err := v.remote(rw)
			if err != nil {
				serverError(w, err)
				return
			}
			items = append(items, one)
		}
		writeJSON(w, map[string]any{"items": items, "total": len(items)})
	}
	api.HandleFunc("GET /api/v2/workers", list)
	api.HandleFunc("GET /api/v2/workers/{id}", func(w http.ResponseWriter, r *http.Request) {
		if rw, ok := v.remoteByID(r.PathValue("id")); ok {
			one, err := v.remote(rw)
			if err != nil {
				serverError(w, err)
				return
			}
			writeJSON(w, one)
			return
		}
		if found(w, r) {
			one(w, r)
		}
	})
	// V7b1: Add a worker makes a one-time pairing code, with the command to run on the other computer
	api.HandleFunc("POST /api/v2/workers/pairings", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		code, expires, err := v.store.NewPairing(time.Now())
		if err != nil {
			serverError(w, err)
			return
		}
		server := "http://" + r.Host
		if r.TLS != nil {
			server = "https://" + r.Host
		}
		writeJSON(w, map[string]any{"code": code, "expiresAt": expires.Format(time.RFC3339),
			"command": "shrink-your-shit worker --server " + server + " --pair " + code, "plainHTTP": r.TLS == nil})
	})
	// Remove worker: its token stops working; its running jobs go back to the queue
	api.HandleFunc("DELETE /api/v2/workers/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == jobs.LocalWorker {
			apiError(w, http.StatusConflict, "local_worker", "This computer's worker can't be removed; stop it instead. Nothing was changed.")
			return
		}
		if err := v.store.RemoveWorker(r.PathValue("id")); errors.Is(err, jobs.ErrNotFound) {
			apiError(w, http.StatusNotFound, "not_found", "There is no such worker. Nothing was changed.")
			return
		} else if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	api.HandleFunc("PATCH /api/v2/workers/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name  *string `json:"name"`
			Slots *struct {
				CPU json.RawMessage `json:"cpu"` // read below: a non-number is invalid_setting, not invalid_json (review a118b1b)
				GPU json.RawMessage `json:"gpu"`
			} `json:"slots"`
			Tags     *[]string `json:"tags"`
			Pool     *string   `json:"pool"`
			Schedule *struct {
				From string `json:"from"`
				To   string `json:"to"`
			} `json:"schedule"`
		}
		if !found(w, r) || !readJSON(w, r, &in) {
			return
		}
		cfg, err := v.store.WorkerConfigOf(jobs.LocalWorker)
		if err != nil {
			serverError(w, err)
			return
		}
		s, err := v.ss.load()
		if err != nil {
			serverError(w, err)
			return
		}
		bad := func(msg string) { apiError(w, http.StatusBadRequest, "invalid_setting", msg+" Nothing was changed.") }
		var cpu, gpu *int
		if in.Slots != nil {
			for _, f := range []struct {
				raw  json.RawMessage
				to   **int
				what string
			}{{in.Slots.CPU, &cpu, "Processor slots are a whole number from 1 to 8."}, {in.Slots.GPU, &gpu, "Graphics slots are a whole number from 0 to 8."}} {
				if len(f.raw) == 0 || string(f.raw) == "null" {
					continue
				}
				var n int
				if json.Unmarshal(f.raw, &n) != nil {
					bad(f.what)
					return
				}
				*f.to = &n
			}
		}
		if in.Name != nil {
			name := strings.TrimSpace(*in.Name)
			if name == "" || len([]rune(name)) > 60 {
				bad("Give the worker a name of 1 to 60 characters.")
				return
			}
			cfg.Name = name
		}
		if gpu != nil {
			if n := *gpu; n < 0 || n > 8 {
				bad("Graphics slots are a whole number from 0 to 8.")
				return
			}
			cfg.GPUSlots = gpu
		}
		if in.Tags != nil {
			tags := []string{}
			for _, t := range *in.Tags {
				if t = strings.TrimSpace(t); t != "" && !slices.Contains(tags, t) {
					tags = append(tags, t)
				}
			}
			if len(tags) > 20 {
				bad("A worker has at most 20 tags.")
				return
			}
			cfg.Tags = tags
		}
		if in.Pool != nil {
			cfg.Pool = nil
			if p := strings.TrimSpace(*in.Pool); p != "" {
				cfg.Pool = &p
			}
		}
		saveSettings := false
		if cpu != nil {
			s.ParallelJobs, saveSettings = *cpu, true // the pool follows it live
		}
		if in.Schedule != nil { // quiet hours (v1's); {"from":"","to":""} clears them
			s.QuietHours.From, s.QuietHours.To, saveSettings = in.Schedule.From, in.Schedule.To, true
		}
		if saveSettings {
			if code, msg := s.problem(); code != "" {
				apiError(w, http.StatusBadRequest, code, msg)
				return
			}
		}
		if err := v.store.SetWorkerConfig(cfg); err != nil {
			serverError(w, err)
			return
		}
		if saveSettings {
			if err := v.ss.save(s); err != nil {
				serverError(w, err)
				return
			}
		}
		one(w, r)
	})
	// act starts, stops or drains: today every worker together (the processing switch); false when
	// action is none of these (the answer is written)
	act := func(w http.ResponseWriter, action string) bool {
		var err error
		switch action {
		case "start":
			err = startWorker(v.store)
		case "drain": // takes nothing new, finishes its files, then is paused (v1's "Stop after these files")
			err = v.store.SetPaused(true)
		case "stop": // the running files go back to the queue, originals untouched (v1's "Stop now")
			if err = v.store.SetPaused(true); err == nil && v.eng.PauseNow != nil {
				v.eng.PauseNow()
				// the workers put their files back a moment later: answer once they have (at most 2 s), so
				// the answer says paused, not draining (review a118b1b)
				for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
					if working, werr := v.store.WorkingJobs(); werr != nil || len(working) == 0 {
						break
					}
				}
			}
		default:
			apiError(w, http.StatusNotFound, "not_found", "A worker can be started, stopped or drained.")
			return false
		}
		if err != nil {
			serverError(w, err)
			return false
		}
		return true
	}
	// every worker at once (user review 11-12 item 2; ui-shop 14:01Z: the Dashboard's switch), answered
	// with the workers list
	api.HandleFunc("POST /api/v2/workers/all/{action}", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) || !act(w, r.PathValue("action")) {
			return
		}
		list(w, r)
	})
	api.HandleFunc("POST /api/v2/workers/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !found(w, r) || !readJSON(w, r, &in) || !act(w, r.PathValue("action")) {
			return
		}
		one(w, r)
	})
	api.HandleFunc("POST /api/v2/workers/{id}/capabilities/test", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !found(w, r) || !readJSON(w, r, &in) {
			return
		}
		if v.eng.Doctor == nil {
			apiError(w, http.StatusServiceUnavailable, "not_available", "The checks are not available.")
			return
		}
		v.eng.Doctor.Retest(r.Context()) // a test frame per encoder, one test at a time
		one(w, r)
	})
}

// remoteByID is the paired worker with this id.
func (v *workerView) remoteByID(id string) (jobs.RemoteWorker, bool) {
	all, err := v.store.RemoteWorkers()
	if err != nil {
		return jobs.RemoteWorker{}, false
	}
	for _, rw := range all {
		if rw.ID == id {
			return rw, true
		}
	}
	return jobs.RemoteWorker{}, false
}
