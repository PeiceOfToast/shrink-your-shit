// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/remote"
)

// workerCommand is "shrink-your-shit worker": this program as a worker for a server on another
// computer (V7b). The first run pairs it with --pair <code> (from the server's Workers page, Add a
// worker); later runs use the token it saved. V7b1: it pairs and shows on the Workers page;
// converting comes with the next update.
func workerCommand(args []string, out, errOut io.Writer) int {
	fl := flag.NewFlagSet("worker", flag.ContinueOnError)
	fl.SetOutput(errOut)
	server := fl.String("server", "", "the server's address, e.g. http://192.168.1.10:8484 (https:// outside your network)")
	pair := fl.String("pair", "", "the pairing code from the server's Workers page (first run only)")
	name := fl.String("name", "", "this worker's name on the Workers page (default: this computer's name)")
	data := fl.String("data", filepath.Join(defaultDataDir(), "worker"), "folder for this worker's token (env SYP_WORKER_DATA)")
	work := fl.String("work", "", "folder for the originals it downloads and the new files it makes (default: <data>/work)")
	ffmpeg := fl.String("ffmpeg", os.Getenv("SYP_FFMPEG"), "path to ffmpeg (default: find it)")
	ffprobe := fl.String("ffprobe", os.Getenv("SYP_FFPROBE"), "path to ffprobe (default: find it)")
	minFree := fl.Int64("min-free-gb", 2, "free space (GB) this worker keeps in its work folder: below it, it takes no job")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if v := os.Getenv("SYP_WORKER_DATA"); v != "" && !flagSet(fl, "data") {
		*data = v
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := remote.Load(*data)
	switch {
	case *pair != "":
		if *server == "" {
			fmt.Fprintln(errOut, "Pairing needs the server's address: --server http://<server>:8484")
			return 2
		}
		if *name == "" {
			*name, _ = os.Hostname()
		}
		c := &remote.Client{Server: *server, Version: version}
		if st, err = c.Pair(ctx, *pair, *name); err != nil {
			fmt.Fprintf(errOut, "Pairing didn't work: %v\n", err)
			return 1
		}
		if err := st.Save(*data); err != nil {
			fmt.Fprintf(errOut, "Paired, but the token couldn't be saved in %s: %v\n", *data, err)
			return 1
		}
		fmt.Fprintf(out, "Paired with %s as %q. It shows on the server's Workers page.\n", st.Server, st.Name)
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintln(errOut, "This worker isn't paired yet. On the server, open Workers > Add a worker, then run the command it shows (with --pair).")
		return 2
	case err != nil:
		fmt.Fprintf(errOut, "This worker's saved state in %s can't be read: %v\n", *data, err)
		return 1
	}
	if *server != "" {
		st.Server = *server
	}
	if *work == "" {
		*work = filepath.Join(*data, "work")
	}
	ff, fp, err := findTools(*ffmpeg, *ffprobe)
	if err != nil {
		fmt.Fprintf(errOut, "This worker needs ffmpeg and ffprobe: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Working for %s as %q. Stop it with Ctrl+C.\n", st.Server, st.Name)
	c := &remote.Client{Server: st.Server, Token: st.Token, Version: version}
	wctx, wcancel := context.WithCancel(ctx)
	defer wcancel()
	go c.Work(wctx, remote.Converter{Exec: media.OSExec{}, FFmpeg: ff, FFprobe: fp, Work: *work, MinFree: uint64(*minFree) << 30})
	if err := c.Run(ctx); err != nil {
		fmt.Fprintf(errOut, "The server refused this worker: %v\n", err)
		return 1
	}
	return 0
}

func flagSet(fl *flag.FlagSet, name string) bool {
	set := false
	fl.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// findTools finds ffmpeg and ffprobe as the server does (a flag, the environment, PATH, beside this program).
func findTools(ffmpeg, ffprobe string) (string, string, error) {
	ff, err := resolveTool("ffmpeg", ffmpeg)
	if err != nil {
		return "", "", err
	}
	fp, err := resolveTool("ffprobe", ffprobe)
	return ff, fp, err
}
