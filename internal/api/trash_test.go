// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// tomorrowPlusEight is a clock eight days on, past the default keep of 7 days.
func tomorrowPlusEight() time.Time { return time.Now().Add(8 * 24 * time.Hour) }

// replacedFixture makes a real replace in the env's store: the library folder holds the original and the new file
// the job wrote. The converted file takes the original's name, or movie.mp4 when mp4 is set. It returns the row id.
func replacedFixture(t *testing.T, e *env, mp4 bool) (orig, final string, id int64) {
	t.Helper()
	lib := t.TempDir()
	orig = filepath.Join(lib, "movie.mkv")
	final = orig
	if mp4 {
		final = filepath.Join(lib, "movie.mp4")
	}
	staged := filepath.Join(lib, "out.staged.mkv")
	if err := os.WriteFile(orig, []byte("ORIGINAL-BYTES-0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("NEW-BYTES-ABCDEFGHIJKLMNOP"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := safety.Swap(context.Background(), safety.NewJournal(e.db), safety.SwapPlan{
		JobID: 1, Original: orig, Staged: staged, Final: final, KeepDays: 7,
	}); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if err := e.db.Read().QueryRow(`SELECT id FROM replace_journal ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return orig, final, id
}

// TestTrashListsTheOriginalWithItsDaysLeft: GET /trash shows the kept original, newest first, with 7 days left.
func TestTrashListsTheOriginalWithItsDaysLeft(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	_, _, id := replacedFixture(t, e, false)
	rec := e.do(http.MethodGet, "/api/v2/trash", "", map[string]string{"Cookie": cookie.String()}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	var items []struct {
		ID        string `json:"id"`
		File      string `json:"file"`
		TrashedAt string `json:"trashedAt"`
		DaysLeft  int    `json:"daysLeft"`
	}
	decodeInto(t, rec, &items)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.ID != fmt.Sprint(id) || it.File != "movie.mkv" || it.DaysLeft != 7 || it.TrashedAt == "" {
		t.Errorf("item = %+v", it)
	}
}

// TestRestoreAnswersOKAndPutsTheOriginalBack: POST /trash/{id}/restore puts the original back and says ok.
func TestRestoreAnswersOKAndPutsTheOriginalBack(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	orig, _, id := replacedFixture(t, e, false)
	rec := e.do(http.MethodPost, fmt.Sprintf("/api/v2/trash/%d/restore", id), "",
		write(map[string]string{"Cookie": cookie.String()}), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	got, err := os.ReadFile(orig)
	if err != nil || string(got) != "ORIGINAL-BYTES-0123456789" {
		t.Errorf("the original is %q, %v; want its own bytes", got, err)
	}
}

// TestRestoreRefusalsAnswerTheirCodeAndSentence: each refusal is a 409 with its code and the page's sentence, and
// nothing changes.
func TestRestoreRefusalsAnswerTheirCodeAndSentence(t *testing.T) {
	cases := []struct {
		name    string
		mp4     bool
		prepare func(t *testing.T, e *env, orig, final string, id int64)
		code    string
		message string
	}{
		{
			name: "original gone",
			prepare: func(t *testing.T, e *env, orig, final string, id int64) {
				if _, err := safety.SweepTrash(context.Background(), safety.NewJournal(e.db), tomorrowPlusEight(), 7); err != nil {
					t.Fatal(err)
				}
			},
			code: "original_gone", message: "The original isn't in the trash any more.",
		},
		{
			name: "converted changed",
			prepare: func(t *testing.T, e *env, orig, final string, id int64) {
				if err := os.WriteFile(final, []byte("EDITED-LATER-XXXXXXXXXXXXXX"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			code: "converted_changed", message: "The converted file was changed after it was made, so it's left alone.",
		},
		{
			name: "name taken",
			mp4:  true,
			prepare: func(t *testing.T, e *env, orig, final string, id int64) {
				if err := os.WriteFile(orig, []byte("a stranger"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			code: "name_taken", message: "Another file now has this name in the folder.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, true)
			cookie := e.setup()
			orig, final, id := replacedFixture(t, e, c.mp4)
			c.prepare(t, e, orig, final, id)
			rec := e.do(http.MethodPost, fmt.Sprintf("/api/v2/trash/%d/restore", id), "",
				write(map[string]string{"Cookie": cookie.String()}), false)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status %d, want 409; body %s", rec.Code, rec.Body)
			}
			code, message, _ := errorBody(t, rec)
			if code != c.code || message != c.message {
				t.Errorf("error = %q %q, want %q %q", code, message, c.code, c.message)
			}
		})
	}
}

// TestRestoreUnknownIsNotFound: an id that is not in the journal is a 404.
func TestRestoreUnknownIsNotFound(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := e.do(http.MethodPost, "/api/v2/trash/999999/restore", "",
		write(map[string]string{"Cookie": cookie.String()}), false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", rec.Code, rec.Body)
	}
}

// TestAttentionListsAReplaceThatNeedsALook: a journal row in "attention" shows on the Needs attention list in plain
// words, and the list is no longer always empty.
func TestAttentionListsAReplaceThatNeedsALook(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	orig, _, id := replacedFixture(t, e, false)
	if err := safety.NewJournal(e.db).SetState(context.Background(), id, safety.StateAttention); err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodGet, "/api/v2/attention", "", map[string]string{"Cookie": cookie.String()}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	var items []struct {
		ID   string `json:"id"`
		What string `json:"what"`
	}
	decodeInto(t, rec, &items)
	if len(items) != 1 || !strings.Contains(items[0].What, filepath.Base(orig)) {
		t.Fatalf("attention = %+v, want one row naming %s", items, filepath.Base(orig))
	}
}
