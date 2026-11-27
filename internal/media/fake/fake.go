// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package fake is a scriptable stand-in for the media package, so every package above it can be
// unit-tested without ffmpeg: probes answer from fixtures keyed by path, and runs follow a script
// (progress ticks, exit code, stderr, or hang until cancelled) while recording the argv they got.
package fake

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

var (
	_ media.Prober = (*Media)(nil)
	_ media.Runner = (*Media)(nil)
	_ media.Media  = (*Media)(nil)
)

// Media is a fake prober and runner. The zero value is ready; a path with no fixture is not media.
type Media struct {
	// Probes answers Probe for a path.
	Probes map[string]media.Info
	// ProbeErrors answers Probe with an error for a path; it wins over Probes.
	ProbeErrors map[string]error
	// Runs scripts the Run calls in order. The last script repeats.
	Runs []RunScript

	mu   sync.Mutex
	gets [][]string
}

// RunScript is one scripted Run: its progress ticks, what it returns, and whether it waits for
// the context instead of finishing.
type RunScript struct {
	// Delay is waited before each progress tick.
	Delay time.Duration
	// Progress is sent to onProgress in order, once per tick.
	Progress []media.Progress
	// Result is returned as-is.
	Result media.Result
	// Err is returned after the progress ticks (or from a hang when its context ends).
	Err error
	// Hang blocks until the context is cancelled, then returns Result and the context's error.
	Hang bool
}

// ProbeJSON records the probe result for path from an ffprobe-style JSON fixture, keyed by the
// path the caller will probe. Call it before the test uses the fake.
func (m *Media) ProbeJSON(path string, data []byte) error {
	info, err := media.ParseProbe(data)
	if err != nil {
		return fmt.Errorf("probe fixture for %s: %w", path, err)
	}
	if info.Path == "" {
		info.Path = path
	}
	if m.Probes == nil {
		m.Probes = make(map[string]media.Info)
	}
	m.Probes[path] = info
	return nil
}

// Probe answers from Probes, then ProbeErrors, and otherwise reports ErrNotMedia.
func (m *Media) Probe(ctx context.Context, path string) (media.Info, error) {
	if err := ctx.Err(); err != nil {
		return media.Info{}, err
	}
	if err, ok := m.ProbeErrors[path]; ok {
		return media.Info{}, err
	}
	if info, ok := m.Probes[path]; ok {
		return info, nil
	}
	return media.Info{}, fmt.Errorf("%s: %w", path, media.ErrNotMedia)
}

// Run records the argv, then plays the next script: progress ticks and the scripted result, or a
// hang until cancellation.
func (m *Media) Run(ctx context.Context, argv []string, onProgress func(media.Progress)) (media.Result, error) {
	m.mu.Lock()
	call := len(m.gets)
	m.gets = append(m.gets, append([]string(nil), argv...))
	m.mu.Unlock()

	var script RunScript
	switch {
	case len(m.Runs) == 0:
	case call < len(m.Runs):
		script = m.Runs[call]
	default:
		script = m.Runs[len(m.Runs)-1]
	}
	if script.Hang {
		<-ctx.Done()
		return script.Result, ctx.Err()
	}
	for _, p := range script.Progress {
		if script.Delay > 0 {
			select {
			case <-time.After(script.Delay):
			case <-ctx.Done():
				return script.Result, ctx.Err()
			}
		} else if err := ctx.Err(); err != nil {
			return script.Result, err
		}
		if onProgress != nil {
			onProgress(p)
		}
	}
	return script.Result, script.Err
}

// Argvs returns the argv of every Run call, in call order.
func (m *Media) Argvs() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]string, len(m.gets))
	for i, argv := range m.gets {
		out[i] = append([]string(nil), argv...)
	}
	return out
}
