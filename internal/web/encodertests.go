// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// recordReportedTests keeps the encoder test run a remote worker reported (its capability list, as
// reportedCaps stores it) for the worker's computer. A list with no test time yet keeps nothing.
func recordReportedTests(store *jobs.Store, workerID, caps string) error {
	if !strings.HasPrefix(strings.TrimSpace(caps), "[") { // "{}": an older worker, or none tested yet
		return nil
	}
	var list []struct {
		Kind, Key, Test, Detail string
		TestedAt                *string `json:"testedAt"`
	}
	if err := json.Unmarshal([]byte(caps), &list); err != nil {
		return err
	}
	var at string
	var rows []jobs.EncoderTest
	for _, c := range list {
		if c.Kind != "encoder" || c.TestedAt == nil {
			continue
		}
		at = *c.TestedAt
		rows = append(rows, jobs.EncoderTest{Encoder: strings.TrimPrefix(c.Key, "encoder:"), Passed: c.Test == "passed", Detail: c.Detail})
	}
	return store.RecordEncoderTests("worker:"+workerID, at, rows)
}
