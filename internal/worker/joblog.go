// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The job log's sections (log-spec section 4), in the order a job writes them. Worker.phase is the section the
// job's own lines (jobf) go to; the job moves it on as it goes.
const (
	secStart     = "start"
	secVariables = "variables"
	secInput     = "input"
	secEncode    = "encode"
	secOutput    = "output"
	secReplace   = "replace"
	secSummary   = "summary"
)

// logLine writes a line of job id's log, in section at level, from the worker's own words (step 0, as jobf does).
func (w *Worker) logLine(id int64, section, level, text string) {
	if w.Queue != nil {
		w.Queue.AppendLogLine(id, 0, section, level, "app", text)
	}
}

// jobNote writes one of the worker's notes about job id to the program's log and to the job's log in section.
func (w *Worker) jobNote(id int64, section, level, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	log.Printf("worker %s: job %d: %s", w.ID, id, msg)
	w.logLine(id, section, level, msg)
}

// stepSectionName is a workflow step's log section ("Step 2: Audio"): "step:<n>:<label>", n from 1. The web
// reads the number and label back from it (v2logdownload.go).
func stepSectionName(n int, label string) string { return fmt.Sprintf("step:%d:%s", n, label) }

// writeStepSections writes the plan's decisions as the log's step sections, in run order, and returns each
// step label's section (the first, if a label repeats). Two steps with one name are numbered (R12): the second
// is "Audio (2)".
func (w *Worker) writeStepSections(id int64, steps []rules.StepDecision) map[string]string {
	sections := map[string]string{}
	seen := map[string]int{}
	for i, st := range steps {
		label := st.Label
		seen[st.Label]++
		if n := seen[st.Label]; n > 1 {
			label = fmt.Sprintf("%s (%d)", st.Label, n)
		}
		sec := stepSectionName(i+1, label)
		if _, ok := sections[st.Label]; !ok {
			sections[st.Label] = sec
		}
		w.logLine(id, sec, jobs.LevelInfo, st.Decision)
	}
	return sections
}

// section is the section the worker's own lines of the running job go to. Read from the watchers' goroutines too.
func (w *Worker) section() string {
	if s, _ := w.phase.Load().(string); s != "" {
		return s
	}
	return secStart
}

// setPhase moves the worker's own lines to section s (the job goroutine only; section may be read from elsewhere).
func (w *Worker) setPhase(s string) { w.phase.Store(s) }

// startLines is the job's start section: what the worker knows at the start of this attempt. Not here yet: the
// app version and commit, why the job started, the workflow's name (the start part of the API has them as null).
func (w *Worker) startLines(job jobs.Job) {
	w.setPhase(secStart)
	w.logLine(job.ID, secStart, jobs.LevelInfo, fmt.Sprintf("Attempt %d on worker %s (%s/%s)", job.Attempts+1, w.ID, runtime.GOOS, runtime.GOARCH))
	w.logLine(job.ID, secStart, jobs.LevelInfo, "ffmpeg at "+w.FFmpeg)
	if job.Library != "" {
		w.logLine(job.ID, secStart, jobs.LevelInfo, fmt.Sprintf("Library %q", job.Library))
	}
}

// inputLines is the job's input section: the source file (its size in bytes and in words, R2), its container,
// length and overall bitrate, one line per stream (R7), and the chapter and attachment counts.
func (w *Worker) inputLines(id int64, path string, fi os.FileInfo, src media.Info) {
	w.setPhase(secInput)
	w.logLine(id, secInput, jobs.LevelInfo, "Source "+path)
	w.logLine(id, secInput, jobs.LevelInfo, fmt.Sprintf("Size %s bytes (%s), modified %s",
		rules.GroupDigits(fi.Size()), rules.HumanBytes(fi.Size()), fi.ModTime().UTC().Format(time.RFC3339)))
	head := "Container " + src.Container + ", length " + clockText(src.DurationSec)
	if src.BitrateKbps > 0 {
		head += ", overall " + bitrateText(src.BitrateKbps)
	}
	w.logLine(id, secInput, jobs.LevelInfo, head)
	for _, s := range src.Streams {
		w.logLine(id, secInput, jobs.LevelInfo, streamLine(s))
	}
	w.logLine(id, secInput, jobs.LevelInfo, fmt.Sprintf("Chapters %d, attachments %d", src.Chapters, len(src.Attachments())))
}

// streamLine is one stream of a file in a few words: "#2 audio · eng · AC-3 5.1 · 640 kbps · default".
func streamLine(s media.Stream) string {
	parts := []string{fmt.Sprintf("#%d %s", s.Index, s.Type)}
	if s.Language != "" {
		parts = append(parts, s.Language)
	}
	switch s.Type {
	case "video":
		parts = append(parts, streamCodecName(s.Codec))
		if s.Width > 0 {
			parts = append(parts, fmt.Sprintf("%dx%d", s.Width, s.Height))
		}
		if s.FrameRate > 0 {
			parts = append(parts, fmt.Sprintf("%.3f fps", s.FrameRate))
		}
		if s.AttachedPic {
			parts = append(parts, "cover art")
		} else if s.IsHDR() {
			parts = append(parts, "HDR")
		} else {
			parts = append(parts, "SDR")
		}
	case "audio":
		parts = append(parts, strings.TrimSpace(streamCodecName(s.Codec)+" "+channelWords(s.Channels)))
		if s.BitrateKbps > 0 {
			parts = append(parts, fmt.Sprintf("%d kbps", s.BitrateKbps))
		}
	case "subtitle":
		parts = append(parts, streamCodecName(s.Codec))
	}
	if s.Default {
		parts = append(parts, "default")
	}
	if s.Forced {
		parts = append(parts, "forced")
	}
	if s.HearingImpaired {
		parts = append(parts, "SDH")
	}
	return strings.Join(parts, " · ")
}

// streamCodecName is a codec's name as people write it; a codec not listed here is shown as ffmpeg names it.
func streamCodecName(c string) string {
	names := map[string]string{"h264": "H.264", "hevc": "HEVC", "av1": "AV1", "mpeg2video": "MPEG-2", "vc1": "VC-1",
		"ac3": "AC-3", "eac3": "E-AC-3", "aac": "AAC", "dts": "DTS", "truehd": "TrueHD", "flac": "FLAC", "opus": "Opus",
		"mp3": "MP3", "subrip": "SRT", "ass": "ASS", "hdmv_pgs_subtitle": "PGS", "dvd_subtitle": "VobSub"}
	if n, ok := names[c]; ok {
		return n
	}
	return c
}

// channelWords is a channel count in words: 2 is stereo, 6 is 5.1.
func channelWords(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return fmt.Sprintf("%d channels", n)
}

// clockText is a length in seconds as h:mm:ss.
func clockText(sec float64) string {
	t := int(sec + 0.5)
	return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
}

// bitrateText is a bitrate in kbps, in Mbps from 1,000 kbps.
func bitrateText(kbps int) string {
	if kbps >= 1000 {
		return fmt.Sprintf("%.1f Mbps", float64(kbps)/1000)
	}
	return fmt.Sprintf("%d kbps", kbps)
}

// resetStats starts a job's run counters (the L2 summary's passes and bytesWritten). A job on another computer
// counts nothing here: its runs are not all on this one, so its summary reads them as null.
func (w *Worker) resetStats(remote bool) {
	w.passes.Store(0)
	w.bytesWritten.Store(0)
	w.statsCounting.Store(!remote)
}

// addPass counts one ffmpeg run of the running job: its encode, a retry, a check.
func (w *Worker) addPass() {
	if w.statsCounting.Load() {
		w.passes.Add(1)
	}
}

// addBytes counts bytes the running job wrote: a temp file's, a .srt file's. The final file is the encode's
// temp file renamed, so it is counted once, through the encode.
func (w *Worker) addBytes(n int64) {
	if n > 0 && w.statsCounting.Load() {
		w.bytesWritten.Add(n)
	}
}

// recordMove keeps one file action of the running job's replace step, in order (the replace step's moves[]):
// the chmod, the owner, each rename, each sidecar and each removal, with its error when it failed.
func (w *Worker) recordMove(id int64, action, from, to string, err error) {
	if w.Queue == nil {
		return
	}
	m := jobs.Move{Action: action, From: from, To: to, OK: err == nil}
	if err != nil {
		m.Error = err.Error()
	}
	if rerr := w.Queue.RecordJobMove(id, m); rerr != nil {
		log.Printf("worker %s: job %d: recording a file action: %v", w.ID, id, rerr)
	}
}

// outputSeen notes that ffmpeg printed a line just now: the quiet warning starts counting again (R9).
func (w *Worker) outputSeen(now time.Time) {
	w.lastOutput.Store(now.UnixNano())
	w.quietWarned.Store(false)
}

// quietCheck writes the quiet warning once per silence: ffmpeg has printed nothing for quietAfter, so the log
// says it is still running (log-spec R9). The stall watcher calls it; nothing happens outside a local encode.
func (w *Worker) quietCheck(id int64, now time.Time) {
	if !w.quietOn.Load() || w.quietWarned.Load() {
		return
	}
	if now.Sub(time.Unix(0, w.lastOutput.Load())) < quietAfter {
		return
	}
	w.quietWarned.Store(true)
	w.logLine(id, secEncode, jobs.LevelWarning, fmt.Sprintf("Still running, no output for %d s.", int(quietAfter/time.Second)))
}

// recordStats stores the running job's counters once its outcome is recorded; a job that gave its lease back
// (another worker has it) records nothing.
func (w *Worker) recordStats(id int64) {
	if !w.statsCounting.Load() || w.Queue == nil {
		return
	}
	j, err := w.Queue.Get(id)
	if err != nil {
		return
	}
	switch j.Status {
	case jobs.Done, jobs.Failed, jobs.Kept:
	default:
		return
	}
	if err := w.Queue.RecordJobStats(id, int(w.passes.Load()), w.bytesWritten.Load()); err != nil {
		log.Printf("worker %s: job %d: recording its run counters: %v", w.ID, id, err)
	}
}

// summaryLine writes the job's last line: its verdict in words, the same verdict as summary.result (R1). A job
// that did not finish (queued again, or removed when it was skipped) has none.
func (w *Worker) summaryLine(id int64) {
	if w.Queue == nil {
		return
	}
	j, err := w.Queue.Get(id)
	if err != nil {
		return
	}
	switch j.Status {
	case jobs.Done:
		if j.SizeBefore > 0 && j.SizeAfter > 0 && j.SizeAfter < j.SizeBefore {
			saved := j.SizeBefore - j.SizeAfter
			w.logLine(id, secSummary, jobs.LevelInfo, fmt.Sprintf("Converted. Saved %s (%.1f%%).",
				rules.HumanBytes(saved), float64(saved)*100/float64(j.SizeBefore)))
		} else {
			w.logLine(id, secSummary, jobs.LevelWarning, "Converted. The new file is not smaller than the original.")
		}
	case jobs.Failed:
		w.logLine(id, secSummary, jobs.LevelError, strings.TrimSpace("Failed. "+j.Error))
	case jobs.Kept:
		w.logLine(id, secSummary, jobs.LevelWarning, strings.TrimSpace("Kept the original. "+j.Error))
	}
}
