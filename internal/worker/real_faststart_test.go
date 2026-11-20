// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// topLevelBoxes lists the top-level MP4 boxes whose headers lie in the first bytes of a file, in order. A box
// larger than the buffer is listed, and the walk ends at the buffer's end.
func topLevelBoxes(b []byte) []string {
	var out []string
	for off := 0; off+8 <= len(b); {
		size := uint64(binary.BigEndian.Uint32(b[off:]))
		hdr := uint64(8)
		if size == 1 && off+16 <= len(b) {
			size = binary.BigEndian.Uint64(b[off+8:])
			hdr = 16
		}
		out = append(out, string(b[off+4:off+8]))
		if size < hdr {
			break
		}
		off += int(min(size, uint64(len(b)+1)))
	}
	return out
}

// The MP4 an MKV is converted to (F8) has its index (moov) before its data (mdat), so a player can start it
// before the whole file is downloaded. Read from the file itself: the first 64 KB hold the index's header.
func TestRealConvertedMP4IndexComesFirst(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "movie.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=5:size=320x180:rate=25",
		"-f", "lavfi", "-i", "sine=duration=5:frequency=440", "-map", "0", "-map", "1",
		"-c:v", "libx264", "-b:v", "300k", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "96k", src)

	st, id := realJob(t, src, 5)
	w := realWorker(st, media.OSExec{})
	s := rules.DefaultSelectors(3)
	s.FileType, s.AfterConverting, s.Subtitles = "mp4", "beside", "keep_all" // beside: the original stays, so no journal is needed
	tmpl := s.Template()
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	outs, _ := filepath.Glob(filepath.Join(dir, "*.mp4"))
	if len(outs) != 1 {
		t.Fatalf("want one MP4 in %s, got %v", dir, outs)
	}
	f, err := os.Open(outs[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	head := make([]byte, 64<<10)
	n, _ := f.Read(head)
	boxes := topLevelBoxes(head[:n])
	if !slices.Contains(boxes, "ftyp") {
		t.Fatalf("the output is not an MP4 (boxes %v)", boxes)
	}
	moov, mdat := slices.Index(boxes, "moov"), slices.Index(boxes, "mdat")
	if moov < 0 || (mdat >= 0 && moov > mdat) {
		t.Errorf("the index (moov) must come before the data (mdat) for a fast start; boxes %v", boxes)
	}
}
