// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
)

// The app never converts its own output again (dev-qa's #109 QA: a flow that replaces with a new file
// type re-queued its outputs on every scan, 4 -> 28 jobs in 6 minutes, each a generation worse). Real
// scanner and worker, each output mode, scanned after the conversion and again after a restart.
func TestRealOwnOutputIsNeverConvertedAgain(t *testing.T) {
	needTools(t)
	for _, c := range []struct{ name, mode, fileType string }{
		{"replace", "replace", "mkv"}, {"replace-same-type", "replace", "keep"}, {"beside", "beside", "mkv"}, {"folder", "folder", "mkv"},
	} {
		mode := c.mode
		t.Run(c.name, func(t *testing.T) {
			lib := t.TempDir()
			src := filepath.Join(lib, "Movie.mp4")
			runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=25", "-f", "lavfi", "-i", "sine=duration=3:frequency=440",
				"-map", "0", "-map", "1", "-c:v", "libx264", "-b:v", "4000k", "-c:a", "aac", "-b:a", "96k", "-metadata:s:a:0", "language=eng", src)
			sel := rules.DefaultSelectors(3)
			// QA's case: H.264 sources, a bitrate cap (so the output is never "already in this format"),
			// the file type changed to MKV
			sel.MaxBitrate, sel.FileType, sel.AfterConverting = "medium", c.fileType, mode
			if mode == "folder" {
				sel.OutputDir = filepath.Join(lib, "Shrunk") // inside the library, so the scan walks it
			}
			tmpl := sel.Template()
			db := filepath.Join(t.TempDir(), "t.db")
			open := func() *jobs.Store {
				st, err := jobs.Open(db)
				if err != nil {
					t.Fatal(err)
				}
				st.SetPaused(false)
				return st
			}
			// a new scanner each time: its two-look memory is lost, like the app's after a restart
			scan := func(st *jobs.Store) int {
				sc := &scanner.Scanner{Store: st, Exec: media.OSExec{}, FFprobe: "ffprobe",
					Targets: func() ([]scanner.Target, error) { return []scanner.Target{{Path: lib, Template: tmpl}}, nil }}
				return sc.ScanOnce(context.Background()) + sc.ScanOnce(context.Background()) // the second look decides
			}
			st := open()
			if n := scan(st); n != 1 {
				t.Fatalf("the original: %d jobs queued, want 1", n)
			}
			w := realWorker(st, media.OSExec{})
			w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
			w.KeepDays, w.Journal = func() int { return 7 }, st
			w.run(t)
			done, _ := st.List(10)
			if len(done) != 1 || done[0].Status != jobs.Done {
				t.Fatalf("done: %+v", done)
			}
			if _, err := os.Stat(done[0].OutputPath); err != nil {
				t.Fatal(err)
			}
			if n := scan(st); n != 0 {
				f, _ := st.GetFile(done[0].OutputPath)
				t.Errorf("after the conversion: %d new jobs for %s (%+v), want 0", n, done[0].OutputPath, f)
			}
			st.Close()
			st = open()
			defer st.Close()
			if n := scan(st); n != 0 {
				t.Errorf("after a restart: %d new jobs, want 0", n)
			}
		})
	}
}
