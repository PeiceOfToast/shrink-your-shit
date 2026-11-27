// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package system

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// fakeClock is a clock the test moves by hand. Sleeps are recorded, never waited for.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *fakeClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
}

func (c *fakeClock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

const testPassword = "correct horse battery"

// newAccess opens a migrated store in a fresh data folder with the fake clock.
func newAccess(t *testing.T) (*Access, *store.DB, string, *fakeClock) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{now: time.Date(2026, 11, 27, 4, 0, 0, 0, time.UTC)}
	a, err := Open(db, dir, Options{Now: clock.Now, Sleep: clock.Sleep})
	if err != nil {
		t.Fatal(err)
	}
	return a, db, dir, clock
}

func TestPasswordLimits(t *testing.T) {
	cases := []struct {
		name     string
		password string
		ok       bool
	}{
		{"seven characters", "abcdefg", false},
		{"eight characters", "abcdefgh", true},
		{"72 bytes", strings.Repeat("a", 72), true},
		{"73 bytes", strings.Repeat("a", 73), false},
		{"eight characters of three bytes each", strings.Repeat("é", 8), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkPasswordRules(c.password)
			if c.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !c.ok && !errors.Is(err, ErrInvalidPassword) {
				t.Fatalf("want ErrInvalidPassword, got %v", err)
			}
		})
	}
}

func TestSetPasswordOnce(t *testing.T) {
	a, _, _, _ := newAccess(t)
	ctx := context.Background()
	if need, _ := a.NeedsAccount(ctx); !need {
		t.Fatal("a new install needs an account")
	}
	if err := a.SetPassword(ctx, "short"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("short password: want ErrInvalidPassword, got %v", err)
	}
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	if need, _ := a.NeedsAccount(ctx); need {
		t.Fatal("after setup no account is needed")
	}
	if err := a.SetPassword(ctx, "another password"); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("second setup: want ErrSetupDone, got %v", err)
	}
}

func TestCheckPassword(t *testing.T) {
	a, _, _, _ := newAccess(t)
	ctx := context.Background()
	if err := a.CheckPassword(ctx, testPassword); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("before setup: want ErrNoAccount, got %v", err)
	}
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	if err := a.CheckPassword(ctx, testPassword); err != nil {
		t.Fatalf("right password: %v", err)
	}
	if err := a.CheckPassword(ctx, "wrong password!"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("wrong password: want ErrBadPassword, got %v", err)
	}
}

// The key file is made once, 32 bytes, mode 0600 on Unix (L-ACC-1, risks K5).
func TestKeyFileIsPrivate(t *testing.T) {
	a, _, dir, _ := newAccess(t)
	if err := a.SetPassword(context.Background(), testPassword); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, keyFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 32 {
		t.Fatalf("key file is %d bytes, want 32", len(b))
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := fi.Mode().Perm(); mode != 0o600 {
			t.Fatalf("key file mode %o, want 600", mode)
		}
	}
}

// The token is stored sealed, not in clear, and opens with the key file to the token that checks.
func TestTokenStoredEncrypted(t *testing.T) {
	a, db, dir, _ := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	token, err := a.RotateToken(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := db.Read().QueryRow(`SELECT sealed FROM tokens WHERE id = ?`, accessTokenID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), token) {
		t.Fatal("the stored token is readable in clear")
	}
	key, err := os.ReadFile(filepath.Join(dir, keyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := unseal(t, key, sealed); got != token {
		t.Fatal("the sealed token does not open to the token that was returned")
	}
	if err := a.CheckToken(ctx, token); err != nil {
		t.Fatalf("the returned token should check: %v", err)
	}
}

func unseal(t *testing.T, key, sealed []byte) string {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	ns := gcm.NonceSize()
	plain, err := gcm.Open(nil, sealed[:ns], sealed[ns:], nil)
	if err != nil {
		t.Fatal(err)
	}
	return string(plain)
}

func TestRotateEndsOtherSessions(t *testing.T) {
	a, _, _, _ := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	keep, _ := a.CreateSession(ctx, "ua", "127.0.0.1")
	oldToken, err := a.RotateToken(ctx, keep) // keeps keep; no other sessions exist yet
	if err != nil {
		t.Fatal(err)
	}
	other, _ := a.CreateSession(ctx, "ua", "127.0.0.1")
	newToken, err := a.RotateToken(ctx, keep)
	if err != nil {
		t.Fatal(err)
	}
	if live, _ := a.LookupSession(ctx, other); live {
		t.Fatal("rotation should end the other session")
	}
	if live, _ := a.LookupSession(ctx, keep); !live {
		t.Fatal("rotation should keep the session it was asked to keep")
	}
	if err := a.CheckToken(ctx, oldToken); !errors.Is(err, ErrBadToken) {
		t.Fatalf("old token: want ErrBadToken, got %v", err)
	}
	if err := a.CheckToken(ctx, newToken); err != nil {
		t.Fatalf("new token: %v", err)
	}
}

// Two rotations at once: whichever token the store ends with is the one that checks; the other is refused.
func TestConcurrentRotationsAgreeWithTheStoredToken(t *testing.T) {
	a, _, _, _ := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	tokens := make([]string, 2)
	errs := make([]error, 2)
	for i := range tokens {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = a.RotateToken(ctx, "")
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	valid := 0
	for _, tok := range tokens {
		if a.CheckToken(ctx, tok) == nil {
			valid++
		}
	}
	if valid != 1 {
		t.Fatalf("%d of the two rotated tokens check; want exactly 1 (the one the store ends with)", valid)
	}
}

// A session lasts 30 days from its last use: use renews it.
func TestSessionRenewedOnUse(t *testing.T) {
	a, _, _, clock := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	cookie, _ := a.CreateSession(ctx, "ua", "127.0.0.1")
	clock.Advance(29 * 24 * time.Hour)
	if live, _ := a.LookupSession(ctx, cookie); !live {
		t.Fatal("session should live 29 days")
	}
	clock.Advance(29 * 24 * time.Hour) // 58 days since creation, 29 since its last use
	if live, _ := a.LookupSession(ctx, cookie); !live {
		t.Fatal("use should have renewed the session")
	}
	clock.Advance(31 * 24 * time.Hour)
	if live, _ := a.LookupSession(ctx, cookie); live {
		t.Fatal("session should end after 30 days without use")
	}
}

// Five wrong passwords in a minute lock sign-in from that address for 60 s, with about a second per failure.
func TestSignInLocksAfterFiveFailures(t *testing.T) {
	a, _, _, clock := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		_, err := a.SignInPassword(ctx, "10.0.0.1", "wrong password!", "ua")
		if !errors.Is(err, ErrBadPassword) {
			t.Fatalf("try %d: want ErrBadPassword, got %v", i, err)
		}
	}
	_, err := a.SignInPassword(ctx, "10.0.0.1", testPassword, "ua")
	var locked *LockedError
	if !errors.As(err, &locked) || locked.RetryAfter != 60 {
		t.Fatalf("6th try: want LockedError with RetryAfter 60, got %v", err)
	}
	for _, d := range clock.Sleeps() {
		if d != time.Second {
			t.Fatalf("failure delay %v, want 1s", d)
		}
	}
	clock.Advance(59 * time.Second)
	if _, err := a.SignInPassword(ctx, "10.0.0.1", testPassword, "ua"); !errors.As(err, &locked) {
		t.Fatalf("at 59 s: want still locked, got %v", err)
	}
	clock.Advance(time.Second)
	if _, err := a.SignInPassword(ctx, "10.0.0.1", testPassword, "ua"); err != nil {
		t.Fatalf("after the minute the right password should sign in: %v", err)
	}
}

// Another address is not locked by this one's failures.
func TestLockIsPerAddress(t *testing.T) {
	a, _, _, _ := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		a.SignInPassword(ctx, "10.0.0.1", "wrong password!", "ua")
	}
	if _, err := a.SignInPassword(ctx, "10.0.0.2", testPassword, "ua"); err != nil {
		t.Fatalf("another address: %v", err)
	}
}

// A failed bearer token is slowed by 500 ms; a good one is not (L-ACC-2).
func TestWrongBearerIsDelayed(t *testing.T) {
	a, _, _, clock := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	token, _ := a.RotateToken(ctx, "")
	if err := a.CheckBearer(ctx, token); err != nil {
		t.Fatal(err)
	}
	if n := len(clock.Sleeps()); n != 0 {
		t.Fatalf("a good token slept %d times", n)
	}
	if err := a.CheckBearer(ctx, "not the token"); !errors.Is(err, ErrBadToken) {
		t.Fatalf("want ErrBadToken, got %v", err)
	}
	if got := clock.Sleeps(); len(got) != 1 || got[0] != 500*time.Millisecond {
		t.Fatalf("wrong bearer delays %v, want [500ms]", got)
	}
}
