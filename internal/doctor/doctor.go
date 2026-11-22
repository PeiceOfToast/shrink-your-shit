// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package doctor runs the setup checks (FEATURE-PLAN FR-3): one set of checks, used by first-run's
// folder check (POST /api/libraries/check), Settings > About (GET /api/system/health) and the
// `shrink-your-shit doctor` command. Every message says in plain words what is wrong and what to do.
package doctor

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// Issue is one finding: a blocker (the folder cannot be used) or a note (worth knowing).
type Issue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Folder and Reason: for no_default_folder, the parent folder and why (jobs.FolderProblem).
	Folder string `json:"folder,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// FolderReport is POST /api/libraries/check's answer (UI-API-CONTRACT.md line 21).
type FolderReport struct {
	OK         bool    `json:"ok"`
	Blockers   []Issue `json:"blockers"`
	Notes      []Issue `json:"notes"`
	VideoCount int     `json:"videoCount"`
	TotalBytes int64   `json:"totalBytes"`

	// free and need are the disk's free space and the conversion gate's need for the largest video
	// (worker.SpaceNeeded), when the disk could be measured: the Doctor's space tiers use them.
	free, need uint64
	spaceKnown bool
}

// Tools is what was found of the converter.
type Tools struct {
	FFmpeg, FFprobe               string // paths; "" = not found
	FFmpegVersion, FFprobeVersion string
	X265, SSIM                    bool     // the HEVC encoder, the picture comparison filter
	Hardware                      []string // hardware HEVC encoders that encoded a test frame
	HardwareListed                []string // listed by ffmpeg but did not work here
	// Hardware10 are the working ones that also encoded a 10-bit (Main10, p010) frame: what HDR needs
	// on hardware (research 1.2; used from V4).
	Hardware10 []string
	// Software are the software video encoders (one per format: libx265, libx264, libsvtav1,
	// libvpx-vp9) that encoded a test frame; SoftwareListed the listed ones that did not (V4a: a worker's
	// capabilities come from real test encodes, never from a list).
	Software, SoftwareListed []string
	TestedAt                 time.Time // when the encoders were tested (zero: not yet)
	// Devices is each graphics family's measured record (decode, scale), from the same test run as the
	// encoders; empty when none was measured.
	Devices []DeviceRecord
}

// softwareEncoders are the formats' software encoders, tested like the hardware ones.
var softwareEncoders = []string{"libx265", "libx264", "libsvtav1", "libvpx-vp9"}

// hardwareEncoders are the graphics encoders tested, in this order: per family HEVC, then H.264, then AV1
// (HEVC first, so a family that has it keeps it as the default primary encoder).
// The families with a profile come from it; VideoToolbox has none, so the worker never uses it: it is found and
// reported, not used.
var hardwareEncoders = append(rules.GraphicsEncoderNames(),
	"hevc_videotoolbox", "h264_videotoolbox", "av1_videotoolbox",
)

// has10Bit says whether enc gets the 10-bit test: HEVC and AV1 only (H.264 has no 10-bit profile here). It asks
// about the encoder, not the job: that is rules.NeedsTenBit.
func has10Bit(enc string) bool {
	return strings.HasPrefix(enc, "hevc_") || strings.HasPrefix(enc, "av1_")
}

// withEncoders is t with the encoder test's result hw, made at at.
func (t Tools) withEncoders(hw [5][]string, at time.Time) Tools {
	t.Hardware, t.HardwareListed, t.Hardware10, t.Software, t.SoftwareListed, t.TestedAt = hw[0], hw[1], hw[2], hw[3], hw[4], at
	return t
}

// Converts reports whether conversions can run: ffmpeg, ffprobe, libx265 and the SSIM filter.
func (t Tools) Converts() bool { return t.FFmpeg != "" && t.FFprobe != "" && t.X265 && t.SSIM }

// Checker runs the checks. FFmpeg and FFprobe are the resolved paths ("" when not found).
type Checker struct {
	Exec            media.Exec
	FFmpeg, FFprobe string
	// FreeSpace reports the writable bytes on the volume holding dir; nil = the real thing.
	FreeSpace func(dir string) (uint64, error)
	// KnownOriginals are the kept originals the database knows (jobs.Store.KnownOriginals); nil: the
	// check for orphaned ones is skipped (no database at hand).
	KnownOriginals func() (map[string]bool, error)
	// WaitsForShare reports a library folder on a saved share that is not connected yet: its orphans are not
	// listed until it is (nil: every library is checked).
	WaitsForShare func(path string) bool
	// JobOutput is jobs.Store.OutputOfJob: it tells a file Convert again moved aside from a real orphan
	// (nil: every orphan gets the orphan wording).
	JobOutput func(id int64) (string, bool, error)
	// OnTested, when set, gets each hardware test run's results once it is stored (the database keeps them;
	// nil: nothing is kept). It is called without the lock.
	OnTested func(at time.Time, results []EncoderResult)

	mu      sync.Mutex
	tools   *Tools // the converter, without the hardware test
	toolsAt time.Time
	// known is the last complete result, readable while mu is held by a check: a caller that asks
	// "now" during a refresh gets what was found before, not an empty answer (user bug 11-20: Quick
	// Sync had passed and the card was idle, yet the processor was chosen with no reason, because
	// ToolsNow answered "nothing" for the moment the check held mu).
	known atomic.Pointer[Tools]
	hw    *[5][]string // hardware: working, listed-but-failing, 10-bit; software: working, failing
	hwAt  time.Time
	// refreshing: ToolsNow started a background check that has not finished
	refreshing bool
	// hwRun is closed when the hardware test in flight ends (nil: none runs): one at a time
	hwRun chan struct{}
	// run collects the 8-bit test encodes of the hardware test in flight (nil: none); probes are the last
	// stored run's, by encoder, for the Doctor page.
	run    map[string]Probe
	probes map[string]Probe
	// decodeOK is Decodes' cache: "family codec bits" -> whether the family's hardware decoder reads it.
	decodeOK map[string]bool
	// dev is the device record of the test whose result is in hw (nil: none measured).
	dev []DeviceRecord
}

const toolsTTL = 10 * time.Minute

// Tools checks the converter, hardware encoders included (cached for ten minutes: the hardware test
// encodes take a moment).
func (c *Checker) Tools(ctx context.Context) Tools {
	t, _ := c.tested(ctx, toolsTTL)
	return t
}

// tested is Tools with each encoder's test encode, and a stored test result older than maxAge is run again.
//
// The hardware test encodes run without the lock (it is taken only to read and store the result), so
// Converter, ToolsNow and the startup check never wait for them (review 0605262).
func (c *Checker) tested(ctx context.Context, maxAge time.Duration) (Tools, map[string]Probe) {
	t := c.Converter(ctx)
	c.mu.Lock()
	for {
		if c.hw != nil && time.Since(c.hwAt) < maxAge { // fresh: shared
			hw, at, probes, dev := *c.hw, c.hwAt, c.probes, c.dev
			c.mu.Unlock()
			return t.withEncoders(hw, at).withDevices(dev), probes
		}
		if run := c.hwRun; run != nil { // one test at a time (GPU session limits, review c66c66b): wait for it
			c.mu.Unlock()
			select {
			case <-run:
			case <-ctx.Done():
				return t, nil // no hardware result for this caller; the running test still stores its own
			}
			c.mu.Lock()
			continue
		}
		run := make(chan struct{})
		c.hwRun = run
		c.run = map[string]Probe{}
		c.mu.Unlock()
		hw := c.hardwareTest(ctx, t)
		c.mu.Lock()
		at := time.Now()
		probes := c.run
		c.run = nil
		stored := ctx.Err() == nil // a test cut short by its caller is not a result: the next caller runs it again
		if stored {
			// The encoder answer is stored before the device probes run (they can take up to 90 s), so the
			// encoder list is never "not finished" while they run. The device record follows in its own lock, and the
			// previous one stays served until the new pass finishes (no blank card on a refresh).
			c.hw, c.hwAt, c.probes = &hw, at, probes
			c.publishLocked()
		}
		c.mu.Unlock()
		if stored && c.OnTested != nil {
			c.OnTested(at, resultsOf(hw))
		}
		// hwRun stays set through the device probes: a second hardware test must not start while the card is in use.
		dev := c.deviceTest(ctx, hw[0], hw[2])
		c.mu.Lock()
		if stored && ctx.Err() == nil {
			c.dev = dev
			c.publishLocked()
		} else if stored {
			c.hw = nil // the device pass was cut short: the next caller runs the whole test again
		}
		c.hwRun = nil
		close(run)
		c.mu.Unlock()
		return t.withEncoders(hw, at).withDevices(dev), probes
	}
}

// Probes is the last test run's results by encoder (a copy): what failed and ffmpeg's last error line for it.
// Empty when no test ran. The job log's encoder choice quotes a failed test's reason from it (log-spec R5).
func (c *Checker) Probes() map[string]Probe {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]Probe, len(c.probes))
	for k, v := range c.probes {
		out[k] = v
	}
	return out
}

// Probe is one encoder's test encode: how long it took, and ffmpeg's last error line when it failed.
type Probe struct {
	OK   bool
	Took time.Duration
	Err  string
}

// EncoderResult is one encoder's result in a test run: Passed when it encoded a test frame; Detail "10-bit"
// for a working hardware encoder that also did 10-bit.
type EncoderResult struct {
	Encoder string
	Passed  bool
	Detail  string
}

// results lists the test run's encoders: the working ones (hardware, then software), then the listed ones
// that did not work.
func resultsOf(hw [5][]string) []EncoderResult {
	var out []EncoderResult
	ten := map[string]bool{}
	for _, e := range hw[2] {
		ten[e] = true
	}
	for _, e := range hw[0] {
		d := ""
		if ten[e] {
			d = "10-bit"
		}
		out = append(out, EncoderResult{Encoder: e, Passed: true, Detail: d})
	}
	for _, e := range hw[3] {
		out = append(out, EncoderResult{Encoder: e, Passed: true})
	}
	for _, e := range hw[1] {
		out = append(out, EncoderResult{Encoder: e})
	}
	for _, e := range hw[4] {
		out = append(out, EncoderResult{Encoder: e})
	}
	return out
}

// hardwareTest finds the hardware HEVC encoders ffmpeg lists and tries each on one tiny frame (listed
// is not the same as usable here): working ones, and listed ones that did not work. Run without the
// lock, one at a time (Tools).
func (c *Checker) hardwareTest(ctx context.Context, t Tools) (hw [5][]string) {
	if t.FFmpeg == "" {
		return hw
	}
	if out, _ := c.Exec.Output(ctx, t.FFmpeg, "-hide_banner", "-encoders"); len(out) > 0 {
		for _, enc := range hardwareEncoders {
			if strings.Contains(string(out), " "+enc+" ") {
				if c.encodes(ctx, enc, false) {
					hw[0] = append(hw[0], enc)
					if has10Bit(enc) && c.encodes(ctx, enc, true) {
						hw[2] = append(hw[2], enc)
					}
				} else {
					hw[1] = append(hw[1], enc)
				}
			}
		}
		for _, enc := range softwareEncoders { // V4a: the software ones too, the same way
			if strings.Contains(string(out), " "+enc+" ") {
				if c.encodes(ctx, enc, false) {
					hw[3] = append(hw[3], enc)
				} else {
					hw[4] = append(hw[4], enc)
				}
			}
		}
	}
	return hw
}

// Retest runs the encoder test again now (POST /api/v2/workers/{id}/capabilities/test), or waits for
// the one already running, and returns the result.
func (c *Checker) Retest(ctx context.Context) Tools {
	c.mu.Lock()
	if c.hwRun == nil {
		c.hw = nil
	}
	c.decodeOK = nil // "test again" re-probes decode too, not just the encoders
	c.mu.Unlock()
	return c.Tools(ctx)
}

// ToolsNow is Tools without waiting: the last result, or ok false when none is known yet. A missing or
// stale result is refreshed in the background, one refresh at a time: a page that only shows the graphics
// card never waits for the hardware test's encodes (S2, ui-shop's 1.5 s stop answer).
//
// While a check holds the lock its answer is the last complete result, not "nothing", and a Retest that has
// cleared the hardware result answers the same way until its test stores a new one: a caller that takes
// "nothing" for an empty capability list would pick the processor on a computer whose card passed (user
// bug 11-20: Quick Sync passed, card idle, "Chosen: x265 (processor)" with no reason).
func (c *Checker) ToolsNow() (Tools, bool) {
	if !c.mu.TryLock() {
		if t := c.known.Load(); t != nil {
			return *t, true // a check is running: what was found before still stands until it stores its own
		}
		return Tools{}, false
	}
	t, ok := Tools{}, c.tools != nil && c.hw != nil
	stale := !ok || time.Since(c.hwAt) >= toolsTTL || time.Since(c.toolsAt) >= toolsTTL
	if ok {
		t = c.tools.withEncoders(*c.hw, c.hwAt).withDevices(c.dev)
	} else if k := c.known.Load(); k != nil {
		// A Retest cleared the hardware result while its test runs: until the new result is stored, the
		// last complete one stands (the same answer a caller gets while the lock is held).
		t, ok = *k, true
	}
	start := stale && !c.refreshing
	if start {
		c.refreshing = true
	}
	c.mu.Unlock()
	if start {
		go func() {
			c.Tools(context.Background())
			c.mu.Lock()
			c.refreshing = false
			c.mu.Unlock()
		}()
	}
	return t, ok
}

// Converter checks ffmpeg, ffprobe, libx265 and the SSIM filter, without the hardware test: quick
// enough for startup. Cached for ten minutes.
func (c *Checker) Converter(ctx context.Context) Tools {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tools != nil && time.Since(c.toolsAt) < toolsTTL {
		return *c.tools
	}
	t := Tools{FFmpeg: c.FFmpeg, FFprobe: c.FFprobe}
	version := regexp.MustCompile(`version (\S+)`)
	if t.FFmpeg != "" {
		if out, err := c.Exec.Output(ctx, t.FFmpeg, "-hide_banner", "-version"); err == nil || len(out) > 0 {
			if m := version.FindSubmatch(out); m != nil {
				t.FFmpegVersion = string(m[1])
			}
		}
		if out, _ := c.Exec.Output(ctx, t.FFmpeg, "-hide_banner", "-encoders"); len(out) > 0 {
			t.X265 = strings.Contains(string(out), " libx265 ")
		}
		if out, _ := c.Exec.Output(ctx, t.FFmpeg, "-hide_banner", "-filters"); len(out) > 0 {
			t.SSIM = regexp.MustCompile(`(?m)^\s*\S+\s+ssim\s`).Match(out)
		}
	}
	if t.FFprobe != "" {
		if out, err := c.Exec.Output(ctx, t.FFprobe, "-hide_banner", "-version"); err == nil || len(out) > 0 {
			if m := version.FindSubmatch(out); m != nil {
				t.FFprobeVersion = string(m[1])
			}
		}
	}
	c.tools, c.toolsAt = &t, time.Now()
	c.publishLocked()
	return t
}

// publishLocked keeps the copy ToolsNow hands out while a check holds the lock current: the last result
// with both parts known. Called with the lock held.
func (c *Checker) publishLocked() {
	if c.tools == nil || c.hw == nil {
		return
	}
	t := c.tools.withEncoders(*c.hw, c.hwAt).withDevices(c.dev)
	c.known.Store(&t)
}

// encodes tries one tiny frame with a hardware encoder (10-bit: Main10 from p010): listed in ffmpeg is
// not the same as usable here.
func (c *Checker) encodes(ctx context.Context, enc string, tenBit bool) bool {
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	start := time.Now()
	_, err := c.Exec.Output(tctx, c.FFmpeg, encodeArgs(enc, tenBit, media.VAAPIDevice())...)
	if !tenBit && c.run != nil { // the hardware test's run, read by Tools (the Doctor page shows it)
		p := Probe{OK: err == nil, Took: time.Since(start)}
		if err != nil {
			p.Err = lastErrorLine(err)
		}
		c.run[enc] = p
	}
	return err == nil
}

// decodeSamples are the sources Decodes can make a sample of: the software encoder and the demuxer's name.
var decodeSamples = map[string][2]string{"h264": {"libx264", "h264"}, "hevc": {"libx265", "hevc"}}

// Decodes says whether the graphics family's hardware decoder reads codec at this bit depth here (the full
// GPU pipeline needs it): a one-frame sample is made in software and decoded on the device. Cached.
func (c *Checker) Decodes(ctx context.Context, fam, codec string, tenBit bool) bool {
	key := fmt.Sprintf("%s %s %t", fam, codec, tenBit)
	c.mu.Lock()
	ok, seen := c.decodeOK[key]
	c.mu.Unlock()
	if seen {
		return ok
	}
	ok, cacheable := c.decodes(ctx, fam, codec, tenBit)
	if !cacheable {
		return ok // a cancel or the timeout says nothing about the device: ask again next time
	}
	c.mu.Lock()
	if c.decodeOK == nil {
		c.decodeOK = map[string]bool{}
	}
	c.decodeOK[key] = ok
	c.mu.Unlock()
	return ok
}

// decodes makes the sample and decodes it on the family's hardware. cacheable is false when the answer may be
// transient: the context was cancelled or the probe timed out, so a false says nothing about the device.
func (c *Checker) decodes(ctx context.Context, fam, codec string, tenBit bool) (ok, cacheable bool) {
	return c.onDevice(ctx, fam, codec, tenBit, "")
}

// onDevice makes the sample and decodes it on the family's hardware, then runs vf on the device's frames ("" for
// none): the probe behind Decodes and the scale test. cacheable is as for decodes.
func (c *Checker) onDevice(ctx context.Context, fam, codec string, tenBit bool, vf string) (ok, cacheable bool) {
	s, known := decodeSamples[codec]
	full, has := rules.ProfileFor("hevc_" + fam) // only a family with a full graphics pipeline decodes here
	if !known || c.FFmpeg == "" || !has || !full.FullPipeline() {
		return false, true
	}
	dir, err := os.MkdirTemp("", "decode-probe")
	if err != nil {
		return false, false
	}
	defer os.RemoveAll(dir)
	sample := filepath.Join(dir, "sample."+s[1])
	pix := "yuv420p"
	if tenBit {
		pix = "yuv420p10le"
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	interrupted := func() bool { return ctx.Err() != nil || tctx.Err() != nil }
	if _, err := c.Exec.Output(tctx, c.FFmpeg, "-hide_banner", "-v", "error", "-f", "lavfi", "-i",
		"color=c=black:s=256x256:d=0.1", "-frames:v", "1", "-pix_fmt", pix, "-c:v", s[0], "-f", s[1], sample); err != nil {
		return false, !interrupted()
	}
	if _, err := c.Exec.Output(tctx, c.FFmpeg, decodeArgsWith(fam, media.VAAPIDevice(), sample, vf)...); err != nil {
		return false, !interrupted()
	}
	return true, true
}

// decodeArgs decodes in on the family's hardware and discards the frames: the same device setup the full
// GPU pipeline uses (rules.FFmpegArgsFullGPU), without the encode.
func decodeArgs(fam, device, in string) []string {
	return decodeArgsWith(fam, device, in, "")
}

// decodeArgsWith is decodeArgs with the video filter vf run on the decoded frames before they are discarded
// ("" for none).
func decodeArgsWith(fam, device, in, vf string) []string {
	args := []string{"-hide_banner", "-v", "error"}
	p, _ := rules.ProfileFor("hevc_" + fam) // the family's set-up and full-pipeline decode (QSV, VAAPI)
	args = append(args, p.Setup(device)...)
	args = append(args, p.FullDecode()...)
	args = append(args, "-i", in)
	if vf != "" {
		args = append(args, "-vf", vf)
	}
	return append(args, "-f", "null", "-")
}

// lastErrorLine is the last line of an ffmpeg failure, without the "ffmpeg: exit status 1:" prefix.
func lastErrorLine(err error) string {
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if i := strings.LastIndex(line, ": "); i >= 0 {
		line = line[i+2:]
	}
	if line == "" {
		return "no reason given"
	}
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}

// encodeArgs is the test encode for enc's family (research 1.2): VAAPI must open its device and upload
// the frames (format=nv12,hwupload; the plain command fails on a working VAAPI box), QSV opens its own;
// the others take software frames as they are. 10-bit asks for Main10 from p010 frames.
func encodeArgs(enc string, tenBit bool, device string) []string {
	return append(encodeInput(enc, tenBit, device), "-f", "null", "-")
}

// encodeInput is encodeArgs without its output: the input and the encoder, for a caller that writes a file.
func encodeInput(enc string, tenBit bool, device string) []string {
	args := []string{"-hide_banner", "-v", "error"}
	p, _ := rules.ProfileFor(enc) // the family's set-up: VAAPI opens its device, QSV its own
	args = append(args, p.Setup(device)...)
	args = append(args, "-f", "lavfi", "-i", "color=c=black:s=256x256:d=0.1", "-frames:v", "1")
	switch up := p.Upload(tenBit); {
	case up != nil: // software frames uploaded to the device (VAAPI)
		args = append(args, "-vf", strings.Join(up, ","))
	case tenBit:
		args = append(args, "-pix_fmt", "p010le")
	}
	args = append(args, "-c:v", enc)
	if tenBit && strings.HasPrefix(enc, "hevc_") { // av1_* reject main10: their p010 frames are enough
		args = append(args, "-profile:v", "main10")
	}
	return args
}

func (c *Checker) freeSpace(dir string) (uint64, error) {
	if c.FreeSpace != nil {
		return c.FreeSpace(dir)
	}
	return worker.DiskFree(dir)
}

// Folder checks a folder for use as a library: it can be opened and written by the user the program
// runs as, it holds videos, its disk has room for converting the largest one, and the converter works.
// It only reads, except the write test's temp file, which is always removed.
func (c *Checker) Folder(ctx context.Context, path string) (r FolderReport) {
	r = FolderReport{Blockers: []Issue{}, Notes: []Issue{}}
	defer func() { r.OK = len(r.Blockers) == 0 }() // r is the named result, so this reaches the caller
	if real, err := jobs.RealPath(path); err == nil {
		path = real // a folder chosen through a link is checked as the folder it points to, as it is stored
	}
	if jobs.SystemFolder(path) { // never a library (slice 23): nothing else to check
		r.Blockers = append(r.Blockers, Issue{Code: "system_folder", Message: jobs.ErrSystemFolder.Error()})
		return r
	}
	st, err := os.Stat(path)
	if err == nil && !st.IsDir() {
		err = fmt.Errorf("not a folder")
	}
	if err == nil {
		_, err = os.ReadDir(path)
	}
	if err != nil {
		r.Blockers = append(r.Blockers, Issue{Code: "unreadable", Message: "We can't open this folder. Check the path, and that shrink-your-shit is allowed to read it."})
		return r
	}
	if err := writeProbe(path); err != nil {
		r.Blockers = append(r.Blockers, Issue{Code: "read_only", Message: ReadOnlyMessage(path)})
	}

	var largest int64
	hardlinked, unreadable := 0, 0
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if p != path && d != nil && d.IsDir() {
				unreadable++ // a folder inside that cannot be opened: its files will not be scanned
			}
			return nil
		}
		if d.IsDir() {
			if p != path && scanner.SkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !scanner.IsVideo(p) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // gone since the folder was listed
		}
		r.VideoCount++
		r.TotalBytes += info.Size()
		largest = max(largest, info.Size())
		if media.LinkCount(p, info) > 1 {
			hardlinked++
		}
		return nil
	})

	if r.VideoCount == 0 {
		r.Blockers = append(r.Blockers, Issue{Code: "no_videos", Message: "We found no videos here. Pick the folder that holds them."})
	} else if free, err := c.freeSpace(path); err != nil {
		// Not silent: converting needs to know the space, and stops a file when it cannot.
		r.Notes = append(r.Notes, Issue{Code: "space_unknown", Message: "We couldn't check how much space is free on this disk. Files here are only converted when there is room, so check that the disk is working."})
	} else {
		r.free, r.need, r.spaceKnown = free, worker.SpaceNeeded(largest), true
		if free < r.need {
			r.Blockers = append(r.Blockers, Issue{Code: "low_space", Message: fmt.Sprintf(
				"Only %s free on this disk. Free up space (about %s is enough to start), then press Check again.", gb(free), gb(r.need))})
		}
	}
	tools := c.Tools(ctx)
	if !tools.Converts() {
		r.Blockers = append(r.Blockers, Issue{Code: "no_converter", Message: "The converter is not installed correctly."})
	}
	// Messages here are shown as they are in first-run: plain words only (DESIGN/ACCEPTANCE.md COPY-1,
	// FIRST-RUN.md "States and errors"). The health report's details may be technical.
	if c.KnownOriginals != nil && (c.WaitsForShare == nil || !c.WaitsForShare(path)) {
		if known, err := c.KnownOriginals(); err == nil {
			var moved []string
			for _, o := range OrphanedOriginals(path, known, c.JobOutput) {
				if o.Files > 0 {
					r.Notes = append(r.Notes, Issue{Code: "orphaned_originals", Message: o.Message()})
				}
				moved = append(moved, o.Moved...)
			}
			if len(moved) > 0 { // one note for the library: its check ID is per code
				r.Notes = append(r.Notes, Issue{Code: "moved_aside_outputs", Message: MovedAsideNote(moved)})
			}
		}
	}
	if hardlinked == 1 {
		r.Notes = append(r.Notes, Issue{Code: "hardlinks", Message: "1 file is shared with another folder (for example by Radarr). We will leave it alone, because changing it would break the link."})
	} else if hardlinked > 1 {
		r.Notes = append(r.Notes, Issue{Code: "hardlinks", Message: fmt.Sprintf("%d files are shared with another folder (for example by Radarr). We will leave them alone, because changing one would break the link.", hardlinked)})
	}
	if unreadable == 1 {
		// A note, not a blocker: a disk's root often holds a folder only the system may open
		// (lost+found), and the rest of the library is still usable.
		r.Notes = append(r.Notes, Issue{Code: "unreadable_folders", Message: "1 folder inside couldn't be read; files in it won't be converted."})
	} else if unreadable > 1 {
		r.Notes = append(r.Notes, Issue{Code: "unreadable_folders", Message: fmt.Sprintf("%d folders inside couldn't be read; files in them won't be converted.", unreadable)})
	}
	if len(tools.Hardware) == 0 {
		r.Notes = append(r.Notes, Issue{Code: "no_hw_encoder", Message: "Not found. That is fine."})
	}
	return r
}

// writeProbe creates and removes a file in dir, as the user this program runs as.
func writeProbe(dir string) error {
	f, err := os.CreateTemp(dir, ".syp-write-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// CanWrite reports whether the program can write a file in dir: the library access check's own probe, so
// the libraries list and the doctor agree.
func CanWrite(dir string) bool { return writeProbe(dir) == nil }

// ReadOnlyMessage is design's read-only copy (FIRST-RUN.md) followed by how to make the folder
// writable for the user the program runs as, on this system: the Linux service runs as "syp", the
// container as a numeric user, macOS and Windows as the person's own account.
func ReadOnlyMessage(path string) string {
	const lead = "This folder is read-only. Make it writable, then press Check again. "
	name := "this user"
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	switch {
	case runtime.GOOS == "windows":
		return lead + fmt.Sprintf("Right-click the folder, choose Properties > Security, and give your account (%s) the Modify permission; also check the folder is not marked Read-only.", name)
	case runtime.GOOS == "darwin":
		return lead + fmt.Sprintf("In Finder, select the folder, choose File > Get Info, and under Sharing & Permissions give your account (%s) Read & Write.", name)
	case inContainer():
		ids := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
		return lead + fmt.Sprintf("shrink-your-shit runs inside its container as user %s. On the host, make the folder you mounted writable for that user, for example: sudo chown -R %s <the host folder>", ids, ids)
	case name == "syp":
		return lead + fmt.Sprintf("shrink-your-shit runs as the user syp. On this server: sudo chown -R syp:syp %s (or add syp to the folder's group and give the group write access).", path)
	default:
		return lead + fmt.Sprintf("shrink-your-shit runs as the user %s. For example: sudo chown -R %s %s", name, name, path)
	}
}

func inContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// gb writes a byte count in GB, with one decimal under 10 GB.
func gb(n uint64) string {
	g := float64(n) / (1 << 30)
	if g < 10 {
		return fmt.Sprintf("%.1f GB", g)
	}
	return fmt.Sprintf("%.0f GB", g)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
