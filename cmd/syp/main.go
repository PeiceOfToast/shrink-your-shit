// Command syp is the shrink-your-shit program.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
	"github.com/PeiceOfToast/shrink-your-shit/internal/system"
	"github.com/PeiceOfToast/shrink-your-shit/internal/worker"
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
	if err := fs.Parse(os.Args[1:]); err != nil || *dataDir == "" {
		fmt.Fprintln(os.Stderr, "usage: syp version | syp -data <dir> [-listen <addr>]")
		os.Exit(2)
	}
	if err := run(*dataDir, *listen); err != nil {
		fmt.Fprintln(os.Stderr, "syp:", err)
		os.Exit(1)
	}
}

// run serves the api while the store opens, so a request during startup gets 503 starting. Once the store is
// ready, jobs left running by a previous stop are recovered, then the worker starts. It stops on Ctrl-C or
// SIGTERM: the worker finishes or requeues its job before the store closes.
func run(dataDir, listen string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := api.New(version)
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
	q := jobs.New(jobs.Options{DB: db, Bus: bus})
	srv.Jobs(q, library.NewCatalog(db), flows, bus)
	if err := q.Recover(ctx, sweepLeftovers(bus)); err != nil {
		httpSrv.Close()
		bus.Close(context.Background())
		return fmt.Errorf("recover jobs: %w", err)
	}
	wk := worker.New(worker.Config{
		Slots: 1, WorkerID: "local", DataDir: dataDir, Bus: bus,
		Source: worker.Builtin{Queue: q, DB: db, Flows: flows},
		Run:    flow.Run, Env: workerEnv(flows, bus),
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
	srv.Ready(api.Deps{
		Access:    access,
		Events:    bus,
		DB:        db,
		Workflows: flows,
		Catalog:   catalog,
		Jobs:      q,
		Scanner:   newScanner(catalog, flows, q, bus, wk.Wake),
		Wake:      wk.Wake,
		DiskFree:  fsx.FreeBytes,
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

// workerEnv is what a real job runs with: the real media tools, the real safety steps, the processor encoder
// until stage 2 adds the hardware picker, and the job events on the bus.
func workerEnv(flows *flow.Store, bus *events.Bus) flow.Env {
	return flow.Env{
		Media:  media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run},
		Safety: flow.RealSafety{}, Pick: flow.ProcessorPick,
		LoadRevision: func(ctx context.Context, id int64, rev int) (flow.Flow, error) {
			r, err := flows.Revision(ctx, id, rev)
			return r.Flow, err
		},
		Bus: bus, Now: time.Now, Sleep: time.Sleep, AppVersion: version,
	}
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
			_, open, err := queue.Enqueue(ctx, fileID, lib.ID, *lib.WorkflowID, path, "scan")
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
