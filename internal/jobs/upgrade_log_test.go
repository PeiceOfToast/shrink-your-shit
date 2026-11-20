// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"bytes"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"testing"
)

// The log says when the database was upgraded, from which version, and where its backup is: after a
// successful upgrade, one line (the support trail for an update).
func TestUpgradeLogsOneLineWithVersionsAndBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE files DROP COLUMN decided_at`, `ALTER TABLE jobs DROP COLUMN workflow_rev`, `ALTER TABLE jobs DROP COLUMN output_gone_at`, `DROP INDEX IF EXISTS jobs_newest`, `PRAGMA user_version=47`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	s.Close()

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.Contains(l, "database upgraded") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("want one upgrade line, got %d: %q", len(lines), buf.String())
	}
	list, err := s.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	name := ""
	for _, b := range list {
		if b.Reason == ReasonBeforeMigration {
			name = b.Name
		}
	}
	if name == "" {
		t.Fatalf("no before-migration backup listed: %+v", list)
	}
	for _, want := range []string{fmt.Sprintf("from version 47 to %d", latestVersion()), "backup before the upgrade: " + name + " ", "(also " + path + ".bak-v47)"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("upgrade line %q lacks %q", lines[0], want)
		}
	}
}
