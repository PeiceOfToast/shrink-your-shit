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
	"syscall"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/api"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
	"github.com/PeiceOfToast/shrink-your-shit/internal/system"
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

// run serves the api while the store opens, so a request during startup gets 503 starting. When the store
// is ready, the api lets requests through. It stops on Ctrl-C or SIGTERM.
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
	srv.Ready(access, bus)
	fmt.Fprintln(os.Stderr, "syp serving on", ln.Addr())

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			bus.Close(context.Background())
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutdown)
	return bus.Close(shutdown)
}
