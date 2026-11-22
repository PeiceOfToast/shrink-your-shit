// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// W7: the Convert step's hardware choices inside the ladder. Same fake rungs as TestAttemptLadder; the plan's
// Hardware and HardwareFail decide whether a graphics failure steps down.
func TestAttemptHardwarePolicy(t *testing.T) {
	const src, enc = "a.mkv", "hevc_qsv"
	fullErr := errors.New("exit status 1")
	decodeLine := "Error while decoding stream #0:0: Invalid data found when processing input"
	unknownEnc := "Unknown encoder 'hevc_qsv'"

	cases := []struct {
		name     string
		hardware string // "", "only", "cpu"
		failMode string // "", "fail"
		full     bool
		outcomes []outcome
		wantRuns int
		wantEnc  string
		wantFell bool
		wantErr  bool
	}{
		{
			name: "default: a hybrid failure steps to the processor", outcomes: []outcome{{unknownEnc, fullErr}, {"", nil}},
			wantRuns: 2, wantEnc: "", wantFell: true,
		},
		{
			name: "default: a full pipeline decode error steps to hybrid", full: true, outcomes: []outcome{{decodeLine, fullErr}, {"", nil}},
			wantRuns: 2, wantEnc: enc,
		},
		{
			name: "only: a hybrid failure does not step to the processor", hardware: "only",
			outcomes: []outcome{{unknownEnc, fullErr}}, wantRuns: 1, wantEnc: enc, wantErr: true,
		},
		{
			name: "only: a full pipeline failure still steps to hybrid", hardware: "only", full: true,
			outcomes: []outcome{{decodeLine, fullErr}, {"", nil}}, wantRuns: 2, wantEnc: enc,
		},
		{
			name: "fail: a full pipeline failure does not step", failMode: "fail", full: true,
			outcomes: []outcome{{decodeLine, fullErr}}, wantRuns: 1, wantEnc: enc, wantErr: true,
		},
		{
			name: "fail: a hybrid failure does not step to the processor", failMode: "fail",
			outcomes: []outcome{{unknownEnc, fullErr}}, wantRuns: 1, wantEnc: enc, wantErr: true,
		},
		{
			name: "only + fail: nothing steps", hardware: "only", failMode: "fail", full: true,
			outcomes: []outcome{{decodeLine, fullErr}}, wantRuns: 1, wantEnc: enc, wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmp := filepath.Join(t.TempDir(), "b.mkv")
			plan := attemptPlan()
			plan.Video.Hardware, plan.Video.HardwareFail = c.hardware, c.failMode
			var first Rung
			if c.full {
				args, ok := plan.FFmpegArgsFullGPU(src, tmp, enc)
				if !ok {
					t.Fatalf("no full GPU pipeline for %s", enc)
				}
				first = Rung{Args: args, Encoder: enc, Full: true}
			} else {
				args, ok := plan.FFmpegArgsWith(src, tmp, enc)
				if !ok {
					t.Fatalf("no hybrid args for %s", enc)
				}
				first = Rung{Args: args, Encoder: enc}
			}
			fake := &fakeRuns{tmp: tmp, outcomes: c.outcomes}
			res := Attempt(context.Background(), AttemptInput{Plan: plan, SrcPath: src, Tmp: tmp, First: first}, fake.run, AttemptHooks{})
			if len(fake.calls) != c.wantRuns {
				t.Fatalf("rungs run: %d, want %d", len(fake.calls), c.wantRuns)
			}
			if res.Encoder != c.wantEnc || res.Fell != c.wantFell {
				t.Errorf("last rung %q fell %v, want %q fell %v", res.Encoder, res.Fell, c.wantEnc, c.wantFell)
			}
			if (res.Err != nil) != c.wantErr {
				t.Errorf("Err %v, want error %v", res.Err, c.wantErr)
			}
		})
	}
}
