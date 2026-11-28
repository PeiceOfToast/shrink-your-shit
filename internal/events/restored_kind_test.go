// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package events

import "testing"

// N7: the restored kind is part of a job's record and its History. Both lists must hold it: the record keeps it,
// and the job's history shows it.
func TestRestoredIsInTheJobRecordAndHistory(t *testing.T) {
	if !historyKinds[KindRestored] {
		t.Error("KindRestored is not in historyKinds, so a job's History would not show it")
	}
	if keepFor(KindRestored) != KeepJob {
		t.Errorf("keepFor(KindRestored) = %s, want the job record", keepFor(KindRestored))
	}
}
