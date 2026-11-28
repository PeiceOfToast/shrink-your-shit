// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/hw"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// The graphics fallback (L-HW-5, L-HW-7): within one attempt a file goes down the ladder — the
// full pipeline, then the hybrid, then the processor — and never back up. A card rung that failed
// on the encoder's account hands the file to the next rung; a failure the processor would hit too
// (a full disk, a refused write, a missing source, refused settings) ends the run instead.

// The failure codes the ladder acts on, in L-HW-14's order. They are internal to the run: the code
// a failed job stores comes later (the failure card's own work).
const (
	codeSettingsRejected   = "settings_rejected"
	codeEncoderUnavailable = "encoder_unavailable"
	codeNoSpace            = "no_space"
	codeNotWritable        = "output_not_writable"
	codeSourceMissing      = "source_missing"
	codeSourceDamaged      = "source_damaged"
	codeFFmpeg             = "ffmpeg_error"
)

// encoderUnavailablePhrases are the lines that say an encoder can't run here: the generic ones,
// then the graphics families' own ("error creating a mfx session" is QSV's, "no usable encoding
// entrypoint" VA-API's).
var encoderUnavailablePhrases = []string{
	"unknown encoder", "encoder not found", "error while opening encoder", "could not open encoder",
	"error initializing output stream", "no device", "device not available", "encoder not available",
	"error creating a mfx session", "no usable encoding entrypoint",
	"vainitialize failed", "no va display", "failed to initialise vaapi", "failed to initialize vaapi",
}

// sourceDamagePhrases are the lines that say the original is damaged, which the ladder never blames
// on the card.
var sourceDamagePhrases = []string{
	"invalid data found when processing input", "moov atom not found", "error while decoding",
	"decode_slice_header error", "non-existing pps", "invalid nal unit",
}

// failureCode is the code of a failed encode, from ffmpeg's key lines, in L-HW-14's order.
func failureCode(lines []string) string {
	text := strings.ToLower(strings.Join(lines, "\n"))
	switch {
	case strings.Contains(text, "could not write header") && strings.Contains(text, "supported for"):
		// The file type cannot hold a track the plan maps in.
		return codeSettingsRejected
	case strings.Contains(text, "quality-based encoding not supported"):
		// An encoder refused a quality setting that reached it. An unknown option name is not this
		// case: it is this build's, and it stays ffmpeg_error so the ladder may step down.
		return codeSettingsRejected
	case containsAny(text, encoderUnavailablePhrases):
		return codeEncoderUnavailable
	case strings.Contains(text, "no space left on device"):
		return codeNoSpace
	case strings.Contains(text, "permission denied"):
		return codeNotWritable
	case strings.Contains(text, "no such file or directory"):
		return codeSourceMissing
	case containsAny(text, sourceDamagePhrases):
		return codeSourceDamaged
	}
	return codeFFmpeg
}

// stepsDown says whether a card rung that failed with code hands the file to the next rung. Off the
// full pipeline only an encoder or ffmpeg failure steps down. On the full rung the card decodes too,
// and a decode error there is the card's as often as the file's, so any failure does — except the
// four the processor would hit just the same.
func stepsDown(rung encode.Rung, code string) bool {
	if rung.Decode == encode.DecodeHardware {
		switch code {
		case codeNoSpace, codeSourceMissing, codeNotWritable, codeSettingsRejected:
			return false
		}
		return true
	}
	switch code {
	case codeEncoderUnavailable, codeFFmpeg:
		return true
	}
	return false
}

// reasonMax is L-HW-7's cap on the reason a fallback line names.
const reasonMax = 300

// failedLine is "<Friendly> (<encoder>) failed: <reason>." — L-HW-7's first sentence, shared by the
// fallback line and the stop wording.
func failedLine(rung encode.Rung, reason string) string {
	return hw.ProductName(rung.Family) + " (" + rung.Encoder + ") failed: " + capReason(reason) + "."
}

// fallbackLine is L-HW-7's whole sentence for a card encoder that failed and the processor taking
// over: "<Friendly> (<encoder>) failed: <reason>. Converted on the processor instead."
func fallbackLine(rung encode.Rung, reason string) string {
	return failedLine(rung, reason) + " Converted on the processor instead."
}

// stopLine is the sentence for a card encoder that failed under ifGpuFails: stop, which never
// reaches the processor (L-HW-5). It says the original is untouched, because nothing was placed.
func stopLine(rung encode.Rung, reason string) string {
	return failedLine(rung, reason) + " This step stops instead of using the processor, so the original was left as it is."
}

// noCardLine is the sentence when no card can take the job and the step stops rather than use the
// processor (L-HW-5, L-HW-9).
func noCardLine(reason string) string {
	if reason == "" {
		reason = "no graphics card was found"
	}
	return "No graphics card can convert this file: " + capReason(reason) + ". This step stops instead of using the processor, so the original was left as it is."
}

// fullPipelineNote is the log line for a full-pipeline failure that still converts on the card,
// where L-HW-7's processor sentence would be wrong: the processor decodes, the card still encodes.
func fullPipelineNote(rung encode.Rung, reason string) string {
	return "The full graphics pipeline failed (" + capReason(reason) + "); the processor decodes for " + rung.Encoder + "."
}

// processorNote is the log line for a job the card cannot take, so the log says why the processor is
// converting it (L-HW-9).
func processorNote(reason string) string {
	if reason == "" {
		return ""
	}
	return "Converting on the processor: " + capReason(reason) + "."
}

// reasonOf is the first thing to say about a failed run: ffmpeg's last key line, or what the run
// itself reported when no line was kept.
func reasonOf(run media.Result, err error) string {
	if n := len(run.KeyLines); n > 0 {
		if line := strings.TrimSpace(run.KeyLines[n-1]); line != "" {
			return line
		}
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// capReason limits a reason to L-HW-7's 300 characters, without the full stop the sentences add.
func capReason(reason string) string {
	reason = strings.TrimRight(strings.TrimSpace(reason), ". ")
	if r := []rune(reason); len(r) > reasonMax {
		reason = string(r[:reasonMax])
	}
	return reason
}

// containsAny says whether the lower-case text holds any of the phrases.
func containsAny(text string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}
