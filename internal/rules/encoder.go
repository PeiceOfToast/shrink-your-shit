// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"slices"
	"strings"
)

// PassedGraphics is the graphics encoders whose capability test passed on a worker, in the doctor's order. caps
// is the worker's capability list (its own, or the one it reported).
func PassedGraphics(caps []map[string]any) []string {
	out := []string{}
	for _, c := range caps {
		key, _ := c["key"].(string)
		if c["kind"] == "encoder" && c["slot"] == "gpu" && c["test"] == "passed" {
			out = append(out, strings.TrimPrefix(key, "encoder:"))
		}
	}
	return out
}

// EncoderFor is the graphics encoder a worker converts a codec's plan with: main's encoder for the codec's
// format, when the worker's own test passed it (passed lists those). "" with a note for the job log when main's
// encoder didn't pass here; "" without one for the processor by choice (main cpu, or no graphics encoder for
// the format).
func EncoderFor(codec, main string, passed []string) (enc, note string) {
	if main == "" || main == "cpu" {
		return "", ""
	}
	want := GraphicsEncoder(codec, main)
	if want == "" {
		return "", ""
	}
	if slices.Contains(passed, want) {
		return want, ""
	}
	return "", fmt.Sprintf("encoder %s didn't pass its test here; using the processor", want)
}
