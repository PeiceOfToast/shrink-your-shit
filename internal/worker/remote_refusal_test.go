// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A result from a remote worker that carries a refusal code keeps it; one that sends words only (an older
// worker) is kept_original, as before; a code this server does not know is not trusted (kept_original).
func TestRemoteRefusalKeepsItsCode(t *testing.T) {
	for _, c := range []struct{ sent, want string }{
		{"output_damaged", "output_damaged"},
		{"check_differs", "check_differs"},
		{"", "kept_original"},
		{"made_up_code", "kept_original"},
	} {
		r := &RemoteJob{planned: make(chan struct{}), result: make(chan RemoteResult, 1)}
		r.result <- RemoteResult{Kept: "the new file is empty", KeptCode: c.sent}
		_, _, err := r.convert(context.Background(), nil, jobs.Job{ID: 1}, rules.Snapshot{}, "", "")
		var ke *KeptError
		if !errors.As(err, &ke) {
			t.Fatalf("sent %q: want a refusal, got %v", c.sent, err)
		}
		if ke.Code != c.want || ke.Reason != "the new file is empty" {
			t.Errorf("sent %q: code %q reason %q, want %q", c.sent, ke.Code, ke.Reason, c.want)
		}
	}
}
