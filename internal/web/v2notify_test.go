// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/notify"
)

// notifyServer serves the notification routes against a fake receiver; nothing reaches a real Discord.
func notifyServer(t *testing.T) (srv *httptest.Server, receiver *httptest.Server) {
	t.Helper()
	receiver = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mux := http.NewServeMux()
	v2Notify(mux, notify.New(st, notify.Options{}))
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, receiver
}

// notifyCall sends one request and returns its status and body text.
func notifyCall(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func notifyErrCode(t *testing.T, body string) string {
	t.Helper()
	var m struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	json.Unmarshal([]byte(body), &m)
	if m.Error.Code == "" {
		var flat struct {
			Code string `json:"code"`
		}
		json.Unmarshal([]byte(body), &flat)
		return flat.Code
	}
	return m.Error.Code
}

func TestNotifyRoutesAnswerTheContractCodes(t *testing.T) {
	srv, rx := notifyServer(t)
	base := srv.URL + "/api/v2/notifications"

	for _, c := range []struct{ name, body, code string }{
		{"bad url", `{"name":"x","kind":"webhook","url":"ftp://nope"}`, "invalid_url"},
		{"bad kind", `{"name":"x","kind":"email","url":"http://a.test/x"}`, "invalid_kind"},
	} {
		code, body := notifyCall(t, "POST", base+"/targets", c.body)
		if code != http.StatusBadRequest || notifyErrCode(t, body) != c.code {
			t.Errorf("%s: %d %s, want 400 %s", c.name, code, body, c.code)
		}
	}

	// a good target: the answer shows the host only, never the address
	secret := rx.URL + "/hook/SECRETPATH9921"
	code, body := notifyCall(t, "POST", base+"/targets", `{"name":"Den","kind":"webhook","url":"`+secret+`"}`)
	if code != http.StatusOK || strings.Contains(body, "SECRETPATH9921") {
		t.Fatalf("create: %d %s", code, body)
	}
	var tg struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(body), &tg)
	_, got := notifyCall(t, "GET", base, "")
	if strings.Contains(got, "SECRETPATH9921") {
		t.Errorf("GET shows the address: %s", got)
	}

	for _, c := range []struct{ name, body, code string }{
		{"summary time", `{"summary":{"mode":"daily","at":"9:00"}}`, "invalid"},
		{"summary mode", `{"summary":{"mode":"hourly","at":"09:00"}}`, "invalid"},
		{"link base", `{"linkBase":"ftp://nas"}`, "invalid"},
	} {
		code, body := notifyCall(t, "PATCH", base, c.body)
		if code != http.StatusBadRequest || notifyErrCode(t, body) != c.code {
			t.Errorf("%s: %d %s, want 400 %s", c.name, code, body, c.code)
		}
	}

	if code, _ := notifyCall(t, "PATCH", base+"/targets/nope", `{"enabled":false}`); code != http.StatusNotFound {
		t.Errorf("patch unknown target: %d, want 404", code)
	}
	if code, body := notifyCall(t, "PATCH", base+"/targets/"+tg.ID, `{"url":"ftp://x"}`); code != http.StatusBadRequest || notifyErrCode(t, body) != "invalid_url" {
		t.Errorf("patch bad url: %d %s", code, body)
	}
	if code, _ := notifyCall(t, "DELETE", base+"/targets/"+tg.ID, ""); code != http.StatusNoContent {
		t.Errorf("delete: %d, want 204", code)
	}
	if code, _ := notifyCall(t, "DELETE", base+"/targets/"+tg.ID, ""); code != http.StatusNotFound {
		t.Errorf("delete again: %d, want 404", code)
	}
	if code, body := notifyCall(t, "POST", base+"/test", `{"id":"nope"}`); code != http.StatusNotFound || notifyErrCode(t, body) != "not_found" {
		t.Errorf("test unknown id: %d %s", code, body)
	}
}

// A failed send is a 200 with ok:false, not an HTTP error.
func TestNotifyTestFormsAnswer200(t *testing.T) {
	srv, rx := notifyServer(t)
	base := srv.URL + "/api/v2/notifications"
	code, body := notifyCall(t, "POST", base+"/test", `{"kind":"webhook","url":"`+rx.URL+`/hook/tok"}`)
	if code != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Errorf("kind+url: %d %s", code, body)
	}
	code, body = notifyCall(t, "POST", base+"/test", `{"kind":"discord","url":"`+rx.URL+`/api/webhooks/1/x"}`)
	if code != http.StatusOK || !strings.Contains(body, `"message"`) {
		t.Errorf("discord kind+url: %d %s", code, body)
	}
}
