package events

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// systemFile writes app events and job warnings and errors to logs/system.log, one line each, and to stderr.
// It rotates the file to system.log.1 … .N when it would pass the size limit.
type systemFile struct {
	path     string
	maxBytes int64
	keep     int
	stderr   io.Writer
	f        *os.File
	size     int64
}

const systemLine = "2006-01-02 15:04:05.000"

func openSystemFile(dir string, o Options) (*systemFile, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("make the log folder: %w", err)
	}
	s := &systemFile{
		path:     filepath.Join(dir, "system.log"),
		maxBytes: o.LogMaxBytes,
		keep:     o.LogKeep,
		stderr:   o.Stderr,
	}
	if err := s.open(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *systemFile) name() string { return "system file" }

// accepts takes app events and job warnings and errors. Progress never goes to a file.
func (s *systemFile) accepts(e Event) bool {
	if e.Kind == KindProgress {
		return false
	}
	return e.JobID == 0 || e.Level == LevelWarn || e.Level == LevelError
}

func (s *systemFile) write(e Event) error {
	line := formatSystemLine(e)
	if s.size > 0 && s.size+int64(len(line)) > s.maxBytes {
		if err := s.rotate(); err != nil {
			return err
		}
	}
	n, err := s.f.WriteString(line)
	s.size += int64(n)
	if err != nil {
		return err
	}
	// stderr is a copy for the terminal and docker logs; a failed copy does not fail the file.
	_, _ = io.WriteString(s.stderr, line)
	return nil
}

func (s *systemFile) close() error {
	return s.f.Close()
}

func (s *systemFile) open() error {
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open the system log: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("stat the system log: %w", err)
	}
	s.f = f
	s.size = fi.Size()
	return nil
}

// rotate moves system.log to .1, shifts .1 … .N-1 up one, and drops the oldest.
func (s *systemFile) rotate() error {
	if err := s.f.Close(); err != nil {
		return fmt.Errorf("close the system log for rotation: %w", err)
	}
	if err := os.Remove(s.numbered(s.keep)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove the oldest system log: %w", err)
	}
	for i := s.keep - 1; i >= 1; i-- {
		if err := os.Rename(s.numbered(i), s.numbered(i+1)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate the system log: %w", err)
		}
	}
	if err := os.Rename(s.path, s.numbered(1)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate the system log: %w", err)
	}
	return s.open()
}

func (s *systemFile) numbered(i int) string {
	return fmt.Sprintf("%s.%d", s.path, i)
}

// formatSystemLine renders one line: `2026-11-27 03:12:45.120 WARN  Scanner  message`.
func formatSystemLine(e Event) string {
	source := e.Source
	if source == "" {
		source = "-"
	}
	msg := strings.NewReplacer("\r", " ", "\n", " ").Replace(e.Message)
	return fmt.Sprintf("%s %-5s %-8s %s\n",
		e.TS.UTC().Format(systemLine), strings.ToUpper(string(e.Level)), source, msg)
}
