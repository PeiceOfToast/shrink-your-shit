// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package system

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Access is the one password, the sessions it signs in, and the access token (backend-plan §12).
// It holds no HTTP: the api package decodes requests and calls these.
type Access struct {
	db    *store.DB
	dir   string
	now   func() time.Time
	sleep func(time.Duration)

	mu          sync.Mutex // guards key and the limiter
	key         []byte     // the AES key from secret.key; nil until the file exists
	failures    map[string][]time.Time
	lockedUntil map[string]time.Time
	inFlight    map[string]int // sign-in tries from each address still being checked
}

// Options configure Access. Zero values take the defaults: the real clock and time.Sleep.
type Options struct {
	Now   func() time.Time
	Sleep func(time.Duration)
}

const (
	sessionTTL       = 30 * 24 * time.Hour // renewed on every use
	minPasswordChars = 8
	maxPasswordBytes = 72
	pbkdfIterations  = 600_000 // OWASP's PBKDF2-HMAC-SHA256 guidance (2023)
	failDelay        = time.Second
	bearerDelay      = 500 * time.Millisecond
	lockWindow       = time.Minute
	lockFailures     = 5
	lockFor          = time.Minute
	keyFileName      = "secret.key"
	accessTokenID    = "access"
)

var (
	ErrSetupDone       = errors.New("setup is already done")
	ErrNoAccount       = errors.New("setup has not been done")
	ErrInvalidPassword = errors.New("the password must be 8 to 72 bytes")
	ErrBadPassword     = errors.New("that password is not right")
	ErrBadToken        = errors.New("that token is not right")
)

// LockedError means sign-in is refused until the lock lifts. RetryAfter is the wait in seconds.
type LockedError struct{ RetryAfter int }

func (e *LockedError) Error() string { return "too many tries" }

// migration 3: the account, its sessions and the access token (events took 2; library will take 4).
func init() {
	store.Register(store.Migration{Version: 3, Name: "access", Up: createAccess})
}

func createAccess(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE auth(
			id            INTEGER PRIMARY KEY CHECK(id = 1),
			password_hash TEXT NOT NULL,
			set_at        INTEGER NOT NULL)`,
		`CREATE TABLE sessions(
			id_hash    TEXT PRIMARY KEY,
			created    INTEGER NOT NULL,
			last_seen  INTEGER NOT NULL,
			expires    INTEGER NOT NULL,
			user_agent TEXT NOT NULL,
			ip         TEXT NOT NULL)`,
		`CREATE INDEX sessions_expires ON sessions(expires)`,
		`CREATE TABLE tokens(
			id        TEXT PRIMARY KEY,
			name      TEXT NOT NULL,
			hash      TEXT NOT NULL,
			sealed    BLOB NOT NULL,
			created   INTEGER NOT NULL,
			last_used INTEGER)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// Open reads the account's key file if it exists. A missing key file is fine: setup makes it.
func Open(db *store.DB, dataDir string, o Options) (*Access, error) {
	a := &Access{
		db:          db,
		dir:         dataDir,
		now:         o.Now,
		sleep:       o.Sleep,
		failures:    map[string][]time.Time{},
		lockedUntil: map[string]time.Time{},
		inFlight:    map[string]int{},
	}
	if a.now == nil {
		a.now = time.Now
	}
	if a.sleep == nil {
		a.sleep = time.Sleep
	}
	b, err := os.ReadFile(filepath.Join(dataDir, keyFileName))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read the key file %s: %w", keyFileName, err)
	case len(b) != 32:
		return nil, fmt.Errorf("the key file %s is not a 32-byte key; it was not changed", keyFileName)
	default:
		a.key = b
	}
	return a, nil
}

// NeedsAccount is true until setup has set a password.
func (a *Access) NeedsAccount(ctx context.Context) (bool, error) {
	var n int
	if err := a.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM auth`).Scan(&n); err != nil {
		return false, fmt.Errorf("read the account: %w", err)
	}
	return n == 0, nil
}

// SetPassword is setup's only write: it sets the password once and creates the access token, stored
// encrypted under the key file. It fails with ErrSetupDone if a password already exists.
func (a *Access) SetPassword(ctx context.Context, password string) error {
	if err := checkPasswordRules(password); err != nil {
		return err
	}
	needs, err := a.NeedsAccount(ctx)
	if err != nil {
		return err
	}
	if !needs {
		return ErrSetupDone
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	token, err := randomSecret()
	if err != nil {
		return err
	}
	a.mu.Lock()
	err = a.ensureKeyLocked()
	key := a.key
	a.mu.Unlock()
	if err != nil {
		return err
	}
	sealed, err := seal(key, []byte(token))
	if err != nil {
		return err
	}
	now := a.now().Unix()
	return a.db.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM auth`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrSetupDone
		}
		if _, err := tx.Exec(`INSERT INTO auth(id, password_hash, set_at) VALUES(1, ?, ?)`, hash, now); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO tokens(id, name, hash, sealed, created, last_used) VALUES(?, ?, ?, ?, ?, NULL)`,
			accessTokenID, "access", digest(token), sealed, now)
		return err
	})
}

// CheckPassword is nil when password is the account's password.
func (a *Access) CheckPassword(ctx context.Context, password string) error {
	var stored string
	err := a.db.Read().QueryRowContext(ctx, `SELECT password_hash FROM auth WHERE id = 1`).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoAccount
	}
	if err != nil {
		return fmt.Errorf("read the account: %w", err)
	}
	if !verifyPassword(stored, password) {
		return ErrBadPassword
	}
	return nil
}

// CheckToken is nil when token is the access token. It does not delay; CheckBearer does.
func (a *Access) CheckToken(ctx context.Context, token string) error {
	var stored string
	err := a.db.Read().QueryRowContext(ctx, `SELECT hash FROM tokens WHERE id = ?`, accessTokenID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoAccount
	}
	if err != nil {
		return fmt.Errorf("read the token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(digest(token))) != 1 {
		return ErrBadToken
	}
	return nil
}

// CheckBearer is CheckToken for a bearer header: a failed token is slowed by 500 ms (L-ACC-2).
func (a *Access) CheckBearer(ctx context.Context, token string) error {
	err := a.CheckToken(ctx, token)
	if errors.Is(err, ErrBadToken) {
		a.sleep(bearerDelay)
	}
	return err
}

// RotateToken replaces the access token and ends every session except keepSession (a cookie value,
// or "" to end them all). The store is one writer, so two rotations at once leave one token that
// matches the store: the last to commit.
func (a *Access) RotateToken(ctx context.Context, keepSession string) (string, error) {
	token, err := randomSecret()
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	key := a.key
	a.mu.Unlock()
	if key == nil {
		return "", ErrNoAccount
	}
	sealed, err := seal(key, []byte(token))
	if err != nil {
		return "", err
	}
	keep := digest(keepSession)
	if keepSession == "" {
		keep = ""
	}
	err = a.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE tokens SET hash = ?, sealed = ?, last_used = NULL WHERE id = ?`,
			digest(token), sealed, accessTokenID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNoAccount
		}
		_, err = tx.Exec(`DELETE FROM sessions WHERE id_hash <> ?`, keep)
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// CreateSession makes a session for a browser that has just signed in and returns its cookie value.
func (a *Access) CreateSession(ctx context.Context, userAgent, ip string) (string, error) {
	cookie, err := randomSecret()
	if err != nil {
		return "", err
	}
	now := a.now()
	err = a.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO sessions(id_hash, created, last_seen, expires, user_agent, ip) VALUES(?, ?, ?, ?, ?, ?)`,
			digest(cookie), now.Unix(), now.Unix(), now.Add(sessionTTL).Unix(), userAgent, ip)
		return err
	})
	if err != nil {
		return "", err
	}
	return cookie, nil
}

// LookupSession reports whether cookie names a live session, and renews it when it does.
func (a *Access) LookupSession(ctx context.Context, cookie string) (bool, error) {
	if cookie == "" {
		return false, nil
	}
	now := a.now()
	live := false
	err := a.db.Tx(ctx, func(tx *sql.Tx) error {
		var expires int64
		err := tx.QueryRow(`SELECT expires FROM sessions WHERE id_hash = ?`, digest(cookie)).Scan(&expires)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if now.Unix() >= expires {
			return nil
		}
		live = true
		_, err = tx.Exec(`UPDATE sessions SET last_seen = ?, expires = ? WHERE id_hash = ?`,
			now.Unix(), now.Add(sessionTTL).Unix(), digest(cookie))
		return err
	})
	return live, err
}

// EndSession ends one session. Ending a session that is already gone is not an error.
func (a *Access) EndSession(ctx context.Context, cookie string) error {
	if cookie == "" {
		return nil
	}
	return a.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM sessions WHERE id_hash = ?`, digest(cookie))
		return err
	})
}

// SignInPassword checks the password from addr. A failure waits about a second; five in a minute from
// one address lock sign-in for a minute (page 13). It returns the new session's cookie value.
func (a *Access) SignInPassword(ctx context.Context, addr, password, userAgent string) (string, error) {
	if err := a.enter(addr); err != nil {
		return "", err
	}
	defer a.leave(addr)
	if err := a.CheckPassword(ctx, password); err != nil {
		if errors.Is(err, ErrBadPassword) {
			a.sleep(failDelay)
			a.fail(addr)
		}
		return "", err
	}
	a.clear(addr)
	return a.CreateSession(ctx, userAgent, addr)
}

// SignInToken is SignInPassword for the access token (the ?token= link and the token sign-in).
func (a *Access) SignInToken(ctx context.Context, addr, token, userAgent string) (string, error) {
	if err := a.enter(addr); err != nil {
		return "", err
	}
	defer a.leave(addr)
	if err := a.CheckToken(ctx, token); err != nil {
		if errors.Is(err, ErrBadToken) {
			a.sleep(failDelay)
			a.fail(addr)
		}
		return "", err
	}
	a.clear(addr)
	return a.CreateSession(ctx, userAgent, addr)
}

// enter starts one sign-in try from addr, or returns a LockedError. Tries still being checked count
// toward the limit with the failures in the window, so parallel guesses cannot all get past the check
// while each waits out its delay. A caller that gets nil must call leave.
func (a *Access) enter(addr string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if until, ok := a.lockedUntil[addr]; ok {
		if now.Before(until) {
			return &LockedError{RetryAfter: int(lockFor / time.Second)}
		}
		delete(a.lockedUntil, addr)
	}
	recent := 0
	for _, t := range a.failures[addr] {
		if now.Sub(t) < lockWindow {
			recent++
		}
	}
	if recent+a.inFlight[addr] >= lockFailures {
		// The tries in flight may yet lock the address; ask the client to come back shortly.
		return &LockedError{RetryAfter: int(failDelay/time.Second) + 1}
	}
	a.inFlight[addr]++
	return nil
}

// leave ends a try that enter started.
func (a *Access) leave(addr string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inFlight[addr]--; a.inFlight[addr] <= 0 {
		delete(a.inFlight, addr)
	}
}

// fail records one failed sign-in from addr. The fifth in a window starts the lock.
func (a *Access) fail(addr string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	kept := a.failures[addr][:0]
	for _, t := range a.failures[addr] {
		if now.Sub(t) < lockWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	if len(kept) >= lockFailures {
		a.lockedUntil[addr] = now.Add(lockFor)
		kept = nil
	}
	a.failures[addr] = kept
}

// clear forgets addr's failures after a good sign-in.
func (a *Access) clear(addr string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, addr)
}

// ensureKeyLocked makes the key file on first use, mode 0600, and loads it if it exists.
// Callers hold a.mu.
func (a *Access) ensureKeyLocked() error {
	if a.key != nil {
		return nil
	}
	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return fmt.Errorf("make the data folder: %w", err)
	}
	path := filepath.Join(a.dir, keyFileName)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		b, rerr := os.ReadFile(path)
		if rerr != nil || len(b) != 32 {
			return fmt.Errorf("the key file %s is unreadable or not a 32-byte key; it was not changed", keyFileName)
		}
		a.key = b
		return nil
	}
	if err != nil {
		return fmt.Errorf("create the key file %s: %w", keyFileName, err)
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("write the key file %s: %w", keyFileName, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("save the key file %s: %w", keyFileName, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return fmt.Errorf("save the key file %s: %w", keyFileName, err)
	}
	a.key = key
	return nil
}

// checkPasswordRules applies page 13's limits: at least 8 characters, at most 72 bytes.
func checkPasswordRules(password string) error {
	if utf8.RuneCountInString(password) < minPasswordChars || len(password) > maxPasswordBytes {
		return ErrInvalidPassword
	}
	return nil
}

// hashPassword is PBKDF2-HMAC-SHA256 with a random salt, in the standard library. The string keeps its
// algorithm and cost, so the cost can rise later without a schema change.
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdfIterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdfIterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// randomSecret is 32 random bytes as URL-safe text: a token, a session cookie value.
func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// digest is what the database keeps of a secret, so a copy of the database holds no usable cookie or token.
func digest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// seal encrypts with AES-256-GCM: a random nonce, then the ciphertext.
func seal(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}
