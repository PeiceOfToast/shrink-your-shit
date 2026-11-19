// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"encoding/json"
	"fmt"
	"time"
)

// EncoderFallbackStep is the job step a graphics encoder leaves when its file converts on the processor instead.
// Its words say which encoder failed and why; a job with this step carries a warning.
const EncoderFallbackStep = "encoder_fallback"

// A worker whose graphics encoder fails encoderPauseAfter times in a row stops using it for encoderPauseFor: a
// broken driver would otherwise fail every file first.
const (
	encoderPauseAfter = 3
	encoderPauseFor   = time.Hour
)

// encoderHealth is one worker's run of graphics-encoder failures, kept in app_meta (no schema change).
type encoderHealth struct {
	Fails       int    `json:"fails"`                 // in a row, since the last success or pause
	PausedUntil string `json:"pausedUntil,omitempty"` // RFC3339 UTC; "" when not paused
}

// FallsBackToProcessor reports whether a graphics encode that failed should convert again on the processor: an
// encoder or ffmpeg failure. A full disk, a refused write, a damaged or missing source are not the encoder's fault.
func FallsBackToProcessor(keyLines []string) bool {
	switch FailureCode(ReasonConvertFailed, keyLines) {
	case "encoder_unavailable", "ffmpeg_error":
		return true
	}
	return false
}

func encoderKey(worker string) string { return "encoderHealth:" + worker }

func (s *Store) encoderHealthOf(worker string) (encoderHealth, error) {
	var h encoderHealth
	v, ok, err := s.Meta(encoderKey(worker))
	if err != nil || !ok {
		return h, err
	}
	if json.Unmarshal([]byte(v), &h) != nil {
		return encoderHealth{}, nil // a damaged record is the same as none
	}
	return h, nil
}

func (s *Store) saveEncoderHealth(worker string, h encoderHealth) error {
	b, _ := json.Marshal(h)
	return s.SetMeta(encoderKey(worker), string(b))
}

// EncoderFell records that job id's graphics encoder (encoder) failed on worker and the job converts on the
// processor. It reports whether this fall-back paused the worker's graphics encoder.
func (s *Store) EncoderFell(id int64, worker, encoder, reason string) (bool, error) {
	if err := s.AddStep(id, EncoderFallbackStep, false, fmt.Sprintf("%s failed (%s); converted on the processor.", encoder, reason)); err != nil {
		return false, err
	}
	h, err := s.encoderHealthOf(worker)
	if err != nil {
		return false, err
	}
	h.Fails++
	paused := h.Fails >= encoderPauseAfter
	if paused {
		h.Fails = 0
		h.PausedUntil = s.now().Add(encoderPauseFor).UTC().Format(time.RFC3339)
	}
	return paused, s.saveEncoderHealth(worker, h)
}

// EncoderWorked records that worker's graphics encoder converted a file: its run of failures starts again.
func (s *Store) EncoderWorked(worker string) error {
	h, err := s.encoderHealthOf(worker)
	if err != nil || h.Fails == 0 {
		return err
	}
	h.Fails = 0
	return s.saveEncoderHealth(worker, h)
}

// EncoderPaused reports whether worker's graphics encoder is paused, and until when (the pause's end, even when
// it has passed).
func (s *Store) EncoderPaused(worker string) (time.Time, bool, error) {
	h, err := s.encoderHealthOf(worker)
	if err != nil || h.PausedUntil == "" {
		return time.Time{}, false, err
	}
	until, err := time.Parse(time.RFC3339, h.PausedUntil)
	if err != nil {
		return time.Time{}, false, nil
	}
	return until, s.now().Before(until), nil
}

// EncoderFallback is one worker's graphics-encoder fall-backs since a time, for the overview.
type EncoderFallback struct {
	Worker string // the worker's id
	Count  int
	At     string // the latest one's time (RFC3339 UTC)
}

// EncoderFallbacksSince counts the fall-backs since at, per worker.
func (s *Store) EncoderFallbacksSince(at time.Time) ([]EncoderFallback, error) {
	rows, err := s.db.Query(`SELECT j.worker, COUNT(*), MAX(s.at) FROM job_steps s JOIN jobs j ON j.id=s.job_id
		WHERE s.step=? AND s.at>=? GROUP BY j.worker ORDER BY MAX(s.at) DESC`, EncoderFallbackStep, at.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EncoderFallback{}
	for rows.Next() {
		var f EncoderFallback
		if err := rows.Scan(&f.Worker, &f.Count, &f.At); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
