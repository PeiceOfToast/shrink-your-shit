// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package helper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/share"
)

// The request decoder is the helper's whole input: one value, no unknown names, nothing after it.
func TestDecodeRequestRefusals(t *testing.T) {
	ok := `{"action":"probe","id":3}`
	if req, err := decodeRequest([]byte(ok)); err != nil || req.Action != ActionProbe || req.ID != 3 {
		t.Fatalf("decode = %+v, %v", req, err)
	}
	for _, body := range []string{
		`{"action":"probe","id":3,"point":"/etc"}`,
		`{"action":"probe","id":3} {"action":"probe","id":3}`,
		`{"action":"probe","id":3} trailing`,
		``,
	} {
		if _, err := decodeRequest([]byte(body)); err == nil {
			t.Errorf("%q decoded, want a refusal", body)
		}
	}
}

// An id becomes a folder name or nothing: never a path the request could point outside the root.
func TestPointName(t *testing.T) {
	if name, err := pointName(7); err != nil || name != "7" {
		t.Errorf("pointName(7) = %q, %v", name, err)
	}
	a, err := pointName(0)
	if err != nil || !strings.HasPrefix(a, ".test-") || len(a) != len(".test-")+16 {
		t.Fatalf("pointName(0) = %q, %v", a, err)
	}
	b, _ := pointName(0)
	if a == b {
		t.Error("two test mounts got one name")
	}
	if _, err := pointName(-1); err == nil {
		t.Error("a negative id made a name")
	}
}

// The options are rebuilt from the request's checked list and the helper's floor. A password can
// only reach the credentials file, never this list.
func TestMountOptionsAreRebuilt(t *testing.T) {
	req := Request{Options: []string{"vers=3.0", "domain=WORKGROUP"}}
	got := strings.Join(mountOptions(share.SMB, req, "/run/syp/.cred-x", 1000, 1000), ",")
	want := "vers=3.0,domain=WORKGROUP,nosuid,nodev,noexec,credentials=/run/syp/.cred-x,file_mode=0660,dir_mode=0770,uid=1000,gid=1000"
	if got != want {
		t.Errorf("SMB options = %q, want %q", got, want)
	}
	guest := strings.Join(mountOptions(share.SMB, Request{Guest: true}, "", 1000, 1000), ",")
	if strings.Contains(guest, "credentials=") || !strings.Contains(guest, "guest") {
		t.Errorf("guest options = %q", guest)
	}
	nfs := strings.Join(mountOptions(share.NFS, Request{}, "", 0, 0), ",")
	if nfs != "nosuid,nodev,noexec,tcp,soft,timeo=100,retrans=2" {
		t.Errorf("NFS options = %q", nfs)
	}
}

// mount's words choose the page's reason; a sentence is always there.
func TestMountFailureReasons(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want string
	}{
		{"mount error(13): Permission denied", share.ReasonBadCredentials},
		{"mount error(2): No such file or directory", share.ReasonNoSuchShare},
		{"mount error(112): Host is down", share.ReasonUnreachable},
		{"mount: /var/lib/syp/shares/1: special device does not exist.", share.ReasonMountFailed},
	} {
		got := mountFailure(share.SMB, []byte(tc.out))
		if got.Reason != tc.want || got.Message == "" {
			t.Errorf("%q = %+v, want %s", tc.out, got, tc.want)
		}
	}
}

// A mount table line is the truth about what is live; the parser's escaping is internal.
func TestLiveMount(t *testing.T) {
	dir := t.TempDir()
	mi := filepath.Join(dir, "mountinfo")
	line := "36 25 0:32 / /var/lib/syp/shares/1 rw - cifs //nas/media rw\n"
	if err := os.WriteFile(mi, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{MountInfo: mi}
	m, ok, err := s.liveMount("/var/lib/syp/shares/1")
	if err != nil || !ok || m.Source != "//nas/media" || m.FSType != "cifs" {
		t.Errorf("liveMount = %+v, %v, %v", m, ok, err)
	}
	if _, ok, _ := s.liveMount("/var/lib/syp/shares/2"); ok {
		t.Error("a point with no line is live")
	}
}

// The record file is root-only and atomic; a torn or unknown file stops the helper rather than
// making it guess.
func TestRecordFileShape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file modes are not Windows's")
	}
	dir := filepath.Join(t.TempDir(), "record")
	recs := []record{{ID: 1, Source: "//nas/media", FSType: "cifs", Point: "/var/lib/syp/shares/1", When: time.Unix(1, 0)}}
	if err := saveRecords(dir, recs); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mounts.json")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("record file mode = %o, want 600", perm)
	}
	if perm := dirMode(t, dir); perm != 0o700 {
		t.Errorf("record folder mode = %o, want 700", perm)
	}
	got, err := loadRecords(path)
	if err != nil || len(got) != 1 || got[0].ID != 1 || got[0].Point != recs[0].Point {
		t.Errorf("loadRecords = %+v, %v", got, err)
	}
	if got, err := loadRecords(filepath.Join(dir, "missing.json")); err != nil || got != nil {
		t.Errorf("missing record file = %+v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{"mounts":[{"id":1}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRecords(path); err == nil {
		t.Error("an unusable record file loaded")
	}
	if err := os.WriteFile(path, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRecords(path); err == nil {
		t.Error("malformed record JSON loaded")
	}
}

func dirMode(t *testing.T, dir string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// A request round-trips the same JSON to both sides.
func TestResponseShape(t *testing.T) {
	b, err := json.Marshal(Response{OK: true, Point: "/var/lib/syp/shares/1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"ok":true,"point":"/var/lib/syp/shares/1"}` {
		t.Errorf("response = %s", b)
	}
	b, _ = json.Marshal(Response{Reason: ReasonRefused, Message: "no"})
	if !strings.Contains(string(b), `"ok":false`) || !strings.Contains(string(b), `"reason":"refused"`) {
		t.Errorf("a refusal = %s", b)
	}
}
