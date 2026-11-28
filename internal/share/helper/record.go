// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// record is one mount the helper made. It is the only authority for unmounting: a live mount
// without a matching record is someone else's.
type record struct {
	ID     int64     `json:"id"`
	Source string    `json:"source"`
	FSType string    `json:"fstype"`
	Point  string    `json:"point"`
	When   time.Time `json:"when"`
}

// recordSet is mounts.json's shape: one object, so a future field can be added without rewriting
// the file as a bare list.
type recordSet struct {
	Mounts []record `json:"mounts"`
}

func (s *Server) recordPath() string {
	return filepath.Join(s.RecordDir, "mounts.json")
}

func (s *Server) findRecord(id int64) (record, bool) {
	for _, r := range s.records {
		if r.ID == id {
			return r, true
		}
	}
	return record{}, false
}

// addRecord stores and saves one record, replacing any earlier one with the same id.
func (s *Server) addRecord(rec record) error {
	out := make([]record, 0, len(s.records)+1)
	for _, r := range s.records {
		if r.ID != rec.ID {
			out = append(out, r)
		}
	}
	out = append(out, rec)
	s.records = out
	return saveRecords(s.RecordDir, s.records)
}

// dropRecord forgets one record and saves the file. A save failure leaves the file one generation
// stale, which the stale-record rule handles safely; it must not undo an operation that happened.
func (s *Server) dropRecord(id int64) {
	out := make([]record, 0, len(s.records))
	for _, r := range s.records {
		if r.ID != id {
			out = append(out, r)
		}
	}
	s.records = out
	if err := saveRecords(s.RecordDir, s.records); err != nil {
		fmt.Fprintf(os.Stderr, "syp helper: save the mount record: %v\n", err)
	}
}

// loadRecords reads mounts.json. A missing file is no records; a file that cannot be read or
// parsed stops the helper, because it must not guess what it mounted.
func loadRecords(path string) ([]record, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the mount record: %w", err)
	}
	var set recordSet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("read the mount record: %w", err)
	}
	for _, r := range set.Mounts {
		if r.ID < 0 || r.Point == "" || r.Source == "" || r.FSType == "" {
			return nil, errors.New("the mount record has an entry that can't be used")
		}
	}
	return set.Mounts, nil
}

// saveRecords writes mounts.json atomically as a 0600 file in a 0700 folder. Only root reads it,
// and only root may change it.
func saveRecords(dir string, recs []record) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(recordSet{Mounts: recs}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(dir, ".mounts-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "mounts.json"))
}
