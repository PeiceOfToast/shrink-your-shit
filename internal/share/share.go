// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package share

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// InvalidError is a request the person can fix: Code is the API's short code, Field names the
// form field, and Message is the sentence the page shows.
type InvalidError struct {
	Code    string
	Field   string
	Message string
}

func (e *InvalidError) Error() string { return e.Message }

var (
	ErrNotFound   = errors.New("share: no such share")
	ErrInUse      = errors.New("share: a library uses it")
	ErrNotMounted = errors.New("share: not mounted")
	// ErrDown is a share that stayed down after a mount attempt: a library cannot be added on it.
	// The share it comes with carries the sentence why.
	ErrDown = errors.New("share: not connected")
)

// Share is one SMB or NFS share the app knows. HasPassword says a sealed password is kept; the
// password itself never leaves the store.
type Share struct {
	ID          int64
	Name        string
	Type        Type
	Address     string // the normalized mount source: //server/share or server:/export
	Sub         string // the folder inside the share, "" for its root
	Username    string
	Domain      string
	Guest       bool
	HasPassword bool
	Connected   bool
	Point       string // where it is (or was last) mounted by the helper
	Since       *time.Time
	Reason      string // why it is not connected, in the page's words
	Created     time.Time
}

// Sealer seals and opens a share's password under the data folder's key. *system.Access is one; a
// test uses a double. The interface is the same code the access token uses, not a second scheme.
type Sealer interface {
	Seal(plain []byte) ([]byte, error)
	Unseal(sealed []byte) ([]byte, error)
}

// Store reads and writes the shares table. It seals the password on the way in and opens it on the
// way out, so no caller handles a sealed blob.
type Store struct {
	db     *store.DB
	sealer Sealer
	// Now is the clock the rows' times come from; nil means time.Now.
	Now func() time.Time
}

func Open(db *store.DB, sealer Sealer) *Store {
	return &Store{db: db, sealer: sealer, Now: time.Now}
}

func (s *Store) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// NewShare is what an insert stores. Password is plaintext on the way in only; an empty password
// stores no sealed blob.
type NewShare struct {
	Name     string
	Address  Address
	Username string
	Domain   string
	Guest    bool
	Password []byte
}

const shareColumns = `id, name, type, address, sub, username, domain, guest,
	password_sealed IS NOT NULL, connected, point, since, reason, created`

func (s *Store) Insert(ctx context.Context, in NewShare) (Share, error) {
	var sealed []byte
	if len(in.Password) > 0 {
		var err error
		sealed, err = s.sealer.Seal(in.Password)
		if err != nil {
			return Share{}, fmt.Errorf("seal the share's password: %w", err)
		}
	}
	var id int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO shares
			(name, type, address, sub, username, domain, guest, password_sealed, created)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.Name, string(in.Address.Type), in.Address.Source, in.Address.Sub,
			in.Username, in.Domain, boolInt(in.Guest), sealed, s.clock().Unix())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return Share{}, fmt.Errorf("save the share: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *Store) Get(ctx context.Context, id int64) (Share, error) {
	sh, err := scanShare(s.db.Read().QueryRowContext(ctx,
		`SELECT `+shareColumns+` FROM shares WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Share{}, ErrNotFound
	}
	if err != nil {
		return Share{}, fmt.Errorf("read the share: %w", err)
	}
	return sh, nil
}

func (s *Store) List(ctx context.Context) ([]Share, error) {
	rows, err := s.db.Read().QueryContext(ctx, `SELECT `+shareColumns+` FROM shares ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, fmt.Errorf("read a share: %w", err)
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// scanShare reads one row, turning the since column into a time and the password flag into a bool.
func scanShare(row interface{ Scan(...any) error }) (Share, error) {
	var sh Share
	var typ string
	var guest, hasPassword, connected int
	var since sql.NullInt64
	var created int64
	if err := row.Scan(&sh.ID, &sh.Name, &typ, &sh.Address, &sh.Sub, &sh.Username, &sh.Domain,
		&guest, &hasPassword, &connected, &sh.Point, &since, &sh.Reason, &created); err != nil {
		return Share{}, err
	}
	sh.Type = Type(typ)
	sh.Guest = guest != 0
	sh.HasPassword = hasPassword != 0
	sh.Connected = connected != 0
	if since.Valid {
		at := time.Unix(since.Int64, 0).UTC()
		sh.Since = &at
	}
	sh.Created = time.Unix(created, 0).UTC()
	return sh, nil
}

// Password opens the stored password for a mount. A share with no password answers nil.
func (s *Store) Password(ctx context.Context, id int64) ([]byte, error) {
	var sealed []byte
	err := s.db.Read().QueryRowContext(ctx, `SELECT password_sealed FROM shares WHERE id = ?`, id).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read the share's password: %w", err)
	}
	if len(sealed) == 0 {
		return nil, nil
	}
	plain, err := s.sealer.Unseal(sealed)
	if err != nil {
		return nil, fmt.Errorf("open the share's password: %w", err)
	}
	return plain, nil
}

// SetConnection records a mount attempt's result. An empty point keeps the last one, so a share
// that dropped still says where it was mounted; reason is the sentence the page shows when it is
// not connected.
func (s *Store) SetConnection(ctx context.Context, id int64, connected bool, point, reason string) error {
	now := s.clock().Unix()
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if point != "" {
			_, err = tx.ExecContext(ctx, `UPDATE shares SET connected = ?, point = ?, since = ?, reason = ? WHERE id = ?`,
				boolInt(connected), point, now, reason, id)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE shares SET connected = ?, since = ?, reason = ? WHERE id = ?`,
				boolInt(connected), now, reason, id)
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("record the share's connection: %w", err)
	}
	return nil
}

// InUse reports whether a library uses the share.
func (s *Store) InUse(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE share_id = ?`, id).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("count the libraries that use the share: %w", err)
	}
	return n > 0, nil
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM shares WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete the share: %w", err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
