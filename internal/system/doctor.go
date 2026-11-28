// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package system

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

// Status is one health check's answer (page 11): ok, a warning, a problem, or a thing that is not there.
type Status string

const (
	OK     Status = "ok"
	Warn   Status = "warn"
	Bad    Status = "bad"
	Absent Status = "absent"
)

// Fix is the one call that clears a check, for the checks a single POST can re-test or repair.
type Fix struct {
	Label  string `json:"label"`
	Method string `json:"method"`
	Path   string `json:"path"`
}

// Check is one of the app's checks on itself and what it said the last time it ran.
type Check struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Status Status  `json:"status"`
	Detail string  `json:"detail"`
	Hint   *string `json:"hint"`
	Fix    *Fix    `json:"fix"`
}

// Result is one run of every check: when it ran and what each one said.
type Result struct {
	RanAt  time.Time `json:"ranAt"`
	Checks []Check   `json:"checks"`
}

// Library is one library folder the doctor looks at.
type Library struct {
	ID   int64
	Name string
	Path string
}

// Sources are what the checks read. A nil source makes its check absent, so a check with nothing behind it is
// listed without claiming the machine is fine.
type Sources struct {
	// Now is the clock the run's time comes from; nil is time.Now.
	Now func() time.Time
	// DataDir is the app's data folder: it is written and its free space is read.
	DataDir string
	// DiskFree reads the free bytes of the volume that holds a folder.
	DiskFree func(path string) (uint64, error)
	// LowSpaceGB is page 11's processing.lowSpaceGB: below it new jobs wait. nil is LowSpaceDefault.
	LowSpaceGB func() int
	// Tools answers the versions of the ffmpeg and ffprobe the app will run, or why one cannot be used.
	Tools func(ctx context.Context) (ffmpeg, ffprobe string, err error)
	// Libraries lists the library folders to check. nil checks no library.
	Libraries func(ctx context.Context) ([]Library, error)
	// Cards is the encoder detector: the card families that passed its tests, whether it found a card at all, and
	// whether it has finished a pass.
	Cards func() (ready []string, found, tested bool)
	// Emit writes a status change to the system log; nil says nothing.
	Emit Emitter
}

// Emitter is the event bus as the doctor uses it.
type Emitter interface {
	Emit(events.Event) error
}

// LowSpaceDefault is page 11's low-space limit when nothing is configured.
const LowSpaceDefault = 10

// Every is how often the checks run after the start's first run. A variable so a test can shorten it.
var Every = 5 * time.Minute

// The fix for the graphics card check: page 10's Test the encoders, which starts the pass whose result the check
// reports. Its POST answers the run's id, not a doctor: a page asks GET /doctor once the run has finished.
var testEncoders = &Fix{Label: "Test the encoders", Method: "POST", Path: "/api/v2/workers/local/capabilities/test"}

// Doctor is the app's health check (page 11): the checks it runs on itself and their last answer.
type Doctor struct {
	src  Sources
	mu   sync.Mutex
	last Result
	ran  bool
}

// NewDoctor makes the health check. now nil is time.Now.
func NewDoctor(src Sources) *Doctor {
	if src.Now == nil {
		src.Now = time.Now
	}
	return &Doctor{src: src}
}

// Start runs the checks now, then every Every until ctx ends.
func (d *Doctor) Start(ctx context.Context) {
	d.Run(ctx)
	go func() {
		t := time.NewTicker(Every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				d.Run(ctx)
			}
		}
	}()
}

// Report is GET /doctor: the last run, or a run now when retest is asked for or nothing has run yet.
func (d *Doctor) Report(ctx context.Context, retest bool) Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ran && !retest {
		return d.last
	}
	return d.run(ctx)
}

// Run checks the app now and keeps the answer as the last run.
func (d *Doctor) Run(ctx context.Context) Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.run(ctx)
}

// run is one pass of every check; the caller holds d.mu. A check whose status changed since the last run is said
// once in the system log (source health), and only a change: the log records what happened, not every run.
func (d *Doctor) run(ctx context.Context) Result {
	res := Result{RanAt: d.src.Now().UTC(), Checks: d.checks(ctx)}
	d.say(d.last, res, d.ran)
	d.last, d.ran = res, true
	return res
}

// checks runs every check that has a source, in the page's order.
func (d *Doctor) checks(ctx context.Context) []Check {
	out := []Check{d.disk(), d.tools(ctx), d.cards()}
	if d.src.Libraries != nil {
		out = append(out, d.libraries(ctx)...)
	}
	return out
}

// disk checks that the data folder can be written and that its disk has room for another file.
func (d *Doctor) disk() Check {
	c := Check{ID: "disk", Label: "Disk space", Status: Absent, Detail: "The disk check cannot run on this server."}
	if d.src.DataDir == "" || d.src.DiskFree == nil {
		return c
	}
	f, err := os.CreateTemp(d.src.DataDir, ".health-*")
	if err != nil {
		return badCheck(c, "The data folder can't be written: "+err.Error()+".", "Check the data folder's disk and its permissions.")
	}
	f.Close()
	os.Remove(f.Name())
	free, err := d.src.DiskFree(d.src.DataDir)
	if err != nil {
		return badCheck(c, "The free space couldn't be read: "+err.Error()+".", "Check the data folder's disk.")
	}
	low := LowSpaceDefault
	if d.src.LowSpaceGB != nil {
		low = d.src.LowSpaceGB()
	}
	if free < uint64(low)<<30 {
		return badCheck(c, "Only "+gb(free)+" free on the data disk; new jobs wait until there is "+fmt.Sprint(low)+" GB.",
			"Free some space, or lower the limit in Processing.")
	}
	c.Status, c.Detail = OK, gb(free)+" free on the data disk."
	return c
}

// tools checks that the ffmpeg and ffprobe the app would run are both there, and says their versions.
func (d *Doctor) tools(ctx context.Context) Check {
	c := Check{ID: "tools", Label: "Tools found", Status: Absent, Detail: "The tools check cannot run on this server."}
	if d.src.Tools == nil {
		return c
	}
	ffmpeg, ffprobe, err := d.src.Tools(ctx)
	if err != nil {
		return badCheck(c, "The tools check failed: "+err.Error()+".", "Install ffmpeg and ffprobe, then run the check again.")
	}
	c.Status, c.Detail = OK, "ffmpeg "+ffmpeg+" and ffprobe "+ffprobe+" are installed."
	return c
}

// cards reports what the encoder detector found: a tested card that passed, one that did not, none at all, or a
// test still to come. The fix is page 10's Test the encoders, which is the one call that changes this answer.
func (d *Doctor) cards() Check {
	c := Check{ID: "gpu", Label: "Graphics card", Status: Absent, Detail: "The graphics card check cannot run on this server."}
	if d.src.Cards == nil {
		return c
	}
	ready, found, tested := d.src.Cards()
	c.Fix = testEncoders
	switch {
	case !tested:
		c.Status, c.Detail, c.Hint = Warn, "The graphics card has not been tested yet.", hint("Files convert on the processor until the test ends.")
	case len(ready) > 0:
		verb := " is ready."
		if len(ready) > 1 {
			verb = " are ready."
		}
		c.Status, c.Detail = OK, strings.Join(ready, ", ")+verb
	case found:
		c.Status, c.Detail, c.Hint = Bad, "The graphics card did not pass the encoder test.", hint("Files convert on the processor until it does.")
	default:
		c.Status, c.Detail, c.Hint = Absent, "No graphics card found.", hint("Files convert on the processor.")
	}
	return c
}

// libraries checks each library folder: it is there and it can be written. The folders are the person's media, so
// the check writes one small file and removes it at once.
func (d *Doctor) libraries(ctx context.Context) []Check {
	libs, err := d.src.Libraries(ctx)
	if err != nil {
		return []Check{badCheck(Check{ID: "libraries", Label: "Libraries"},
			"The libraries couldn't be read: "+err.Error()+".", "Check the database file and its permissions.")}
	}
	out := make([]Check, 0, len(libs))
	for _, lib := range libs {
		out = append(out, libraryCheck(lib))
	}
	return out
}

// libraryCheck is one library folder's answer.
func libraryCheck(lib Library) Check {
	c := Check{ID: fmt.Sprintf("library-%d", lib.ID), Label: "Library " + lib.Name}
	fi, err := os.Stat(lib.Path)
	if err != nil {
		return badCheck(c, "The folder "+lib.Path+" can't be reached: "+err.Error()+".",
			"Check the drive is connected, or change the folder on the Libraries page.")
	}
	if !fi.IsDir() {
		return badCheck(c, "The folder "+lib.Path+" is not a folder.",
			"Change the folder on the Libraries page.")
	}
	f, err := os.CreateTemp(lib.Path, ".health-*")
	if err != nil {
		return badCheck(c, "The folder "+lib.Path+" can't be written: "+err.Error()+".",
			"Check the folder's permissions, or change it on the Libraries page.")
	}
	f.Close()
	os.Remove(f.Name())
	c.Status, c.Detail = OK, "The folder "+lib.Path+" is there and can be written."
	return c
}

// say writes one event per check whose status changed since the last run. The first run says only what is worth a
// look: a working check, or one that is simply not there, needs no line at start.
func (d *Doctor) say(was Result, now Result, ran bool) {
	if d.src.Emit == nil {
		return
	}
	prev := map[string]Status{}
	if ran {
		for _, c := range was.Checks {
			prev[c.ID] = c.Status
		}
	}
	for _, c := range now.Checks {
		old, had := prev[c.ID]
		if had && old == c.Status {
			continue
		}
		if !had && (c.Status == OK || c.Status == Absent) {
			continue
		}
		ev := events.Event{Source: "health", Kind: events.KindNote, Level: events.LevelInfo,
			Message: c.Label + ": " + c.Detail}
		if c.Status == Bad {
			// The declared notifiable event (page 11): a check went from fine to a problem.
			ev.Kind, ev.Level, ev.Code = events.KindWarning, events.LevelWarn, "health.failed"
		}
		if had && c.Status == OK {
			ev.Message = c.Label + " is fine again."
		}
		_ = d.src.Emit.Emit(ev)
	}
}

// badCheck is a check with a problem and what the person can do about it.
func badCheck(c Check, detail, hintText string) Check {
	c.Status, c.Detail, c.Hint = Bad, detail, hint(hintText)
	return c
}

// hint is a sentence-level pointer for one of the struct's nullable strings.
func hint(s string) *string { return &s }

// gb is a byte count as whole gigabytes, or one decimal below a gigabyte.
func gb(b uint64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%d GB", b>>30)
	}
	return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
}
