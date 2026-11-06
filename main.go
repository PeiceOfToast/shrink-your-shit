// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.
// This program is free software: see the LICENSE file.

// shrink-your-shit watches folders for video files and re-encodes them to
// save disk space. Run it with -h for options.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

type libFlag []string

func (l *libFlag) String() string     { return strings.Join(*l, ",") }
func (l *libFlag) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	var libs libFlag
	flag.Var(&libs, "library", "folder to watch (repeat for several)")
	def := rules.Default()
	data := flag.String("data", "./data", "folder for the database")
	addr := flag.String("addr", "127.0.0.1:8484", "web UI address")
	from := flag.String("from-codec", def.FromCodec, "only convert files with this video codec")
	minKbps := flag.Int("min-bitrate", def.MinBitrateKbps, "only convert files above this video bitrate (kbps)")
	crf := flag.Int("crf", def.CRF, "HEVC quality (lower = better quality, bigger file)")
	preset := flag.String("preset", def.Preset, "x265 speed preset (ultrafast … veryslow)")
	mode := flag.String("output", string(worker.Replace), "replace: swap in the new file once it is complete; beside: keep the original, write name.hevc.ext")
	every := flag.Duration("scan-every", 30*time.Second, "how often to look for new files")
	settle := flag.Duration("settle", 15*time.Second, "ignore files modified more recently than this (still copying)")
	ffmpeg := flag.String("ffmpeg", "ffmpeg", "path to ffmpeg")
	ffprobe := flag.String("ffprobe", "ffprobe", "path to ffprobe")
	flag.Parse()

	if len(libs) == 0 {
		fmt.Fprintln(os.Stderr, "Tell me which folder to watch, e.g.:\n  shrink-your-shit -library /path/to/videos")
		os.Exit(2)
	}
	if *mode != string(worker.Replace) && *mode != string(worker.Beside) {
		log.Fatalf("-output must be %q or %q", worker.Replace, worker.Beside)
	}
	for i, l := range libs {
		abs, err := filepath.Abs(l)
		if err != nil {
			log.Fatal(err)
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			log.Fatalf("library %q is not a folder", l)
		}
		libs[i] = abs
	}
	for _, tool := range []string{*ffmpeg, *ffprobe} {
		if _, err := exec.LookPath(tool); err != nil {
			log.Fatalf("can't find %q. Install ffmpeg (Ubuntu/WSL2: sudo apt install ffmpeg) and try again.", tool)
		}
	}
	if err := os.MkdirAll(*data, 0o755); err != nil {
		log.Fatal(err)
	}
	store, err := jobs.Open(filepath.Join(*data, "syp.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if n, _ := store.RequeueRunning(); n > 0 {
		log.Printf("requeued %d job(s) interrupted by the last shutdown", n)
	}

	rule := rules.Rule{FromCodec: *from, MinBitrateKbps: *minKbps, CRF: *crf, Preset: *preset}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	x := media.OSExec{}
	sc := &scanner.Scanner{Store: store, Exec: x, FFprobe: *ffprobe, Rule: rule, Libraries: libs, MinAge: *settle}
	go sc.Run(ctx, *every)
	w := &worker.Worker{ID: "builtin", Queue: store, Exec: x, FFmpeg: *ffmpeg, Rule: rule,
		Mode: worker.OutputMode(*mode), Poll: 3 * time.Second}
	go w.Run(ctx)

	srv := &http.Server{Addr: *addr, Handler: web.Handler(store, libs)}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	log.Printf("watching %v; web UI at http://%s", []string(libs), *addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
