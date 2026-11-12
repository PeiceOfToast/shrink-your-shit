// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// Converter is what this worker converts with.
type Converter struct {
	Exec            media.Exec
	FFmpeg, FFprobe string
	Work            string // its folder for downloads and new files
	// MinFree is the free space this worker keeps for itself: it takes no lease below it (orch-lead
	// 13:28Z Q4), and gives back a lease whose file wouldn't fit.
	MinFree uint64
	// FreeSpace says a folder's free bytes (worker.DiskFree; tests replace it).
	FreeSpace func(string) (uint64, error)
}

// Poll is how long a worker with nothing to do waits before it asks again.
var Poll = 5 * time.Second

// PlanPoll is how often it asks whether a leased job's plan is ready.
var PlanPoll = 2 * time.Second

func (c *Client) request(ctx context.Context, method, path string, body io.Reader, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.Server, "/")+"/api/worker/v1/"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-SYP-Worker", fmt.Sprintf("%s; api=%d", c.Version, APILevel))
	req.Header.Set("Authorization", "Bearer "+c.Token)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	h := c.HTTP
	if h == nil {
		h = &http.Client{}
	}
	return h.Do(req)
}

func apiErr(res *http.Response) error {
	var e struct {
		Error struct{ Code, Message string }
	}
	json.NewDecoder(res.Body).Decode(&e)
	if e.Error.Message == "" {
		e.Error.Message = res.Status
	}
	return &APIError{Status: res.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
}

// Lease is a job this worker leased.
type Lease struct {
	ID  string
	Job struct {
		ID          string
		Name        string
		Bytes       int64
		DurationSec float64
	}
}

// Lease asks for one job; nil when there is none.
func (c *Client) Lease(ctx context.Context) (*Lease, error) {
	var out struct{ Lease *Lease }
	if err := c.call(ctx, "lease", map[string]any{"slots": 1}, &out); err != nil {
		return nil, err
	}
	return out.Lease, nil
}

// leaseGone: the lease ended (the job went back to the queue): drop the work and ask for new work.
func leaseGone(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == "lease_gone"
}

// Work leases and converts jobs until ctx ends, one at a time (the heartbeats run beside it, in Run).
func (c *Client) Work(ctx context.Context, cv Converter) {
	for ctx.Err() == nil {
		did, err := c.workOne(ctx, cv)
		var ae *APIError
		if errors.As(err, &ae) && (ae.Code == "worker_token" || ae.Code == "update_worker" || ae.Code == "https_required") {
			return // Run says why
		}
		if err != nil && ctx.Err() == nil && !leaseGone(err) {
			log.Printf("work: %v", err)
		}
		if !did || err != nil {
			select {
			case <-ctx.Done():
			case <-time.After(Poll):
			}
		}
	}
}

func (cv Converter) free() uint64 {
	f := cv.FreeSpace
	if f == nil {
		f = worker.DiskFree
	}
	n, err := f(cv.Work)
	if err != nil {
		return 0
	}
	return n
}

// workOne leases one job and does it; did is false when there was nothing to do.
func (c *Client) workOne(ctx context.Context, cv Converter) (did bool, err error) {
	if err := os.MkdirAll(cv.Work, 0o700); err != nil {
		return false, err
	}
	if free := cv.free(); free < cv.MinFree { // no room: no lease, and it says why (#174 QA e)
		c.holding(fmt.Sprintf("Not taking work: %s free on %s, below the %s it keeps free (--min-free-gb).", gb(free), cv.Work, gb(cv.MinFree)))
		return false, nil
	}
	c.holding("")
	l, err := c.Lease(ctx)
	if err != nil || l == nil {
		return false, err
	}
	dir := filepath.Join(cv.Work, filepath.Base(l.ID))
	defer os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return true, err
	}
	if cv.free() < uint64(l.Job.Bytes)+worker.SpaceNeeded(l.Job.Bytes)+cv.MinFree { // its copy and the new file
		log.Printf("%s doesn't fit in %s: given back", l.Job.Name, cv.Work)
		return true, c.call(ctx, "leases/"+l.ID+"/release", map[string]any{}, nil)
	}
	log.Printf("job %s: %s", l.Job.ID, l.Job.Name)
	pl, err := c.plan(ctx, l)
	if err != nil {
		return true, err
	}
	snap, subs := pl.Snapshot, pl.Subtitles
	src := filepath.Join(dir, "source"+filepath.Ext(l.Job.Name))
	if pl.SourceOnWorker != "" { // V7c: read it through the shared folder, not downloaded
		if _, err := os.Stat(pl.SourceOnWorker); err == nil {
			src = pl.SourceOnWorker
		} else {
			c.note(ctx, l, "Reading the original through the shared folder didn't work, so it was downloaded instead: "+err.Error())
		}
	}
	if src != pl.SourceOnWorker {
		if err := c.download(ctx, l, src); err != nil {
			return true, err
		}
	}
	ext := filepath.Ext(l.Job.Name)
	if snap.Plan.Container != "" {
		ext = "." + snap.Plan.Container
	}
	out := filepath.Join(dir, "new"+ext)
	stderr, err := c.convert(ctx, cv, l, snap, src, out)
	if err != nil {
		if leaseGone(err) || ctx.Err() != nil {
			return true, err
		}
		return true, c.report(ctx, l, map[string]any{"ok": false, "message": err.Error(), "stderr": stderr})
	}
	if err := worker.CheckResult(ctx, cv.Exec, cv.FFmpeg, cv.FFprobe, snap, src, out); err != nil {
		var k *worker.KeptError
		if errors.As(err, &k) {
			return true, c.report(ctx, l, map[string]any{"kept": k.Reason, "stderr": stderr})
		}
		return true, c.report(ctx, l, map[string]any{"ok": false, "message": "the checks didn't finish: " + err.Error(), "stderr": stderr})
	}
	var sum string
	var size int64
	if pl.StageOnWorker != "" { // V7c: write it into the run's staged file through the shared folder
		if sum, size, err = copyInto(out, pl.StageOnWorker); err != nil {
			c.note(ctx, l, "Writing the new file through the shared folder didn't work, so it was uploaded instead: "+err.Error())
		}
	}
	if pl.StageOnWorker == "" || err != nil {
		if sum, size, err = c.upload(ctx, l, out, "video", 0); err != nil {
			return true, err
		}
	}
	subSums := map[string]string{}
	for k := 0; k < subs; k++ {
		s, _, err := c.upload(ctx, l, rules.StagedSrt(out, k), "sub", k)
		if err != nil {
			return true, err
		}
		subSums[strconv.Itoa(k)] = s
	}
	log.Printf("job %s: converted and checked; the server puts it in place", l.Job.ID)
	return true, c.report(ctx, l, map[string]any{"ok": true, "sha256": sum, "bytes": size, "subs": subSums, "stderr": stderr})
}

// Plan is a leased job's plan, as the server made it.
type Plan struct {
	Snapshot                      rules.Snapshot
	Subtitles                     int
	SourceOnWorker, StageOnWorker string // V7c: through a shared folder; "" = transfer
}

// plan waits for the server to make the job's plan (asking renews the lease).
func (c *Client) plan(ctx context.Context, l *Lease) (Plan, error) {
	for {
		res, err := c.request(ctx, http.MethodGet, "leases/"+l.ID+"/plan", nil, nil)
		if err != nil {
			return Plan{}, err
		}
		switch res.StatusCode {
		case http.StatusOK:
			var out Plan
			err := json.NewDecoder(res.Body).Decode(&out)
			res.Body.Close()
			return out, err
		case http.StatusAccepted:
			res.Body.Close()
		default:
			err := apiErr(res)
			res.Body.Close()
			return Plan{}, err
		}
		select {
		case <-ctx.Done():
			return Plan{}, ctx.Err()
		case <-time.After(PlanPoll):
		}
	}
}

// note adds a line to the job's run log on the server.
func (c *Client) note(ctx context.Context, l *Lease, text string) {
	log.Printf("job %s: %s", l.Job.ID, text)
	c.call(ctx, "leases/"+l.ID+"/note", map[string]any{"text": text}, nil)
}

// copyInto writes the file at from into the existing file at to (the run's staged file, through a
// shared folder: never a new file, so the server's identity check holds), and returns its SHA-256 and
// size.
func copyInto(from, to string) (string, int64, error) {
	in, err := os.Open(from)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_TRUNC, 0) // no O_CREATE: only the staged file the run made
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// download fetches the original into to, going on from what is there after an interruption.
func (c *Client) download(ctx context.Context, l *Lease, to string) error {
	for try := 0; ; try++ {
		have := int64(0)
		if st, err := os.Stat(to); err == nil {
			have = st.Size()
		}
		hdr := map[string]string{}
		if have > 0 {
			hdr["Range"] = "bytes=" + strconv.FormatInt(have, 10) + "-"
		}
		res, err := c.request(ctx, http.MethodGet, "leases/"+l.ID+"/source", nil, hdr)
		if err == nil && res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent {
			if res.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0 { // it is all here
				res.Body.Close()
				return nil
			}
			err = apiErr(res)
			res.Body.Close()
			return err
		}
		if err == nil {
			flag := os.O_WRONLY | os.O_CREATE | os.O_APPEND
			if res.StatusCode == http.StatusOK { // the whole file again
				flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			}
			var f *os.File
			if f, err = os.OpenFile(to, flag, 0o600); err == nil {
				_, err = io.Copy(f, res.Body)
				if cerr := f.Close(); err == nil {
					err = cerr
				}
			}
			res.Body.Close()
			if err == nil {
				return nil
			}
		}
		if ctx.Err() != nil || try >= 5 {
			return fmt.Errorf("downloading the original: %w", err)
		}
		time.Sleep(time.Second)
	}
}

// convert runs the plan's ffmpeg, reporting progress (which renews the lease).
func (c *Client) convert(ctx context.Context, cv Converter, l *Lease, snap rules.Snapshot, src, out string) (string, error) {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var last time.Time
	var gone error
	stderr, err := media.Transcode(cctx, cv.Exec, cv.FFmpeg, snap.Plan.FFmpegArgs(src, out), l.Job.DurationSec, func(p media.Progress) {
		if l.Job.DurationSec <= 0 || time.Since(last) < 5*time.Second {
			return // no length: no percent to report, as before Transcode reported the position alone (#187)
		}
		last = time.Now()
		if e := c.call(cctx, "leases/"+l.ID+"/progress", map[string]any{"percent": p.Percent, "stage": "converting"}, nil); leaseGone(e) {
			gone = e
			cancel() // the job went back to the queue: stop converting it
		}
	})
	if gone != nil {
		return stderr, gone
	}
	return stderr, err
}

// upload sends the file at name as part (index), going on from what the server has; it returns the
// file's SHA-256 and size.
func (c *Client) upload(ctx context.Context, l *Lease, name, part string, index int) (string, int64, error) {
	q := "?part=" + part + "&index=" + strconv.Itoa(index)
	for try := 0; ; try++ {
		res, err := c.request(ctx, http.MethodHead, "leases/"+l.ID+"/result"+q, nil, nil)
		if err != nil {
			return "", 0, err
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return "", 0, &APIError{Status: res.StatusCode, Code: "lease_gone", Message: res.Status}
		}
		off, _ := strconv.ParseInt(res.Header.Get("Upload-Offset"), 10, 64)
		f, err := os.Open(name)
		if err != nil {
			return "", 0, err
		}
		st, _ := f.Stat()
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			f.Close()
			return "", 0, err
		}
		res, err = c.request(ctx, http.MethodPut, "leases/"+l.ID+"/result"+q, f, map[string]string{"Upload-Offset": strconv.FormatInt(off, 10),
			"Content-Type": "application/octet-stream"})
		f.Close()
		if err == nil && res.StatusCode == http.StatusOK {
			res.Body.Close()
			sum, err := fileSum(name)
			return sum, st.Size(), err
		}
		if err == nil {
			err = apiErr(res)
			res.Body.Close()
			if leaseGone(err) {
				return "", 0, err
			}
		}
		if ctx.Err() != nil || try >= 5 {
			return "", 0, fmt.Errorf("uploading the new file: %w", err)
		}
		time.Sleep(time.Second)
	}
}

func fileSum(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// report tells the server how it went (on an upload it doesn't take, the file goes once more).
func (c *Client) report(ctx context.Context, l *Lease, body map[string]any) error {
	b, _ := json.Marshal(body)
	res, err := c.request(ctx, http.MethodPost, "leases/"+l.ID+"/report", bytes.NewReader(b), map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return apiErr(res)
	}
	return nil
}

// gb is a size in GB, to one decimal.
func gb(n uint64) string { return strconv.FormatFloat(float64(n)/(1<<30), 'f', 1, 64) + " GB" }
