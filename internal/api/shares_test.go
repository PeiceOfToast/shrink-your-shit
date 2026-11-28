// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/share"
)

// fakeMounter stands in for the root helper: it records what it was asked, makes a real folder for
// each share (so a library can be added on the point, as with the helper) and answers a real folder
// for a test mount so the service's stat succeeds.
type fakeMounter struct {
	mu       sync.Mutex
	base     string
	testAt   string
	fail     map[string]*share.MountError
	mounts   []share.Spec
	unmounts []share.Spec
}

func newFakeMounter(base string) *fakeMounter {
	return &fakeMounter{base: base, testAt: base, fail: map[string]*share.MountError{}}
}

// failOn makes the fake refuse one source, as a helper that cannot reach the server would.
func (m *fakeMounter) failOn(source string, err *share.MountError) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail[source] = err
}

// heal makes a source answer again.
func (m *fakeMounter) heal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = map[string]*share.MountError{}
}

func (m *fakeMounter) Mount(_ context.Context, spec share.Spec) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mounts = append(m.mounts, spec)
	if err := m.fail[spec.Address.Source]; err != nil {
		return "", err
	}
	if spec.ID == 0 {
		return m.testAt, nil
	}
	point := filepath.Join(m.base, strconv.FormatInt(spec.ID, 10))
	if err := os.MkdirAll(point, 0o755); err != nil {
		return "", err
	}
	return point, nil
}

func (m *fakeMounter) Unmount(_ context.Context, spec share.Spec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unmounts = append(m.unmounts, spec)
	return nil
}

func (m *fakeMounter) mountCalls() []share.Spec {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]share.Spec(nil), m.mounts...)
}

func (m *fakeMounter) unmountCalls() []share.Spec {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]share.Spec(nil), m.unmounts...)
}

func shareHeaders(cookie *http.Cookie) map[string]string {
	return map[string]string{"Cookie": cookie.String()}
}

// TestSharesStartEmptyAndSayHelperMode pins the list's shape before anything is added: items is a
// list, never null, and the mode says this computer can mount its own shares.
func TestSharesStartEmptyAndSayHelperMode(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := e.do(http.MethodGet, "/api/v2/shares", "", shareHeaders(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	var body struct {
		Mode  string            `json:"mode"`
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Mode != "helper" {
		t.Errorf("mode = %q, want helper", body.Mode)
	}
	if body.Items == nil || len(body.Items) != 0 {
		t.Errorf("items = %v, want an empty list", body.Items)
	}
}

// TestAddShareSealsThePasswordAndNeverLeaksIt: the row stores no readable password, the answer has
// hasPassword only, and the events log holds no copy either.
func TestAddShareSealsThePasswordAndNeverLeaksIt(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	const password = "hunter2-hunter2"
	body := mustJSON(t, map[string]any{
		"address": `\\nas\media\Films`, "username": `WORKGROUP\viewer`, "password": password,
	})
	rec := e.do(http.MethodPost, "/api/v2/shares", body, write(shareHeaders(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), password) {
		t.Fatalf("the answer carries the password: %s", rec.Body)
	}
	var row struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		Address     string `json:"address"`
		Username    string `json:"username"`
		HasPassword bool   `json:"hasPassword"`
		Connected   bool   `json:"connected"`
		Point       string `json:"point"`
		Since       string `json:"since"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.ID != "1" || row.Name != "Films" || row.Type != "smb" ||
		row.Address != "//nas/media/Films" || row.Username != `WORKGROUP\viewer` {
		t.Errorf("share row = %+v", row)
	}
	if !row.HasPassword {
		t.Error("hasPassword = false, want true")
	}
	if !row.Connected || row.Point != filepath.Join(e.mounter.base, "1") || row.Since == "" {
		t.Errorf("connection = %+v, want it mounted with since", row)
	}
	specs := e.mounter.mountCalls()
	if len(specs) != 1 {
		t.Fatalf("mount calls = %d, want 1", len(specs))
	}
	if specs[0].Username != "viewer" || specs[0].Domain != "WORKGROUP" {
		t.Errorf("helper got username %q domain %q, want the two parts", specs[0].Username, specs[0].Domain)
	}
	if string(specs[0].Password) != password {
		t.Error("the helper did not get the password")
	}

	var sealed []byte
	if err := e.db.Read().QueryRow(`SELECT password_sealed FROM shares WHERE id = 1`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(password)) {
		t.Fatal("the stored password is readable in clear")
	}
	plain, err := e.access.Unseal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != password {
		t.Errorf("the sealed password opened to %q", plain)
	}

	// The list answer is the other place the row is rendered: it must not carry it either.
	rec = e.do(http.MethodGet, "/api/v2/shares", "", shareHeaders(cookie), false)
	if strings.Contains(rec.Body.String(), password) {
		t.Fatalf("the list carries the password: %s", rec.Body)
	}
	e.closeBus()
	evs, _, err := e.bus.Query(t.Context(), events.Filter{}, events.Page{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(password)) {
			t.Fatalf("event %d carries the password: %s", ev.Seq, raw)
		}
	}
}

// TestAddShareWithAFailedMountIsKeptNotConnected is question 1: the share is saved and Try again
// has something to retry, rather than losing what the person typed.
func TestAddShareWithAFailedMountIsKeptNotConnected(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	e.mounter.failOn("//offline/media", &share.MountError{
		Reason: share.ReasonUnreachable, Message: "The share did not answer.",
	})
	body := mustJSON(t, map[string]any{
		"address": "//offline/media", "username": "viewer", "password": "hunter2-hunter2",
	})
	rec := e.do(http.MethodPost, "/api/v2/shares", body, write(shareHeaders(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	var row struct {
		ID        string `json:"id"`
		Connected bool   `json:"connected"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.Connected {
		t.Error("a share whose first mount failed is connected")
	}
	if row.Reason != "The share did not answer." {
		t.Errorf("reason = %q", row.Reason)
	}
	rec = e.do(http.MethodGet, "/api/v2/shares", "", shareHeaders(cookie), false)
	if !strings.Contains(rec.Body.String(), `"id":"1"`) {
		t.Fatalf("the failed share was not kept: %s", rec.Body)
	}
}

// TestTestShareAnswersOkOrAReason: Test never saves and never mounts as a library; it answers the
// page's one question with a code and a sentence.
func TestTestShareAnswersOkOrAReason(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	e.mounter.failOn("//offline/media", &share.MountError{
		Reason: share.ReasonUnreachable, Message: "The share did not answer.",
	})
	ok := mustJSON(t, map[string]any{"address": "//nas/media", "username": "viewer", "password": "pw-1234-5678"})
	rec := e.do(http.MethodPost, "/api/v2/shares/test", ok, write(shareHeaders(cookie)), false)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("ok test: status %d, body %q", rec.Code, rec.Body)
	}
	bad := mustJSON(t, map[string]any{"address": "//offline/media", "username": "viewer", "password": "pw-1234-5678"})
	rec = e.do(http.MethodPost, "/api/v2/shares/test", bad, write(shareHeaders(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("failed test: status %d, body %s", rec.Code, rec.Body)
	}
	var answer struct {
		OK      bool   `json:"ok"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.OK || answer.Reason != "unreachable" || answer.Message != "The share did not answer." {
		t.Errorf("failed test answer = %+v", answer)
	}
	// A test mounts for itself and unmounts again; nothing is saved.
	calls := e.mounter.mountCalls()
	if len(calls) != 2 || calls[0].ID != 0 || calls[1].ID != 0 {
		t.Errorf("mount calls = %+v, want two test mounts (ID 0)", calls)
	}
	if n := len(e.mounter.unmountCalls()); n != 1 {
		t.Errorf("unmount calls = %d, want the one successful test mount unmounted", n)
	}
	if rec := e.do(http.MethodGet, "/api/v2/shares", "", shareHeaders(cookie), false); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("a test saved a share: %s", rec.Body)
	}
}

// TestShareFormErrorsPointAtTheField: the address decides the type; a password is required for an
// SMB sign-in; NFS takes none. Each refusal is 400 with the field the form highlights.
func TestShareFormErrorsPointAtTheField(t *testing.T) {
	cases := []struct {
		name  string
		body  map[string]any
		code  string
		field string
	}{
		{"empty address", map[string]any{"username": "u", "password": "pw-1234-5678"}, "invalid_address", "address"},
		{"unknown shape", map[string]any{"address": "nas/media", "username": "u", "password": "pw-1234-5678"}, "invalid_address", "address"},
		{"type disagrees", map[string]any{"type": "nfs", "address": "//nas/media", "username": "u", "password": "pw-1234-5678"}, "invalid_address", "address"},
		{"unknown type", map[string]any{"type": "ftp", "address": "//nas/media", "username": "u", "password": "pw-1234-5678"}, "invalid_type", "type"},
		{"no password", map[string]any{"address": "//nas/media", "username": "u"}, "missing_password", "password"},
		{"guest with password", map[string]any{"address": "//nas/media", "guest": true, "password": "pw-1234-5678"}, "invalid_request", "password"},
		{"nfs with password", map[string]any{"address": "nas:/export", "password": "pw-1234-5678"}, "invalid_request", "password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, true)
			cookie := e.setup()
			rec := e.do(http.MethodPost, "/api/v2/shares", mustJSON(t, tc.body), write(shareHeaders(cookie)), false)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, body %s", rec.Code, rec.Body)
			}
			var body struct {
				Error struct {
					Code  string `json:"code"`
					Field string `json:"field"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tc.code || body.Error.Field != tc.field {
				t.Errorf("error = %+v, want %s on %s", body.Error, tc.code, tc.field)
			}
			if n := len(e.mounter.mountCalls()); n != 0 {
				t.Errorf("a refused form reached the helper %d times", n)
			}
		})
	}
}

// TestNFSShareNeedsNoPassword: an export mounts with no sign-in, and the row says so.
func TestNFSShareNeedsNoPassword(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	body := mustJSON(t, map[string]any{"address": "nas:/export/Films"})
	rec := e.do(http.MethodPost, "/api/v2/shares", body, write(shareHeaders(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	var row struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Address     string `json:"address"`
		HasPassword bool   `json:"hasPassword"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.Name != "Films" || row.Type != "nfs" || row.Address != "nas:/export/Films" || row.HasPassword {
		t.Errorf("row = %+v", row)
	}
	if p := e.mounter.mountCalls()[0].Password; p != nil {
		t.Errorf("the helper got a password for an NFS export: %q", p)
	}
}

// TestReconnectAnswersTheShareAndRecordsTheOutcome: the retry that follows a not-connected save.
func TestReconnectAnswersTheShareAndRecordsTheOutcome(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	e.mounter.failOn("//nas/media", &share.MountError{Reason: share.ReasonUnreachable, Message: "The share did not answer."})
	body := mustJSON(t, map[string]any{"address": "//nas/media", "username": "viewer", "password": "pw-1234-5678"})
	if rec := e.do(http.MethodPost, "/api/v2/shares", body, write(shareHeaders(cookie)), false); rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	e.mounter.heal()
	rec := e.do(http.MethodPost, "/api/v2/shares/1/reconnect", "", write(shareHeaders(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("reconnect: %d %s", rec.Code, rec.Body)
	}
	var row struct {
		ID        string `json:"id"`
		Connected bool   `json:"connected"`
		Point     string `json:"point"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.ID != "1" || !row.Connected || row.Point != filepath.Join(e.mounter.base, "1") || row.Reason != "" {
		t.Errorf("reconnected share = %+v", row)
	}
	if rec := e.do(http.MethodPost, "/api/v2/shares/99/reconnect", "", write(shareHeaders(cookie)), false); rec.Code != http.StatusNotFound {
		t.Errorf("unknown share: status %d, body %s", rec.Code, rec.Body)
	}
}

// TestDeleteShareRefusesWhileALibraryUsesIt then forgets the share and unmounts it.
func TestDeleteShareRefusesWhileALibraryUsesIt(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	body := mustJSON(t, map[string]any{"address": "//nas/media", "username": "viewer", "password": "pw-1234-5678"})
	if rec := e.do(http.MethodPost, "/api/v2/shares", body, write(shareHeaders(cookie)), false); rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	if err := e.db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO libraries (name, path, share_id, watch) VALUES ('Films', '/srv/films', 1, 'daily')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodDelete, "/api/v2/shares/1", "", write(shareHeaders(cookie)), false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("in-use delete: status %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "share_in_use") {
		t.Errorf("in-use delete body = %s", rec.Body)
	}
	if err := e.db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE libraries SET share_id = NULL WHERE path = '/srv/films'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rec = e.do(http.MethodDelete, "/api/v2/shares/1", "", write(shareHeaders(cookie)), false)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d, body %s", rec.Code, rec.Body)
	}
	if n := len(e.mounter.unmountCalls()); n != 1 {
		t.Errorf("unmount calls = %d, want 1", n)
	}
	if rec := e.do(http.MethodGet, "/api/v2/shares", "", shareHeaders(cookie), false); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("the deleted share is still listed: %s", rec.Body)
	}
	if rec := e.do(http.MethodDelete, "/api/v2/shares/1", "", write(shareHeaders(cookie)), false); rec.Code != http.StatusNotFound {
		t.Errorf("deleting twice: status %d, body %s", rec.Code, rec.Body)
	}
}

// mustAddShare adds one share through the API and answers its row's point.
func mustAddShare(t *testing.T, e *env, cookie *http.Cookie, address, username, password string) string {
	t.Helper()
	body := mustJSON(t, map[string]any{"address": address, "username": username, "password": password})
	rec := e.do(http.MethodPost, "/api/v2/shares", body, write(shareHeaders(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add share %s: %d %s", address, rec.Code, rec.Body)
	}
	var row struct {
		Point string `json:"point"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	return row.Point
}

// TestAddLibraryOnAShareTakesTheSharesFolder: Add with a shareId mounts the share (the helper leaves
// a mounted one alone), and the library is added on the point the helper chose, with the share's own
// name when the form left it empty and the shareId on the row.
func TestAddLibraryOnAShareTakesTheSharesFolder(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	point := mustAddShare(t, e, cookie, `\\nas\media`, "viewer", "pw-1234-5678")

	rec := e.do(http.MethodPost, "/api/v2/libraries", `{"shareId":"1"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add library: %d %s", rec.Code, rec.Body)
	}
	var row struct {
		Name    string  `json:"name"`
		Path    string  `json:"path"`
		ShareID *string `json:"shareId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.Path != point {
		t.Errorf("path = %q, want the share's point %q", row.Path, point)
	}
	if row.Name != "media" {
		t.Errorf("name = %q, want the share's own name", row.Name)
	}
	if row.ShareID == nil || *row.ShareID != "1" {
		t.Errorf("shareId = %v, want 1", row.ShareID)
	}
	// The add mounts it again: the helper answers a share it already mounted with the same point.
	specs := e.mounter.mountCalls()
	if len(specs) < 2 || specs[len(specs)-1].ID != 1 || specs[len(specs)-1].Address.Sub != "" {
		t.Errorf("last mount = %+v, want the share's whole source", specs)
	}
}

// TestAddLibraryOnAShareUsesTheAddressesSubfolder: a share saved on //nas/media/Films is mounted
// whole and its folder is inside the mount, so the library's path is the point plus Films. A share
// without that folder is refused with a sentence that names it.
func TestAddLibraryOnAShareUsesTheAddressesSubfolder(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	point := mustAddShare(t, e, cookie, `\\nas\media\Films`, "viewer", "pw-1234-5678")

	rec := e.do(http.MethodPost, "/api/v2/libraries", `{"shareId":"1"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing subfolder: %d %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); !strings.Contains(body, "no folder named Films") || !strings.Contains(body, `"field":"shareId"`) {
		t.Fatalf("missing subfolder answer = %s", body)
	}
	if err := os.MkdirAll(filepath.Join(point, "Films"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec = e.do(http.MethodPost, "/api/v2/libraries", `{"shareId":"1"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add library: %d %s", rec.Code, rec.Body)
	}
	var row struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(point, "Films"); row.Path != want {
		t.Errorf("path = %q, want %q", row.Path, want)
	}
}

// TestAddLibraryOnADownShareIsRefused: a share the helper could not mount has no folder to add, and
// the answer says what the helper said. Nothing is added.
func TestAddLibraryOnADownShareIsRefused(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	mustAddShare(t, e, cookie, `\\nas\media`, "viewer", "pw-1234-5678")
	const sentence = "The server did not answer. Check its address and try again."
	e.mounter.failOn("//nas/media", &share.MountError{Reason: share.ReasonUnreachable, Message: sentence})

	rec := e.do(http.MethodPost, "/api/v2/libraries", `{"shareId":"1"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("down share: %d %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); !strings.Contains(body, "share_down") || !strings.Contains(body, sentence) {
		t.Fatalf("down share answer = %s", body)
	}
	if rec := e.do(http.MethodGet, "/api/v2/libraries", "", write(withSession(cookie)), false); !strings.Contains(rec.Body.String(), `[]`) {
		t.Errorf("the refused add still added a library: %s", rec.Body)
	}
	rec = e.do(http.MethodPost, "/api/v2/libraries", `{"shareId":"9"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown share: %d %s", rec.Code, rec.Body)
	}
}

// TestHostModeSaysSoAndRefusesAShareAdd: without the root helper GET /shares names host mode with
// its sentence, and Add cannot mount the share for the person, so it refuses instead of guessing at
// a folder that may not be the share at all.
func TestHostModeSaysSoAndRefusesAShareAdd(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	mustAddShare(t, e, cookie, `\\nas\media`, "viewer", "pw-1234-5678")
	e.shares.HostMode(share.NoHelperReason)

	rec := e.do(http.MethodGet, "/api/v2/shares", "", shareHeaders(cookie), false)
	var body struct {
		Mode       string `json:"mode"`
		ModeReason string `json:"modeReason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Mode != "host" || body.ModeReason != share.NoHelperReason {
		t.Errorf("mode = %q reason = %q, want host mode with its sentence", body.Mode, body.ModeReason)
	}

	before := len(e.mounter.mountCalls())
	rec = e.do(http.MethodPost, "/api/v2/libraries", `{"shareId":"1"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("host add: %d %s", rec.Code, rec.Body)
	}
	if b := rec.Body.String(); !strings.Contains(b, "no share helper") || !strings.Contains(b, `"field":"shareId"`) {
		t.Fatalf("host add answer = %s", b)
	}
	if got := len(e.mounter.mountCalls()); got != before {
		t.Errorf("host mode tried to mount: %d calls, want %d", got, before)
	}
}
