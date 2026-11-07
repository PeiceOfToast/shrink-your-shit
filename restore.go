// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// restoreCommand is "shrink-your-shit restore [--force] <kept id or file path>": put a kept original back
// (slice 18). It asks the running service to do it, so it is done by the user the service runs as, with
// the same journal as the page's "Get original back". --force puts it back even if the converted file
// was changed since it was made: only here, not over the web page or its API, so it is done directly on
// the database (SYP_DATA or the installed data folder), as the user running this command.
func restoreCommand(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(errOut)
	force := fs.Bool("force", false, "put the original back even if the converted file was changed since")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(errOut, "usage: shrink-your-shit restore [--force] <kept id | path of the converted file>")
		fmt.Fprintln(errOut, "The kept originals and their ids are listed under Activity > Kept originals.")
		return 2
	}
	env := envFile(linuxEnvFile)
	addr, tok := serviceAddr(env)
	if *force {
		return restoreForced(fs.Arg(0), env, out, errOut)
	}
	call := func(method, path string, body any, v any) (int, error) {
		var r io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			r = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, localURL(addr)+path, r)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return resp.StatusCode, json.NewDecoder(resp.Body).Decode(v)
	}
	which := fs.Arg(0)
	id, err := strconv.ParseInt(which, 10, 64)
	if err != nil { // a path: find its kept original
		want, _ := filepath.Abs(which)
		if real, err := jobs.RealPath(which); err == nil {
			want = real
		}
		var kept struct {
			Items []struct{ KeptID, Path string }
		}
		if _, err := call("GET", "/api/activity/kept", nil, &kept); err != nil {
			fmt.Fprintf(errOut, "The service did not answer at %s (%v). Start it, then run this again. Nothing was changed.\n", localURL(addr), err)
			return 1
		}
		for _, k := range kept.Items {
			if k.Path == want || k.Path == which {
				id, _ = strconv.ParseInt(k.KeptID, 10, 64)
			}
		}
		if id == 0 {
			fmt.Fprintf(errOut, "No kept original for %s. Nothing was changed. (Activity > Kept originals lists them.)\n", which)
			return 1
		}
	}
	var res struct {
		Message string
		Error   struct{ Code, Message string }
	}
	code, err := call("POST", fmt.Sprintf("/api/kept/%d/restore", id), map[string]bool{"force": *force}, &res)
	switch {
	case err != nil && code == 0:
		fmt.Fprintf(errOut, "The service did not answer at %s (%v). Start it, then run this again. Nothing was changed.\n", localURL(addr), err)
		return 1
	case code != http.StatusOK:
		fmt.Fprintln(errOut, firstNonEmpty(res.Error.Message, fmt.Sprintf("The service answered %d.", code)))
		return 1
	}
	fmt.Fprintln(out, res.Message)
	return 0
}

// restoreForced is restore --force: on the database directly, so the HTTP API never offers force.
func restoreForced(which string, env map[string]string, out, errOut io.Writer) int {
	data := firstNonEmpty(os.Getenv("SYP_DATA"), env["SYP_DATA"], defaultDataDir())
	// The data folder's lock, held for the whole restore: with the service running (it holds the lock
	// for its life, on any address) two programs would move the same files and rows.
	release, err := lockDataDir(data)
	if errors.Is(err, errDataLocked) {
		fmt.Fprintln(errOut, "The service is running; stop it first or use Get original back in the web page. Nothing was changed.")
		return 1
	}
	if err != nil {
		fmt.Fprintf(errOut, "Could not use the data folder %s (%v). Nothing was changed.\n", data, err)
		return 1
	}
	defer release()
	st, err := jobs.Open(filepath.Join(data, "syp.db"))
	if err != nil {
		fmt.Fprintf(errOut, "Could not open the data folder %s (%v). Nothing was changed.\n", data, err)
		return 1
	}
	defer st.Close()
	id, err := strconv.ParseInt(which, 10, 64)
	if err != nil {
		want, _ := filepath.Abs(which)
		if real, err := jobs.RealPath(which); err == nil {
			want = real
		}
		items, err := st.KeptItems()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		for _, k := range items {
			if k.Path == want || k.Path == which {
				id = k.ID
			}
		}
		if id == 0 {
			fmt.Fprintf(errOut, "No kept original for %s. Nothing was changed.\n", which)
			return 1
		}
	}
	o, err := worker.Restore(st, id, true)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if worker.IdentityConfirmed(o) {
		fmt.Fprintln(out, "The original is back, and its identity is confirmed. It will not be converted again.")
	} else {
		fmt.Fprintln(out, "The original is back, but its identity could not be confirmed. It will not be converted again.")
	}
	return 0
}
