// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// useHome points this test's share key folder at its own temp dir (HOME on Linux and macOS, APPDATA on Windows).
func useHome(t *testing.T) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home)
}

// replaceShareKey writes a different key as this computer's share key, as a reinstall or another machine has.
func replaceShareKey(t *testing.T, fill byte) {
	t.Helper()
	wrapped, err := wrapShareKey(bytes.Repeat([]byte{fill}, ShareKeySize))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(shareKeyPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shareKeyPath(), wrapped, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Each kind of sealed secret is checked: a backup made under this computer's key previews as readable; the same
// backup under another key, or with no key, says its secrets won't work here.
func TestPreviewWarnsWhenAStoredSecretWontOpenHere(t *testing.T) {
	seal := func(t *testing.T, s *Store, plaintext string) ([]byte, []byte) {
		t.Helper()
		ct, nonce, err := SealSecret(plaintext)
		if err != nil {
			t.Fatal(err)
		}
		return ct, nonce
	}
	cases := []struct {
		name  string
		store func(t *testing.T, s *Store)
	}{
		{"share password", func(t *testing.T, s *Store) {
			if _, err := s.SaveShare(Share{Type: "smb", Address: "//nas/movies", Name: "movies", Username: "me"}, "hunter2"); err != nil {
				t.Fatal(err)
			}
		}},
		{"integration key", func(t *testing.T, s *Store) {
			ct, nonce := seal(t, s, "api-key-123")
			doc, _ := json.Marshal(map[string]any{"items": []map[string]any{{"id": "i1", "kind": "sonarr",
				"name": "Sonarr", "url": "http://sonarr.local", "sealed": ct, "nonce": nonce}}})
			if err := s.SetMeta("integrations", string(doc)); err != nil {
				t.Fatal(err)
			}
		}},
		{"workflow secret", func(t *testing.T, s *Store) {
			f, err := s.AddWorkflow("Sealed", workflow.Preset())
			if err != nil {
				t.Fatal(err)
			}
			d, _, err := s.Draft(f.ID)
			if err != nil {
				t.Fatal(err)
			}
			d.Doc.Variables = []workflow.Variable{{Name: "api_key", Type: "secret", Value: "api-key-123", HasValue: true}}
			if _, err := s.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag); err != nil {
				t.Fatal(err)
			}
		}},
		{"notification target", func(t *testing.T, s *Store) {
			ct, nonce, err := EncryptSecret("https://ntfy.example/topic")
			if err != nil {
				t.Fatal(err)
			}
			doc, _ := json.Marshal(map[string]any{"targets": []map[string]any{{"id": "t1", "name": "phone",
				"kind": "ntfy", "ct": ct, "nonce": nonce}}})
			if err := s.SetMeta("notifications", string(doc)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			useHome(t) // this computer's key is created by the first seal
			s := open(t)
			c.store(t, s)
			b, err := s.WriteBackup(ReasonManual)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.backupDir(), b.ID+".json.gz")

			p, err := s.PreviewBackup(path)
			if err != nil {
				t.Fatal(err)
			}
			if p.SecretsUnreadable || slices.Contains(p.Warnings, secretsWarning) {
				t.Errorf("under the key that sealed it: unreadable %v, warnings %q; want readable", p.SecretsUnreadable, p.Warnings)
			}

			replaceShareKey(t, 9)
			p, err = s.PreviewBackup(path)
			if err != nil {
				t.Fatal(err)
			}
			if !p.SecretsUnreadable || !slices.Contains(p.Warnings, secretsWarning) {
				t.Errorf("under another key: unreadable %v, warnings %q; want unreadable with the warning", p.SecretsUnreadable, p.Warnings)
			}

			if err := os.Remove(shareKeyPath()); err != nil {
				t.Fatal(err)
			}
			p, err = s.PreviewBackup(path)
			if err != nil {
				t.Fatal(err)
			}
			if !p.SecretsUnreadable {
				t.Errorf("with no key on this computer: unreadable %v, want true", p.SecretsUnreadable)
			}
			if _, err := os.Stat(shareKeyPath()); !os.IsNotExist(err) {
				t.Errorf("previewing created a key file: %v", err)
			}
		})
	}
}

// A backup that holds no sealed secrets is never unreadable, whatever key is here.
func TestPreviewWithNoSecretsIsNeverUnreadable(t *testing.T) {
	useHome(t)
	s := open(t)
	b, err := s.WriteBackup(ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	replaceShareKey(t, 9)
	p, err := s.PreviewBackup(filepath.Join(s.backupDir(), b.ID+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if p.SecretsUnreadable || slices.Contains(p.Warnings, secretsWarning) {
		t.Errorf("no secrets: unreadable %v, warnings %q", p.SecretsUnreadable, p.Warnings)
	}
}
