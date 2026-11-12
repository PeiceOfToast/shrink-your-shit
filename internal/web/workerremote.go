// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// V7b2, transfer mode (slices/v2-engine/v7-plan.md "V7b design"): the remote worker fetches its plan
// and the original, converts and checks, uploads its file (resumable), and reports. The server checks
// the upload's size and checksum on arrival, then its run of the job re-checks the file and commits it.

// remoteRuns are the server's runs of leased jobs, by lease.
type remoteRuns struct {
	mu      sync.Mutex
	byLease map[string]*worker.RemoteJob
}

func (rr *remoteRuns) add(rj *worker.RemoteJob) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.byLease[rj.Lease] = rj
}

// remove forgets a run that ended (its staged files went with it).
func (rr *remoteRuns) remove(lease string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	delete(rr.byLease, lease)
}

func (rr *remoteRuns) get(lease string) *worker.RemoteJob {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.byLease[lease]
}

// uploadName is where part ("video", or "sub" with its index) of the upload is written: into the run's
// staged files on the destination's disk (no copy at the commit; the stage checks it is that file).
func uploadName(p worker.RemotePlan, part string, index int) (string, bool) {
	switch part {
	case "video":
		return p.Stage, p.Stage != ""
	case "sub":
		if index >= 0 && index < len(p.Snapshot.Plan.SubtitleFiles()) {
			return rules.StagedSrt(p.Stage, index), true
		}
	}
	return "", false
}

func remoteRoutes(mux *http.ServeMux, store *jobs.Store, gate func(http.ResponseWriter, *http.Request, bool) (jobs.RemoteWorker, bool),
	leaseOf func(http.ResponseWriter, *http.Request) (jobs.RemoteWorker, jobs.Job, string, bool), runs *remoteRuns, eng Engine) {
	// run is the server's run of the request's lease (the lease held, and renewed: the worker is there)
	run := func(w http.ResponseWriter, r *http.Request) (*worker.RemoteJob, jobs.Job, bool) {
		wk, j, lease, ok := leaseOf(w, r)
		if !ok {
			return nil, j, false
		}
		r.Header.Set("X-SYP-Worker-Id", wk.ID) // for the handlers below (not from the worker: overwritten here)
		rj := runs.get(lease)
		if rj == nil {
			apiError(w, http.StatusConflict, "lease_gone", "This job was given back to the queue (its lease ended). Ask for new work.")
			return nil, j, false
		}
		store.RenewLease(j.ID, lease)
		return rj, j, true
	}
	mux.HandleFunc("GET /api/worker/v1/leases/{lease}/plan", func(w http.ResponseWriter, r *http.Request) {
		rj, _, ok := run(w, r)
		if !ok {
			return
		}
		p, ready := rj.Plan()
		if !ready {
			writeJSONStatus(w, http.StatusAccepted, map[string]any{"preparing": true})
			return
		}
		st, err := os.Stat(p.Source)
		if err != nil {
			serverError(w, err)
			return
		}
		wk := r.Header.Get("X-SYP-Worker-Id") // set by run (below)
		writeJSON(w, map[string]any{"snapshot": p.Snapshot, "source": map[string]any{"name": filepath.Base(p.Source), "bytes": st.Size()},
			"subtitles": len(p.Snapshot.Plan.SubtitleFiles()),
			// V7c: through a shared folder that passed its test: read the original there, write the checked
			// file into the run's staged file there (the same checks either way); "" = transfer
			"sourceOnWorker": usableMapping(store, wk, p.Source, false), "stageOnWorker": usableMapping(store, wk, p.Stage, true)})
	})
	mux.HandleFunc("GET /api/worker/v1/leases/{lease}/source", func(w http.ResponseWriter, r *http.Request) {
		rj, _, ok := run(w, r)
		if !ok {
			return
		}
		p, ready := rj.Plan()
		if !ready {
			apiError(w, http.StatusConflict, "not_ready", "The plan isn't ready yet.")
			return
		}
		f, err := os.Open(p.Source)
		if err != nil {
			serverError(w, err)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			serverError(w, err)
			return
		}
		http.ServeContent(w, r, filepath.Base(p.Source), st.ModTime(), f) // Range: a download goes on where it stopped
	})
	part := func(r *http.Request) (string, int) {
		i, err := strconv.Atoi(r.URL.Query().Get("index"))
		if err != nil {
			i = -1
		}
		p := r.URL.Query().Get("part")
		if p == "" {
			p = "video"
		}
		return p, i
	}
	// how much of a part arrived (a resumed upload starts there)
	mux.HandleFunc("HEAD /api/worker/v1/leases/{lease}/result", func(w http.ResponseWriter, r *http.Request) {
		rj, _, ok := run(w, r)
		if !ok {
			return
		}
		pl, ready := rj.Plan()
		p, i := part(r)
		name, ok := uploadName(pl, p, i)
		if !ready || !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var n int64
		if st, err := os.Stat(name); err == nil {
			n = st.Size()
		}
		w.Header().Set("Upload-Offset", strconv.FormatInt(n, 10))
		w.WriteHeader(http.StatusOK)
	})
	// a part's bytes, at the offset its Upload-Offset header names (0: from the start)
	mux.HandleFunc("PUT /api/worker/v1/leases/{lease}/result", func(w http.ResponseWriter, r *http.Request) {
		rj, _, ok := run(w, r)
		if !ok {
			return
		}
		pl, ready := rj.Plan()
		p, i := part(r)
		name, ok := uploadName(pl, p, i)
		if !ready || !ok {
			apiError(w, http.StatusBadRequest, "invalid", "Upload part video, or sub with an index this conversion makes.")
			return
		}
		off, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
		if err != nil {
			off = 0
		}
		have := int64(0)
		if st, err := os.Stat(name); err == nil {
			have = st.Size()
		}
		if off != have { // a resume must go on exactly where the file ends
			w.Header().Set("Upload-Offset", strconv.FormatInt(have, 10))
			apiError(w, http.StatusConflict, "offset_mismatch", fmt.Sprintf("This upload has %d bytes; go on from there.", have))
			return
		}
		flags := os.O_WRONLY | os.O_APPEND // into the very file the run staged (its identity is checked)
		if p == "sub" {
			flags |= os.O_CREATE
		}
		f, err := os.OpenFile(name, flags, 0o600)
		if err != nil {
			serverError(w, err)
			return
		}
		n, cerr := io.Copy(f, r.Body)
		if err := f.Close(); cerr == nil {
			cerr = err
		}
		w.Header().Set("Upload-Offset", strconv.FormatInt(have+n, 10))
		if cerr != nil { // what arrived stays: the worker goes on from Upload-Offset
			apiError(w, http.StatusServiceUnavailable, "upload_interrupted", "The upload stopped; go on from Upload-Offset.")
			return
		}
		writeJSON(w, map[string]any{"bytes": have + n})
	})
	// V7c: a line for the job's run log (a shared folder that didn't work, so the bytes were transferred)
	mux.HandleFunc("POST /api/worker/v1/leases/{lease}/note", func(w http.ResponseWriter, r *http.Request) {
		_, j, ok := run(w, r)
		if !ok {
			return
		}
		var in struct{ Text string }
		if !readJSON(w, r, &in) {
			return
		}
		text := cleanWorkerText(in.Text, NoteMaxChars) // review 15b1898: one line, no control characters, capped
		if text == "" {
			apiError(w, http.StatusBadRequest, "invalid", "A note needs some text.")
			return
		}
		if eng.RunLogs != nil {
			eng.RunLogs.Append(j.ID, "Worker: "+text) // prefixed: a worker's note can't pass for the server's line
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/worker/v1/leases/{lease}/report", func(w http.ResponseWriter, r *http.Request) {
		rj, j, ok := run(w, r)
		if !ok {
			return
		}
		var in struct {
			OK      bool
			SHA256  string
			Bytes   int64
			Subs    map[string]string // index -> sha256
			Kept    string            // its checks said no (the words)
			Message string            // it failed: why
			Stderr  string
		}
		if !readJSON(w, r, &in) {
			return
		}
		pl, _ := rj.Plan()
		res := worker.RemoteResult{Stderr: in.Stderr}
		switch {
		case in.Kept != "":
			res.Kept = in.Kept
		case !in.OK:
			msg := in.Message
			if msg == "" {
				msg = "the other computer couldn't convert it"
			}
			res.Err = errors.New(msg)
		default:
			video, _ := uploadName(pl, "video", 0)
			if err := sameUpload(video, in.Bytes, in.SHA256); err != nil {
				os.Truncate(video, 0) // send it again (the same staged file)
				apiError(w, http.StatusUnprocessableEntity, "upload_damaged", "The uploaded file isn't the one that was checked ("+err.Error()+"). Upload it again.")
				return
			}
			want := len(pl.Snapshot.Plan.SubtitleFiles())
			for k := 0; k < want; k++ {
				name, _ := uploadName(pl, "sub", k)
				if sameUpload(name, -1, in.Subs[strconv.Itoa(k)]) != nil {
					os.Remove(name)
					apiError(w, http.StatusUnprocessableEntity, "upload_damaged", "An uploaded subtitle isn't the one that was made. Upload it again.")
					return
				}
			}
		}
		if !rj.Deliver(res) {
			apiError(w, http.StatusConflict, "lease_gone", "This job already has an answer, or was given back to the queue.")
			return
		}
		writeJSONStatus(w, http.StatusAccepted, map[string]any{"accepted": true, "jobId": strconv.FormatInt(j.ID, 10)})
	})
}

// sameUpload checks the file at name has size bytes (unless -1) and this SHA-256 (hex).
func sameUpload(name string, size int64, sum string) error {
	f, err := os.Open(name)
	if err != nil {
		return errors.New("nothing arrived")
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if size >= 0 && n != size {
		return fmt.Errorf("%d bytes arrived, not %d", n, size)
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), sum) {
		return errors.New("its checksum differs")
	}
	return nil
}
