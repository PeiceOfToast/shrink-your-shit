// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// outcome is what one fake rung prints and how it ends.
type outcome struct {
	stderr string
	err    error
}

// fakeRuns plays the rungs in order. Each rung writes partial bytes into tmp and records tmp's size as it starts, so
// a test can see that the partial output was cleared before the next rung.
type fakeRuns struct {
	tmp      string
	outcomes []outcome
	calls    [][]string
	sizes    []int64
}

func (f *fakeRuns) run(_ context.Context, args []string) (string, error) {
	size := int64(-1)
	if st, err := os.Stat(f.tmp); err == nil {
		size = st.Size()
	}
	f.sizes = append(f.sizes, size)
	f.calls = append(f.calls, args)
	if err := os.WriteFile(f.tmp, []byte("partial output"), 0o644); err != nil {
		return "", err
	}
	i := len(f.calls) - 1
	if i >= len(f.outcomes) {
		return "", errors.New("unexpected rung")
	}
	return f.outcomes[i].stderr, f.outcomes[i].err
}

// attemptPlan is a real plan for an H.264 source to HEVC: the graphics encoder rungs exist for it.
func attemptPlan() rules.Plan {
	info := media.Info{Container: "matroska,webm", VideoCodec: "h264", DurationSec: 60, Streams: []media.Stream{
		{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720},
		{Index: 1, Type: "audio", Codec: "aac", Language: "eng"},
	}}
	return rules.DefaultSelectors(3).Template().Compile(info)
}

// The attempt ladder: each case gives the rungs' outcomes in order and says which rungs run.
func TestAttemptLadder(t *testing.T) {
	const src, enc = "a.mkv", "hevc_qsv"
	fullErr := errors.New("exit status 1")
	decodeLine := "Error while decoding stream #0:0: Invalid data found when processing input"
	unknownEnc := "Unknown encoder 'hevc_qsv'"
	noSpace := "No space left on device"

	cases := []struct {
		name      string
		full      bool // the first rung is the full GPU pipeline
		outcomes  []outcome
		stopped   bool
		wantRuns  int
		wantEnc   string // the last rung's encoder; "" is the processor
		wantFull  bool   // the last rung is the full pipeline
		wantFell  bool
		wantErr   bool
		wantFails int // Failed hook calls
	}{
		{
			name: "full pipeline decode error steps to hybrid", full: true,
			outcomes: []outcome{{decodeLine, fullErr}, {"", nil}},
			wantRuns: 2, wantEnc: enc, wantFails: 1,
		},
		{
			name:     "hybrid unknown encoder steps to processor",
			outcomes: []outcome{{unknownEnc, fullErr}, {"", nil}},
			wantRuns: 2, wantEnc: "", wantFell: true, wantFails: 1,
		},
		{
			name:     "hybrid no space does not step",
			outcomes: []outcome{{noSpace, fullErr}},
			wantRuns: 1, wantEnc: enc, wantErr: true, wantFails: 0,
		},
		{
			name:     "stopped does not step",
			outcomes: []outcome{{unknownEnc, fullErr}},
			stopped:  true,
			wantRuns: 1, wantEnc: enc, wantErr: true, wantFails: 0,
		},
		{
			name:     "first rung succeeds, no step",
			outcomes: []outcome{{"", nil}},
			wantRuns: 1, wantEnc: enc, wantFell: false, wantFails: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			tmp := filepath.Join(dir, "b.mkv")
			plan := attemptPlan()
			if plan.Skip != "" {
				t.Fatalf("plan skipped: %s", plan.Skip)
			}
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
			fails := 0
			hooks := AttemptHooks{
				Failed:  func(Rung, string, error) { fails++ },
				Stopped: func() bool { return c.stopped },
			}
			res := Attempt(context.Background(), AttemptInput{Plan: plan, SrcPath: src, Tmp: tmp, First: first}, fake.run, hooks)

			if len(fake.calls) != c.wantRuns {
				t.Fatalf("rungs run: %d, want %d", len(fake.calls), c.wantRuns)
			}
			if res.Encoder != c.wantEnc {
				t.Errorf("last rung encoder %q, want %q", res.Encoder, c.wantEnc)
			}
			if res.Full != c.wantFull {
				t.Errorf("last rung full %v, want %v", res.Full, c.wantFull)
			}
			if res.Fell != c.wantFell {
				t.Errorf("Fell %v, want %v", res.Fell, c.wantFell)
			}
			if c.wantFell && res.FellReason == "" {
				t.Error("the processor took over but FellReason is empty")
			}
			if (res.Err != nil) != c.wantErr {
				t.Errorf("Err %v, want error %v", res.Err, c.wantErr)
			}
			if fails != c.wantFails {
				t.Errorf("Failed hook called %d times, want %d", fails, c.wantFails)
			}
			// A step clears the partial file before the next rung: the next rung starts with an empty file.
			for i := 1; i < len(fake.sizes); i++ {
				if fake.sizes[i] != 0 {
					t.Errorf("rung %d started with %d bytes in the temp file, want 0", i, fake.sizes[i])
				}
			}
		})
	}
}
