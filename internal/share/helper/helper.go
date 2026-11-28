// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package helper is the root half of shares: the process syp-mount.service runs as root. The app
// asks it over a unix socket to mount or unmount one SMB or NFS share; the helper derives the mount
// point itself, rebuilds the mount options from an allow-list, and records what it mounted so it
// can never unmount a mount a person made by hand.
package helper

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/share"
)

// The actions one connection may ask for. Nothing else is accepted.
const (
	ActionMount   = "mount"
	ActionUnmount = "unmount"
	ActionProbe   = "probe"
)

// Response reasons that belong to the protocol rather than to a mount. A mount's own reasons are
// share's (unreachable, bad_credentials, no_such_share, mount_failed, timeout).
const (
	ReasonInvalidRequest = "invalid_request"
	ReasonRefused        = "refused"
	ReasonNotMounted     = "not_mounted"
)

const (
	// maxRequest is one JSON request's cap. The request carries a password, never a file.
	maxRequest = 64 << 10
	// requestTimeout bounds reading the request, so a stalled client cannot hold the helper.
	requestTimeout = 5 * time.Second
	// The helper bounds its own work as well as the client bounding the call: a mount the client
	// gave up on must not leave the helper busy forever.
	mountTimeout   = 30 * time.Second
	unmountTimeout = 10 * time.Second
	probeTimeout   = 5 * time.Second
)

// Request is the one JSON value a connection carries. The app never sends a mount point: it sends
// the share's row id and the helper derives the point. Extra fields are refused, so the protocol
// cannot grow by accident.
type Request struct {
	Action   string   `json:"action"`
	ID       int64    `json:"id"`
	Type     string   `json:"type,omitempty"`
	Source   string   `json:"source,omitempty"`
	Options  []string `json:"options,omitempty"`
	Username string   `json:"username,omitempty"`
	Domain   string   `json:"domain,omitempty"`
	Guest    bool     `json:"guest,omitempty"`
	Password string   `json:"password,omitempty"`
}

// Response is the answer: ok with the point the helper chose, or a reason and a sentence.
type Response struct {
	OK      bool   `json:"ok"`
	Point   string `json:"point,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// Runner runs one program with fixed argv, no shell and no environment. The helper uses it for
// /bin/mount and /bin/umount; a test replaces it.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// PeerUID reports the uid behind an accepted unix connection. nil means the platform's SO_PEERCRED
// check (peerUID).
type PeerUID func(*net.UnixConn) (uint32, error)

// Server is the helper without its process wrapper: the accept loop, the peer check and the three
// actions. Run builds one for syp-mount.service; a test builds one over a temporary folder.
type Server struct {
	Root      string // the shares' folder, a real path: the only place a mount point may be
	RecordDir string // root-only folder holding mounts.json
	RunDir    string // the socket's folder; credentials files live here and nowhere else
	MountBin  string
	UmountBin string
	MountInfo string // /proc/self/mountinfo, or a text file in a test
	UID, GID  int    // the syp user: the credentials' owner asks to be these

	// Now is the clock a record's time comes from; nil means time.Now.
	Now func() time.Time
	// Run executes mount and umount; nil means exec with an empty environment.
	Run Runner
	// PeerUID and PeerOK are the who-may-talk rule; Run sets them from the syp user.
	PeerUID PeerUID
	PeerOK  func(uid uint32) bool

	mu      sync.Mutex
	records []record
}

// Serve loads the record file and answers connections until ctx is cancelled. One operation is
// carried out at a time, as the unit promises: the accept loop handles a connection to the end
// before taking the next, and an action also holds the mutex for a direct caller.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	recs, err := loadRecords(s.recordPath())
	if err != nil {
		return err
	}
	s.records = recs
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = ln.Close()
		case <-done:
		}
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.handleConn(ctx, conn)
	}
}

// handleConn checks who is asking, reads one bounded JSON request and answers once.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		_ = writeResponse(conn, refused("This request is not allowed."))
		return
	}
	uidOf, allowed := s.PeerUID, s.PeerOK
	if uidOf == nil {
		uidOf = peerUID
	}
	if allowed == nil {
		allowed = func(uid uint32) bool { return uid == 0 }
	}
	uid, err := uidOf(uc)
	if err != nil || !allowed(uid) {
		_ = writeResponse(conn, refused("This request is not allowed."))
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(requestTimeout))
	body, err := io.ReadAll(io.LimitReader(conn, maxRequest+1))
	if err != nil {
		_ = writeResponse(conn, badRequest("The request could not be read."))
		return
	}
	if len(body) > maxRequest {
		_ = writeResponse(conn, badRequest("The request is too big."))
		return
	}
	req, err := decodeRequest(body)
	if err != nil {
		_ = writeResponse(conn, badRequest(err.Error()))
		return
	}
	_ = writeResponse(conn, s.do(ctx, req))
}

// decodeRequest reads exactly one request and refuses unknown fields and trailing data.
func decodeRequest(body []byte) (Request, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var req Request
	if err := dec.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return Request{}, errors.New("The request is empty.")
		}
		// A fixed phrase: encoding/json quotes the offending byte or field, which can be a password's.
		return Request{}, errors.New("The request can't be read.")
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Request{}, errors.New("The request has extra data.")
	}
	return req, nil
}

// do carries out one request. The mutex makes "one operation at a time" true even for a caller
// that did not come through Serve.
func (s *Server) do(ctx context.Context, req Request) Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch req.Action {
	case ActionMount:
		return s.mount(ctx, req)
	case ActionUnmount:
		return s.unmount(ctx, req)
	case ActionProbe:
		return s.probe(ctx, req)
	default:
		return badRequest("The request's action must be mount, unmount or probe.")
	}
}

// mount derives the point, prepares the credentials, runs mount and records what it mounted.
func (s *Server) mount(ctx context.Context, req Request) Response {
	if req.Type != string(share.SMB) && req.Type != string(share.NFS) {
		return badRequest("The share's type must be smb or nfs.")
	}
	addr, err := share.ParseAddress(req.Source, req.Type)
	if err != nil {
		return badRequest(err.Error())
	}
	if addr.Sub != "" {
		return badRequest("The helper mounts a whole share, not a folder inside it.")
	}
	if req.ID < 0 {
		return badRequest("The share's id is not valid.")
	}
	if err := share.CheckSignIn(addr.Type, req.Username, req.Domain, req.Guest, req.Password); err != nil {
		return badRequest(err.Error())
	}
	if err := share.CheckOptions(addr.Type, req.Options); err != nil {
		return badRequest(err.Error())
	}
	name, err := pointName(req.ID)
	if err != nil {
		return badRequest(err.Error())
	}
	point, err := s.point(name)
	if err != nil {
		return refused("The share's mount point can't be used.")
	}
	if req.ID == 0 {
		s.cleanTestMount()
	} else if rec, ok := s.findRecord(req.ID); ok {
		// A share we mounted and still have mounted is not mounted twice.
		line, live, err := s.liveMount(point)
		if err != nil {
			return mountFailed("The mount table could not be read.")
		}
		if live && line.Source == addr.Source && line.FSType == rec.FSType {
			return Response{OK: true, Point: point}
		}
		if live {
			return refused("Something this app did not mount is at the share's point, so it was left alone.")
		}
		s.dropRecord(req.ID)
	}
	line, live, err := s.liveMount(point)
	if err != nil {
		return mountFailed("The mount table could not be read.")
	}
	if live {
		return refused("Something this app did not mount is at the share's point, so it was left alone.")
	}
	credPath, cleanup, err := s.writeCredentials(addr.Type, req)
	if err != nil {
		return mountFailed("The share's sign-in could not be prepared.")
	}
	defer cleanup()
	args := []string{"-t", fstypeFor(addr.Type), "-o",
		strings.Join(mountOptions(addr.Type, req, credPath, s.UID, s.GID), ","), addr.Source, point}
	mctx, cancel := context.WithTimeout(ctx, mountTimeout)
	defer cancel()
	out, err := s.run(mctx, s.MountBin, args...)
	if err != nil {
		return mountFailure(addr.Type, out)
	}
	line, live, err = s.liveMount(point)
	if err != nil || !live || line.Source != addr.Source || !fstypeAllowed(addr.Type, line.FSType) {
		s.bestEffortUmount(point)
		return mountFailed("The share did not mount.")
	}
	when := time.Now()
	if s.Now != nil {
		when = s.Now()
	}
	if err := s.addRecord(record{
		ID: req.ID, Source: addr.Source, FSType: line.FSType, Point: point, When: when.UTC(),
	}); err != nil {
		s.bestEffortUmount(point)
		return mountFailed("The share mounted, but its record could not be saved.")
	}
	return Response{OK: true, Point: point}
}

// unmount removes only a mount this helper recorded and that the mount table still shows exactly.
// A hand mount, a mismatch, or a mount with no record is refused and never touched.
func (s *Server) unmount(ctx context.Context, req Request) Response {
	if req.ID < 0 {
		return badRequest("The share's id is not valid.")
	}
	rec, hasRec := s.findRecord(req.ID)
	var point string
	if req.ID > 0 {
		point = filepath.Join(s.Root, strconv.FormatInt(req.ID, 10))
	} else {
		if !hasRec {
			return notMounted("The share is not mounted.")
		}
		point = rec.Point
		if !s.underRoot(point) {
			s.dropRecord(req.ID)
			return notMounted("The share is not mounted.")
		}
	}
	if err := checkPoint(point); err != nil {
		return refused("The share's mount point can't be used.")
	}
	line, live, err := s.liveMount(point)
	if err != nil {
		return mountFailed("The mount table could not be read.")
	}
	if !live {
		if hasRec {
			s.dropRecord(req.ID)
		}
		return notMounted("The share is not mounted.")
	}
	if !hasRec || rec.Point != point || rec.Source != line.Source || rec.FSType != line.FSType {
		if hasRec {
			s.dropRecord(req.ID)
		}
		return refused("That mount was not made by this app, so it was left alone.")
	}
	uctx, cancel := context.WithTimeout(ctx, unmountTimeout)
	defer cancel()
	if _, err := s.run(uctx, s.UmountBin, point); err != nil {
		return mountFailed("The share could not be unmounted. It may still be in use.")
	}
	s.dropRecord(req.ID)
	return Response{OK: true}
}

// probe bounds a stat of the share's point: a dead network mount must not hang the helper.
func (s *Server) probe(ctx context.Context, req Request) Response {
	if req.ID <= 0 {
		return badRequest("A probe needs a saved share's id.")
	}
	point := filepath.Join(s.Root, strconv.FormatInt(req.ID, 10))
	if err := checkPoint(point); err != nil {
		return notMounted("The share is not mounted.")
	}
	_, live, err := s.liveMount(point)
	if err != nil {
		return mountFailed("The mount table could not be read.")
	}
	if !live {
		return notMounted("The share is not mounted.")
	}
	err = statBounded(point, probeTimeout)
	if errors.Is(err, errStatTimeout) {
		return Response{Reason: share.ReasonTimeout, Message: "The share is not answering."}
	}
	if err != nil {
		return Response{Reason: share.ReasonUnreachable, Message: "The share is not answering."}
	}
	return Response{OK: true, Point: point}
}

// pointName is the folder inside the root: a share's id, or a fresh name for a test mount. The
// name never comes from the request, so no request can name a path.
func pointName(id int64) (string, error) {
	if id < 0 {
		return "", errors.New("The share's id is not valid.")
	}
	if id == 0 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		return ".test-" + hex.EncodeToString(b[:]), nil
	}
	return strconv.FormatInt(id, 10), nil
}

// point makes the mount point under the root, refusing a symlink or a file where a folder belongs.
func (s *Server) point(name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", errors.New("the mount point's name is not valid")
	}
	point := filepath.Join(s.Root, name)
	if filepath.Dir(point) != filepath.Clean(s.Root) {
		return "", errors.New("the mount point is outside the shares folder")
	}
	fi, err := os.Lstat(point)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.Mkdir(point, 0o770); err != nil {
			return "", err
		}
		if os.Geteuid() == 0 {
			if err := os.Chown(point, s.UID, s.GID); err != nil {
				return "", err
			}
		}
	case err != nil:
		return "", err
	case fi.Mode()&fs.ModeSymlink != 0:
		return "", errors.New("the mount point is a symbolic link")
	case !fi.IsDir():
		return "", errors.New("the mount point is not a folder")
	}
	return point, nil
}

// checkPoint refuses a symlink at a point the helper is about to pass to umount or stat.
func checkPoint(point string) error {
	fi, err := os.Lstat(point)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return errors.New("the mount point is a symbolic link")
	}
	return nil
}

func (s *Server) underRoot(point string) bool {
	point = filepath.Clean(point)
	return point != filepath.Clean(s.Root) && strings.HasPrefix(point, filepath.Clean(s.Root)+string(os.PathSeparator))
}

// liveMount reads the mount table and finds the line at the point.
func (s *Server) liveMount(point string) (library.Mount, bool, error) {
	path := s.MountInfo
	if path == "" {
		path = "/proc/self/mountinfo"
	}
	f, err := os.Open(path)
	if err != nil {
		return library.Mount{}, false, err
	}
	defer f.Close()
	mounts, err := library.ParseMountInfo(f)
	if err != nil {
		return library.Mount{}, false, err
	}
	for _, m := range mounts {
		if m.Point == point {
			return m, true, nil
		}
	}
	return library.Mount{}, false, nil
}

// cleanTestMount removes a test mount a crash left behind. It is best effort: a new test mount is
// about to be made at a fresh point either way.
func (s *Server) cleanTestMount() {
	rec, ok := s.findRecord(0)
	if !ok {
		return
	}
	if s.underRoot(rec.Point) && checkPoint(rec.Point) == nil {
		if line, live, err := s.liveMount(rec.Point); err == nil && live &&
			line.Source == rec.Source && line.FSType == rec.FSType {
			ctx, cancel := context.WithTimeout(context.Background(), unmountTimeout)
			_, _ = s.run(ctx, s.UmountBin, rec.Point)
			cancel()
		}
	}
	s.dropRecord(0)
}

// bestEffortUmount takes back a mount whose record could not be written, so no mount exists that
// this helper can never unmount.
func (s *Server) bestEffortUmount(point string) {
	ctx, cancel := context.WithTimeout(context.Background(), unmountTimeout)
	defer cancel()
	_, _ = s.run(ctx, s.UmountBin, point)
}

// run executes a binary through the server's Runner, or through exec with an empty environment.
func (s *Server) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if s.Run != nil {
		return s.Run(ctx, name, args...)
	}
	return execRunner(ctx, name, args...)
}

// fstypeFor is the mount command's -t for a share's type.
func fstypeFor(t share.Type) string {
	if t == share.SMB {
		return "cifs"
	}
	return "nfs"
}

// fstypeAllowed is what the mount table may say after a successful mount of that type.
func fstypeAllowed(t share.Type, fstype string) bool {
	if t == share.SMB {
		return fstype == "cifs" || fstype == "smb3"
	}
	return fstype == "nfs" || fstype == "nfs4"
}

// mountOptions rebuilds the option list: the request's checked options, then the floor the helper
// always adds. A name the request cannot say (credentials, file_mode, uid, gid) only comes from here.
func mountOptions(t share.Type, req Request, credPath string, uid, gid int) []string {
	opts := append([]string{}, req.Options...)
	opts = append(opts, share.SafeOptions(t)...)
	if t == share.SMB {
		if req.Guest {
			opts = append(opts, "guest")
		}
		if credPath != "" {
			opts = append(opts, "credentials="+credPath)
		}
		opts = append(opts, "file_mode=0660", "dir_mode=0770",
			"uid="+strconv.Itoa(uid), "gid="+strconv.Itoa(gid))
	}
	return opts
}

// writeCredentials puts the sign-in in a 0600 file for mount.cifs. The password is never in argv:
// the mount command only sees the file's name. cleanup removes the file when the mount returns.
func (s *Server) writeCredentials(t share.Type, req Request) (path string, cleanup func(), err error) {
	cleanup = func() {}
	if t != share.SMB || req.Password == "" {
		return "", cleanup, nil
	}
	dir := s.RunDir
	if dir == "" {
		dir = "/run/syp"
	}
	f, err := os.CreateTemp(dir, ".cred-")
	if err != nil {
		return "", cleanup, err
	}
	remove := func() { _ = os.Remove(f.Name()) }
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		remove()
		return "", cleanup, err
	}
	var b strings.Builder
	b.WriteString("username=" + req.Username + "\n")
	b.WriteString("password=" + req.Password + "\n")
	if req.Domain != "" {
		b.WriteString("domain=" + req.Domain + "\n")
	}
	if _, err := io.WriteString(f, b.String()); err != nil {
		_ = f.Close()
		remove()
		return "", cleanup, err
	}
	if err := f.Close(); err != nil {
		remove()
		return "", cleanup, err
	}
	return f.Name(), remove, nil
}

// mountFailure turns mount's own words into the page's reason. The message stays generic: the
// command's output may name paths a person does not need to read, and the reason is the contract.
func mountFailure(_ share.Type, out []byte) Response {
	text := strings.ToLower(string(out))
	has := func(parts ...string) bool {
		for _, p := range parts {
			if strings.Contains(text, p) {
				return true
			}
		}
		return false
	}
	switch {
	case has("permission denied", "access denied", "mount error(13)", "logon failure",
		"status_logon_failure", "authentication"):
		return Response{Reason: share.ReasonBadCredentials,
			Message: "The server did not accept that username and password."}
	case has("mount error(2)", "no such file", "no such share", "not found"):
		return Response{Reason: share.ReasonNoSuchShare, Message: "The server has no share at that address."}
	case has("no route to host", "host is down", "connection refused", "timed out", "unreachable",
		"could not resolve", "name or service not known", "no such host"):
		return Response{Reason: share.ReasonUnreachable, Message: "The share did not answer."}
	}
	return Response{Reason: share.ReasonMountFailed, Message: "The share could not be mounted."}
}

// statBounded is os.Stat with a deadline. A dead network mount can leave a stat blocked in the
// kernel; the helper answers the deadline and leaves the goroutine to the kernel.
var errStatTimeout = errors.New("the stat timed out")

func statBounded(point string, d time.Duration) error {
	ch := make(chan error, 1)
	go func() {
		_, err := os.Stat(point)
		ch <- err
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(d):
		return errStatTimeout
	}
}

func writeResponse(w io.Writer, resp Response) error {
	return json.NewEncoder(w).Encode(resp)
}

func badRequest(message string) Response {
	return Response{Reason: ReasonInvalidRequest, Message: message}
}

func refused(message string) Response {
	return Response{Reason: ReasonRefused, Message: message}
}

func notMounted(message string) Response {
	return Response{Reason: ReasonNotMounted, Message: message}
}

func mountFailed(message string) Response {
	return Response{Reason: share.ReasonMountFailed, Message: message}
}
