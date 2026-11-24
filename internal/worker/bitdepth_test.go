// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A flow that asks for 10 bits of an 8-bit file is a 10-bit encode, so with "Hardware only" on a card that cannot
// make 10 bits it is refused in the same words a 10-bit source is refused today, before any encode, and the
// processor never takes it.
func TestBitDepthTenHardwareOnlyRefusedLikeATenBitSource(t *testing.T) {
	s := newScenario(t)
	sel := rules.DefaultSelectors(3)
	sel.Hardware, sel.BitDepth = "only", "10"
	w := s.worker(Replace)
	tmpl := sel.Template()
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	var asked []bool
	w.Encoder = func(_ string, need10 bool, _ *rules.HDR) (string, string) {
		asked = append(asked, need10)
		return "", ""
	}
	w.RunOne(context.Background()) // a failed job returns its error; the status below is the check
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, HardwareOnlyNoEncoderWords) {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if len(asked) == 0 || !asked[0] {
		t.Errorf("the encoder was asked with need10 = %v, want true", asked)
	}
	if enc := encodeCalls(s); len(enc) != 0 {
		t.Errorf("an encode ran: %v", enc)
	}
}
