// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.
// This program is free software: see the LICENSE file.

// shrink-your-shit watches folders for video files and re-encodes them to
// save disk space. Run it with -h for options.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/preview"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/runlog"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// version is set at release time: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

type libFlag []string

func (l *libFlag) String() string     { return strings.Join(*l, ",") }
func (l *libFlag) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	if len(os.Args) > 1 && os.Args[1] == "token" {
		os.Exit(tokenCommand(os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		os.Exit(doctorCommand(os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "worker" { // V7b: a worker for a server on another computer
		os.Exit(workerCommand(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "restore" {
		os.Exit(restoreCommand(os.Args[2:], os.Stdout, os.Stderr))
	}
	logs := &doctor.LogTail{} // the last log lines, for the health report
	log.SetOutput(io.MultiWriter(os.Stderr, logs))
	var libs libFlag
	flag.Var(&libs, "library", "folder to watch: added as a library if no library has it yet, never changed or removed (repeat for several; or env SYP_LIBRARIES, separated by : on Linux/macOS, ; on Windows). Libraries can also be added on the web page")
	def := rules.Default()
	data := flag.String("data", defaultDataDir(), "folder for the database (default: your user config folder; env SYP_DATA)")
	addr := flag.String("addr", envOr("SYP_ADDR", "127.0.0.1:8484"), "web UI address; use 0.0.0.0:8484 to allow other machines, which then need the access token (see: shrink-your-shit token) (env SYP_ADDR)")
	const forNew = " (only for an install upgraded from before libraries: the Legacy flows of the libraries -library adds there; a new install's libraries get their setting in first-run)"
	from := flag.String("from-codec", def.FromCodec, "only convert files with this video codec"+forNew)
	minKbps := flag.Int("min-bitrate", def.MinBitrateKbps, "only convert files above this video bitrate (kbps)"+forNew)
	crf := flag.Int("crf", def.CRF, "HEVC quality (lower = better quality, bigger file)"+forNew)
	preset := flag.String("preset", def.Preset, "x265 speed preset (ultrafast … veryslow)"+forNew)
	mode := flag.String("output", string(worker.Replace), "replace: swap in the new file once it is complete; beside: keep the original, write name.hevc.ext"+forNew)
	decode := flag.String("decode-check", "auto", "play the new file through a decoder before replacing the original: auto (completely if up to 3 hours long, otherwise at 3 places) or always")
	minSSIM := flag.Float64("min-ssim", worker.DefaultMinSSIM, "lowest picture similarity (0.90-1.0) between the original and the new file; a starting value that has not been tuned on real material")
	every := flag.Duration("scan-every", 30*time.Second, "how often to look for new files")
	settle := flag.Duration("settle", 15*time.Second, "ignore files modified more recently than this (still copying)")
	stallAfter := flag.Duration("stall-after", worker.DefaultStallAfter, "stop a conversion whose progress has not moved for this long (it fails; the original is untouched)")
	ffmpeg := flag.String("ffmpeg", os.Getenv("SYP_FFMPEG"), "path to ffmpeg (default: find it; env SYP_FFMPEG)")
	ffprobe := flag.String("ffprobe", os.Getenv("SYP_FFPROBE"), "path to ffprobe (default: find it; env SYP_FFPROBE)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	trustedProxies := flag.String("trusted-proxies", os.Getenv("SYP_TRUSTED_PROXIES"), "addresses or ranges (comma-separated) of reverse proxies whose X-Forwarded-Proto/For are believed, for remote workers outside your network (env SYP_TRUSTED_PROXIES)")
	flag.Parse()
	if *showVersion {
		fmt.Println("shrink-your-shit", version)
		return
	}

	if len(libs) == 0 {
		libs = librariesFromEnv()
	}
	switch *decode {
	case "auto", "always":
	default:
		log.Fatalf("-decode-check must be auto or always, not %q", *decode)
	}
	if *minSSIM < 0.90 || *minSSIM > 1 {
		log.Fatalf("-min-ssim must be between 0.90 and 1.0, not %v", *minSSIM)
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
	// A missing converter does not stop the program: the web page and its health report still run
	// and say what is wrong; only converting waits (see below).
	ffmpegPath, err := resolveTool("ffmpeg", *ffmpeg)
	if err != nil {
		log.Print(err)
	}
	ffprobePath, err := resolveTool("ffprobe", *ffprobe)
	if err != nil {
		log.Print(err)
	}
	if err := os.MkdirAll(*data, 0o755); err != nil {
		log.Fatal(err)
	}
	// One program per data folder: held for the service's whole life (a restore --force takes it too).
	releaseLock, err := lockDataDir(*data)
	if errors.Is(err, errDataLocked) {
		log.Fatalf("Another shrink-your-shit is already using the data folder %s (a running service, or a restore --force). Stop it first.", *data)
	}
	if err != nil {
		log.Fatalf("data folder %s: %v", *data, err)
	}
	defer releaseLock()
	store, err := jobs.Open(filepath.Join(*data, "syp.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	// keepDays is how long replaced originals are kept in .syp-trash (Settings > Safety; 7 by default).
	keepDays := func() int {
		s, err := web.LoadSettings(store)
		if err != nil {
			log.Printf("settings: %v; originals are kept for 7 days", err)
			return 7
		}
		return s.KeepOriginalsDays
	}
	// A swap a crash interrupted is finished or undone first, so its job is not converted twice.
	for _, said := range worker.RecoverSwaps(store, store, keepDays(), time.Now()) {
		log.Printf("recovery: %s", said)
	}
	for _, said := range worker.SweepStaging(worker.OutputFolders(store)) { // before any worker starts
		log.Printf("recovery: %s", said)
	}
	if paths, err := store.JobPaths(); err == nil { // and next to each job's file, where a replace stages (#163 QA)
		for _, said := range worker.SweepNextTo(paths) {
			log.Printf("recovery: %s", said)
		}
	}
	for _, said := range worker.RecoverSidecars(store) {
		log.Printf("recovery: %s", said)
	}
	if n, _ := store.RequeueRunning(); n > 0 {
		log.Printf("requeued %d job(s) interrupted by the last shutdown", n)
	}
	// Kept originals whose days are up are deleted now and then every hour (slice 18).
	runLogs := &runlog.Logs{Dir: filepath.Join(*data, "logs")} // each job's run log (S5)
	store.OnJobsRemoved = func(ids []int64) {                  // a job's log goes with it (review 43e6f66)
		for _, id := range ids {
			runLogs.Remove(id)
		}
	}
	sweepLogs := func() { // the run logs' limits: their job is gone, 30 days, 100 MiB in all
		ids, err := store.ForgetGoneJobSteps()
		if err != nil {
			log.Printf("run logs: %v", err)
			return
		}
		if entries, err := os.ReadDir(runLogs.Dir); err == nil {
			for _, e := range entries {
				if id, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".log"), 10, 64); err == nil && !ids[id] {
					runLogs.Remove(id)
				}
			}
		}
		if _, err := runLogs.Sweep(time.Now(), runlog.MaxAge, runlog.Total); err != nil {
			log.Printf("run logs: %v", err)
		}
		if _, err := store.PruneJobEvents(time.Now().Add(-runlog.MaxAge)); err != nil { // jobs' history: the logs' 30 days
			log.Printf("job history: %v", err)
		}
	}
	purge := func() {
		sweepLogs()
		for _, said := range worker.Purge(store, keepDays(), time.Now()) {
			log.Printf("purge: %s", said)
		}
	}
	purge()

	rule := rules.Rule{FromCodec: *from, MinBitrateKbps: *minKbps, CRF: *crf, Preset: *preset}
	n, skipped, err := store.EnsureLibraries(libs, rules.LegacyTemplate(rule, *mode))
	if err != nil {
		log.Fatalf("libraries: %v", err)
	}
	for _, why := range skipped { // not fatal: the libraries that are fine still run
		log.Printf("NOT added as a library from -library/SYP_LIBRARIES: %s", why)
	}
	if n > 0 {
		how := "converting as before"
		if up, _ := store.Upgraded(); !up {
			how = "waiting for first run on the web page (nothing is converted before Start)"
		}
		log.Printf("added %d librar%s from -library/SYP_LIBRARIES, %s", n, map[bool]string{true: "y", false: "ies"}[n == 1], how)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM) // SIGTERM is a no-op on Windows; Ctrl+C works everywhere
	defer stop()

	x := media.OSExec{}
	chk := &doctor.Checker{Exec: x, FFmpeg: ffmpegPath, FFprobe: ffprobePath, KnownOriginals: store.KnownOriginals}
	tools := chk.Converter(ctx)
	converts := tools.Converts()
	if tools.FFmpeg != "" { // the video formats Standard can offer (S6): the encoders this ffmpeg has
		rules.SetAvailableFormats(media.Encoders(ctx, x, tools.FFmpeg))
	}
	chk.ToolsNow() // the hardware check starts in the background, after the converter's: Workers has it soon after
	sc := &scanner.Scanner{Store: store, Exec: x, FFprobe: ffprobePath, Targets: scanTargets(store), MinAge: *settle}
	if converts {
		if s, err := web.LoadSettings(store); err == nil && s.ScanEvery > 0 { // a saved interval wins over the flag (design 01:41Z)
			sc.SetEvery(time.Duration(s.ScanEvery) * time.Second)
		}
		go sc.Run(ctx, *every)
		go func() { // V7a: a job whose worker stopped answering (its lease ran out) goes back to the queue
			t := time.NewTicker(15 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if n, err := store.RequeueExpiredLeases(5 * time.Minute); err != nil {
						log.Printf("leases: %v", err)
					} else if n > 0 {
						log.Printf("leases: %d job(s) back in the queue: their worker stopped answering", n)
					}
				}
			}
		}()
	} else {
		log.Print("The converter is not installed correctly (ffmpeg and ffprobe with libx265 and the ssim filter are needed), so nothing is scanned or converted. The web page's Settings > About says what is missing; fix it, then restart shrink-your-shit.")
	}
	// Kept originals are purged every hour whether or not converting can run (review 172f049).
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				purge()
			}
		}
	}()
	// As many workers as the worker's parallel jobs setting (Workers page; followed live); Pause all stops them
	// taking work and survives a restart.
	pool := &worker.Pool{
		New: func(n int) *worker.Worker {
			id := "builtin"
			if n > 1 {
				id = fmt.Sprintf("builtin-%d", n)
			}
			return &worker.Worker{ID: id, Logs: runLogs, Queue: store, Exec: x, FFmpeg: ffmpegPath, FFprobe: ffprobePath,
				Template: store.TemplateFor, Poll: 3 * time.Second,
				DecodeCheck: *decode, MinSSIM: *minSSIM, KeepDays: keepDays, Journal: store, Sidecars: store,
				Paused: func() bool { return !store.WorkerEnabled(jobs.LocalWorker) }, StallAfter: *stallAfter} // this computer's own switch
		},
		Size: func() int {
			if s, err := web.LoadSettings(store); err == nil {
				return s.ParallelJobs
			}
			return 1
		},
	}
	if store.Paused() {
		log.Printf("converting is paused: turn the Workers switch on (Dashboard or Workers)")
	}
	workerDone := make(chan struct{})
	if converts {
		go func() { pool.Run(ctx); close(workerDone) }()
	} else {
		close(workerDone)
	}

	access, created, err := web.OpenAccess(*data, exposedToNetwork(*addr))
	if err != nil {
		log.Fatalf("access token: %v", err)
	}
	proxies, err := web.ParseTrustedProxies(*trustedProxies)
	if err != nil {
		log.Fatalf("-trusted-proxies: %v", err)
	}
	srv := &http.Server{Addr: *addr, Handler: web.Handler(store, access, web.Engine{
		Transport: web.Transport{TrustedProxies: proxies},
		RunRemote: func(job jobs.Job, workerID string, rj *worker.RemoteJob) { // V7b2: a remote worker's job, run here
			w := pool.New(1)
			w.ID, w.Paused = workerID, nil // a pause stops new leases; a leased job goes on
			if err := w.RunRemote(ctx, job, rj); err != nil {
				log.Printf("worker %s: %v", workerID, err)
			}
		},
		ScanNow: sc.ScanNow, NextScan: sc.NextScan, ScanEvery: *every, Settle: *settle, SetScanEvery: sc.SetEvery, Scanning: sc.Scanning, RequeueJob: pool.RequeueJob, PauseNow: pool.PauseNow, StopFile: pool.StopFile,
		RunLogs: runLogs, Doctor: chk, DataDir: *data, Version: version, Logs: logs,
		Preview:    &preview.Previewer{Store: store, Exec: media.OSExec{}, FFprobe: ffprobePath},
		FirstFiles: &preview.FirstLister{Exec: media.OSExec{}, FFprobe: ffprobePath}})}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	switch {
	case created: // the only time the token is logged
		log.Printf("A new access token was made. Sign in at %s (to see it again: shrink-your-shit token)", signInURL(*addr, access.Token()))
	case access.Required():
		log.Print("The web page needs the access token. To see it: shrink-your-shit token")
	}
	if exposedToNetwork(*addr) {
		log.Print("The web page is reachable from other machines (with the access token). There is no HTTPS: use it on a network you trust, never on the internet.")
	}
	if all, _ := store.Libraries(); len(all) == 0 {
		log.Printf("shrink-your-shit %s: no libraries yet; add one on the web page at http://%s", version, *addr)
	} else {
		log.Printf("shrink-your-shit %s: %d librar%s; web UI at http://%s", version, len(all), map[bool]string{true: "y", false: "ies"}[len(all) == 1], *addr)
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		stop()
		<-workerDone
		log.Print(err)
		return // deferred store.Close runs after the worker has stopped
	}
	<-workerDone // let an interrupted job go back in the queue before closing the database
}

// scanTargets lists the started libraries for the scanner, each with its flow's rule. A library whose
// flow cannot run (a kind a later slice adds) is left out, with a log line.
func scanTargets(store *jobs.Store) func() ([]scanner.Target, error) {
	return func() ([]scanner.Target, error) {
		libs, err := store.Libraries()
		if err != nil {
			return nil, err
		}
		var out []scanner.Target
		for _, l := range libs {
			if !l.Started {
				continue
			}
			if jobs.SystemFolder(l.Path) { // made before slice 23's guard: never scanned
				log.Printf("scan: skipping library %q: %v", l.Name, jobs.ErrSystemFolder)
				continue
			}
			f, err := store.GetFlow(l.FlowID)
			if err != nil {
				log.Printf("scan: skipping library %q: %v", l.Name, err)
				continue
			}
			out = append(out, scanner.Target{LibraryID: l.ID, Path: l.Path, Template: f.Template})
		}
		return out, nil
	}
}
