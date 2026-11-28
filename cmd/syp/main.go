// Command syp is the shrink-your-shit program.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/api"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	_ "github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	// The hardware detector's migration (7) is registered by this package's init; the detector
	// itself is wired below to choose a card for a job.
	"github.com/PeiceOfToast/shrink-your-shit/internal/hw"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
	"github.com/PeiceOfToast/shrink-your-shit/internal/settings"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
	"github.com/PeiceOfToast/shrink-your-shit/internal/system"
	"github.com/PeiceOfToast/shrink-your-shit/internal/ui"
	"github.com/PeiceOfToast/shrink-your-shit/internal/worker"
	"github.com/PeiceOfToast/shrink-your-shit/internal/workers"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println("syp", version)
		return
	}
	fs := flag.NewFlagSet("syp", flag.ContinueOnError)
	dataDir := fs.String("data", "", "the data folder (required)")
	listen := fs.String("listen", "127.0.0.1:8484", "the address to serve on")
	ffmpegDir := fs.String("ffmpeg", "", "the folder with ffmpeg and ffprobe (default: $SYP_FFMPEG_DIR, then PATH)")
	if err := fs.Parse(os.Args[1:]); err != nil || *dataDir == "" {
		fmt.Fprintln(os.Stderr, "usage: syp version | syp -data <dir> [-listen <addr>] [-ffmpeg <dir>]")
		os.Exit(2)
	}
	dir := *ffmpegDir
	if dir == "" {
		dir = os.Getenv("SYP_FFMPEG_DIR")
	}
	media.SetToolDir(dir)
	if err := run(*dataDir, *listen); err != nil {
		fmt.Fprintln(os.Stderr, "syp:", err)
		os.Exit(1)
	}
}

// run serves the api while the store opens, so a request during startup gets 503 starting. Once the store is
// ready, jobs left running by a previous stop are recovered, then the worker starts. It stops on Ctrl-C or
// SIGTERM: the worker finishes or requeues its job before the store closes.
func run(dataDir, listen string) error {
	started := time.Now()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := api.New(version)
	srv.SetUI(ui.Files()) // the built interface, embedded; a build without it serves the page that says so
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", listen, err)
	}
	httpSrv := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()

	db, err := store.Open(dataDir)
	if err != nil {
		httpSrv.Close()
		return err
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		httpSrv.Close()
		return err
	}
	bus, err := events.New(events.Options{DataDir: dataDir, DB: db, Stderr: os.Stderr})
	if err != nil {
		httpSrv.Close()
		return err
	}
	access, err := system.Open(db, dataDir, system.Options{})
	if err != nil {
		httpSrv.Close()
		bus.Close(context.Background())
		return err
	}
	flows := flow.Open(db)
	// A replace that a crash left unsettled is settled from the files' identity before any job runs (L-SAFE-9).
	journal := safety.NewJournal(db)
	if rep, err := safety.Recover(ctx, journal); err != nil {
		_ = bus.Emit(events.Event{Level: events.LevelWarn, Kind: events.KindWarning,
			Source: "safety", Code: "replace.recover_failed", Message: err.Error()})
	} else {
		// A restore a crash left was finished now: its job's History says so (page 03), as a live restore does.
		for _, ev := range recoveredRestoreEvents(rep.Restored) {
			_ = bus.Emit(ev)
		}
		if rep.Attention > 0 {
			_ = bus.Emit(events.Event{Level: events.LevelWarn, Kind: events.KindWarning,
				Source: "safety", Code: "replace.attention",
				Message: fmt.Sprintf("%d replaced original(s) need a look: the original is in the library's %s folder, and nothing was removed.", rep.Attention, safety.TrashDir)})
		}
	}
	// The trash is emptied once a day: copies past keepOriginalsDays go, and nothing else (S2-10c).
	go trashSweepLoop(ctx, journal, bus)
	// Anything left in the app's temp folder came from a process that stopped without tidying
	// up: a try's sample and staging both live there. Only entries older than this start are
	// removed, so a try already running in another process is left alone.
	sweepTryFolder(ctx, filepath.Join(dataDir, "tmp"), started, func(message string) {
		_ = bus.Emit(events.Event{Level: events.LevelWarn, Kind: events.KindWarning,
			Source: "safety", Code: "sweep.try_left", Message: message})
	})
	q := jobs.New(jobs.Options{DB: db, Bus: bus})
	srv.Jobs(q, library.NewCatalog(db), flows, bus)
	if err := q.Recover(ctx, sweepLeftovers(bus)); err != nil {
		httpSrv.Close()
		bus.Close(context.Background())
		return fmt.Errorf("recover jobs: %w", err)
	}
	// A stop between placing a file's .srt subtitles and the file itself leaves records: each .srt
	// of ours whose new file is not in place goes, so a retry finds no name taken by our own file.
	for _, said := range safety.RecoverSidecars(jobs.SidecarJournal{DB: db}) {
		_ = bus.Emit(events.Event{Level: events.LevelWarn, Kind: events.KindWarning,
			Source: "safety", Code: "sidecar.recovered", Message: said})
	}
	// The card detector: one background pass over the encoders this machine lists, with a device
	// probe for each one it finds, so a job can choose a card rung only for the tests the card
	// passed (L-HW-3, L-HW-9). A job that starts before the pass ends converts on the processor and
	// says so in its log.
	scratch := filepath.Join(dataDir, "tmp")
	var cards *hw.Detector
	var pick func(flow.Want) (flow.Attempt, error)
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		_ = bus.Emit(events.Event{Level: events.LevelWarn, Kind: events.KindWarning,
			Source: "hw", Code: "hw.no_temp",
			Message: "The graphics card cannot be tested, so every job runs on the processor: " + err.Error()})
	} else {
		cards = hw.NewDetector(
			media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run, CaptureFunc: media.Capture},
			scratch,
			hw.Candidates(hw.ReadRenderNodes("/sys/class/drm", "/dev/dri")),
			bus,
		).WithDevices()
		cards.Start(ctx)
		pick = cardPicker(cards)
	}
	// The worker's own settings (page 10): the files it runs at once are its setting at start, its
	// graphics slots are read per job, and its hours decide whether a slot may take a new job. The
	// migration created the built-in row, so a read that fails is a real problem worth stopping for.
	// The worker's row is the authority for files at once (the brief's reading): page 11's
	// processing.parallelJobs is only the default a new worker row is created with.
	media.SetLowPriority(settings.Get(ctx, db, "processing.lowPriority", true))
	workerStore := workers.Open(db)
	local, err := workerStore.Get(ctx, workers.LocalID)
	if err != nil {
		httpSrv.Close()
		bus.Close(context.Background())
		return fmt.Errorf("read the worker's settings: %w", err)
	}
	env := workerEnv(flows, bus, db, pick)
	env.GPUSlots = gpuSlots(workerStore)
	wk := worker.New(worker.Config{
		Slots: local.Slots, WorkerID: local.ID, DataDir: dataDir, Bus: bus,
		MayClaim: mayClaim(workerStore),
		Source:   worker.Builtin{Queue: q, DB: db, Flows: flows},
		Run:      flow.Run, Env: env,
	})
	wctx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() {
		wk.Run(wctx)
		close(workerDone)
	}()

	// Each job a scan enqueues wakes the worker, so the first encode starts before the scan has
	// finished; the same Wake is what the api's setup uses after the first scan.
	catalog := library.NewCatalog(db)
	reportLibraryOverlaps(ctx, catalog, bus)
	// A detector that could not be built leaves the card answers nil, not a nil pointer behind the
	// interface: the Workers page then shows no card.
	var cardAnswer api.CardAnswer
	if cards != nil {
		cardAnswer = cards
	}
	srv.Ready(api.Deps{
		Access:     access,
		Events:     bus,
		DB:         db,
		AppVersion: version,
		Workflows:  flows,
		Catalog:    catalog,
		Jobs:       q,
		Scanner:    newScanner(catalog, flows, q, bus, wk.Wake),
		Wake:       wk.Wake,
		Workers:    workerStore,
		Cards:      cardAnswer,
		// Cancel of a running job (S3-6a): the api marks the row cancelled, then this ends the run's
		// context, which kills ffmpeg and lets the flow remove its staging.
		StopJob:  func(id int64) { wk.CancelJob(id) },
		DiskFree: fsx.FreeBytes,
		// Try on one file (S2-5): the same engine as a job, with its own events that answer the
		// page rather than going to the log.
		Try: func(ctx context.Context, f flow.Flow, file string) (flow.TryResult, error) {
			// A sample is a real copy of the video (a 4K minute can be hundreds of MB), so it goes
			// in the app's own data folder rather than the system temp, which is often a small
			// memory-backed one. The engine's staging goes there too, so a try never writes in the
			// library. Each folder is removed when the try ends, and whatever a killed process
			// leaves is swept at the next start.
			scratch := filepath.Join(dataDir, "tmp")
			if err := os.MkdirAll(scratch, 0o700); err != nil {
				return flow.TryResult{}, fmt.Errorf("make the try folder: %w", err)
			}
			return flow.Try(ctx, f, file, engineEnv(pick), scratch)
		},
	})
	fmt.Fprintln(os.Stderr, "syp serving on", ln.Addr())

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}
	// The worker must finish or requeue its job before the store closes (deferred above).
	stopWorker()
	<-workerDone
	srv.CloseStreams() // event streams never end on their own
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutdown)
	if err := bus.Close(shutdown); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

// reportLibraryOverlaps writes one System log warning for each pair of stored libraries that watch
// one folder or nested folders. An install that predates the one-real-folder rule can hold such a
// pair; start-up carries on, and the folder checks refuse further edits that keep the overlap, so
// the person can move a library. Failing to read the libraries is reported the same way and does not
// stop start-up either: the first request that needs them says more.
func reportLibraryOverlaps(ctx context.Context, catalog *library.Catalog, bus *events.Bus) {
	overlaps, err := catalog.Overlaps(ctx)
	if err != nil {
		_ = bus.Emit(events.Event{Level: events.LevelWarn, Kind: events.KindWarning, Source: "library",
			Code: "library.overlap_unknown", Message: "The library folders could not be compared: " + err.Error()})
		return
	}
	for _, o := range overlaps {
		_ = bus.Emit(events.Event{Level: events.LevelWarn, Kind: events.KindWarning, Source: "library",
			Code: "library.overlap", Message: o.Message()})
	}
}

// loadRevisionFor is the flow a run starts from: the saved revision, with "my languages" looked up now (page 07), so a
// change applies to the next run. The run and its Try both use it.
func loadRevisionFor(flows *flow.Store, db *store.DB) func(ctx context.Context, id int64, rev int) (flow.Flow, error) {
	return func(ctx context.Context, id int64, rev int) (flow.Flow, error) {
		r, err := flows.Revision(ctx, id, rev)
		if err != nil {
			return r.Flow, err
		}
		return flow.ResolveMyLanguages(r.Flow, settings.Languages(ctx, db)), nil
	}
}

// trashSweepLoop runs the trash sweep at start and then every day until the app stops.
func trashSweepLoop(ctx context.Context, j *safety.Journal, bus *events.Bus) {
	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	for {
		sweepTrashOnce(ctx, j, bus)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func sweepTrashOnce(ctx context.Context, j *safety.Journal, bus *events.Bus) {
	days, err := j.KeepDays(ctx)
	if err == nil {
		var rep safety.TrashSweepReport
		rep, err = safety.SweepTrash(ctx, j, time.Now(), days)
		if rep.Purged > 0 {
			_ = bus.Emit(events.Event{Level: events.LevelInfo, Kind: events.KindWarning,
				Source: "safety", Code: "trash.swept",
				Message: fmt.Sprintf("%d original(s) left the trash after %d days.", rep.Purged, days)})
		}
	}
	if err != nil && ctx.Err() == nil {
		_ = bus.Emit(events.Event{Level: events.LevelWarn, Kind: events.KindWarning,
			Source: "safety", Code: "trash.sweep_failed", Message: err.Error()})
	}
}

// sweepLeftovers is the startup hook for a job that was running when the app stopped. It removes that job's
// staging folder leftovers that are certainly ours before the job goes back to waiting. A folder it cannot clear
// is reported and the job still requeues; the next run tries again. Only a cancelled startup stops recovery.
func sweepLeftovers(bus *events.Bus) func(context.Context, jobs.Job) error {
	return func(ctx context.Context, j jobs.Job) error {
		rep, err := safety.Sweep(ctx, []string{filepath.Dir(j.Path)})
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			_ = bus.Emit(events.Event{JobID: j.ID, Level: events.LevelWarn, Kind: events.KindWarning,
				Source: "safety", Code: "sweep.failed", Message: err.Error()})
		}
		for _, l := range rep.Left {
			_ = bus.Emit(events.Event{JobID: j.ID, Level: events.LevelWarn, Kind: events.KindWarning,
				Source: "safety", Code: "sweep.left", Message: l.Path + ": " + l.Reason})
		}
		return nil
	}
}

// sweepTryFolder clears the app's temp folder at startup. Everything in it is ours (a try's sample
// and its staging folder), but only what is older than a second before this start is removed: a
// newer entry may belong to a try another process is running. (The second leaves room for the file
// system's coarse timestamps.) What it cannot clear is passed to warn, and start-up carries on.
func sweepTryFolder(ctx context.Context, dir string, started time.Time, warn func(string)) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		warn("The app's temp folder could not be read: " + err.Error())
		return
	}
	cutoff := started.Add(-time.Second)
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.RemoveAll(path); err != nil {
			warn(path + ": " + err.Error())
		}
	}
}

// engineEnv is the world the flow engine runs in: the real media tools, the real safety check, the
// card picker and the clock. The worker adds its log and revision loader; a try adds neither, so
// its events answer the page and nothing reaches a job's log. A nil pick runs every job on the
// processor, which is what a machine with no detector has.
func engineEnv(pick func(flow.Want) (flow.Attempt, error)) flow.Env {
	return flow.Env{
		Media:  media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run, CaptureFunc: media.Capture},
		Safety: flow.RealSafety{}, Pick: pick,
		Now: time.Now, Sleep: time.Sleep, AppVersion: version,
	}
}

// cardPicker answers a job from the detector's last finished pass: the rungs the card's own tests
// allow (L-HW-9). Before the first pass ends it answers "not yet", so the job converts on the
// processor and its log says why.
func cardPicker(cards *hw.Detector) func(flow.Want) (flow.Attempt, error) {
	return func(want flow.Want) (flow.Attempt, error) {
		ans, ok := cards.Current()
		if !ok {
			return flow.Attempt{Reason: "the graphics card has not been tested yet"}, nil
		}
		return flow.PickFromAnswer(ans, want), nil
	}
}

// gpuSlots is the built-in worker's graphics slots, read before each job's first card encode (L-HW-8).
// A read that fails leaves the card without a limit rather than failing the job: the setting is a
// schedule of the card, and the job's log already has the database's problem.
func gpuSlots(store *workers.Store) func() int {
	return func() int {
		w, err := store.Get(context.Background(), workers.LocalID)
		if err != nil {
			return -1
		}
		return w.GPUSlots
	}
}

// mayClaim says whether the built-in worker's hours allow a new job now (page 10). A read that fails
// allows it, for the same reason as gpuSlots.
func mayClaim(store *workers.Store) func() bool {
	return func() bool {
		w, err := store.Get(context.Background(), workers.LocalID)
		return err != nil || w.AllowsWork(time.Now())
	}
}

// workerEnv is what a real job runs with: engineEnv and the job's own events on the bus.
func workerEnv(flows *flow.Store, bus *events.Bus, db *store.DB, pick func(flow.Want) (flow.Attempt, error)) flow.Env {
	env := engineEnv(pick)
	env.Replacer = flow.RealReplacer{Journal: safety.NewJournal(db)}
	env.LoadRevision = loadRevisionFor(flows, db)
	env.Bus = bus
	env.Sidecars = jobs.SidecarJournal{DB: db}
	return env
}

// newScanner is the library scanner wired to the real world: ffprobe for the probe, the library's
// workflow to decide, and the job queue to enqueue what should convert. A file is queued only when
// the workflow has work for it; the reasons it is left alone are stored on the file's row. Each
// queued job wakes the worker, so it starts before the scan has finished.
func newScanner(catalog *library.Catalog, flows *flow.Store, queue *jobs.Queue, bus *events.Bus, wake func()) *library.Scanner {
	return &library.Scanner{
		Catalog: catalog,
		Bus:     bus,
		Prober:  media.MediaFuncs{ProbeFunc: media.Probe},
		Decide: func(f library.File, info media.Info) (bool, string) {
			ctx := context.Background()
			lib, err := catalog.GetLibrary(ctx, f.LibraryID)
			if err != nil || lib.WorkflowID == nil {
				return false, "needs_workflow"
			}
			workflow, err := flows.Get(ctx, *lib.WorkflowID)
			if err != nil || workflow.CurrentRev == 0 {
				return false, "workflow_missing"
			}
			rev, err := flows.Revision(ctx, *lib.WorkflowID, workflow.CurrentRev)
			if err != nil {
				return false, "workflow_missing"
			}
			decisions, _, err := flow.PlanFile(rev, info, flow.Env{})
			if err != nil {
				return false, "plan_failed"
			}
			if !decisions.Work() {
				return false, "nothing_to_do"
			}
			return true, ""
		},
		Enqueue: func(ctx context.Context, lib library.Library, f library.File) error {
			if lib.WorkflowID == nil {
				return errors.New("the library has no workflow")
			}
			fileID, err := catalog.SaveFile(ctx, f, library.Queued, "", f.Probe)
			if err != nil {
				return err
			}
			path := filepath.Join(lib.Path, filepath.FromSlash(f.RelPath))
			// The published revision at this moment: the job keeps it even if the workflow is
			// published again before the job runs.
			rev := 0
			if wf, err := flows.Get(ctx, *lib.WorkflowID); err == nil {
				rev = wf.CurrentRev
			}
			_, open, err := queue.Enqueue(ctx, fileID, lib.ID, *lib.WorkflowID, rev, path, "scan")
			if err != nil {
				return err
			}
			if open {
				return library.ErrAlreadyQueued
			}
			if wake != nil {
				wake()
			}
			return nil
		},
	}
}
