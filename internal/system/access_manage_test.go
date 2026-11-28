// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package system

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// A password is 8 characters to 72 bytes; a multi-byte character is three bytes, so 24 of them is exactly 72.
func TestChangePasswordLimits(t *testing.T) {
	a, _, _, _ := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	keep, _ := a.CreateSession(ctx, "ua", "127.0.0.1")
	cases := []struct {
		name     string
		password string
		want     error
	}{
		{"seven characters", strings.Repeat("a", 7), ErrPasswordTooShort},
		{"eight characters", strings.Repeat("a", 8), nil},
		{"seventy-two bytes", strings.Repeat("字", 24), nil},
		{"seventy-three bytes", strings.Repeat("字", 24) + "a", ErrPasswordTooLong},
		{"eight multi-byte characters", strings.Repeat("字", 8), nil},
	}
	for _, c := range cases {
		err := a.ChangePassword(ctx, keep, c.password)
		if c.want == nil && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if c.want != nil && !errors.Is(err, ErrInvalidPassword) {
			t.Errorf("%s: the refusal must also match ErrInvalidPassword for setup's callers", c.name)
		}
	}
}

// The old password is refused and the new one accepted, as sign-in checks them.
func TestChangePasswordReplacesTheOldOne(t *testing.T) {
	a, _, _, _ := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	if err := a.ChangePassword(ctx, "", "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if err := a.CheckPassword(ctx, testPassword); !errors.Is(err, ErrBadPassword) {
		t.Errorf("the old password: err = %v, want ErrBadPassword", err)
	}
	if err := a.CheckPassword(ctx, "a brand new password"); err != nil {
		t.Errorf("the new password: %v", err)
	}
}

// A password change ends every session except the one that made it (L-ACC-1). The access token is not changed.
func TestChangePasswordEndsOtherSessions(t *testing.T) {
	a, _, _, _ := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	tokenBefore, err := a.Token(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keep, _ := a.CreateSession(ctx, "ua", "127.0.0.1")
	other, _ := a.CreateSession(ctx, "ua", "127.0.0.1")
	if err := a.ChangePassword(ctx, keep, "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if live, _ := a.LookupSession(ctx, other); live {
		t.Error("the other session is still live after a password change")
	}
	if live, _ := a.LookupSession(ctx, keep); !live {
		t.Error("the session that made the change was ended")
	}
	if tokenAfter, _ := a.Token(ctx); tokenAfter != tokenBefore {
		t.Error("a password change changed the access token")
	}
}

// The token is read back from its sealed copy, and it is the one the bearer check accepts.
func TestTokenReadsTheStoredOne(t *testing.T) {
	a, _, _, _ := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	token, err := a.Token(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CheckToken(ctx, token); err != nil {
		t.Errorf("the read token is not the stored one: %v", err)
	}
}

// Before the token row exists the answer is ErrNoToken (page 11's no_token), not an error to show.
func TestTokenMissingIsNoToken(t *testing.T) {
	a, db, _, _ := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM tokens`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Token(ctx); !errors.Is(err, ErrNoToken) {
		t.Errorf("err = %v, want ErrNoToken", err)
	}
}

// Rotation: the old token stops working at once, and the new one is what Token reads back.
func TestRotateTokenInvalidatesTheOldOne(t *testing.T) {
	a, _, _, _ := newAccess(t)
	ctx := context.Background()
	if err := a.SetPassword(ctx, testPassword); err != nil {
		t.Fatal(err)
	}
	old, _ := a.Token(ctx)
	fresh, err := a.RotateToken(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CheckToken(ctx, old); !errors.Is(err, ErrBadToken) {
		t.Errorf("the old token: err = %v, want ErrBadToken", err)
	}
	if got, _ := a.Token(ctx); got != fresh {
		t.Errorf("Token() = %q after rotation, want the new token", got)
	}
}
