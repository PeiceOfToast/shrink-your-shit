// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

const newPassword = "a brand new password"

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func tokenOf(t *testing.T, e *env, cookie *http.Cookie) string {
	t.Helper()
	rec := e.do(http.MethodGet, "/api/v2/access/token", "", withSession(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET token = %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Token string `json:"token"`
	}
	decodeInto(t, rec, &body)
	return body.Token
}

func signInWith(e *env, password string) *http.Response {
	return e.do(http.MethodPost, "/api/v2/signin", `{"password":"`+password+`"}`, write(nil), false).Result()
}

// The access routes need the person's session. A bearer token alone is refused, and so is no sign-in at all.
func TestAccessRoutesNeedASession(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	token := tokenOf(t, e, cookie)
	for _, c := range []struct {
		method, path, body string
		headers            map[string]string
	}{
		{http.MethodPost, "/api/v2/access/password", `{"password":"` + newPassword + `"}`, write(bearer(token))},
		{http.MethodPost, "/api/v2/access/password", `{"password":"` + newPassword + `"}`, write(nil)},
		{http.MethodGet, "/api/v2/access/token", "", bearer(token)},
		{http.MethodGet, "/api/v2/access/token", "", nil},
		{http.MethodPost, "/api/v2/access/token/rotate", "", write(bearer(token))},
		{http.MethodPost, "/api/v2/access/token/rotate", "", write(nil)},
	} {
		rec := e.do(c.method, c.path, c.body, c.headers, false)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", c.method, c.path, rec.Code)
		}
	}
	// The bearer token still works everywhere else (it is the same token): the summary answers.
	if rec := e.do(http.MethodGet, "/api/v2/summary", "", bearer(token), false); rec.Code != http.StatusOK {
		t.Errorf("summary with the bearer token = %d, want 200", rec.Code)
	}
}

// The password limits answer their codes with the field, and a valid change answers 204 and sign-in follows it.
func TestAccessPasswordLimitsAndSignIn(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	post := func(password string) (int, string) {
		rec := e.do(http.MethodPost, "/api/v2/access/password", `{"password":"`+password+`"}`, write(withSession(cookie)), false)
		var body struct {
			Error struct {
				Code  string `json:"code"`
				Field string `json:"field"`
			} `json:"error"`
		}
		if rec.Code != http.StatusNoContent {
			decodeInto(t, rec, &body)
		}
		return rec.Code, body.Error.Code + "/" + body.Error.Field
	}
	if code, why := post("short"); code != http.StatusBadRequest || why != "password_too_short/password" {
		t.Errorf("short = %d %s, want 400 password_too_short/password", code, why)
	}
	if code, why := post(strings.Repeat("字", 24) + "a"); code != http.StatusBadRequest || why != "password_too_long/password" {
		t.Errorf("73 bytes = %d %s, want 400 password_too_long/password", code, why)
	}
	if code, _ := post(strings.Repeat("字", 24)); code != http.StatusNoContent {
		t.Errorf("72 bytes = %d, want 204", code)
	}
	if code, _ := post(newPassword); code != http.StatusNoContent {
		t.Fatalf("new password = %d, want 204", code)
	}
	if rec := signInWith(e, testPassword); rec.StatusCode == http.StatusNoContent {
		t.Error("the old password still signs in")
	}
	if rec := signInWith(e, newPassword); rec.StatusCode != http.StatusNoContent {
		t.Errorf("the new password signs in = %d, want 204", rec.StatusCode)
	}
}

// A password change ends the other sessions and keeps the one that made it. Rotating the token does the same.
func TestAccessChangesEndOtherSessions(t *testing.T) {
	e := newEnv(t, true)
	first := e.setup()
	second := cookieNamed(t, e.do(http.MethodPost, "/api/v2/signin", `{"password":"`+testPassword+`"}`, write(nil), false), sessionCookie)

	rec := e.do(http.MethodPost, "/api/v2/access/password", `{"password":"`+newPassword+`"}`, write(withSession(first)), false)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change = %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/api/v2/summary", "", withSession(second), false); rec.Code != http.StatusUnauthorized {
		t.Errorf("the other session after a password change = %d, want 401", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/api/v2/summary", "", withSession(first), false); rec.Code != http.StatusOK {
		t.Errorf("the session that changed it = %d, want 200", rec.Code)
	}

	third := cookieNamed(t, e.do(http.MethodPost, "/api/v2/signin", `{"password":"`+newPassword+`"}`, write(nil), false), sessionCookie)
	rec = e.do(http.MethodPost, "/api/v2/access/token/rotate", "", write(withSession(first)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d: %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodGet, "/api/v2/summary", "", withSession(third), false); rec.Code != http.StatusUnauthorized {
		t.Errorf("the other session after a rotation = %d, want 401", rec.Code)
	}
}

// Rotation hands back a new token; the old bearer is refused as bad_token and the new one works.
func TestAccessRotateTheToken(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	old := tokenOf(t, e, cookie)
	rec := e.do(http.MethodPost, "/api/v2/access/token/rotate", "", write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d", rec.Code)
	}
	var body struct {
		Token string `json:"token"`
	}
	decodeInto(t, rec, &body)
	if body.Token == "" || body.Token == old {
		t.Fatalf("rotate returned %q, want a new token", body.Token)
	}
	if rec := e.do(http.MethodGet, "/api/v2/summary", "", bearer(old), false); rec.Code != http.StatusUnauthorized {
		t.Errorf("the old token = %d, want 401", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/api/v2/summary", "", bearer(body.Token), false); rec.Code != http.StatusOK {
		t.Errorf("the new token = %d, want 200", rec.Code)
	}
	if got := tokenOf(t, e, cookie); got != body.Token {
		t.Errorf("GET token after rotate = %q, want the new one", got)
	}
}

// Nothing secret reaches the event log: no password and no token, in any event.
func TestAccessSecretsNeverReachTheEventLog(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	token := tokenOf(t, e, cookie)
	e.do(http.MethodPost, "/api/v2/access/password", `{"password":"`+newPassword+`"}`, write(withSession(cookie)), false)
	e.do(http.MethodPost, "/api/v2/access/token/rotate", "", write(withSession(cookie)), false)
	signInWith(e, newPassword)
	if err := e.closeBus(); err != nil {
		t.Fatal(err)
	}
	evs, _, err := e.bus.Query(context.Background(), events.Filter{}, events.Page{Limit: 1000, Newest: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		blob := ev.Message + ev.Reason + ev.NextAction + ev.Code
		if strings.Contains(blob, newPassword) || strings.Contains(blob, token) || strings.Contains(blob, testPassword) {
			t.Errorf("an event carries a secret: %+v", ev)
		}
	}
}
