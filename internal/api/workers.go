// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	encode "github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/hw"
	"github.com/PeiceOfToast/shrink-your-shit/internal/workers"
)

// The Workers page (page 10): the list, and the built-in worker's settings. Stage 3 has one worker,
// "local"; the store holds its settings, the queue says what it is doing, and the hardware detector
// says which card it has.

// workerJSON is one row of the Workers table, as types.ts shapes Worker. The live parts (state, now,
// doneToday) come from the queue; the settings from the workers store.
type workerJSON struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Address        string               `json:"address"`
	Local          bool                 `json:"local"`
	State          string               `json:"state"`
	Since          *string              `json:"since"`
	Slots          int                  `json:"slots"`
	GPUSlots       int                  `json:"gpuSlots"`
	GPU            *workerGPU           `json:"gpu"`
	Now            *workerNow           `json:"now"`
	DoneToday      int                  `json:"doneToday"`
	PausedGlobally bool                 `json:"pausedGlobally"`
	Test           *workerTestStateJSON `json:"test"`
}

// workerTestStateJSON is where the latest encoder test the person asked for stands (page 10): its id, whether it is
// still going, and when it finished. nil before any run.
type workerTestStateJSON struct {
	RunID      string  `json:"runId"`
	Running    bool    `json:"running"`
	FinishedAt *string `json:"finishedAt"`
}

// workerGPU is the card's friendly name and the family its HEVC encoder belongs to (page 10's
// gpu.family: qsv, nvenc, vaapi or amf). nil means the worker converts on the processor only.
type workerGPU struct {
	Name   string `json:"name"`
	Family string `json:"family"`
}

// workerNow is the running job's file and latest step, as the Jobs list shows them.
type workerNow struct {
	JobID string       `json:"jobId"`
	File  string       `json:"file"`
	Step  *stepRefJSON `json:"step"`
}

// workerEncoderJSON is one encoder the worker has; only the ones whose test passed are offered.
type workerEncoderJSON struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	TestOk bool   `json:"testOk"`
}

// hoursJSON is the worker's hours in the server's local time. From later than To runs overnight.
type hoursJSON struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// workerDetailJSON is the Worker page, as types.ts shapes WorkerDetail. This computer's worker takes
// every library, so its libraries and newLibraries are always empty.
type workerDetailJSON struct {
	workerJSON
	Hours          *hoursJSON          `json:"hours"`
	PrimaryEncoder *string             `json:"primaryEncoder"`
	Encoders       []workerEncoderJSON `json:"encoders"`
	Libraries      []struct{}          `json:"libraries"`
	NewLibraries   []struct{}          `json:"newLibraries"`
}

// workerTestJSON is POST /workers/{id}/capabilities/test's answer (page 10): the run's id. What the run finds
// arrives as probe events on /api/v2/events.
type workerTestJSON struct {
	RunID string `json:"runId"`
}

// workerStore is the workers' settings: the one the program wired, or a store over the open database.
func (s *Server) workerStore(rs *Deps) *workers.Store {
	if rs.Workers != nil {
		return rs.Workers
	}
	return workers.Open(rs.DB)
}

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	ctx := r.Context()
	list, err := s.workerStore(rs).List(ctx)
	if err != nil {
		internalError(w)
		return
	}
	out := make([]workerJSON, 0, len(list))
	for _, wk := range list {
		row, err := s.workerRow(ctx, rs, wk)
		if err != nil {
			internalError(w)
			return
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getWorker(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	wk, ok := s.findWorker(w, r, rs)
	if !ok {
		return
	}
	detail, err := s.workerDetail(r.Context(), rs, wk)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// findWorker reads the {id} worker, or writes page 10's not-found answer.
func (s *Server) findWorker(w http.ResponseWriter, r *http.Request, rs *Deps) (workers.Worker, bool) {
	wk, err := s.workerStore(rs).Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, workers.ErrNotFound) {
		writeError(w, http.StatusNotFound, "worker_not_found", "That worker doesn't exist.", "", 0)
		return workers.Worker{}, false
	}
	if err != nil {
		internalError(w)
		return workers.Worker{}, false
	}
	return wk, true
}

// testWorkerCapabilities re-runs the encoder pass for this computer's worker (page 10's Test the encoders). The
// pass runs in the background and the page watches the probe events; one run at a time, so a click while one is
// going gets that run's id rather than a second pass. Only this computer's encoders can be tested here.
func (s *Server) testWorkerCapabilities(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	wk, ok := s.findWorker(w, r, rs)
	if !ok {
		return
	}
	if !wk.Local {
		writeError(w, http.StatusConflict, "worker_offline",
			"This worker isn't connected to this computer, so its encoders can't be tested from here.", "", 0)
		return
	}
	if rs.Cards == nil {
		writeError(w, http.StatusConflict, "encoder_test_unavailable", "This computer can't test its encoders.", "", 0)
		return
	}
	id := rs.Cards.TestRun(wk.ID)
	if id == "" {
		writeError(w, http.StatusConflict, "worker_busy",
			"The encoders are already being tested. The result is in the System log when it ends.", "", 0)
		return
	}
	writeJSON(w, http.StatusOK, workerTestJSON{RunID: id})
}

// workerRow is one list row: the settings, and the live state from the queue.
func (s *Server) workerRow(ctx context.Context, rs *Deps, wk workers.Worker) (workerJSON, error) {
	row := workerJSON{
		ID: wk.ID, Name: wk.Name, Address: wk.Address, Local: wk.Local,
		State: "idle", Slots: wk.Slots, GPUSlots: wk.GPUSlots,
	}
	gpu, _ := s.workerCard(rs, wk)
	row.GPU = gpu
	row.Test = workerTest(rs, wk)
	running, err := rs.Jobs.RunningFor(ctx, wk.ID)
	if err != nil {
		return workerJSON{}, err
	}
	if running != nil {
		row.State = "converting"
		row.Now = &workerNow{
			JobID: strconv.FormatInt(running.ID, 10),
			File:  filepath.Base(running.Path),
			Step:  latestStepOf(ctx, rs.Events, rs.Workflows, *running),
		}
	}
	done, err := rs.Jobs.DoneSince(ctx, wk.ID, startOfDay(rs.now()))
	if err != nil {
		return workerJSON{}, err
	}
	row.DoneToday = done
	return row, nil
}

// workerTest is where this computer's latest encoder test stands, or nil before the first run. A remote worker's
// encoders are not tested here, so its item never carries one.
func workerTest(rs *Deps, wk workers.Worker) *workerTestStateJSON {
	if !wk.Local || rs.Cards == nil {
		return nil
	}
	st, ok := rs.Cards.Test()
	if !ok {
		return nil
	}
	out := &workerTestStateJSON{RunID: st.RunID, Running: st.Running}
	if !st.FinishedAt.IsZero() {
		at := st.FinishedAt.UTC().Format(time.RFC3339)
		out.FinishedAt = &at
	}
	return out
}

// workerDetail is the Worker page's answer: the list row and the settings it can change.
func (s *Server) workerDetail(ctx context.Context, rs *Deps, wk workers.Worker) (workerDetailJSON, error) {
	row, err := s.workerRow(ctx, rs, wk)
	if err != nil {
		return workerDetailJSON{}, err
	}
	d := workerDetailJSON{workerJSON: row, Encoders: []workerEncoderJSON{}, Libraries: []struct{}{}, NewLibraries: []struct{}{}}
	if wk.Hours != nil {
		d.Hours = &hoursJSON{From: wk.Hours.From, To: wk.Hours.To}
	}
	if wk.PrimaryEncoder != "" {
		enc := wk.PrimaryEncoder
		d.PrimaryEncoder = &enc
	}
	_, d.Encoders = s.workerCard(rs, wk)
	return d, nil
}

// workerCard is the card this worker encodes on and every card encoder it has, from the detector's
// last pass. With no pass yet (or no card) both are empty, and the worker converts on the processor.
// The card is the one its chosen encoder names, and otherwise the HEVC encoder automatic picks.
func (s *Server) workerCard(rs *Deps, wk workers.Worker) (*workerGPU, []workerEncoderJSON) {
	out := []workerEncoderJSON{}
	if rs.Cards == nil {
		return nil, out
	}
	ans, ok := rs.Cards.Current()
	if !ok {
		return nil, out
	}
	for _, v := range ans.Inventory.Verdicts {
		out = append(out, workerEncoderJSON{
			Name: v.Encoder, Label: codecLabel(v.Codec) + " · " + hw.ProductName(v.Family), TestOk: v.Available,
		})
	}
	card := func(encoder string, family encode.Family) *workerGPU {
		if encoder == "" {
			return nil
		}
		return &workerGPU{Name: hw.ProductName(family) + " (" + encoder + ")", Family: familyName(family)}
	}
	// The chosen encoder, when it is one the card runs: the worker encodes on that card.
	if wk.PrimaryEncoder != "" && wk.PrimaryEncoder != "cpu" {
		for _, v := range ans.Inventory.Verdicts {
			if v.Encoder == wk.PrimaryEncoder && v.Available {
				return card(v.Encoder, v.Family), out
			}
		}
		return nil, out
	}
	// The processor is chosen, so no card is used; automatic takes the card's HEVC encoder.
	if wk.PrimaryEncoder == "cpu" {
		return nil, out
	}
	for _, d := range ans.Devices {
		if d.Codec == "hevc" {
			return card(d.Encoder, d.Family), out
		}
	}
	for _, v := range ans.Inventory.Verdicts {
		if v.Available && v.Codec == "hevc" {
			return card(v.Encoder, v.Family), out
		}
	}
	return nil, out
}

// familyName is page 10's gpu.family: the encoder name's family part (hevc_qsv gives qsv).
func familyName(f encode.Family) string {
	switch f {
	case encode.FamilyQSV, encode.FamilyNVENC, encode.FamilyVAAPI, encode.FamilyAMF:
		return string(f)
	}
	return ""
}

// codecLabel is the codec as a person reads it.
func codecLabel(codec string) string {
	switch codec {
	case "hevc":
		return "HEVC"
	case "h264":
		return "H.264"
	case "av1":
		return "AV1"
	}
	return codec
}

// startOfDay is the moment today began in the server's local time, which is where "Done today" starts.
func startOfDay(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

// workerPatchJSON is PATCH /workers/{id}: the settings a change may carry. A missing key leaves its
// setting alone; the two nullable ones tell a missing key from a sent null.
type workerPatchJSON struct {
	Slots          *int            `json:"slots"`
	GPUSlots       *int            `json:"gpuSlots"`
	Hours          json.RawMessage `json:"hours"`
	PrimaryEncoder json.RawMessage `json:"primaryEncoder"`
	Libraries      json.RawMessage `json:"libraries"`
}

func (s *Server) patchWorker(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	ctx := r.Context()
	wk, ok := s.findWorker(w, r, rs)
	if !ok {
		return
	}
	var p workerPatchJSON
	if !decode(w, r, &p) {
		return
	}
	// This computer's worker takes every library, at the same paths (page 10): a list is refused
	// rather than stored and ignored.
	if p.Libraries != nil {
		writeError(w, http.StatusConflict, "local_worker", "This computer takes every library, at the same paths.", "", 0)
		return
	}
	if p.Slots != nil {
		if *p.Slots < 1 {
			writeError(w, http.StatusBadRequest, "invalid_setting", "Files at once is at least 1.", "slots", 0)
			return
		}
		wk.Slots = *p.Slots
	}
	if p.GPUSlots != nil {
		if *p.GPUSlots < 0 {
			writeError(w, http.StatusBadRequest, "invalid_setting", "Graphics slots is 0 or more.", "gpuSlots", 0)
			return
		}
		wk.GPUSlots = *p.GPUSlots
	}
	if p.Hours != nil {
		if string(p.Hours) == "null" {
			wk.Hours = nil
		} else {
			var h hoursJSON
			if err := json.Unmarshal(p.Hours, &h); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_setting", "Pick two different times. From later than To runs overnight.", "hours", 0)
				return
			}
			wk.Hours = &workers.Hours{From: h.From, To: h.To}
		}
	}
	if p.PrimaryEncoder != nil {
		enc, ok := s.pickEncoder(w, rs, wk, p.PrimaryEncoder)
		if !ok {
			return
		}
		wk.PrimaryEncoder = enc
	}
	if err := s.workerStore(rs).Save(ctx, wk); err != nil {
		var invalid *workers.InvalidError
		if errors.As(err, &invalid) {
			writeError(w, http.StatusBadRequest, "invalid_setting", invalid.Message, invalid.Field, 0)
			return
		}
		internalError(w)
		return
	}
	// The hours or the files at once may have opened work the worker was holding: let every idle
	// slot look now rather than at its next tick.
	if rs.Wake != nil {
		rs.Wake()
	}
	detail, err := s.workerDetail(ctx, rs, wk)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// pickEncoder reads a wanted primaryEncoder: null is automatic, "cpu" is the processor, and a name
// must be an encoder the card passed its test on (page 10's invalid_encoder and encoder_not_working).
func (s *Server) pickEncoder(w http.ResponseWriter, rs *Deps, wk workers.Worker, raw json.RawMessage) (string, bool) {
	if string(raw) == "null" {
		return "", true
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_encoder", "This worker has no such encoder.", "primaryEncoder", 0)
		return "", false
	}
	if name == "cpu" {
		return "cpu", true
	}
	_, encoders := s.workerCard(rs, wk)
	for _, e := range encoders {
		if e.Name != name {
			continue
		}
		if !e.TestOk {
			writeError(w, http.StatusConflict, "encoder_not_working", "That encoder failed its test. Test the encoders, then pick it again.", "primaryEncoder", 0)
			return "", false
		}
		return name, true
	}
	writeError(w, http.StatusBadRequest, "invalid_encoder", "This worker has no such encoder.", "primaryEncoder", 0)
	return "", false
}
