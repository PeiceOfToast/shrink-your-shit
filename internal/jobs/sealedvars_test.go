// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W4b QA step 3: a workflow's secret variable is stored sealed wherever the doc is stored (the draft and
// every revision), so the database file and its backups hold no secret in the clear; a job still resolves
// it, and a computer whose share key cannot open it stops the job instead of running with it empty.

const sealedTestSecret = "w4b-test-secret-9f2c" // a value no test writes anywhere but a secret

// storedBytes is what the database holds on disk: the file and its write-ahead log, after the log is folded
// back into the file where SQLite allows it.
func storedBytes(t *testing.T, s *Store) []byte {
	t.Helper()
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Logf("checkpoint: %v (checking the files as they are)", err)
	}
	var out []byte
	for _, p := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if b, err := os.ReadFile(p); err == nil {
			out = append(out, b...)
		}
	}
	return out
}

// assertNotStored fails when the database holds s in the clear.
func assertNotStored(t *testing.T, st *Store, secret string) {
	t.Helper()
	if bytes.Contains(storedBytes(t, st), []byte(secret)) {
		t.Errorf("the database holds %q in the clear", secret)
	}
}

// assertStoredDocsSealed checks what the rows hold, not the file's bytes: an in-place rewrite of a doc a
// build before sealing wrote leaves old pages in SQLite's free space until a VACUUM, which the upgrade does
// not run. What must hold is that no stored doc has a secret in the clear.
func assertStoredDocsSealed(t *testing.T, st *Store, secret string) {
	t.Helper()
	rows, err := st.db.Query(`SELECT doc FROM workflow_revisions`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(raw, secret) {
			t.Errorf("a stored doc holds the secret in the clear: %s", raw)
		}
		var d workflow.Doc
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatal(err)
		}
		for _, v := range d.Variables {
			if v.Type == "secret" {
				if _, _, ok := sealedOf(v.Value); !ok {
					t.Errorf("%s is stored unsealed: %v", v.Name, v.Value)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// flowWithSecret is a published workflow with a secret variable and a library that uses it.
func flowWithSecret(t *testing.T, s *Store) (Library, Flow) {
	t.Helper()
	f, err := s.AddWorkflow("Sealed", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := s.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Doc.Variables = []workflow.Variable{
		{Name: "api_key", Type: "secret", Value: sealedTestSecret, HasValue: true},
		{Name: "studio", Type: "string", Value: "Mine"},
	}
	saved, err := s.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	tm, issues := workflow.ToTemplate(saved.Doc, rules.SliderTemplate(3))
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if _, err := s.Publish(f.ID, saved.Etag, tm); err != nil {
		t.Fatal(err)
	}
	lib, err := s.AddLibraryOnFlow("Movies", t.TempDir(), f.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return lib, f
}

// The draft and the revision hold the secret sealed, the API's copy says it has one and never shows it, and
// the job's RunInfo gives it in the clear.
func TestASecretIsStoredSealedAndAJobResolvesIt(t *testing.T) {
	useHome(t)
	s := open(t)
	lib, _ := flowWithSecret(t, s)
	assertNotStored(t, s, sealedTestSecret)

	info, err := s.RunInfoFor(Job{LibraryID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]workflow.Variable{}
	for _, v := range info.Variables {
		got[v.Name] = v
	}
	if v := got["api_key"]; v.Value != sealedTestSecret || !v.HasValue {
		t.Errorf("the job's api_key: %+v, want its value", v)
	}
	if v := got["studio"]; v.Value != "Mine" {
		t.Errorf("the job's studio: %+v", v)
	}

	// what the API is handed: the secret's value is never there, only that it has one
	rev, ok, err := s.LatestRevision(lib.FlowID)
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if v := workflow.Masked(rev.Doc).Variables[0]; v.Value != nil || !v.HasValue {
		t.Errorf("the API's api_key: %+v, want no value and hasValue", v)
	}
}

// Saving the draft back as the page does (it is never sent the secret) keeps the sealed value as it is: no
// plaintext, and the job resolves the same secret.
func TestASecretSurvivesADraftSaveThePageNeverSaw(t *testing.T) {
	useHome(t)
	s := open(t)
	lib, f := flowWithSecret(t, s)
	d, _, err := s.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Doc.Nodes[0].Config["test_edit"] = "an edit beside the secret"
	sent := workflow.Masked(d.Doc)
	sent = workflow.KeepSecretValues(sent, d.Doc)
	if _, err := s.SaveDraft(f.ID, sent, d.Layout, d.Etag); err != nil {
		t.Fatal(err)
	}
	assertNotStored(t, s, sealedTestSecret)
	info, err := s.RunInfoFor(Job{LibraryID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range info.Variables {
		if v.Name == "api_key" {
			found = v.Value == sealedTestSecret
		}
	}
	if !found {
		t.Errorf("after the page's save, the job's secret is %+v", info.Variables)
	}
}

// A secret sealed on another computer (a backup restored here) is never handed to a job as an empty string:
// the job's RunInfo stops with the secret's name.
func TestASecretSealedOnAnotherComputerStopsTheJob(t *testing.T) {
	useHome(t)
	s := open(t)
	lib, _ := flowWithSecret(t, s)
	replaceShareKey(t, 9) // a backup restored from another machine: this computer's key is another one
	info, err := s.RunInfoFor(Job{LibraryID: lib.ID})
	var unreadable *SecretUnreadableError
	if !errors.As(err, &unreadable) {
		t.Fatalf("err %v, want a SecretUnreadableError", err)
	}
	if !slices.Equal(unreadable.Names, []string{"api_key"}) {
		t.Errorf("names %q, want api_key", unreadable.Names)
	}
	if !strings.Contains(err.Error(), "api_key") || !strings.Contains(err.Error(), "share key") {
		t.Errorf("the message: %q", err)
	}
	for _, v := range info.Variables {
		if v.Name == "api_key" && v.Value == "" {
			t.Errorf("the secret was handed over empty: %+v", v)
		}
	}
}

// A draft or revision saved before secret values were sealed is sealed when the store opens, still opens
// for a job, and leaves no plaintext behind.
func TestAnOlderPlaintextSecretIsSealedWhenTheStoreOpens(t *testing.T) {
	useHome(t)
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, f := flowWithSecret(t, s)
	// a build before sealing wrote the value in the clear: put one back, beside the draft's sealed one
	doc := workflow.Preset()
	doc.Variables = []workflow.Variable{{Name: "api_key", Type: "secret", Value: sealedTestSecret, HasValue: true}}
	raw, _ := json.Marshal(doc)
	if _, err := s.db.Exec(`UPDATE workflow_revisions SET doc=? WHERE workflow_id=?`, string(raw), f.ID); err != nil {
		t.Fatal(err)
	}
	// and a draft holding the same doc: the pass seals one plaintext to one form, so the two stay equal
	if _, err := s.db.Exec(`INSERT INTO workflow_revisions(workflow_id,number,status,doc,layout,logic_hash,etag,created) VALUES(?,NULL,'draft',?,?,?,?,?)`,
		f.ID, string(raw), "{}", "x", "etag-old-draft", "2020-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(storedBytes(t, s), []byte(sealedTestSecret)) {
		t.Fatal("the test did not put a plaintext secret in the database")
	}
	s.Close()

	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	assertStoredDocsSealed(t, s, sealedTestSecret)
	// the memoised seal: docs that were equal are still equal after the pass
	rows, err := s.db.Query(`SELECT status, logic_hash FROM workflow_revisions WHERE workflow_id=? ORDER BY id`, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{}
	for rows.Next() {
		var status, hash string
		if err := rows.Scan(&status, &hash); err != nil {
			t.Fatal(err)
		}
		hashes[status] = hash
	}
	rows.Close()
	if hashes["draft"] == "" || hashes["published"] == "" || hashes["draft"] != hashes["published"] {
		t.Errorf("after the pass the draft hashes %q and the published revision %q, want one form",
			hashes["draft"], hashes["published"])
	}
	info, err := s.RunInfoFor(Job{LibraryID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, v := range info.Variables {
		if v.Name == "api_key" {
			ok = v.Value == sealedTestSecret
		}
	}
	if !ok {
		t.Errorf("after the upgrade the job's secret is %+v", info.Variables)
	}
}

// A draft the open-time pass sealed hashes the same when the page saves it back unchanged: the pass writes
// the sealed value as a struct and the page's save marshals it as JSON again, so the two forms must come out
// byte for byte (nonce first), or a no-op save would look like a change (draftDiffers).
func TestAPassSealedDraftHashesTheSameAfterAnUnchangedSave(t *testing.T) {
	useHome(t)
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.AddWorkflow("Sealed", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := s.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Doc.Variables = []workflow.Variable{{Name: "api_key", Type: "secret", Value: sealedTestSecret, HasValue: true}}
	raw, _ := json.Marshal(d.Doc)
	if _, err := s.db.Exec(`UPDATE workflow_revisions SET doc=? WHERE workflow_id=? AND status='draft'`, string(raw), f.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(path); err != nil { // the open-time pass seals the draft and writes its hash
		t.Fatal(err)
	}
	defer s.Close()
	sealed, _, err := s.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := sealedOf(sealed.Doc.Variables[0].Value); !ok {
		t.Fatalf("the pass did not seal the draft: %+v", sealed.Doc.Variables[0])
	}
	sent := workflow.KeepSecretValues(workflow.Masked(sealed.Doc), sealed.Doc)
	saved, err := s.SaveDraft(f.ID, sent, sealed.Layout, sealed.Etag)
	if err != nil {
		t.Fatal(err)
	}
	if saved.LogicHash != sealed.LogicHash {
		t.Errorf("the unchanged save-back hashes %s, the pass wrote %s", saved.LogicHash, sealed.LogicHash)
	}
}

// A secret whose value is not text (an older build or another client may have sent an object) is sealed as
// its JSON text rather than stored in the clear; the API refuses that shape now, and this is the store's
// own side of it. A job gets the text back, which is what a secret's value is.
func TestASecretHoldingAnObjectIsSealedToo(t *testing.T) {
	for _, c := range []struct {
		why    string
		value  any
		secret string
		text   string
	}{
		{"an object", map[string]any{"k": "OBJSECRET-7731"}, "OBJSECRET-7731", `{"k":"OBJSECRET-7731"}`},
		{"a list", []any{"ARRSECRET-4420", 2}, "ARRSECRET-4420", `["ARRSECRET-4420",2]`},
	} {
		t.Run(c.why, func(t *testing.T) {
			useHome(t)
			s := open(t)
			f, err := s.AddWorkflow("Objs", workflow.Preset())
			if err != nil {
				t.Fatal(err)
			}
			d, _, err := s.Draft(f.ID)
			if err != nil {
				t.Fatal(err)
			}
			d.Doc.Variables = []workflow.Variable{{Name: "api_key", Type: "secret", Value: c.value, HasValue: true}}
			saved, err := s.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag)
			if err != nil {
				t.Fatal(err)
			}
			assertNotStored(t, s, c.secret)
			assertStoredDocsSealed(t, s, c.secret)
			tm, issues := workflow.ToTemplate(saved.Doc, rules.SliderTemplate(3))
			if len(issues) > 0 {
				t.Fatal(issues)
			}
			if _, err := s.Publish(f.ID, saved.Etag, tm); err != nil {
				t.Fatal(err)
			}
			lib, err := s.AddLibraryOnFlow("Movies", t.TempDir(), f.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			info, err := s.RunInfoFor(Job{LibraryID: lib.ID})
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			for _, v := range info.Variables {
				if v.Name == "api_key" {
					got, _ = v.Value.(string)
				}
			}
			if got != c.text {
				t.Errorf("the job's api_key is %q, want the value's JSON text", got)
			}
		})
	}
}

// An object- or list-valued secret saved before this build is sealed by the open-time pass, as a string
// secret is.
func TestAnOlderObjectSecretIsSealedWhenTheStoreOpens(t *testing.T) {
	for _, c := range []struct {
		why   string
		value any
		text  string
	}{
		{"an object", map[string]any{"k": sealedTestSecret}, `{"k":"` + sealedTestSecret + `"}`},
		{"a list", []any{sealedTestSecret}, `["` + sealedTestSecret + `"]`},
	} {
		t.Run(c.why, func(t *testing.T) {
			useHome(t)
			path := filepath.Join(t.TempDir(), "syp.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			lib, f := flowWithSecret(t, s)
			doc := workflow.Preset()
			doc.Variables = []workflow.Variable{{Name: "api_key", Type: "secret", Value: c.value, HasValue: true}}
			raw, _ := json.Marshal(doc)
			if _, err := s.db.Exec(`UPDATE workflow_revisions SET doc=? WHERE workflow_id=?`, string(raw), f.ID); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(storedBytes(t, s), []byte(sealedTestSecret)) {
				t.Fatal("the test did not put a plaintext secret in the database")
			}
			s.Close()
			if s, err = Open(path); err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			assertStoredDocsSealed(t, s, sealedTestSecret)
			info, err := s.RunInfoFor(Job{LibraryID: lib.ID})
			if err != nil {
				t.Fatal(err)
			}
			ok := false
			for _, v := range info.Variables {
				if v.Name == "api_key" {
					text, _ := v.Value.(string)
					ok = text == c.text
				}
			}
			if !ok {
				t.Errorf("after the upgrade the job's secret is %+v", info.Variables)
			}
		})
	}
}

// A draft saved with a secret the client just typed holds no copy of it in the clear; this is the save
// path itself, before anything is published (a mutation that drops that seal leaves it in the database).
func TestANewDraftSecretIsNeverStoredInTheClear(t *testing.T) {
	useHome(t)
	s := open(t)
	f, err := s.AddWorkflow("Fresh", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := s.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Doc.Variables = []workflow.Variable{{Name: "api_key", Type: "secret", Value: sealedTestSecret, HasValue: true}}
	if _, err := s.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag); err != nil {
		t.Fatal(err)
	}
	assertNotStored(t, s, sealedTestSecret)
	assertStoredDocsSealed(t, s, sealedTestSecret)
}

// A backup holds no secret in the clear, and restoring it keeps the secret usable here.
func TestABackupHoldsNoSecretInTheClear(t *testing.T) {
	useHome(t)
	s := open(t)
	lib, _ := flowWithSecret(t, s)
	b, err := s.WriteBackup(ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.backupDir(), b.ID+".json.gz")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(sealedTestSecret)) {
		t.Errorf("the backup holds the secret in the clear")
	}
	if _, err := s.RestoreBackup(path); err != nil {
		t.Fatal(err)
	}
	assertNotStored(t, s, sealedTestSecret)
	info, err := s.RunInfoFor(Job{LibraryID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, v := range info.Variables {
		if v.Name == "api_key" {
			ok = v.Value == sealedTestSecret
		}
	}
	if !ok {
		t.Errorf("after the restore the job's secret is %+v", info.Variables)
	}
}

// A backup made before secrets were sealed holds plaintext; restoring it seals it in the same transaction,
// so the restored database leaves none behind and the secret still opens.
func TestARestoredOldBackupSealsItsSecrets(t *testing.T) {
	useHome(t)
	s := open(t)
	lib, f := flowWithSecret(t, s)
	doc := workflow.Preset()
	doc.Variables = []workflow.Variable{{Name: "api_key", Type: "secret", Value: sealedTestSecret, HasValue: true}}
	raw, _ := json.Marshal(doc)
	if _, err := s.db.Exec(`UPDATE workflow_revisions SET doc=? WHERE workflow_id=?`, string(raw), f.ID); err != nil {
		t.Fatal(err)
	}
	b, err := s.WriteBackup(ReasonManual) // an old build's backup: the secret in the clear
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreBackup(filepath.Join(s.backupDir(), b.ID+".json.gz")); err != nil {
		t.Fatal(err)
	}
	assertStoredDocsSealed(t, s, sealedTestSecret)
	info, err := s.RunInfoFor(Job{LibraryID: lib.ID})
	if err != nil {
		t.Fatalf("the restored secret does not open here: %v", err)
	}
	ok := false
	for _, v := range info.Variables {
		if v.Name == "api_key" {
			ok = v.Value == sealedTestSecret
		}
	}
	if !ok {
		t.Errorf("after restoring the old backup the job's secret is %+v", info.Variables)
	}
}
