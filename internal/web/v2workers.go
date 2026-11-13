// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/pathmap"
	"github.com/shrink-your-shit/shrink-your-shit/internal/sysload"
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

	meter       sysload.Meter       // this computer's load (V7d)
	remoteSince map[string]statusAt // remote workers' statuses, as last seen, and since when
	seen        map[string]string   // every worker's status at the last check (worker.updated on a change)
	checked     time.Time           // when the statuses were last checked
}

// statusAt is a status and when it began.
type statusAt struct {
	status string
	since  time.Time
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
	remote := map[string]bool{}
	if rs, err := v.store.RemoteWorkers(); err == nil {
		for _, rw := range rs {
			remote[rw.ID] = true
		}
	}
	mine := working[:0] // this computer's own (a remote worker's job, run here, is that worker's)
	for _, j := range working {
		if !remote[j.Worker] {
			mine = append(mine, j)
		}
	}
	working = mine
	ids := []string{}
	for _, j := range working {
		ids = append(ids, strconv.FormatInt(j.ID, 10))
	}
	enabled := v.store.WorkerEnabled(jobs.LocalWorker)
	status := "online"
	switch paused := !enabled; {
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
		"id": jobs.LocalWorker, "name": cfg.Name, "kind": "local", "enabled": enabled, "host": v.host, "os": runtime.GOOS, "arch": runtime.GOARCH,
		"appVersion": v.eng.Version, "status": status, "statusSince": since.UTC().Format(time.RFC3339),
		"lastHeartbeatAt": time.Now().UTC().Format(time.RFC3339), // in this program: answering is its heartbeat
		"slots":           v.slots(cfg, s, len(working), t),
		"live":            liveLoad(v.meter.Sample()), // V7d: this computer, measured as remote workers measure theirs
		"update":          nil,
		"gpus":            gpus, "ffmpeg": ff, "capabilities": capabilities(t, ok), "currentJobIds": ids,
		"tags": cfg.Tags, "pool": cfg.Pool, "schedule": schedule, "pathMappings": []any{},
	}, nil
}

// RemoteOfflineAfter: a remote worker not heard from (any call: a heartbeat, a lease, an upload...) for
// this long shows offline; it heartbeats every 10 s, beside its work (V7d).
var RemoteOfflineAfter = 30 * time.Second

// liveLoad is a load as the contract's live figures (nil: not measured; GPU load isn't yet).
func liveLoad(l sysload.Load) map[string]any {
	return map[string]any{"cpuPercent": l.CPUPercent, "memUsedBytes": l.MemUsedBytes, "memTotalBytes": l.MemTotalBytes, "gpu": []any{}}
}

// remote is a paired worker on another computer in the contract's shape (V7b1): what it reported, its
// running jobs, and offline once it stops calling.
// all is every worker as GET /api/v2/workers lists them: this computer's pool, then each paired worker.
// The summary and the overview list the same, so the three never disagree (orch-lead 18:43Z, 18:49Z).
func (v *workerView) all(ctx context.Context) ([]map[string]any, error) {
	wk, err := v.local(ctx)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{wk}
	remotes, err := v.store.RemoteWorkers()
	if err != nil {
		return nil, err
	}
	for _, rw := range remotes {
		one, err := v.remote(rw)
		if err != nil {
			return nil, err
		}
		items = append(items, one)
	}
	return items, nil
}

// running is how many of the workers take work now: online, busy or draining (not paused or offline).
func running(items []map[string]any) int {
	n := 0
	for _, it := range items {
		switch it["status"] {
		case "online", "busy", "draining":
			n++
		}
	}
	return n
}

func (v *workerView) remote(rw jobs.RemoteWorker) (map[string]any, error) {
	working, err := v.store.WorkingJobs()
	if err != nil {
		return nil, err
	}
	return v.remoteWith(rw, working)
}

// remoteWith is remote with the running jobs read once for a whole list (review da46389).
func (v *workerView) remoteWith(rw jobs.RemoteWorker, working []jobs.Working) (map[string]any, error) {
	ids := []string{}
	for _, j := range working {
		if j.Worker == rw.ID {
			ids = append(ids, strconv.FormatInt(j.ID, 10))
		}
	}
	seen, _ := time.Parse(time.RFC3339, rw.LastSeen)
	enabled := v.store.WorkerEnabled(rw.ID)
	status := "online"
	switch {
	case seen.IsZero() || time.Since(seen) > RemoteOfflineAfter:
		status = "offline"
	case !enabled && len(ids) > 0:
		status = "draining"
	case !enabled:
		status = "paused"
	case len(ids) > 0:
		status = "busy"
	}
	var caps any = []any{}
	if c := json.RawMessage(rw.Capabilities); json.Valid(c) && string(c) != "{}" {
		caps = c
	}
	since := v.remoteStatusSince(rw.ID, status, seen)
	tests.expire(v.store, func(id string) bool { return id == rw.ID && status != "offline" })
	mappings := []any{}
	if ms, err := v.store.PathMappings(rw.ID); err == nil {
		for _, m := range ms {
			mappings = append(mappings, mappingView(m))
		}
	}
	var load sysload.Load
	if l, ok := workerLoads.Load(rw.ID); ok && status != "offline" {
		load = l.(sysload.Load)
	}
	var holding any // why it takes no work now (#174 QA e); null when it does, or is offline
	if h, ok := workerHolds.Load(rw.ID); ok && status != "offline" {
		holding = h
	}
	var update any // Q7: no self-update; the words say what to do
	if rw.Version != "" && v.eng.Version != "" && rw.Version != v.eng.Version {
		update = map[string]any{"serverVersion": v.eng.Version,
			"words": "This worker runs " + rw.Version + "; the server runs " + v.eng.Version + ". Install " + v.eng.Version + " on that computer, then start the worker again."}
	}
	return map[string]any{
		"id": rw.ID, "name": rw.Name, "kind": "remote", "enabled": enabled, "host": nil, "os": nil, "arch": nil,
		"appVersion": rw.Version, "status": status, "statusSince": since, "lastHeartbeatAt": nilIfEmpty(rw.LastSeen), "update": update,
		"holdingBack": holding,
		"slots":       map[string]any{"cpu": map[string]any{"total": 1, "used": len(ids)}, "gpu": nil},
		"live":        liveLoad(load),
		"gpus":        []any{}, "ffmpeg": nil, "capabilities": caps, "currentJobIds": ids,
		"tags": []string{}, "pool": nil, "schedule": nil, "pathMappings": mappings,
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

const failed = "ffmpeg lists it, but a test frame failed on this computer."

// usesDevDRI: only Linux reaches the GPU through /dev/dri (Windows Quick Sync goes through D3D11), so
// the device lines would mislead anywhere else.
var usesDevDRI = runtime.GOOS == "linux"

// Seams for intelFailure: the render nodes doctor's vaapiDevice globs, and an open for read and write
// as this user. Tests swap them; the real ones touch /dev/dri.
var renderNodes = func() []string {
	m, _ := filepath.Glob("/dev/dri/renderD*")
	return m
}

var openRender = func(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

// intelFailure says why a failed Quick Sync or VA-API test is failing when the device is the cause;
// otherwise the generic line. Only hevc_qsv and hevc_vaapi come here.
func intelFailure() string {
	nodes := renderNodes()
	if len(nodes) == 0 {
		return "No graphics device here (/dev/dri is missing). In Docker, add the Intel GPU block from docker-compose.yml and restart; on a host install, check the graphics driver is loaded."
	}
	if err := openRender(nodes[0]); err != nil {
		return "Can't open " + nodes[0] + ": the app's user isn't in its group. In Docker, set group_add to the host's render group id (getent group render | cut -d: -f3); on a host install, add the app's user to the render group."
	}
	return failed
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
		detail := failed
		if usesDevDRI && (e == "hevc_qsv" || e == "hevc_vaapi") {
			detail = intelFailure()
		}
		add("encoder", e, encoderLabels[e], "gpu", "failed", detail)
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
		if _, remote := v.remoteByID(r.PathValue("id")); !remote && r.PathValue("id") != jobs.LocalWorker {
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
		items, err := v.all(r.Context())
		if err != nil {
			serverError(w, err)
			return
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
	// V7c: a real test of a shared folder (202; the result comes as worker.updated, at most 60 s later)
	api.HandleFunc("POST /api/v2/workers/{id}/paths/test", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Server string }
		if !readJSON(w, r, &in) {
			return
		}
		rw, ok := v.remoteByID(r.PathValue("id"))
		if !ok {
			apiError(w, http.StatusNotFound, "not_found", "There is no such worker on another computer.")
			return
		}
		if item, err := v.remote(rw); err == nil && item["status"] == "offline" {
			apiError(w, http.StatusConflict, "worker_offline", "This worker is offline, so its shared folder can't be tested now. Start it, then test again.")
			return
		}
		switch err := tests.start(v.store, rw.ID, filepath.Clean(in.Server)); {
		case errors.Is(err, errMappingUnknown):
			apiError(w, http.StatusNotFound, "not_found", "This worker has no shared folder for "+in.Server+".")
		case err != nil:
			apiError(w, http.StatusConflict, "test_failed", "The test file couldn't be written in "+in.Server+" on this server: "+err.Error())
		default:
			writeJSONStatus(w, http.StatusAccepted, map[string]any{"status": "pending"})
		}
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
		v.checkStatuses(r.Context(), true)
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
			PathMappings *[]struct{ Server, Worker, Mode string } `json:"pathMappings"` // V7c: remote workers only
		}
		if !found(w, r) || !readJSON(w, r, &in) {
			return
		}
		if rw, remote := v.remoteByID(r.PathValue("id")); remote {
			if in.PathMappings == nil {
				apiError(w, http.StatusBadRequest, "not_available", "For a worker on another computer, only its shared folders (pathMappings) can be set here. Nothing was changed.")
				return
			}
			list := []jobs.PathMapping{}
			seen := map[string]bool{}
			for _, m := range *in.PathMappings {
				sv := filepath.Clean(strings.TrimSpace(m.Server))
				wp := strings.TrimSpace(m.Worker)
				if !filepath.IsAbs(sv) || wp == "" || (m.Mode != "read" && m.Mode != "read_write") || seen[sv] {
					apiError(w, http.StatusBadRequest, "invalid_setting", "Each shared folder needs a full path on this server, its path on the worker, and read or read_write (each server folder once). Nothing was changed.")
					return
				}
				seen[sv] = true
				list = append(list, jobs.PathMapping{Server: sv, Worker: pathmap.CleanWorker(wp), Mode: m.Mode})
			}
			if err := v.store.SetPathMappings(rw.ID, list); err != nil {
				serverError(w, err)
				return
			}
			one, err := v.remote(rw)
			if err != nil {
				serverError(w, err)
				return
			}
			writeJSON(w, one)
			return
		}
		if in.PathMappings != nil {
			apiError(w, http.StatusBadRequest, "not_available", "This computer reads its own folders: shared folders are for workers on other computers. Nothing was changed.")
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
	// act starts, stops or drains one worker (local, or a remote one's id), or every worker ("all"): each
	// worker's own flag is its only on/off state (user review 11-12 item 2, orch-lead 14:22Z). false when
	// action is none of these (the answer is written).
	act := func(w http.ResponseWriter, target, action string) bool {
		var err error
		on := action == "start"
		switch {
		case action != "start" && action != "stop" && action != "drain":
			apiError(w, http.StatusNotFound, "not_found", "A worker can be started, stopped or drained.")
			return false
		case target == "all" && on:
			err = startWorker(v.store) // every worker on (and the after-update notice ends)
		case target == "all":
			err = v.store.SetPaused(true) // every worker off: drain lets their files finish
		case on:
			if err = v.store.SetWorkerEnabled(target, true); err == nil && target == jobs.LocalWorker {
				err = v.store.ClearStoppedByUpdate()
			}
		default:
			err = v.store.SetWorkerEnabled(target, false)
		}
		if err != nil {
			serverError(w, err)
			return false
		}
		if action == "stop" { // the running files go back to the queue, originals untouched (v1's "Stop now")
			if (target == "all" || target == jobs.LocalWorker) && v.eng.PauseNow != nil {
				v.eng.PauseNow()
			}
			remotes, _ := v.store.RemoteWorkers()
			working, _ := v.store.WorkingJobs()
			for _, j := range working {
				for _, rw := range remotes { // a remote worker's leases end: it is told on its next call
					if j.Worker == rw.ID && (target == "all" || target == rw.ID) {
						v.store.Release(j.ID)
					}
				}
			}
			// the local workers put their files back a moment later: answer once they have (at most 2 s),
			// so the answer says paused, not draining (review a118b1b)
			for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
				if working, werr := v.store.WorkingJobs(); werr != nil || len(working) == 0 {
					break
				}
			}
		}
		return true
	}
	// every worker at once (user review 11-12 item 2; ui-shop 14:01Z: the Dashboard's switch), answered
	// with the workers list
	api.HandleFunc("POST /api/v2/workers/all/{action}", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !readJSON(w, r, &in) || !act(w, "all", r.PathValue("action")) {
			return
		}
		v.checkStatuses(r.Context(), true)
		list(w, r)
	})
	// one worker, answered with that worker
	api.HandleFunc("POST /api/v2/workers/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !found(w, r) || !readJSON(w, r, &in) || !act(w, r.PathValue("id"), r.PathValue("action")) {
			return
		}
		v.checkStatuses(r.Context(), true)
		if rw, ok := v.remoteByID(r.PathValue("id")); ok {
			one, err := v.remote(rw)
			if err != nil {
				serverError(w, err)
				return
			}
			writeJSON(w, one)
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

// remoteStatusSince is when remote worker id's status began: offline, when it was last heard from plus
// the offline wait; otherwise when this program first saw it in that status.
func (v *workerView) remoteStatusSince(id, status string, lastSeen time.Time) string {
	if status == "offline" && !lastSeen.IsZero() {
		return lastSeen.Add(RemoteOfflineAfter).UTC().Format(time.RFC3339)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.remoteSince == nil {
		v.remoteSince = map[string]statusAt{}
	}
	at, ok := v.remoteSince[id]
	if !ok || at.status != status {
		at = statusAt{status: status, since: time.Now()}
		v.remoteSince[id] = at
	}
	return at.since.UTC().Format(time.RFC3339)
}

// StatusCheckEvery is the least time between two checks of every worker's status.
var StatusCheckEvery = 2 * time.Second

// checkStatuses records worker.updated {workerId, status} for each worker whose status changed since
// the last check (offline, back, switched on or off, paired, removed: "removed"), and nothing when none
// did (V7d). The first check only takes note. now forces a check within StatusCheckEvery.
func (v *workerView) checkStatuses(ctx context.Context, now bool) {
	v.mu.Lock()
	if !now && time.Since(v.checked) < StatusCheckEvery {
		v.mu.Unlock()
		return
	}
	v.checked = time.Now()
	v.mu.Unlock()
	cur := map[string]string{}
	if l, err := v.local(ctx); err == nil {
		cur[jobs.LocalWorker] = l["status"].(string)
	}
	remotes, err := v.store.RemoteWorkers()
	if err != nil {
		return
	}
	working, err := v.store.WorkingJobs()
	if err != nil {
		return
	}
	for _, rw := range remotes {
		if r, err := v.remoteWith(rw, working); err == nil {
			cur[rw.ID] = r["status"].(string)
			if h, ok := r["holdingBack"].(map[string]string); ok { // its reason starting, ending or changing code counts too (ui-shop 16:12Z), not its figures
				cur[rw.ID] += "\x00" + h["code"]
			}
		}
	}
	v.mu.Lock()
	prev, first := v.seen, v.seen == nil
	v.seen = cur
	v.mu.Unlock()
	if first {
		return
	}
	emit := func(id, status string) {
		status, _, _ = strings.Cut(status, "\x00") // the status alone; the reason's code only told a change
		v.store.RecordEvent("worker.updated", map[string]any{"type": "worker.updated", "workerId": id, "status": status})
	}
	for id, st := range cur {
		if prev[id] != st {
			emit(id, st)
		}
	}
	for id := range prev {
		if _, ok := cur[id]; !ok {
			emit(id, "removed")
		}
	}
}

// offlineCount is how many workers are offline now (the summary's badge).
func (v *workerView) offlineCount() int {
	remotes, err := v.store.RemoteWorkers()
	if err != nil {
		return 0
	}
	n := 0
	for _, rw := range remotes {
		if seen, _ := time.Parse(time.RFC3339, rw.LastSeen); seen.IsZero() || time.Since(seen) > RemoteOfflineAfter {
			n++
		}
	}
	return n
}
