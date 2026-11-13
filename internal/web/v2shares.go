// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/shrink-your-shit/shrink-your-shit/internal/helper"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cloudsoda/go-smb2"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

func v2Shares(api *http.ServeMux, store *jobs.Store) {
	mode, platform := detectMode()
	nfsOK := platform != "darwin"
	tested := platform != "darwin"

	api.HandleFunc("GET /api/v2/shares", func(w http.ResponseWriter, r *http.Request) {
		mode, platform = detectMode()
		all, err := store.LoadShares()
		if err != nil {
			serverError(w, err)
			return
		}
		items := make([]map[string]any, 0)
		for _, sh := range all {
			items = append(items, shareToJSON(store, sh))
		}
		writeJSON(w, map[string]any{
			"mode": mode, "platform": platform,
			"nfsSupported": nfsOK, "testedPlatform": tested,
			"serverAddress": nil, "items": items,
		})
	})

	api.HandleFunc("POST /api/v2/shares", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Type, Address, Name, Username, Password string
			Guest                                   bool
		}
		if !readJSON(w, r, &in) {
			return
		}
		in.Address = strings.TrimSpace(in.Address)
		in.Name = strings.TrimSpace(in.Name)
		if in.Address == "" || in.Name == "" {
			apiError(w, http.StatusBadRequest, "invalid_address", "Address and name are required.")
			return
		}
		if in.Type != "" && in.Type != "smb" && in.Type != "nfs" {
			apiError(w, http.StatusBadRequest, "invalid", "Share type must be smb or nfs.")
			return
		}
		if err := nameTaken(store, in.Name, 0); err != nil {
			apiError(w, http.StatusConflict, "name_taken", err.Error())
			return
		}
		addr, typ, err := parseAddress(in.Address)
		if err != nil {
			apiError(w, http.StatusBadRequest, "invalid_address", err.Error())
			return
		}
		sh := jobs.Share{Type: typ, Address: addr, Name: in.Name, Guest: in.Guest, Username: in.Username}
		saved, err := store.SaveShare(sh, in.Password)
		if err != nil {
			serverError(w, err)
			return
		}
		if mode == "app" {
			tryMount(store, saved, in.Password)
		}
		writeJSONStatus(w, http.StatusCreated, shareToJSON(store, saved))
	})

	api.HandleFunc("PATCH /api/v2/shares/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such share.")
			return
		}
		existing, err := store.ShareByID(id)
		if err != nil || existing == nil {
			apiError(w, http.StatusNotFound, "not_found", "There is no such share.")
			return
		}
		// Decode as a map so we only apply fields actually present in the body
		var raw map[string]any
		if !readJSON(w, r, &raw) {
			return
		}
		name := existing.Name
		address := existing.Address
		typeStr := existing.Type
		username := existing.Username
		guest := existing.Guest
		pw := ""
		changed := false
		if pwStr, ok := raw["password"].(string); ok {
			pw = pwStr
			changed = true
		}
		if v, ok := raw["name"].(string); ok && v != "" {
			if err := nameTaken(store, v, id); err != nil {
				apiError(w, http.StatusConflict, "name_taken", err.Error())
				return
			}
			name = v
			changed = true
		}
		if v, ok := raw["type"].(string); ok && v != "" {
			if v != "smb" && v != "nfs" {
				apiError(w, http.StatusBadRequest, "invalid", "Share type must be smb or nfs.")
				return
			}
			typeStr = v
			changed = true
		}
		if v, ok := raw["address"].(string); ok && v != "" {
			a, t, pErr := parseAddress(v)
			if pErr != nil {
				apiError(w, http.StatusBadRequest, "invalid_address", pErr.Error())
				return
			}
			address = a
			typeStr = t
			changed = true
		}
		if v, ok := raw["username"].(string); ok {
			username = v
			changed = true
		}
		if _, ok := raw["guest"].(bool); ok {
			if v, ok2 := raw["guest"].(bool); ok2 {
				guest = v
				changed = true
			}
		}
		if err := store.UpdateShare(id, name, address, typeStr, username, guest, pw); err != nil {
			serverError(w, err)
			return
		}
		if mode == "app" && changed {
			sh, err := store.ShareByID(id)
			if err == nil && sh != nil {
				stale := pw
				if stale == "" {
					stale, _ = store.DecryptSharePassword(id)
				}
				tryMount(store, *sh, stale)
			}
		}
		sh, err := store.ShareByID(id)
		if err != nil || sh == nil {
			apiError(w, http.StatusNotFound, "not_found", "There is no such share.")
			return
		}
		writeJSON(w, shareToJSON(store, *sh))
	})

	api.HandleFunc("DELETE /api/v2/shares/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such share.")
			return
		}
		if mode == "app" {
			sh, err := store.ShareByID(id)
			if err == nil && sh != nil {
				mpt := "/var/lib/syp/shares/" + sanitiseName(sh.Name, sh.ID)
				if err := helper.Unmount(mpt); err != nil {
					apiError(w, http.StatusConflict, "share_in_use",
						"The share could not be unmounted. " + err.Error() + ".")
					return
				}
			}
		}
		if err := store.DeleteShare(id); err != nil {
			serverError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	api.HandleFunc("POST /api/v2/shares/test", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Type, Address, Username, Password, ID string
			Guest                                 bool
		}
		if !readJSON(w, r, &in) {
			return
		}
		if in.Type != "" && in.Type != "smb" && in.Type != "nfs" {
			writeJSON(w, map[string]any{"ok": false, "code": "invalid", "message": "Share type must be smb or nfs."})
			return
		}
		addr, typ, err := parseAddress(in.Address)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "code": "invalid_address", "message": err.Error()})
			return
		}
		pw := in.Password
		if pw == "" && in.ID != "" {
			if id, parseErr := strconv.ParseInt(in.ID, 10, 64); parseErr == nil {
				if sh, loadErr := store.ShareByID(id); loadErr == nil && sh != nil {
					pw, _ = store.DecryptSharePassword(id)
				}
			}
		}
		writeJSON(w, testShare(typ, addr, in.Username, pw, in.Guest))
	})

	api.HandleFunc("POST /api/v2/shares/{id}/reconnect", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such share.")
			return
		}
		sh, err := store.ShareByID(id)
		if err != nil || sh == nil {
			apiError(w, http.StatusNotFound, "not_found", "There is no such share.")
			return
		}
		if mode == "app" {
			pw, _ := store.DecryptSharePassword(id)
			tryMount(store, *sh, pw)
		}
		writeJSON(w, shareToJSON(store, *sh))
	})

	api.HandleFunc("POST /api/v2/shares/{id}/check", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such share.")
			return
		}
		sh, err := store.ShareByID(id)
		if err != nil || sh == nil {
			apiError(w, http.StatusNotFound, "not_found", "There is no such share.")
			return
		}
		writeJSON(w, shareToJSON(store, *sh))
	})
}

// tryMount attempts to mount the share via the helper, storing the result.
func tryMount(store *jobs.Store, sh jobs.Share, password string) {
	mode, _ := detectMode()
	if mode != "app" {
		return
	}
	addr := mountAddress(sh.Type, sh.Address)
	kind := "cifs"
	if sh.Type == "nfs" {
		kind = "nfs"
	}
	mpt := "/var/lib/syp/shares/" + sanitiseName(sh.Name, sh.ID)
	opts := mountOptions(sh.Type)
	err := helper.Mount(kind, addr, mpt, opts, sh.Username, password)
	if err != nil {
		reason := mountErrReason(err)
		store.SetShareMountStatus(sh.ID, "linux", "not_connected", reason)
		return
	}
	store.SetShareMountStatus(sh.ID, "linux", "connected", "")
}

func mountAddress(typ, addr string) string {
	if typ == "nfs" {
		return addr // server:/export stays as-is
	}
	// smb: \\server\share → //server/share
	base := strings.TrimPrefix(addr, `\\`)
	return "//" + strings.ReplaceAll(base, `\`, "/")
}

func mountOptions(typ string) map[string]string {
	if typ == "nfs" {
		return map[string]string{}
	}
	return map[string]string{"vers": "3.1.1", "soft": ""}
}

// mountErrReason serialises a helper error to a JSON string storing code + message.
func mountErrReason(err error) string {
	msg := err.Error()
	low := strings.ToLower(msg)
	// helper unreachable (socket dial failed)
	if strings.HasPrefix(low, "helper:") {
		return `{"code":"helper_missing","message":"The network share helper isn't running. Run the installer again and answer yes, or connect it yourself."}`
	}
	switch {
	case strings.Contains(low, "not_allowed"), strings.Contains(low, "bad_request"):
		return `{"code":"mount_failed","message":` + jsonStr(msg) + `}`
	case strings.Contains(low, "permission denied"), strings.Contains(low, "error(13)"):
		return `{"code":"auth_failed","message":"The username or password is wrong."}`
	case strings.Contains(low, "no such file or directory"), strings.Contains(low, "error(2)"):
		return `{"code":"share_not_found","message":"` + jsonStr(msg) + `"}`
	case strings.Contains(low, "host is down"), strings.Contains(low, "no route"), strings.Contains(low, "error(112)"), strings.Contains(low, "error(113)"):
		return `{"code":"host_unreachable","message":"The server is not reachable."}`
	case strings.Contains(low, "timed out"):
		return `{"code":"timeout","message":"The server didn't answer in time."}`
	}
	return `{"code":"mount_failed","message":` + jsonStr(msg) + `}`
}

// jsonStr wraps a string in JSON quotes with escaping.
func jsonStr(s string) string {
	esc := strings.ReplaceAll(s, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	esc = strings.ReplaceAll(esc, "\n", `\n`)
	return `"` + esc + `"`
}

func parseAddress(raw string) (addr, typ string, err error) {
	raw = strings.TrimSpace(raw)
	low := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(low, "smb://"):
		rest := raw[6:]
		if rest == "" {
			return "", "", errors.New("SMB address missing server and share after smb://")
		}
		addr = `\\` + strings.ReplaceAll(rest, "/", `\`)
		return addr, "smb", nil
	case strings.HasPrefix(low, "nfs://"):
		rest := raw[6:]
		if rest == "" {
			return "", "", errors.New("NFS address missing server and export after nfs://")
		}
		addr = rest
		return addr, "nfs", nil
	case strings.HasPrefix(raw, `\\`):
		if raw == `\\` {
			return "", "", errors.New("SMB address missing server and share after \\\\")
		}
		return raw, "smb", nil
	case strings.Contains(raw, ":/"):
		if strings.HasPrefix(raw, ":") {
			return "", "", errors.New("NFS address missing server before :/")
		}
		return raw, "nfs", nil
	}
	return "", "", errors.New("Address must start with \\\\ or smb:// for SMB, or server:/export for NFS.")
}

func detectMode() (mode, platform string) {
	if m := os.Getenv("SYP_HELPER_MODE"); m != "" {
		return m, runtime.GOOS
	}
	// Restore: docker detection (was removed in 10d3843)
	if os.Getenv("container") != "" {
		return "docker", runtime.GOOS
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker", runtime.GOOS
	}
	if runtime.GOOS == "linux" {
		// app mode when the helper socket answers (dial with 1s timeout)
		conn, err := net.DialTimeout("unix", helper.HelperSocket, time.Second)
		if err == nil {
			conn.Close()
			return "app", runtime.GOOS
		}
	}
	return "manual", runtime.GOOS
}

func testShare(typ, address, username, password string, guest bool) map[string]any {
	if typ == "smb" {
		return testSMB(address, username, password, guest)
	}
	return testNFS(address)
}

func testSMB(address, username, password string, guest bool) map[string]any {
	parts := strings.Split(strings.TrimPrefix(address, `\\`), `\`)
	if len(parts) < 2 {
		return map[string]any{"ok": false, "code": "invalid_address", "message": "Use \\\\server\\share."}
	}
	server := parts[0]
	shareName := strings.Join(parts[1:], "\\")
	ini := &smb2.NTLMInitiator{User: username, Password: password}
	dialer := &smb2.Dialer{Initiator: ini}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := dialer.Dial(ctx, net.JoinHostPort(server, smbPort))
	if err != nil {
		return smbError(err)
	}
	share, err := session.Mount(shareName)
	if err != nil {
		session.Logoff()
		return smbError(err)
	}
	entries, err := share.ReadDir("")
	session.Logoff()
	if err != nil {
		return smbError(err)
	}
	var folders []string
	more := 0
	for _, e := range entries {
		if e.IsDir() && len(folders) < 3 {
			folders = append(folders, e.Name())
		} else if e.IsDir() {
			more++
		}
	}
	return map[string]any{"ok": true, "address": address, "folders": folders, "moreFolders": more, "portOnly": false}
}

// smbPort and nfsPort are the ports the Test button tries. Tests point them at a port known to be closed:
// a Windows machine (CI runners included) runs its own SMB server on 445.
var smbPort, nfsPort = "445", "2049"

func smbError(err error) map[string]any {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "no such host"), strings.Contains(low, "nodename nor servname"):
		return map[string]any{"ok": false, "code": "host_not_found", "message": msg}
	case strings.Contains(low, "refused"), strings.Contains(low, "no route"):
		return map[string]any{"ok": false, "code": "host_unreachable", "message": msg}
	case strings.Contains(low, "timeout"), strings.Contains(low, "timed out"):
		return map[string]any{"ok": false, "code": "timeout", "message": "The server didn't answer in time."}
	case strings.Contains(low, "logon failure"), strings.Contains(low, "logon"):
		return map[string]any{"ok": false, "code": "auth_failed", "message": "The username or password is not correct."}
	case strings.Contains(low, "access denied"):
		return map[string]any{"ok": false, "code": "access_denied", "message": "Access denied: the account doesn't have permission on this share."}
	case strings.Contains(low, "not found"), strings.Contains(low, "does not exist"):
		return map[string]any{"ok": false, "code": "share_not_found", "message": msg}
	}
	return map[string]any{"ok": false, "code": "mount_failed", "message": msg}
}

func testNFS(address string) map[string]any {
	server := strings.Split(address, ":")[0]
	if server == "" {
		return map[string]any{"ok": false, "code": "invalid_address", "message": "Use server:/export."}
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(server, nfsPort), 10*time.Second)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such host") {
			return map[string]any{"ok": false, "code": "host_not_found", "message": "The NFS server was not found."}
		}
		if strings.Contains(strings.ToLower(err.Error()), "refused") {
			return map[string]any{"ok": false, "code": "host_unreachable", "message": "The NFS server didn't answer."}
		}
		return map[string]any{"ok": false, "code": "timeout", "message": "The NFS server didn't answer in time."}
	}
	conn.Close()
	return map[string]any{"ok": true, "address": address, "folders": []string{}, "moreFolders": 0, "portOnly": true}
}

func shareToJSON(store *jobs.Store, sh jobs.Share) map[string]any {
	status := "waiting_setup"
	var reason any = nil
	var since string = ""
	mode, _ := detectMode()
	if mode == "app" {
		status = "not_connected"
	}
	mounts, err := store.ShareMountsByShare(sh.ID)
	if err == nil && len(mounts) > 0 {
		m := mounts[0]
		status = m.Status
		since = m.Since
		if m.Error != "" {
			// Try to parse as structured JSON error
			var parsed map[string]any
			if json.Unmarshal([]byte(m.Error), &parsed) == nil && parsed["code"] != nil {
				reason = parsed
			} else {
				reason = map[string]any{"code": "mount_failed", "message": m.Error}
			}
		}
		if m.Status == "connected" {
			status = "connected"
			reason = nil
		}
	}
	addr := sh.Address
	if sh.Type == "smb" {
		addr = strings.ReplaceAll(sh.Address, "/", "\\")
	}
	return map[string]any{
		"id": strconv.FormatInt(sh.ID, 10), "name": sh.Name, "type": sh.Type,
		"address": addr, "guest": sh.Guest, "username": nilIfEmpty(sh.Username),
		"hasPassword": sh.HasPassword, "status": status, "since": nilIfEmpty(since), "reason": reason,
		"path": "/var/lib/syp/shares/" + sanitiseName(sh.Name, sh.ID),
		"libraries": []any{}, "setup": setupForMode(store, sh),
	}
}

func sanitiseName(name string, id int64) string {
	r := strings.ToLower(name)
	r = strings.ReplaceAll(r, " ", "-")
	var out []rune
	for _, c := range r {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	result := string(out)
	if result == "" || result[0] == '-' || result[0] == '_' {
		if id > 0 {
			return "share-" + strconv.FormatInt(id, 10)
		}
		return "share"
	}
	return result
}

// shellQuote wraps a value in single quotes, escaping any internal single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// nameTaken checks whether another share with the same name exists (case-insensitive).
func nameTaken(store *jobs.Store, name string, excludeID int64) error {
	all, err := store.LoadShares()
	if err != nil {
		return err
	}
	low := strings.ToLower(strings.TrimSpace(name))
	for _, sh := range all {
		if strings.ToLower(strings.TrimSpace(sh.Name)) == low && sh.ID != excludeID {
			return errors.New("A share with this name already exists.")
		}
	}
	return nil
}

func setupForMode(store *jobs.Store, sh jobs.Share) any {
	mode, _ := detectMode()
	mpt := "/var/lib/syp/shares/" + sanitiseName(sh.Name, sh.ID)
	mntAddr := mountAddress(sh.Type, sh.Address)
	switch mode {
	case "manual":
		mkdir := "sudo mkdir -p " + shellQuote(mpt) + " && "
		var opts string
		if sh.Type == "smb" {
			mountCmd := "sudo mount -t cifs " + shellQuote(mntAddr) + " " + shellQuote(mpt) + " -o "
			if sh.Guest {
				opts = "guest,vers=3.1.1,soft,nosuid,nodev,noexec,uid=syp,gid=syp,file_mode=0660,dir_mode=0770"
			} else {
				opts = "username=" + shellQuote(sh.Username) + ",vers=3.1.1,soft,nosuid,nodev,noexec,uid=syp,gid=syp,file_mode=0660,dir_mode=0770"
			}
			return map[string]any{
				"kind": "command",
				"text": mkdir + mountCmd + shellQuote(opts),
				"removeText": "sudo umount " + shellQuote(mpt),
			}
		}
		// NFS
		opts = "soft,nosuid,nodev,noexec"
		return map[string]any{
			"kind": "command",
			"text": mkdir + "sudo mount -t nfs " + shellQuote(mntAddr) + " " + shellQuote(mpt) + " -o " + shellQuote(opts),
			"removeText": "sudo umount " + shellQuote(mpt),
		}
	case "docker":
		return map[string]any{
			"kind": "compose",
			"text": fmt.Sprintf("  %s:\n    driver_opts:\n      type: %s\n      device: %q\n      o: credentials=/path/to/credentials-file,vers=3.1.1,soft",
				sanitiseName(sh.Name, sh.ID), sh.Type, sh.Address),
			"removeText": fmt.Sprintf("Remove the %q volume from your compose file and restart.", sanitiseName(sh.Name, sh.ID)),
		}
	}
	return nil
}