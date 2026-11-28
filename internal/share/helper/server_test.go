// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/share"
)

// fakeRunner stands in for /bin/mount and /bin/umount: it records the argv and keeps a text
// /proc/self/mountinfo in step, so the helper's record checks run against the same table a real
// mount would produce.
type fakeRunner struct {
	mu      sync.Mutex
	calls   [][]string
	fail    map[string]string
	delay   time.Duration
	fstype  string // what the mount table says for a successful mount; "" uses the -t value
	onMount func([]string)
	active  int
	maxSeen int
	info    string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.active++
	if f.active > f.maxSeen {
		f.maxSeen = f.active
	}
	delay, cb := f.delay, f.onMount
	msg, fail := f.fail[name]
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if fail {
		return []byte(msg), errors.New("exit status 1")
	}
	switch name {
	case "/bin/mount":
		if cb != nil {
			cb(args)
		}
		fstype := args[1]
		f.mu.Lock()
		if f.fstype != "" {
			fstype = f.fstype
		}
		f.mu.Unlock()
		f.appendMount(args[5], args[4], fstype)
	case "/bin/umount":
		f.removeMount(args[0])
	}
	return nil, nil
}

func (f *fakeRunner) appendMount(point, source, fstype string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := os.ReadFile(f.info)
	line := fmt.Sprintf("36 25 0:32 / %s rw - %s %s rw\n", point, fstype, source)
	_ = os.WriteFile(f.info, append(body, line...), 0o644)
}

func (f *fakeRunner) removeMount(point string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := os.ReadFile(f.info)
	var keep []string
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 4 && fields[4] == point {
			continue
		}
		keep = append(keep, line)
	}
	_ = os.WriteFile(f.info, []byte(strings.Join(keep, "\n")), 0o644)
}

func (f *fakeRunner) failOn(bin, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[bin] = msg
}

func (f *fakeRunner) setDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

func (f *fakeRunner) setOnMount(cb func([]string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onMount = cb
}

func (f *fakeRunner) useFstype(fstype string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fstype = fstype
}

func (f *fakeRunner) recorded() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.calls...)
}

func (f *fakeRunner) parallelPeak() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxSeen
}

// harness is a helper over temporary folders, speaking over a real unix socket.
type harness struct {
	t                             *testing.T
	srv                           *Server
	run                           *fakeRunner
	root, recordDir, runDir, sock string
}

func newHarness(t *testing.T, mutate ...func(*Server)) *harness {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the helper's socket and record tests are Linux-only")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "shares")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	recordDir := filepath.Join(dir, "record")
	runDir := filepath.Join(dir, "run")
	if err := os.Mkdir(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mountinfo := filepath.Join(dir, "mountinfo")
	if err := os.WriteFile(mountinfo, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Unix socket paths are short on some systems; data stays in t.TempDir.
	sockDir, err := os.MkdirTemp("", "syp-helper-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "s")
	run := &fakeRunner{fail: map[string]string{}, info: mountinfo}
	srv := &Server{
		Root: root, RecordDir: recordDir, RunDir: runDir,
		MountBin: "/bin/mount", UmountBin: "/bin/umount", MountInfo: mountinfo,
		UID: 1000, GID: 1000, Run: run.Run,
		PeerUID: func(*net.UnixConn) (uint32, error) { return 1000, nil },
		PeerOK:  func(uid uint32) bool { return uid == 0 || uid == 1000 },
	}
	for _, m := range mutate {
		m(srv)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the helper did not stop")
		}
	})
	return &harness{t: t, srv: srv, run: run, root: root, recordDir: recordDir, runDir: runDir, sock: sock}
}

// roundTrip sends one request and reads one answer. The client half-closes so the helper reads the
// whole request, as the app's client will.
func (h *harness) roundTrip(body string) (Response, error) {
	conn, err := net.Dial("unix", h.sock)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(body)); err != nil {
		return Response{}, err
	}
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func (h *harness) ask(body string) Response {
	h.t.Helper()
	resp, err := h.roundTrip(body)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp
}

func (h *harness) askRequest(req Request) Response {
	h.t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.ask(string(b))
}

func (h *harness) mountInfo() string {
	h.t.Helper()
	b, err := os.ReadFile(h.srv.MountInfo)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

func (h *harness) setMountInfo(text string) {
	h.t.Helper()
	if err := os.WriteFile(h.srv.MountInfo, []byte(text), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) records() []record {
	h.t.Helper()
	recs, err := loadRecords(filepath.Join(h.recordDir, "mounts.json"))
	if err != nil {
		h.t.Fatal(err)
	}
	return recs
}

type credential struct {
	path string
	mode os.FileMode
	body string
	err  error
}

// TestMountAndUnmount: the helper mounts at the point it derives, writes the sign-in to a 0600
// file only mount sees, records the mount, and removes the credentials file before it answers.
func TestMountAndUnmount(t *testing.T) {
	h := newHarness(t)
	seen := make(chan credential, 1)
	h.run.setOnMount(func(args []string) {
		var c credential
		if len(args) > 3 {
			for _, opt := range strings.Split(args[3], ",") {
				if path, ok := strings.CutPrefix(opt, "credentials="); ok {
					c.path = path
					if fi, err := os.Stat(path); err == nil {
						c.mode = fi.Mode().Perm()
					} else {
						c.err = err
					}
					b, err := os.ReadFile(path)
					if err != nil {
						c.err = err
					}
					c.body = string(b)
				}
			}
		}
		seen <- c
	})
	resp := h.askRequest(Request{
		Action: ActionMount, ID: 1, Type: "smb", Source: "//nas/media",
		Options: []string{"vers=3.0"}, Username: "viewer", Domain: "WORKGROUP",
		Password: "hunter2-hunter2",
	})
	point := filepath.Join(h.root, "1")
	if !resp.OK || resp.Point != point {
		t.Fatalf("mount = %+v, want %s", resp, point)
	}
	var c credential
	select {
	case c = <-seen:
	case <-time.After(time.Second):
		t.Fatal("mount ran without a credentials file")
	}
	if c.err != nil || c.mode != 0o600 {
		t.Errorf("credentials file = %q mode %o, err %v", c.path, c.mode, c.err)
	}
	want := "username=viewer\npassword=hunter2-hunter2\ndomain=WORKGROUP\n"
	if c.body != want {
		t.Errorf("credentials file body = %q", c.body)
	}
	if _, err := os.Stat(c.path); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the credentials file survived the mount")
	}
	if entries, _ := os.ReadDir(h.runDir); len(entries) != 0 {
		t.Errorf("the run folder still holds %d files", len(entries))
	}
	calls := h.run.recorded()
	if len(calls) != 1 {
		t.Fatalf("commands = %v", calls)
	}
	argv := calls[0]
	if len(argv) != 7 || argv[0] != "/bin/mount" || argv[1] != "-t" || argv[2] != "cifs" ||
		argv[3] != "-o" || argv[5] != "//nas/media" || argv[6] != point {
		t.Fatalf("argv = %q", argv)
	}
	for _, opt := range []string{"vers=3.0", "nosuid,nodev,noexec", "credentials=" + c.path,
		"file_mode=0660", "dir_mode=0770", "uid=1000", "gid=1000"} {
		if !strings.Contains(argv[4], opt) {
			t.Errorf("options = %q, missing %q", argv[4], opt)
		}
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "hunter2") || strings.Contains(joined, "password=") {
		t.Error("the password reached the mount command's argv")
	}
	if !strings.Contains(h.mountInfo(), point) {
		t.Error("the mount table does not show the mount")
	}
	recs := h.records()
	if len(recs) != 1 || recs[0].ID != 1 || recs[0].Source != "//nas/media" ||
		recs[0].FSType != "cifs" || recs[0].Point != point || recs[0].When.IsZero() {
		t.Errorf("record = %+v", recs)
	}
	fi, err := os.Stat(filepath.Join(h.recordDir, "mounts.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("record file mode = %v, %v", fi.Mode().Perm(), err)
	}

	if resp := h.askRequest(Request{Action: ActionUnmount, ID: 1}); !resp.OK {
		t.Fatalf("unmount = %+v", resp)
	}
	calls = h.run.recorded()
	last := calls[len(calls)-1]
	if last[0] != "/bin/umount" || last[1] != point {
		t.Errorf("umount argv = %q", last)
	}
	if len(h.records()) != 0 {
		t.Error("the record survived the unmount")
	}
	if strings.Contains(h.mountInfo(), point) {
		t.Error("the mount table still shows the mount")
	}
}

// TestHandMountIsNotClaimed: a live mount under the root with no record is someone's own mount,
// and unmount refuses it.
func TestHandMountIsNotClaimed(t *testing.T) {
	h := newHarness(t)
	point := filepath.Join(h.root, "7")
	if err := os.Mkdir(point, 0o770); err != nil {
		t.Fatal(err)
	}
	h.setMountInfo(fmt.Sprintf("36 25 0:32 / %s rw - cifs //person/media rw\n", point))
	resp := h.askRequest(Request{Action: ActionUnmount, ID: 7})
	if resp.Reason != ReasonRefused {
		t.Errorf("unmount = %+v, want a refusal", resp)
	}
	if len(h.run.recorded()) != 0 {
		t.Error("umount ran on a hand mount")
	}
	if !strings.Contains(h.mountInfo(), point) {
		t.Error("the hand mount was removed")
	}
}

// TestMismatchedRecordIsDroppedAndTheMountLeftAlone: the record and the live line must agree on the
// point, source and type; anything else is refused and the record is dropped.
func TestMismatchedRecordIsDroppedAndTheMountLeftAlone(t *testing.T) {
	h := newHarness(t)
	point := filepath.Join(h.root, "1")
	resp := h.askRequest(Request{Action: ActionMount, ID: 1, Type: "smb", Source: "//nas/media", Guest: true})
	if !resp.OK {
		t.Fatalf("mount = %+v", resp)
	}
	h.setMountInfo(fmt.Sprintf("36 25 0:32 / %s rw - cifs //other/share rw\n", point))
	if resp := h.askRequest(Request{Action: ActionUnmount, ID: 1}); resp.Reason != ReasonRefused {
		t.Errorf("unmount = %+v, want a refusal", resp)
	}
	if n := len(h.run.recorded()); n != 1 {
		t.Errorf("umount ran on a mismatched mount (%d commands)", n)
	}
	if len(h.records()) != 0 {
		t.Error("the mismatched record survived")
	}
	if !strings.Contains(h.mountInfo(), point) {
		t.Error("the mount was removed")
	}
}

// TestUnmountWhenTheMountIsGoneDropsTheRecord: a stale record for a mount that is no longer there
// is answered not_mounted and forgotten, without running umount.
func TestUnmountWhenTheMountIsGoneDropsTheRecord(t *testing.T) {
	h := newHarness(t)
	if resp := h.askRequest(Request{Action: ActionMount, ID: 1, Type: "smb", Source: "//nas/media", Guest: true}); !resp.OK {
		t.Fatalf("mount = %+v", resp)
	}
	h.setMountInfo("")
	if resp := h.askRequest(Request{Action: ActionUnmount, ID: 1}); resp.Reason != ReasonNotMounted {
		t.Errorf("unmount = %+v, want not_mounted", resp)
	}
	if n := len(h.run.recorded()); n != 1 {
		t.Errorf("umount ran for a mount that was gone (%d commands)", n)
	}
	if len(h.records()) != 0 {
		t.Error("the stale record survived")
	}
}

// TestPointCannotBeSymlinkedOutsideTheRoot: the point is derived under the root, and a symlink
// there is refused rather than followed (it could name /etc).
func TestPointCannotBeSymlinkedOutsideTheRoot(t *testing.T) {
	h := newHarness(t)
	if err := os.Symlink("/etc", filepath.Join(h.root, "5")); err != nil {
		t.Fatal(err)
	}
	resp := h.askRequest(Request{Action: ActionMount, ID: 5, Type: "smb", Source: "//nas/media", Guest: true})
	if resp.Reason != ReasonRefused {
		t.Errorf("mount = %+v, want a refusal", resp)
	}
	if len(h.run.recorded()) != 0 {
		t.Error("mount ran at a symlinked point")
	}
}

// TestRequestRefusals: unknown fields, unknown actions, a point in the request, an address with a
// folder inside the share, a password in options and other bad shapes all stop at the door.
func TestRequestRefusals(t *testing.T) {
	h := newHarness(t)
	for _, body := range []string{
		`{"action":"mount","id":1,"type":"smb","source":"//nas/media","guest":true,"point":"/etc"}`,
		`{"action":"format","id":1}`,
		`{"action":"mount","id":1,"source":"//nas/media","guest":true}`,
		`{"action":"mount","id":1,"type":"smb","source":"//nas/media/Films","guest":true}`,
		`{"action":"mount","id":1,"type":"smb","source":"//nas/media","guest":true,"options":["password=hunter2"]}`,
		`{"action":"mount","id":1,"type":"smb","source":"//nas/media","options":["nosuid"]}`,
		`{"action":"mount","id":-1,"type":"smb","source":"//nas/media","guest":true}`,
		`{"action":"mount","id":1,"type":"nfs","source":"nas.local:/export","username":"viewer"}`,
		`{"action":"probe","id":0}`,
		`{"action":"probe","id":1} {"action":"probe","id":1}`,
		`{}`,
	} {
		if resp := h.ask(body); resp.Reason != ReasonInvalidRequest {
			t.Errorf("%s = %+v, want invalid_request", body, resp)
		}
	}
	if len(h.run.recorded()) != 0 {
		t.Error("a refused request ran a command")
	}
}

// TestRequestCap: one request is at most 64 KiB.
func TestRequestCap(t *testing.T) {
	h := newHarness(t)
	body := `{"action":"probe","id":1,"pad":"` + strings.Repeat("a", maxRequest) + `"}`
	if resp := h.ask(body); resp.Reason != ReasonInvalidRequest || !strings.Contains(resp.Message, "big") {
		t.Errorf("oversized request = %+v", resp)
	}
}

// TestPeerRefused: a uid outside the rule gets an answer and nothing runs.
func TestPeerRefused(t *testing.T) {
	h := newHarness(t, func(s *Server) {
		s.PeerOK = func(uint32) bool { return false }
	})
	resp := h.askRequest(Request{Action: ActionProbe, ID: 1})
	if resp.Reason != ReasonRefused {
		t.Errorf("probe = %+v, want a refusal", resp)
	}
	if len(h.run.recorded()) != 0 {
		t.Error("a refused peer ran a command")
	}
}

// TestMountFailureCleansUp: mount's error chooses the reason, and the credentials file is still
// removed and no record is written.
func TestMountFailureCleansUp(t *testing.T) {
	h := newHarness(t)
	h.run.failOn("/bin/mount", "mount error(13): Permission denied")
	resp := h.askRequest(Request{
		Action: ActionMount, ID: 1, Type: "smb", Source: "//nas/media",
		Username: "viewer", Password: "wrong-password",
	})
	if resp.OK || resp.Reason != share.ReasonBadCredentials || resp.Message == "" {
		t.Errorf("mount = %+v", resp)
	}
	if entries, _ := os.ReadDir(h.runDir); len(entries) != 0 {
		t.Errorf("the run folder still holds %d files", len(entries))
	}
	if len(h.records()) != 0 {
		t.Error("a failed mount was recorded")
	}
}

// TestNFS: an NFS mount has no credentials file and the safe options the plan names; the record
// keeps the type the mount table reports (nfs4 here).
func TestNFS(t *testing.T) {
	h := newHarness(t)
	h.run.useFstype("nfs4")
	resp := h.askRequest(Request{Action: ActionMount, ID: 2, Type: "nfs", Source: "nas.local:/export"})
	if !resp.OK {
		t.Fatalf("mount = %+v", resp)
	}
	calls := h.run.recorded()
	if len(calls) != 1 || calls[0][2] != "nfs" || calls[0][3] != "-o" ||
		calls[0][4] != "nosuid,nodev,noexec,tcp,soft,timeo=100,retrans=2" {
		t.Fatalf("argv = %q", calls)
	}
	if entries, _ := os.ReadDir(h.runDir); len(entries) != 0 {
		t.Errorf("an NFS mount made %d credentials files", len(entries))
	}
	recs := h.records()
	if len(recs) != 1 || recs[0].FSType != "nfs4" {
		t.Errorf("record = %+v", recs)
	}
	if resp := h.askRequest(Request{Action: ActionUnmount, ID: 2}); !resp.OK {
		t.Errorf("unmount = %+v", resp)
	}
}

// TestMountIsNotMadeTwice: asking again while the helper's own mount is live answers the same
// point without running mount again.
func TestMountIsNotMadeTwice(t *testing.T) {
	h := newHarness(t)
	req := Request{Action: ActionMount, ID: 1, Type: "smb", Source: "//nas/media", Guest: true}
	first := h.askRequest(req)
	second := h.askRequest(req)
	if !first.OK || !second.OK || first.Point != second.Point {
		t.Fatalf("mounts = %+v, %+v", first, second)
	}
	if n := len(h.run.recorded()); n != 1 {
		t.Errorf("mount ran %d times", n)
	}
}

// TestOneOperationAtATime: two clients asking at once are served one after the other.
func TestOneOperationAtATime(t *testing.T) {
	h := newHarness(t)
	h.run.setDelay(40 * time.Millisecond)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []int64{1, 2} {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			body, _ := json.Marshal(Request{Action: ActionMount, ID: id, Type: "smb", Source: "//nas/media", Guest: true})
			resp, err := h.roundTrip(string(body))
			if err != nil {
				errs <- err
			} else if !resp.OK {
				errs <- fmt.Errorf("mount %d = %+v", id, resp)
			}
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if peak := h.run.parallelPeak(); peak != 1 {
		t.Errorf("%d operations ran at once", peak)
	}
}

// TestProbe: a mounted share answers ok; when its line leaves the mount table it is not_mounted.
func TestProbe(t *testing.T) {
	h := newHarness(t)
	if resp := h.askRequest(Request{Action: ActionMount, ID: 1, Type: "smb", Source: "//nas/media", Guest: true}); !resp.OK {
		t.Fatalf("mount = %+v", resp)
	}
	if resp := h.askRequest(Request{Action: ActionProbe, ID: 1}); !resp.OK || resp.Point == "" {
		t.Errorf("probe = %+v", resp)
	}
	h.setMountInfo("")
	if resp := h.askRequest(Request{Action: ActionProbe, ID: 1}); resp.Reason != ReasonNotMounted {
		t.Errorf("probe of a gone mount = %+v", resp)
	}
}

// TestTestMountIsFresh: a test mount gets a hidden fresh name every time and the earlier one is
// taken back, so nothing accumulates.
func TestTestMountIsFresh(t *testing.T) {
	h := newHarness(t)
	first := h.askRequest(Request{Action: ActionMount, ID: 0, Type: "smb", Source: "//nas/media", Guest: true})
	second := h.askRequest(Request{Action: ActionMount, ID: 0, Type: "smb", Source: "//nas/media", Guest: true})
	if !first.OK || !second.OK || first.Point == second.Point {
		t.Fatalf("test mounts = %+v, %+v", first, second)
	}
	if !strings.HasPrefix(filepath.Base(first.Point), ".test-") ||
		filepath.Dir(first.Point) != h.root {
		t.Errorf("test point = %q", first.Point)
	}
	calls := h.run.recorded()
	if len(calls) != 3 || calls[1][0] != "/bin/umount" || calls[1][1] != first.Point {
		t.Errorf("commands = %v", calls)
	}
	if resp := h.askRequest(Request{Action: ActionUnmount, ID: 0}); !resp.OK {
		t.Errorf("unmount test mount = %+v", resp)
	}
	if len(h.records()) != 0 {
		t.Error("a test mount's record survived")
	}
}

// TestMountIsTakenBackWhenItCannotBeRecorded: a mount the helper cannot record must not stay up,
// because nothing would ever be allowed to unmount it. The action runs directly, because a helper
// whose record file cannot be read refuses to start at all (Serve's load).
func TestMountIsTakenBackWhenItCannotBeRecorded(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "shares")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	mountinfo := filepath.Join(dir, "mountinfo")
	if err := os.WriteFile(mountinfo, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := &fakeRunner{fail: map[string]string{}, info: mountinfo}
	srv := &Server{
		Root: root, RecordDir: filepath.Join(blocked, "record"), RunDir: filepath.Join(dir, "run"),
		MountBin: "/bin/mount", UmountBin: "/bin/umount", MountInfo: mountinfo,
		UID: 1000, GID: 1000, Run: run.Run,
	}
	resp := srv.do(context.Background(), Request{Action: ActionMount, ID: 1, Type: "smb", Source: "//nas/media", Guest: true})
	if resp.Reason != share.ReasonMountFailed {
		t.Errorf("mount = %+v, want mount_failed", resp)
	}
	calls := run.recorded()
	if len(calls) != 2 || calls[1][0] != "/bin/umount" {
		t.Errorf("commands = %v, want a mount then its umount", calls)
	}
	if strings.Contains(readFile(t, mountinfo), "/shares/1") {
		t.Error("the unrecorded mount stayed up")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
