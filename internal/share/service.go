// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package share

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// Mode says how this computer reaches a share. Helper is the app's own root helper mounting it;
// host is a share the person mounted themselves, which the app only checks (slice 4).
const (
	ModeHelper = "helper"
	ModeHost   = "host"
)

// Test result reasons. The page branches on the code only enough to stay out of the way; the
// message is what the person reads.
const (
	ReasonUnreachable    = "unreachable"
	ReasonBadCredentials = "bad_credentials"
	ReasonNoSuchShare    = "no_such_share"
	ReasonMountFailed    = "mount_failed"
	ReasonTimeout        = "timeout"
	testMountTimeout     = 10 * time.Second
	testUnmountTimeout   = 5 * time.Second
)

// MountError is a failed mount or unmount with a code and the sentence the page shows.
type MountError struct {
	Reason  string
	Message string
}

func (e *MountError) Error() string { return e.Message }

// Spec is one mount request for the Mounter. ID is the share's row (0 for a test mount, which the
// helper names itself); Password is plaintext on the way to the helper only and is never logged.
type Spec struct {
	ID       int64
	Address  Address
	Username string
	Domain   string
	Guest    bool
	Password []byte
}

// Mounter mounts and unmounts a share. The root helper's client implements it (slice 2); a test
// uses a fake, and the app before the client lands uses Unavailable.
type Mounter interface {
	// Mount makes the share's mount point and mounts it. The point is the helper's choice, never
	// the app's.
	Mount(ctx context.Context, spec Spec) (point string, err error)
	// Unmount removes the mount. It answers ErrNotMounted when nothing is mounted.
	Unmount(ctx context.Context, spec Spec) error
}

// Unavailable is the mounter this build runs with until the helper client lands: a share can be
// recorded, and it honestly says it is not connected.
type Unavailable struct{}

func (Unavailable) Mount(context.Context, Spec) (string, error) {
	return "", &MountError{Reason: ReasonUnreachable,
		Message: "This build can't mount shares yet. Add the folder on this computer instead."}
}

func (Unavailable) Unmount(context.Context, Spec) error { return nil }

// Input is Add's or Test's form: the address, the sign-in and an optional name. Type is optional;
// the address decides it.
type Input struct {
	Name     string
	Type     string
	Address  string
	Username string
	Domain   string
	Password string
	Guest    bool
}

// TestResult is Add's "Test the connection": the one shape the page shows either way.
type TestResult struct {
	OK      bool
	Reason  string
	Message string
}

// Service is the share API's work: validate, save, mount, and record what happened.
type Service struct {
	store   *Store
	mounter Mounter
	mode    string
	modeWhy string
}

func NewService(store *Store, mounter Mounter) *Service {
	return &Service{store: store, mounter: mounter, mode: ModeHelper}
}

// HostMode marks a computer with no helper: shares are mounted by the person, and the app checks
// them instead (slice 4). reason explains it on the page.
func (s *Service) HostMode(reason string) {
	s.mode, s.modeWhy = ModeHost, reason
}

func (s *Service) Mode() string       { return s.mode }
func (s *Service) ModeReason() string { return s.modeWhy }

func (s *Service) List(ctx context.Context) ([]Share, error) {
	return s.store.List(ctx)
}

func (s *Service) Get(ctx context.Context, id int64) (Share, error) {
	return s.store.Get(ctx, id)
}

// Create validates the form, saves the share with its password sealed, then tries to mount it. A
// failed first mount is not a refusal (question 1): the share is kept, not connected, with the
// sentence that says why, and Reconnect tries again.
func (s *Service) Create(ctx context.Context, in Input) (Share, error) {
	p, err := prepare(in)
	if err != nil {
		return Share{}, err
	}
	name, err := shareName(in.Name, p.address)
	if err != nil {
		return Share{}, err
	}
	sh, err := s.store.Insert(ctx, NewShare{
		Name: name, Address: p.address, Username: p.username, Domain: p.domain,
		Guest: p.guest, Password: p.password,
	})
	if err != nil {
		return Share{}, err
	}
	if err := s.attempt(ctx, &sh); err != nil {
		return Share{}, err
	}
	return sh, nil
}

// Test validates the form and tries a real mount at the helper's own test point, then unmounts it.
// A form error is an InvalidError (the page points at the field); a mount failure is a TestResult
// with a reason code and a sentence.
func (s *Service) Test(ctx context.Context, in Input) (TestResult, error) {
	p, err := prepare(in)
	if err != nil {
		return TestResult{}, err
	}
	user, domain := splitUser(p.username, p.domain)
	spec := Spec{Address: p.address, Username: user, Domain: domain, Guest: p.guest, Password: p.password}
	mountCtx, cancel := context.WithTimeout(ctx, testMountTimeout)
	defer cancel()
	point, err := s.mounter.Mount(mountCtx, spec)
	if err != nil {
		return failure(err), nil
	}
	if _, err := os.Stat(point); err != nil {
		s.unmountTest(spec)
		return TestResult{OK: false, Reason: ReasonUnreachable,
			Message: "The share mounted, but its folder did not answer."}, nil
	}
	if err := s.mounter.Unmount(mountCtx, spec); err != nil {
		return TestResult{OK: false, Reason: ReasonMountFailed,
			Message: "The share worked, but it could not be unmounted. Try again."}, nil
	}
	return TestResult{OK: true}, nil
}

// unmountTest is the best-effort cleanup of a test mount that failed after mounting.
func (s *Service) unmountTest(spec Spec) {
	ctx, cancel := context.WithTimeout(context.Background(), testUnmountTimeout)
	defer cancel()
	_ = s.mounter.Unmount(ctx, spec)
}

// Reconnect mounts the saved share again. A failure is recorded on the share (not connected, with
// the sentence) and still answers the share, so the page can show why.
func (s *Service) Reconnect(ctx context.Context, id int64) (Share, error) {
	sh, err := s.store.Get(ctx, id)
	if err != nil {
		return Share{}, err
	}
	if err := s.attempt(ctx, &sh); err != nil {
		return Share{}, err
	}
	return sh, nil
}

// attempt mounts the saved share and records the result. A mount failure is the share's state,
// not the caller's error; a store failure is a real error.
func (s *Service) attempt(ctx context.Context, sh *Share) error {
	password, err := s.store.Password(ctx, sh.ID)
	if err != nil {
		return err
	}
	user, domain := splitUser(sh.Username, sh.Domain)
	spec := Spec{
		ID: sh.ID, Address: Address{Type: sh.Type, Source: sh.Address, Sub: sh.Sub},
		Username: user, Domain: domain, Guest: sh.Guest, Password: password,
	}
	point, mountErr := s.mounter.Mount(ctx, spec)
	if mountErr != nil {
		if err := s.store.SetConnection(ctx, sh.ID, false, "", messageOf(mountErr)); err != nil {
			return err
		}
	} else {
		if err := s.store.SetConnection(ctx, sh.ID, true, point, ""); err != nil {
			return err
		}
	}
	updated, err := s.store.Get(ctx, sh.ID)
	if err != nil {
		return err
	}
	*sh = updated
	return nil
}

// Delete refuses while a library uses the share; otherwise it unmounts and forgets the row.
func (s *Service) Delete(ctx context.Context, id int64) error {
	inUse, err := s.store.InUse(ctx, id)
	if err != nil {
		return err
	}
	if inUse {
		return ErrInUse
	}
	sh, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if sh.Point != "" {
		spec := Spec{ID: sh.ID, Address: Address{Type: sh.Type, Source: sh.Address, Sub: sh.Sub}}
		if err := s.mounter.Unmount(ctx, spec); err != nil && !errors.Is(err, ErrNotMounted) {
			return err
		}
	}
	return s.store.Delete(ctx, id)
}

// prepared is a validated form before it is stored.
type prepared struct {
	address  Address
	username string
	domain   string
	guest    bool
	password []byte
}

// prepare checks the address and sign-in. The rules are the helper's: SMB without Guest needs a
// password; Guest and NFS carry none; a username may include its domain as DOMAIN\user.
func prepare(in Input) (prepared, error) {
	addr, err := ParseAddress(in.Address, in.Type)
	if err != nil {
		return prepared{}, err
	}
	p := prepared{address: addr, guest: in.Guest}
	if addr.Type == NFS {
		if in.Password != "" || in.Username != "" || in.Domain != "" || in.Guest {
			return prepared{}, &InvalidError{Code: "invalid_request", Field: "password",
				Message: "An NFS share takes no username or password."}
		}
		return p, nil
	}
	// The username is kept as typed: a form with no domain field can still say DOMAIN\user, and
	// the page shows it back. The mount splits the two.
	p.username = strings.TrimSpace(in.Username)
	p.domain = strings.TrimSpace(in.Domain)
	if p.username != "" && !validUser(p.username) {
		return prepared{}, &InvalidError{Code: "invalid_request", Field: "username", Message: "That username can't be used."}
	}
	if i := strings.IndexByte(p.username, '\\'); i >= 0 {
		if !validDomain(p.username[:i]) || !validUser(p.username[i+1:]) {
			return prepared{}, &InvalidError{Code: "invalid_request", Field: "username", Message: "That username can't be used."}
		}
	}
	if p.domain != "" && !validDomain(p.domain) {
		return prepared{}, &InvalidError{Code: "invalid_request", Field: "domain", Message: "That domain can't be used."}
	}
	if in.Guest {
		if in.Password != "" {
			return prepared{}, &InvalidError{Code: "invalid_request", Field: "password", Message: "A guest share takes no password."}
		}
		if p.username != "" {
			return prepared{}, &InvalidError{Code: "invalid_request", Field: "username", Message: "A guest share takes no username."}
		}
		return p, nil
	}
	if in.Password == "" {
		return prepared{}, &InvalidError{Code: "missing_password", Field: "password",
			Message: "Type the share's password, or tick Guest."}
	}
	p.password = []byte(in.Password)
	return p, nil
}

// splitUser separates DOMAIN\user, which a form without a domain field may type, for the mount.
// A separate domain field wins over the prefix.
func splitUser(username, domain string) (user, dom string) {
	if i := strings.IndexByte(username, '\\'); i >= 0 && domain == "" {
		return username[i+1:], username[:i]
	}
	return username, domain
}

// validUser is what the helper can put in a credentials file: anything printable, so a Windows
// name with a space works, but no control character and no newline that could start a new line.
func validUser(user string) bool {
	if user == "" || len(user) > 256 {
		return false
	}
	for _, r := range user {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validDomain(domain string) bool {
	if domain == "" || len(domain) > 63 {
		return false
	}
	for _, r := range domain {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`/\ `, r) {
			return false
		}
	}
	return true
}

// shareName is the form's name, or the last folder the address names. It is the page's label and
// nothing else depends on it.
func shareName(raw string, addr Address) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		name = addr.DefaultName()
	}
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return "", &InvalidError{Code: "invalid_request", Field: "name", Message: "The share's name can be at most 64 characters."}
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", &InvalidError{Code: "invalid_request", Field: "name", Message: "The share's name has a character that can't be used."}
		}
	}
	return name, nil
}

// failure turns a mounter error into the page's reason and sentence. A helper error already chose
// its code; anything else is a mount failure with the error's own words.
func failure(err error) TestResult {
	var me *MountError
	if errors.As(err, &me) {
		return TestResult{OK: false, Reason: me.Reason, Message: me.Message}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return TestResult{OK: false, Reason: ReasonTimeout, Message: "The share did not answer in time."}
	}
	return TestResult{OK: false, Reason: ReasonMountFailed, Message: "The share could not be mounted: " + err.Error()}
}

// messageOf is the sentence to keep on a share whose mount failed.
func messageOf(err error) string {
	return failure(err).Message
}
