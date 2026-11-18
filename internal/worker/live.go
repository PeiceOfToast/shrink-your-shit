// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// LiveJob is what a converting file's card shows (S5, the Files view's processing card): where ffmpeg
// is and how the file is being converted. It lives in memory only, for as long as the encode runs:
// ffmpeg reports several times a second and nothing needs these after the job.
type LiveJob struct {
	Worker       string
	Started      time.Time
	ConvertingTo string // "HEVC · 10-bit · MKV", or "repackaged as MKV"
	Encoder      string // "x265 (processor)", or "copied, not encoded"
	Decoder      string // "H.264 (processor)"
	Progress     media.Progress
}

var live = struct {
	sync.Mutex
	jobs map[int64]LiveJob
}{jobs: map[int64]LiveJob{}}

func setLive(id int64, j LiveJob) {
	live.Lock()
	defer live.Unlock()
	live.jobs[id] = j
}

func updateLive(id int64, p media.Progress) {
	live.Lock()
	defer live.Unlock()
	if j, ok := live.jobs[id]; ok {
		j.Progress = p
		live.jobs[id] = j
	}
}

// ReportLive puts a remote worker's report of job id's encode into its Live entry (percent and
// position; the report has no speed or fps): every reader of Live then shows it.
func ReportLive(id int64, percent, outTimeUS float64) {
	live.Lock()
	defer live.Unlock()
	if j, ok := live.jobs[id]; ok {
		j.Progress.Percent, j.Progress.OutTimeUS = percent, outTimeUS
		live.jobs[id] = j
	}
}

// setLiveEncoder changes the encoder words of a job that is running (a remote worker's own choice).
func setLiveEncoder(id int64, words string) {
	live.Lock()
	defer live.Unlock()
	if j, ok := live.jobs[id]; ok {
		j.Encoder = words
		live.jobs[id] = j
	}
}

func dropLive(id int64) {
	live.Lock()
	defer live.Unlock()
	delete(live.jobs, id)
}

// Live is the running encode of job id, if any worker in this program is converting it now.
func Live(id int64) (LiveJob, bool) {
	live.Lock()
	defer live.Unlock()
	j, ok := live.jobs[id]
	return j, ok
}

// codecWords are source codecs in plain words.
var codecWords = map[string]string{"h264": "H.264", "hevc": "HEVC", "mpeg2video": "MPEG-2", "mpeg4": "MPEG-4",
	"vc1": "VC-1", "msmpeg4v3": "DivX", "wmv3": "WMV", "av1": "AV1", "vp9": "VP9", "mpeg1video": "MPEG-1"}

func codecName(c string) string {
	if w, ok := codecWords[c]; ok {
		return w
	}
	return strings.ToUpper(c)
}

// convertingTo says what the new file will be: "HEVC · 10-bit · MKV" ("repackaged as MKV" when the
// video is copied).
func convertingTo(plan rules.Plan, src media.Info) string {
	kind := plan.Container
	if kind == "" { // the original's type
		kind = strings.TrimPrefix(strings.ToLower(src.Container), ".")
		if i := strings.IndexByte(kind, ','); i >= 0 {
			kind = kind[:i]
		}
		if kind == "matroska" {
			kind = "mkv"
		}
		if kind == "mov" {
			kind = "mp4"
		}
		if src.Ext == ".webm" { // ffprobe names WebM matroska (#97 QA)
			kind = "webm"
		}
	}
	if kind == "webm" {
		kind = "WebM"
	} else {
		kind = strings.ToUpper(kind)
	}
	if plan.Video.Copy {
		return "repackaged as " + kind
	}
	depth := "8-bit"
	if v := src.Video(); plan.Video.HDR != nil || (v != nil && v.BitDepth > 8) {
		depth = "10-bit"
	}
	name := "HEVC" // a Plan from before S6 has no Format
	if f, ok := rules.VideoFormats[plan.Video.Format]; ok {
		name = f.Name
	}
	return name + " · " + depth + " · " + kind
}

func encoderWords(plan rules.Plan) string {
	if plan.Video.Copy {
		return "copied, not encoded"
	}
	if w, ok := encoderNames[plan.Video.Codec]; ok {
		return w + " (processor)"
	}
	return plan.Video.Codec
}

// encoderWordsUsed is encoderWords for the encoder that actually runs: gpu is a graphics encoder's
// ffmpeg name, or "" for the processor.
func encoderWordsUsed(plan rules.Plan, gpu string) string {
	label, ok := graphicsNames["hevc_"+rules.GraphicsFamily(gpu)] // the family's name, whichever format it encodes
	if gpu == "" || plan.Video.Copy || !ok {
		return encoderWords(plan)
	}
	if plan.Video.Codec == "libx265" {
		return label
	}
	return label + ", " + formatWords[plan.Video.Codec]
}

// formatWords are the non-HEVC graphics formats as people name them.
var formatWords = map[string]string{"libx264": "H.264", "libsvtav1": "AV1"}

// graphicsNames are the graphics families as people name them, by their HEVC encoder.
var graphicsNames = map[string]string{"hevc_nvenc": "NVIDIA NVENC (graphics)", "hevc_qsv": "Intel Quick Sync (graphics)",
	"hevc_vaapi": "VA-API (graphics)", "hevc_amf": "AMD AMF (graphics)"}

// encoderNames are ffmpeg's software encoders as people name them (S5 proposal).
var encoderNames = map[string]string{"libx265": "x265", "libx264": "x264", "libsvtav1": "SVT-AV1", "libvpx-vp9": "VP9"}

func decoderWords(src media.Info) string {
	if v := src.Video(); v != nil {
		return codecName(v.Codec) + " (processor)"
	}
	return ""
}

// progressLines writes the run log's progress lines (design, REDESIGN C Log panel, 03:40Z): one at each
// 10% of the conversion, 10% to 90% (the "converted" step says when it is done), at most one per
// progress point, so a jump past several marks writes only the highest.
type progressLines struct{ done int } // the last tenth written

func (l *progressLines) next(p media.Progress) string {
	t := min(int(p.Percent)/10, 9)
	if t <= l.done {
		return ""
	}
	l.done = t
	return progressWords(t*10, p.ETASeconds)
}

// progressWords: "40% done · about 22 min left", with etaSeconds's time in minutes ("about 1 min left"
// under 2 min); only "40% done" while the time left is unknown (negative).
func progressWords(pct int, etaSeconds float64) string {
	s := strconv.Itoa(pct) + "% done"
	switch {
	case etaSeconds < 0:
		return s
	case etaSeconds < 120:
		return s + " · about 1 min left"
	}
	return s + " · about " + strconv.Itoa(int(math.Round(etaSeconds/60))) + " min left"
}

// LiveIDs are the jobs some worker in this program is converting now (the live stream's progress, V3b).
func LiveIDs() []int64 {
	live.Lock()
	defer live.Unlock()
	ids := make([]int64, 0, len(live.jobs))
	for id := range live.jobs {
		ids = append(ids, id)
	}
	return ids
}

// SetLiveForTest puts a running encode in the registry (tests of what reads it).
func SetLiveForTest(id int64, j LiveJob) { setLive(id, j) }

// DropLiveForTest takes it out again.
func DropLiveForTest(id int64) { dropLive(id) }
