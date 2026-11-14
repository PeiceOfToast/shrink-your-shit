package jobs

import (
	"path/filepath"
	"testing"
)

// The main encoder a person picks for a worker survives closing and reopening the database.
func TestPrimaryEncoderSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := "hevc_nvenc"
	if err := st.SetWorkerConfig(WorkerConfig{ID: LocalWorker, Name: "This computer", Tags: []string{}, PrimaryEncoder: &enc}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, err := st.WorkerConfigOf(LocalWorker)
	if err != nil {
		t.Fatal(err)
	}
	if c.PrimaryEncoder == nil || *c.PrimaryEncoder != "hevc_nvenc" {
		t.Fatalf("after reopen PrimaryEncoder = %v, want hevc_nvenc", c.PrimaryEncoder)
	}
}
