// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Guard (qa-697332b clause e, 03:14Z): a replace keeps the original in the folder's .syp-trash as
// "<name>.<job id><ext>", unchanged, never in place beside the new file; its delete day is the keep days
// counted from when it was kept. Both with the same file type and with a new one.
func TestRealReplaceKeepsTheOriginalInTrash(t *testing.T) {
	needTools(t)
	for _, c := range []struct{ name, fileType, newExt string }{{"same type", "keep", ".mp4"}, {"new type", "mkv", ".mkv"}} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "clip-d.mp4")
			runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=25", "-f", "lavfi", "-i", "sine=duration=3",
				"-map", "0", "-map", "1", "-c:v", "libx264", "-b:v", "4000k", "-c:a", "aac", src)
			before, _ := os.ReadFile(src)
			sel := rules.DefaultSelectors(3)
			sel.FileType = c.fileType
			tmpl := sel.Template()
			st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			st.SetPaused(false)
			other, _ := st.Enqueue(jobs.Job{Library: dir, Path: filepath.Join(dir, "other.mp4")})
			st.Skip(other, "not this one", rules.SkipAlreadyInFormat) // so this job's id is 2, not 1: the name must carry the id
			id, _ := st.Enqueue(jobs.Job{Library: dir, Path: src, DurationSec: 3})
			if id == 1 {
				t.Fatal("the job id should not be 1 here")
			}
			w := realWorker(st, media.OSExec{})
			w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
			w.KeepDays, w.Journal = func() int { return 7 }, st
			w.run(t)
			if j, _ := st.Get(id); j.Status != jobs.Done {
				t.Fatalf("%s %q", j.Status, j.Error)
			}
			if _, err := os.Stat(filepath.Join(dir, "clip-d"+c.newExt)); err != nil {
				t.Errorf("the new file isn't at the name: %v", err)
			}
			trash := filepath.Join(dir, TrashDir, "clip-d."+strconv.FormatInt(id, 10)+".mp4")
			if b, err := os.ReadFile(trash); err != nil || string(b) != string(before) {
				t.Errorf("the original isn't kept unchanged at %s: %v", trash, err)
			}
			entries, _ := os.ReadDir(dir) // nothing else beside the new file: no "<name>.1.<ext>" in place
			for _, e := range entries {
				if n := e.Name(); n != TrashDir && n != "clip-d"+c.newExt {
					t.Errorf("%s left in the folder", n)
				}
			}
			o, err := st.OriginalOfJob(id)
			if err != nil || o.State != jobs.OriginalKept || o.Trash != trash {
				t.Fatalf("record %+v %v", o, err)
			}
			kept, err1 := time.Parse(time.RFC3339, o.KeptAt)
			del, err2 := time.Parse(time.RFC3339, o.DeleteOn)
			if err1 != nil || err2 != nil || del.Sub(kept) < 7*24*time.Hour-time.Minute || del.Sub(kept) > 7*24*time.Hour+time.Minute {
				t.Errorf("kept %q, delete on %q: want 7 days after it was kept", o.KeptAt, o.DeleteOn)
			}
		})
	}
}
