package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// listedWorker is what GET /api/v2/workers shows for a remote worker.
type listedWorker struct {
	Name, Kind, OS, Arch, PrimaryEncoder string
	Capabilities                         []map[string]any
}

// remoteListed is the Workers API's view of the remote worker called name.
func remoteListed(t *testing.T, srv *httptest.Server, name string) listedWorker {
	t.Helper()
	res, err := http.Get(srv.URL + "/api/v2/workers")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct{ Items []listedWorker }
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, w := range out.Items {
		if w.Kind == "remote" && w.Name == name {
			return w
		}
	}
	t.Fatalf("no remote worker %q in the Workers API", name)
	return listedWorker{}
}

// tests maps each capability key the listing shows to its test result.
func tests(w listedWorker) map[string]string {
	out := map[string]string{}
	for _, c := range w.Capabilities {
		key, _ := c["key"].(string)
		test, _ := c["test"].(string)
		out[key] = test
	}
	return out
}

// G1: a remote worker reports its encoder test with its OS and CPU type when it pairs, and on each heartbeat;
// the Workers API shows exactly what it reported. An older worker that sends {} still pairs and shows none, and
// its heartbeat leaves what a newer one reported alone.
func TestRemoteWorkerReportsItsEncoderTests(t *testing.T) {
	srv, st, _ := server(t)
	ctx := context.Background()
	fake := []map[string]any{
		{"key": "encoder:hevc_qsv", "kind": "encoder", "label": "HEVC (Intel Quick Sync)", "slot": "gpu", "test": "passed", "format": "hevc", "family": "intel"},
		{"key": "encoder:h264_qsv", "kind": "encoder", "label": "H.264 (Intel Quick Sync)", "slot": "gpu", "test": "failed", "format": "h264", "family": "intel", "detail": "The test encode failed."},
	}
	code, _, err := st.NewPairing(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Server: srv.URL, Version: "test", Caps: fake}
	state, err := c.Pair(ctx, code, "Fake remote")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	c.Token = state.Token
	if err := c.Heartbeat(ctx, 1); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	w := remoteListed(t, srv, "Fake remote")
	if got := tests(w); len(got) != 2 || got["encoder:hevc_qsv"] != "passed" || got["encoder:h264_qsv"] != "failed" {
		t.Errorf("the listing shows %v, want hevc_qsv passed and h264_qsv failed only", got)
	}
	if w.OS != runtime.GOOS || w.Arch != runtime.GOARCH {
		t.Errorf("os %q arch %q, want %q %q", w.OS, w.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if w.PrimaryEncoder != "hevc_qsv" {
		t.Errorf("primary encoder %q, want hevc_qsv", w.PrimaryEncoder)
	}

	// an older worker: pairs with {} and no OS or CPU type, and shows none
	old, _, err := st.NewPairing(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/worker/v1/pair", strings.NewReader(`{"code":"`+old+`","name":"Old remote","capabilities":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SYP-Worker", "test; api="+strconv.Itoa(APILevel))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var paired struct{ Token string }
	json.NewDecoder(res.Body).Decode(&paired)
	res.Body.Close()
	if res.StatusCode != 200 || paired.Token == "" {
		t.Fatalf("an older worker's pairing: %d", res.StatusCode)
	}
	if o := remoteListed(t, srv, "Old remote"); len(o.Capabilities) != 0 || o.OS != "" {
		t.Errorf("an older worker shows %v and os %q, want none", o.Capabilities, o.OS)
	}
	oldClient := &Client{Server: srv.URL, Token: paired.Token, Version: "test"} // sends no report, as before
	if err := oldClient.Heartbeat(ctx, 1); err != nil {
		t.Fatalf("an older worker's heartbeat: %v", err)
	}
	if got := tests(remoteListed(t, srv, "Fake remote")); got["encoder:hevc_qsv"] != "passed" {
		t.Errorf("after an older worker's heartbeat the newer worker shows %v", got)
	}
}
