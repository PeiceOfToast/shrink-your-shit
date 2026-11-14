// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/shrink-your-shit/shrink-your-shit/internal/helper"
	"log"
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
	_, platform := detectMode()
	tested := platform != "darwin"

	api.HandleFunc("GET /api/v2/shares", func(w http.ResponseWriter, r *http.Request) {
		mode, _ := detectMode() // per request: the helper can start or stop while the app runs
		nfsOK := platform != "darwin" && !onWindows()
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
		if typ == "nfs" && onWindows() {
			apiError(w, http.StatusBadRequest, "nfs_unsupported", "NFS shares can't be used on Windows. Add the share as SMB.")
			return
		}
		sh := jobs.Share{Type: typ, Address: addr, Name: in.Name, Guest: in.Guest, Username: in.Username}
		saved, err := store.SaveShare(sh, in.Password)
		if err != nil {
			serverError(w, err)
			return
		}
		tryMount(store, saved, in.Password)
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
		oldMpt := mountPointFor(*existing)
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
		if mode, _ := detectMode(); mode == "app" && changed {
			// The old mount must go first: the helper treats an already-mounted
			// point as done, so a busy old mount would stay on the old server.
			if uErr := unmountShare(oldMpt); uErr != nil {
				store.SetShareMountStatus(id, runtime.GOOS, "not_connected",
					`{"code":"mount_failed","message":`+jsonStr("The old connection is still in use, so the change isn't connected yet. Stop what is using the share, then Reconnect. "+uErr.Error())+`}`)
			} else if sh, err := store.ShareByID(id); err == nil && sh != nil {
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
		if mode, _ := detectMode(); mode == "app" {
			sh, err := store.ShareByID(id)
			if err == nil && sh != nil {
				mpt := mountPointFor(*sh)
				if err := unmountShare(mpt); err != nil {
					apiError(w, http.StatusConflict, "share_in_use",
						"The share could not be unmounted. "+err.Error()+".")
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
				// The saved password only ever goes to the share's own address and type.
				if sh, loadErr := store.ShareByID(id); loadErr == nil && sh != nil && sh.Address == addr && sh.Type == typ {
					pw, _ = store.DecryptSharePassword(id)
				}
			}
		}
		writeJSON(w, testShareFn(typ, addr, in.Username, pw, in.Guest))
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
		if mode, _ := detectMode(); mode == "app" {
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
		if shareWatched() {
			checkShare(store, *sh) // the person has just set it up: look now, not in a minute
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
	if onWindows() {
		if err := connectWindowsShare(sh, password); err != nil {
			store.SetShareMountStatus(sh.ID, runtime.GOOS, "not_connected", winMountReason(err))
			return
		}
		store.SetShareMountStatus(sh.ID, runtime.GOOS, "connected", "")
		return
	}
	addr := mountAddress(sh.Type, sh.Address)
	kind := "cifs"
	if sh.Type == "nfs" {
		kind = "nfs"
	}
	mpt := mountPointFor(sh)
	opts := mountOptions(sh.Type)
	err := helper.Mount(kind, addr, mpt, opts, sh.Username, password)
	if err != nil {
		reason := mountErrReason(err)
		store.SetShareMountStatus(sh.ID, runtime.GOOS, "not_connected", reason)
		return
	}
	store.SetShareMountStatus(sh.ID, runtime.GOOS, "connected", "")
}

// RemountShares mounts every saved share again when the server starts (app mode only), so
// libraries on shares are not empty after a reboot. It logs one line per share, never the password.
func RemountShares(store *jobs.Store) {
	if mode, _ := detectMode(); mode != "app" {
		return
	}
	shares, err := store.LoadShares()
	if err != nil {
		log.Printf("shares: could not load saved shares: %v", err)
		return
	}
	for _, sh := range shares {
		pw, _ := store.DecryptSharePassword(sh.ID)
		tryMount(store, sh, pw)
		status, _ := shareToJSON(store, sh)["status"].(string)
		log.Printf("share %q: %s", sh.Name, status)
	}
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
	var he *helper.MountError
	if errors.As(err, &he) && (he.Code == "not_allowed" || he.Code == "bad_request") {
		return `{"code":"mount_failed","message":` + jsonStr(msg) + `}`
	}
	switch {
	case strings.Contains(low, "permission denied"), strings.Contains(low, "error(13)"):
		return `{"code":"auth_failed","message":"The username or password is wrong."}`
	case strings.Contains(low, "no such file or directory"), strings.Contains(low, "error(2)"):
		return `{"code":"share_not_found","message":` + jsonStr(msg) + `}`
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

// parseAddress cleans a share address and refuses one with a port: the root helper takes none (SMB always uses
// 445, NFS 2049), so a share saved with one could never connect (orch-lead, ui-shop QA of #217).
func parseAddress(raw string) (addr, typ string, err error) {
	addr, typ, err = parseAddressForm(raw)
	if err != nil {
		return "", "", err
	}
	if hasPort(addressHost(typ, addr)) {
		if typ == "smb" {
			return "", "", errors.New("Leave out the port: SMB always uses 445.")
		}
		return "", "", errors.New("Leave out the port: NFS uses 2049.")
	}
	return addr, typ, nil
}

// addressHost is the server part of a cleaned address: \\server\share or server:/export (IPv6 in brackets).
func addressHost(typ, addr string) string {
	if typ == "smb" {
		return strings.SplitN(strings.TrimPrefix(addr, `\\`), `\`, 2)[0]
	}
	if strings.HasPrefix(addr, "[") { // [fd00::2]:/export, or with a port [fd00::2]:2049:/export
		if i := strings.Index(addr, "]"); i >= 0 {
			rest := addr[i+1:]
			if j := strings.Index(rest, ":/"); j >= 0 {
				return addr[:i+1] + rest[:j]
			}
			return addr[:i+1] + strings.SplitN(rest, "/", 2)[0]
		}
	}
	if i := strings.Index(addr, ":/"); i >= 0 {
		return addr[:i]
	}
	return strings.SplitN(addr, "/", 2)[0]
}

// hasPort reports whether a host names a port: "nas:1445", or "[fe80::1]:1445" (a bracketed IPv6 address alone has none).
func hasPort(host string) bool {
	if strings.HasPrefix(host, "[") {
		i := strings.Index(host, "]")
		return i >= 0 && strings.HasPrefix(host[i+1:], ":")
	}
	return strings.Contains(host, ":")
}

// dialHost is a host for net.JoinHostPort: an IPv6 address without its brackets.
func dialHost(host string) string { return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]") }

func parseAddressForm(raw string) (addr, typ string, err error) {
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

// uidMapFile is the kernel's user-namespace map for this process. A package var so tests can point
// it at a fake map.
var uidMapFile = "/proc/self/uid_map"

// inUserNamespace reports whether the process is in a user namespace (for example Docker inside a
// Proxmox LXC), which stops the kernel allowing NFS and SMB mounts. The map is the whole host range
// ("0 0 4294967295") outside one. An unreadable map counts as not in one.
func inUserNamespace() bool {
	b, err := os.ReadFile(uidMapFile)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "0" && f[1] == "0" && f[2] == "4294967295" {
			return false
		}
	}
	return true
}

func detectMode() (mode, platform string) {
	if runtime.GOOS == "linux" {
		// app mode when the helper socket answers (dial with 1s timeout). This comes before the
		// Docker checks: in the image the entrypoint starts the helper, and then the app mounts itself.
		conn, err := net.DialTimeout("unix", helper.HelperSocket, time.Second)
		if err == nil {
			conn.Close()
			return "app", runtime.GOOS
		}
	}
	if os.Getenv("container") != "" {
		return "docker", runtime.GOOS
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker", runtime.GOOS
	}
	if onWindows() { // the app runs as the person, and connects the share itself
		return "app", runtime.GOOS
	}
	return "manual", runtime.GOOS
}

// testShareFn is testShare; a package var so tests can see which password a test would send.
var testShareFn = testShare

func testShare(typ, address, username, password string, guest bool) map[string]any {
	if typ == "smb" {
		return testSMB(address, username, password, guest)
	}
	return testNFS(address)
}

// smbInitiator is the NTLM sign-in for the Test button: DOMAIN\user becomes User=user, Domain=DOMAIN.
func smbInitiator(username, password string) *smb2.NTLMInitiator {
	user, domain := helper.SplitDomain(username)
	return &smb2.NTLMInitiator{User: user, Password: password, Domain: domain}
}

func testSMB(address, username, password string, guest bool) map[string]any {
	parts := strings.Split(strings.TrimPrefix(address, `\\`), `\`)
	if len(parts) < 2 {
		return map[string]any{"ok": false, "code": "invalid_address", "message": "Use \\\\server\\share."}
	}
	server := parts[0]
	shareName := strings.Join(parts[1:], "\\")
	if hasPort(server) { // parseAddress refuses these first; kept for a direct call
		return map[string]any{"ok": false, "code": "invalid_address",
			"message": "Leave out the port: SMB always uses 445."}
	}
	dialer := &smb2.Dialer{Initiator: smbInitiator(username, password)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := dialer.Dial(ctx, net.JoinHostPort(dialHost(server), smbPort))
	if err != nil {
		return smbError(err, server, shareName)
	}
	share, err := session.Mount(shareName)
	if err != nil {
		session.Logoff()
		return smbError(err, server, shareName)
	}
	entries, err := share.ReadDir("")
	session.Logoff()
	if err != nil {
		return smbError(err, server, shareName)
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

func smbError(err error, server, share string) map[string]any {
	msg := err.Error()
	low := strings.ToLower(msg)
	var code, message string
	switch {
	case strings.Contains(low, "no such host"), strings.Contains(low, "nodename nor servname"):
		code, message = "host_not_found", "Can't find a computer called "+server+". Check the name, or use its IP address."
	case strings.Contains(low, "refused"), strings.Contains(low, "no route"):
		code, message = "host_unreachable", server+" doesn't answer. Check it's on and on the same network."
	case strings.Contains(low, "timeout"), strings.Contains(low, "timed out"):
		code, message = "timeout", "Timed out after 10 seconds."
	case strings.Contains(low, "logon failure"), strings.Contains(low, "logon"):
		code, message = "auth_failed", "The username or password is wrong."
	case strings.Contains(low, "access denied"):
		code, message = "access_denied", "This user can't open "+share+"."
	case strings.Contains(low, "not found"), strings.Contains(low, "does not exist"):
		code, message = "share_not_found", server+" has no shared folder called "+share+"."
	default:
		code, message = "mount_failed", msg
	}
	log.Printf("smb test: server=%s share=%s err=%s code=%s", server, share, msg, code)
	return map[string]any{"ok": false, "code": code, "message": message}
}

func testNFS(address string) map[string]any {
	server := addressHost("nfs", address)
	if server == "" {
		return map[string]any{"ok": false, "code": "invalid_address", "message": "Use server:/export."}
	}
	if hasPort(server) { // parseAddress refuses these first; kept for a direct call
		return map[string]any{"ok": false, "code": "invalid_address",
			"message": "Leave out the port: NFS uses 2049."}
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(dialHost(server), nfsPort), 10*time.Second)
	if err != nil {
		msg := err.Error()
		if strings.Contains(strings.ToLower(msg), "no such host") {
			return map[string]any{"ok": false, "code": "host_not_found",
				"message": "Can't find a computer called " + server + ". Check the name, or use its IP address."}
		}
		if strings.Contains(strings.ToLower(msg), "refused") {
			return map[string]any{"ok": false, "code": "host_unreachable",
				"message": server + " doesn't answer. Check it's on and on the same network."}
		}
		return map[string]any{"ok": false, "code": "timeout", "message": "Timed out after 10 seconds."}
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
		"path":      mountPointFor(sh),
		"libraries": []any{}, "setup": setupForMode(store, sh),
	}
}

// passwordEnv is the .env variable that holds a share's password for its compose volume: SYP_<VOL>_PASSWORD,
// with every character outside A-Z 0-9 in the volume name turned into _.
func passwordEnv(vol string) string {
	name := strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, strings.ToUpper(vol))
	return "SYP_" + name + "_PASSWORD"
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
		return "share-" + strconv.FormatInt(id, 10)
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

// domainOpt is the ",domain=" mount option for a Windows domain, or nothing when there is none.
func domainOpt(domain string) string {
	if domain == "" {
		return ""
	}
	return ",domain=" + domain
}

func setupForMode(store *jobs.Store, sh jobs.Share) any {
	mode, _ := detectMode()
	mpt := mountPointFor(sh)
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
				user, dom := helper.SplitDomain(sh.Username)
				opts = "username=" + user + domainOpt(dom) + ",vers=3.1.1,soft,nosuid,nodev,noexec,uid=syp,gid=syp,file_mode=0660,dir_mode=0770"
			}
			return map[string]any{
				"kind":       "command",
				"text":       mkdir + mountCmd + shellQuote(opts),
				"removeText": "sudo umount " + shellQuote(mpt),
			}
		}
		// NFS
		opts = "soft,nosuid,nodev,noexec"
		return map[string]any{
			"kind":       "command",
			"text":       mkdir + "sudo mount -t nfs " + shellQuote(mntAddr) + " " + shellQuote(mpt) + " -o " + shellQuote(opts),
			"removeText": "sudo umount " + shellQuote(mpt),
		}
	case "docker":
		vol := sanitiseName(sh.Name, sh.ID)
		var driver, note string
		if sh.Type == "nfs" {
			server, export, ok := strings.Cut(sh.Address, ":")
			if !ok {
				export = "/"
			}
			driver = fmt.Sprintf("      type: nfs\n      device: %q\n      o: %q", ":"+export, "addr="+server+",soft,nfsvers=4")
		} else {
			// Docker's local driver mounts with mount(2): it needs addr=, and it reads no credentials file.
			// uid/gid 1000 match the image's USER, so the app can write to the mount.
			host := addressHost("smb", sh.Address)
			owner := "uid=1000,gid=1000,file_mode=0660,dir_mode=0770"
			if sh.Guest {
				driver = fmt.Sprintf("      type: cifs\n      device: %q\n      o: %q", mntAddr,
					"addr="+host+",guest,vers=3.1.1,soft,"+owner)
			} else {
				envName := passwordEnv(vol)
				user, dom := helper.SplitDomain(sh.Username)
				driver = fmt.Sprintf("      type: cifs\n      device: %q\n      o: %q", mntAddr,
					"addr="+host+",username="+user+domainOpt(dom)+",password=${"+envName+"},vers=3.1.1,soft,"+owner)
				note = "# Put " + envName + "=<the share password> in the .env file beside your compose file (chmod 600 .env).\n"
			}
		}
		// Offer the app's own mount only when the container can make one. In a user namespace it can't,
		// so the text says why and gives only the volume.
		// In a user namespace Docker's own volume can't mount NFS or SMB either (the kernel refuses both
		// there), so the share has to be mounted outside and passed in as a folder.
		if inUserNamespace() {
			return map[string]any{
				"kind": "compose",
				"text": "# This container runs in a user namespace (for example an unprivileged Proxmox LXC), so neither the app nor Docker can mount network shares here.\n" +
					"# Mount the share on the Proxmox host, pass the folder into the LXC (pct set <id> -mp0 <host folder>,mp=/mnt/" + vol + "),\n" +
					"# then add under services: shrink-your-shit: volumes: in your compose file:\n      - " + fmt.Sprintf("%q", "/mnt/"+vol+":"+mpt),
				"removeText": fmt.Sprintf("Remove the %q line from your compose file and restart.", "/mnt/"+vol+":"+mpt),
			}
		}
		prefix := "# To let the app mount this share itself, add these lines under services: shrink-your-shit: and restart:\n" +
			"    cap_add:\n      - SYS_ADMIN\n      - DAC_READ_SEARCH\n    security_opt:\n      - \"apparmor:unconfined\"\n" +
			"# Or mount it with a Docker volume instead:\n"
		return map[string]any{
			"kind": "compose",
			// Pasted into an existing compose file, so no top-level keys: each part says where it goes.
			"text": prefix + note + "# Add under the top-level volumes: in your compose file:\n  " + vol + ":\n    driver_opts:\n" + driver +
				"\n\n# Add under services: shrink-your-shit: volumes: in your compose file:\n      - " +
				fmt.Sprintf("%q", vol+":"+mpt),
			"removeText": fmt.Sprintf("Remove the %q volume from your compose file and restart.", vol),
		}
	}
	return nil
}
