// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"strings"
	"time"
)

// Doctor is GET /api/v2/doctor (UI-API-CONTRACT-v2.md "Doctor"): the checks, and the encoder the local
// worker converts with now.
type Doctor struct {
	Version          string        `json:"version"`
	RanAt            string        `json:"ranAt"`
	EncodersTestedAt *string       `json:"encodersTestedAt"`
	Encoder          DoctorEncoder `json:"encoder"`
	Checks           []DoctorCheck `json:"checks"`
}

// DoctorEncoder names the encoder conversions use: "libx265" or a graphics encoder such as "hevc_qsv".
type DoctorEncoder struct {
	Name     string `json:"name"`
	Hardware bool   `json:"hardware"`
	Label    string `json:"label"`
}

// DoctorCheck is one line of the Doctor page. Hint is nil when the check is ok.
type DoctorCheck struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Status string  `json:"status"` // ok | warn | bad | absent (absent: the hardware isn't here; counts as nothing)
	Detail string  `json:"detail"`
	Hint   *string `json:"hint"`
}

// APIInput is what the Doctor needs beside the checker: the app version, the data folder, the libraries,
// the encoder the local worker converts with now ("" means libx265), and Device, which says whether a
// graphics family's device is on this computer ("found", "missing", or "" when not checked).
type APIInput struct {
	Version, DataDir string
	Libs             []Library
	InUse            string
	Device           func(enc string) string
}

// graphicsWords are the graphics families as people name them, by family (the suffix of hevc_qsv).
var graphicsWords = map[string]string{"qsv": "Intel Quick Sync", "nvenc": "NVIDIA NVENC", "vaapi": "VA-API",
	"amf": "AMD AMF", "videotoolbox": "VideoToolbox"}

// notFoundWords say a graphics encoder the platform could have is not on this computer.
var notFoundWords = map[string]string{"qsv": "No Intel graphics found.", "nvenc": "No NVIDIA card found.",
	"vaapi": "No VA-API graphics found.", "amf": "No AMD card found.", "videotoolbox": "No VideoToolbox encoder found."}

// encoderHints say what to do when a present encoder fails its test.
var encoderHints = map[string]string{"qsv": "Give the container /dev/dri (see README, Hardware).",
	"vaapi": "Give the container /dev/dri (see README, Hardware).",
	"nvenc": "Check the NVIDIA driver, and that the container can use the card (see README, Hardware).",
	"amf":   "Check the AMD driver.", "videotoolbox": "Check the macOS version, and that ffmpeg was built with VideoToolbox."}

// cpuWords are the software encoders as the Doctor names them.
var cpuWords = map[string]string{"libx265": "CPU (x265)", "libx264": "CPU (x264)", "libsvtav1": "CPU (SVT-AV1)", "libvpx-vp9": "CPU (VP9)"}

// platformFamilies are the graphics families this computer could have (the Doctor checks only these).
func platformFamilies() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"videotoolbox"}
	case "windows":
		return []string{"qsv", "nvenc", "amf"}
	}
	return []string{"qsv", "nvenc", "vaapi"}
}

// family is an encoder's graphics family ("qsv" for hevc_qsv); "" for a processor encoder.
func family(enc string) string {
	if i := strings.LastIndex(enc, "_"); i >= 0 {
		if _, ok := graphicsWords[enc[i+1:]]; ok {
			return enc[i+1:]
		}
	}
	return ""
}

// API builds the Doctor page. retest runs the encoder test again; otherwise its result is kept until then.
// The fast checks run on every call.
func (c *Checker) API(ctx context.Context, in APIInput, retest bool) Doctor {
	if retest {
		c.Retest(ctx)
	}
	t, probes := c.tested(ctx, time.Duration(math.MaxInt64))
	inUse := in.InUse
	if inUse == "" {
		inUse = "libx265"
	}
	d := Doctor{Version: in.Version, RanAt: time.Now().Format(time.RFC3339), Encoder: encoderOf(inUse)}
	if !t.TestedAt.IsZero() {
		s := t.TestedAt.Format(time.RFC3339)
		d.EncodersTestedAt = &s
	}
	d.Checks = toolChecks(t)
	d.Checks = append(d.Checks, encoderChecks(in, probes, inUse)...)
	d.Checks = append(d.Checks, dataCheck(in.DataDir))
	if len(in.Libs) == 0 {
		d.Checks = append(d.Checks, DoctorCheck{ID: "libraries", Label: "Libraries", Status: "warn",
			Detail: "No libraries yet. Add one on the Libraries page."})
	}
	for _, l := range in.Libs {
		d.Checks = append(d.Checks, c.libraryChecks(ctx, l)...)
	}
	return d
}

// encoderOf names an encoder for the page: its label is the graphics family's name, or the processor's.
func encoderOf(enc string) DoctorEncoder {
	if fam := family(enc); fam != "" {
		return DoctorEncoder{Name: enc, Hardware: true, Label: graphicsWords[fam]}
	}
	label := cpuWords[enc]
	if label == "" {
		label = enc
	}
	return DoctorEncoder{Name: enc, Label: label}
}

func hint(s string) *string { return &s }

func toolChecks(t Tools) []DoctorCheck {
	const fix = "Install the full ffmpeg build; the app can download one in Settings -> System."
	var out []DoctorCheck
	if t.FFmpeg != "" {
		out = append(out, DoctorCheck{ID: "ffmpeg", Label: "ffmpeg", Status: "ok", Detail: "ffmpeg " + t.FFmpegVersion + " found."})
	} else {
		out = append(out, DoctorCheck{ID: "ffmpeg", Label: "ffmpeg", Status: "bad", Detail: "ffmpeg is not found.", Hint: hint(fix)})
	}
	if t.FFprobe != "" {
		out = append(out, DoctorCheck{ID: "ffprobe", Label: "ffprobe", Status: "ok", Detail: "ffprobe " + t.FFprobeVersion + " found."})
	} else {
		out = append(out, DoctorCheck{ID: "ffprobe", Label: "ffprobe", Status: "bad", Detail: "ffprobe is not found.", Hint: hint(fix)})
	}
	if t.X265 {
		out = append(out, DoctorCheck{ID: "x265", Label: "HEVC encoder (libx265)", Status: "ok", Detail: "This ffmpeg has the HEVC encoder (x265)."})
	} else {
		out = append(out, DoctorCheck{ID: "x265", Label: "HEVC encoder (libx265)", Status: "bad", Detail: "This ffmpeg has no HEVC encoder (x265).", Hint: hint(fix)})
	}
	if t.SSIM {
		out = append(out, DoctorCheck{ID: "ssim", Label: "Picture check (ssim)", Status: "ok", Detail: "This ffmpeg has the picture-check filter (ssim)."})
	} else {
		out = append(out, DoctorCheck{ID: "ssim", Label: "Picture check (ssim)", Status: "bad", Detail: "This ffmpeg has no picture-check filter (ssim), which the safety check needs.", Hint: hint(fix)})
	}
	return out
}

// encoderChecks has one check per graphics family the platform could have, then whether conversions use
// one. A family that ffmpeg lists is test-encoded; a present device that fails the test is bad, an absent
// one is absent (no warning).
func encoderChecks(in APIInput, probes map[string]Probe, inUse string) []DoctorCheck {
	var out []DoctorCheck
	var passed []string
	for _, fam := range platformFamilies() {
		enc := "hevc_" + fam
		word := graphicsWords[fam]
		id, label := "encoder:"+fam, "Graphics encoder: "+word
		p, listed := probes[enc]
		device := ""
		if in.Device != nil {
			device = in.Device(enc)
		}
		switch {
		case listed && p.OK:
			passed = append(passed, fam)
			out = append(out, DoctorCheck{ID: id, Label: label, Status: "ok",
				Detail: fmt.Sprintf("%s encoded a test clip in %.1f s.", word, p.Took.Seconds())})
		case listed && device != "missing":
			out = append(out, DoctorCheck{ID: id, Label: label, Status: "bad",
				Detail: fmt.Sprintf("%s is present but the test encode failed: %s", word, p.Err), Hint: hint(encoderHints[fam])})
		default:
			detail := notFoundWords[fam]
			if device == "found" { // the device is there; ffmpeg just has no encoder for it
				detail = word + " is present, but ffmpeg has no " + enc + " encoder."
			}
			status := "warn"
			if device == "missing" { // no card here: nothing to fix, so it isn't a warning
				status = "absent"
				detail += " That is fine."
			}
			out = append(out, DoctorCheck{ID: id, Label: label, Status: status, Detail: detail})
		}
	}
	cpu := "Conversions use the " + cpuWords["libx265"] + "."
	switch {
	case family(inUse) != "":
		out = append(out, DoctorCheck{ID: "encoder:in-use", Label: "Encoder in use", Status: "ok",
			Detail: "Conversions use " + graphicsWords[family(inUse)] + "."})
	case len(passed) > 0:
		out = append(out, DoctorCheck{ID: "encoder:in-use", Label: "Encoder in use", Status: "warn",
			Detail: fmt.Sprintf("Conversions use the CPU (x265); %s passed its test.", graphicsWords[passed[0]]),
			Hint:   hint("Turn on hardware encoding in the workflow's Convert step.")})
	default:
		out = append(out, DoctorCheck{ID: "encoder:in-use", Label: "Encoder in use", Status: "ok", Detail: cpu})
	}
	return out
}

func dataCheck(dir string) DoctorCheck {
	if err := writeProbe(dir); err != nil {
		return DoctorCheck{ID: "data", Label: "Data folder", Status: "bad",
			Detail: "Cannot write to " + dir + ". " + ReadOnlyMessage(dir), Hint: hint("Give the app's user write access to the data folder.")}
	}
	return DoctorCheck{ID: "data", Label: "Data folder", Status: "ok", Detail: "Data folder is writable."}
}

// libraryChecks has the library's access, space and hard-link checks (library:<id>:access and so on), and one
// more per other problem Folder reports. A check with no problem is ok.
func (c *Checker) libraryChecks(ctx context.Context, l Library) []DoctorCheck {
	r := c.Folder(ctx, l.Path)
	prefix := fmt.Sprintf("library:%d", l.ID)
	type slot struct {
		status, detail, hint string
		id, label, good      string
	}
	slots := map[string]*slot{
		"access": {id: prefix + ":access", label: "Library access", good: "readable and writable.",
			hint: "Give the app's user write access to the folder."},
		"space": {id: prefix + ":space", label: "Library free space", good: "enough free space.",
			hint: "Free space or point Temporary files at another disk."},
		"hardlinks": {id: prefix + ":hardlinks", label: "Hard-linked files", good: "no hard-linked files.",
			hint: "Leave them, or exclude the seeding folder."},
	}
	var extra []DoctorCheck
	rank := map[string]int{"ok": 0, "warn": 1, "bad": 2}
	where := l.Name + " (" + l.Path + ")" // the access and folder rows name the folder, so the person can find it
	add := func(issues []Issue, bad bool) {
		for _, is := range issues {
			if is.Code == "no_converter" || is.Code == "no_hw_encoder" { // reported by the tool checks
				continue
			}
			if is.Code == "low_space" { // the space tiers below say it, with the gate's own numbers
				continue
			}
			status := "warn"
			if bad {
				status = "bad"
			}
			key := map[string]string{"read_only": "access", "unreadable": "access", "system_folder": "access",
				"low_space": "space", "hardlinks": "hardlinks"}[is.Code]
			if key == "" {
				h := "Change the folder on the Libraries page, then press Check again."
				if is.Code == "orphaned_originals" {
					h = "Move them back or delete them, then press Check again."
				}
				if is.Code == "moved_aside_outputs" { // never "move back": that would overwrite the new conversion
					h = "Delete them when you no longer need them, then press Check again."
				}
				extra = append(extra, DoctorCheck{ID: prefix + ":" + is.Code, Label: "Library: " + l.Name, Status: status,
					Detail: where + ": " + is.Message, Hint: hint(h)})
				continue
			}
			s := slots[key]
			if s.status == "" || rank[status] > rank[s.status] {
				s.status = status
			}
			if s.detail != "" {
				s.detail += " "
			}
			if key == "access" {
				s.detail += where + ": " + is.Message
				switch is.Code { // each folder code says the access it lacks, or that the folder is the wrong one
				case "unreadable": // the app cannot read the folder at all
					s.hint = "Check that " + l.Path + " exists, and give the app's user read access to it."
				case "system_folder":
					s.hint = "Change the folder on the Libraries page, then press Check again."
				}
			} else {
				s.detail += l.Name + ": " + is.Message
			}
		}
	}
	add(r.Blockers, true)
	add(r.Notes, false)
	if r.spaceKnown && r.free < 2*r.need { // the conversion gate's need: bad where it refuses, warn below twice that
		st := "warn"
		if r.free < r.need {
			st = "bad"
		}
		s := slots["space"]
		if rank[st] > rank[s.status] {
			s.status = st
		}
		if s.detail != "" {
			s.detail += " "
		}
		s.detail += fmt.Sprintf("%s: %s free; the largest video here needs %s.", l.Name, gb(r.free), gb(r.need))
	}
	out := make([]DoctorCheck, 0, 3+len(extra))
	for _, key := range []string{"access", "space", "hardlinks"} {
		s := slots[key]
		ch := DoctorCheck{ID: s.id, Label: s.label, Status: "ok", Detail: l.Name + ": " + s.good}
		if s.status != "" {
			ch.Status, ch.Detail, ch.Hint = s.status, s.detail, hint(s.hint)
		}
		out = append(out, ch)
	}
	return append(out, extra...)
}
