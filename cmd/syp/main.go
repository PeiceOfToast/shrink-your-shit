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
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	_ "github.com/PeiceOfToast/shrink-your-shit/internal/library" // registers the libraries and files migration
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
	"github.com/PeiceOfToast/shrink-your-shit/internal/system"
	"github.com/PeiceOfToast/shrink-your-shit/internal/ui"
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
	q := jobs.New(jobs.Options{DB: db, Bus: bus})
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

	srv.Ready(access, bus)
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
