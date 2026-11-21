// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// FB2c (migration 57): a job's record carries tags. A new job has none; SetJobTags removes, then adds,
// each trimmed, a comma a separator and never part of a tag, the same tag in another case taken once, cut
// to MaxTagLen, at most MaxTags kept; the job and the list row read them back; a job that isn't there is
// ErrNotFound; the activity lists read tags too, and a skipped row carries an empty list, never nil.
func TestJobTags(t *testing.T) {
	s := open(t)
	id, err := s.Enqueue(Job{Library: "/media", Path: "/media/a.mkv", DurationSec: 10, SizeBefore: 100})
	if err != nil {
		t.Fatal(err)
	}
	if tags, err := s.JobTags(id); err != nil || !reflect.DeepEqual(tags, []string{}) {
		t.Fatalf("a new job: %v %v (want an empty list, not null)", tags, err)
	}
	if j, _ := s.Get(id); !reflect.DeepEqual(j.Tags, []string{}) {
		t.Fatalf("Get: %#v", j.Tags)
	}
	got, err := s.SetJobTags(id, []string{" remux ", "Needs  review", "remux", "REMUX"}, nil)
	if err != nil || !reflect.DeepEqual(got, []string{"remux", "Needs review"}) {
		t.Fatalf("add: %v %v", got, err)
	}
	got, err = s.SetJobTags(id, []string{"converted"}, []string{"needs review"})
	if err != nil || !reflect.DeepEqual(got, []string{"remux", "converted"}) {
		t.Fatalf("remove then add: %v %v", got, err)
	}
	if got, _ = s.SetJobTags(id, []string{"a,b"}, []string{"converted"}); !reflect.DeepEqual(got, []string{"remux", "a", "b"}) { // a comma is a separator, never part of a tag
		t.Fatalf("commas: %v", got)
	}
	long := strings.Repeat("x", MaxTagLen+5)
	got, _ = s.SetJobTags(id, []string{long}, nil)
	if got[len(got)-1] != strings.Repeat("x", MaxTagLen) {
		t.Errorf("a long tag is cut: %q", got[len(got)-1])
	}
	var many []string
	for i := 0; i < MaxTags+5; i++ {
		many = append(many, "t"+strings.Repeat("a", i))
	}
	if got, _ = s.SetJobTags(id, many, nil); len(got) != MaxTags {
		t.Errorf("the cap: %d tags", len(got))
	}
	if j, _ := s.Get(id); !reflect.DeepEqual(j.Tags, got) {
		t.Errorf("Get after: %v", j.Tags)
	}
	rows, _, _, err := s.FileList(FileFilter{Show: CardUnprocessed, Limit: 10})
	if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0].Tags, got) {
		t.Errorf("the list row: %v %v", rows, err)
	}
	if _, err := s.SetJobTags(id+99, []string{"x"}, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such job: %v", err)
	}
	if _, err := s.JobTags(id + 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such job: %v", err)
	}

	// the activity lists read tags too (a running job shows the tags set on it), and a skipped row carries an
	// empty list, never nil (review2-545: the API sends an array on every tab)
	j, err := s.Claim("w1")
	if err != nil {
		t.Fatal(err)
	}
	s.SetJobTags(j.ID, []string{"running-tag"}, []string{"remux"}) // the record is at the cap: one off, one on
	ws, err := s.WorkingJobs()
	if err != nil || len(ws) != 1 || !rules.HasTag(ws[0].Tags, "running-tag") {
		t.Errorf("working jobs' tags: %+v %v", ws, err)
	}
	if err := s.Skip(j.ID, "already in this format", "already_hevc"); err != nil {
		t.Fatal(err)
	}
	skipped, _, _, err := s.FileList(FileFilter{Show: CardSkipped, Limit: 10})
	if err != nil || len(skipped) == 0 {
		t.Fatalf("skipped rows: %v %v", skipped, err)
	}
	for _, r := range skipped {
		if r.Tags == nil {
			t.Errorf("a skipped row with nil tags: %+v", r)
		}
	}
}
