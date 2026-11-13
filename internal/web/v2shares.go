// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// v2Shares is the network shares API (item D, user review 11-13). See slices/11-ui-web/D-API-SHAPE.md.
func v2Shares(api *http.ServeMux, store *jobs.Store) {
	mode, platform := detectMode()
	nfsOK := platform != "darwin"
	tested := platform != "darwin"

	api.HandleFunc("GET /api/v2/shares", func(w http.ResponseWriter, r *http.Request) {
		all, err := store.LoadShares()
		if err != nil {
			serverError(w, err)
			return
		}
		var items []map[string]any
		for _, sh := range all {
			items = append(items, shareToJSON(store, sh))
		}
		writeJSON(w, map[string]any{
			"mode": mode, "platform": platform,
			"nfsSupported": nfsOK, "testedPlatform": tested,
			"serverAddress": nil,
			"items": items,
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
		// Normalise the address
		if strings.HasPrefix(in.Address, "\\\\") || strings.HasPrefix(in.Address, "//") {
			in.Type = "smb"
		} else if strings.Contains(in.Address, ":/") {
			in.Type = "nfs"
		} else {
			apiError(w, http.StatusBadRequest, "invalid_address",
				"Address must start with \\\\ for SMB or contain :/ for NFS (e.g. server:/export).")
			return
		}
		sh := jobs.Share{Type: in.Type, Address: in.Address, Name: in.Name,
			Guest: in.Guest, Username: in.Username}
		saved, err := store.SaveShare(sh, in.Password)
		if err != nil {
			serverError(w, err)
			return
		}
		if mode == "app" {
			// TODO D2/D4: trigger mount via helper
		}
		writeJSONStatus(w, http.StatusCreated, shareToJSON(store, saved))
	})

	api.HandleFunc("PATCH /api/v2/shares/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such share.")
			return
		}
		var in struct {
			Name, Address, Type, Username, Password string
			Guest                                   bool
			_Guest                                  bool // sentinel: was it provided?
		}
		if !readJSON(w, r, &in) {
			return
		}
		if err := store.UpdateShare(id, in.Name, in.Address, in.Type, in.Username, in.Guest, in.Password); err != nil {
			serverError(w, err)
			return
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
		// Check if any library uses this share (stub: actual check in D4)
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
		// TODO D2: call the pure-Go SMB client (go-smb2) for SMB, or TCP connect for NFS
		// For now, return a placeholder result
		writeJSON(w, map[string]any{
			"ok": true, "address": in.Address,
			"folders": []string{}, "moreFolders": 0, "portOnly": in.Type == "nfs",
		})
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

// detectMode returns (mode, platform) for the current install.
func detectMode() (mode, platform string) {
	// D2/install.sh sets SYP_HELPER_MODE=app|manual|docker
	if m := os.Getenv("SYP_HELPER_MODE"); m != "" {
		return m, runtime.GOOS
	}
	// Default: detect Docker, then fall back to app or manual
	if os.Getenv("container") != "" {
		return "docker", runtime.GOOS
	}
	// Linux without the helper: manual mode
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		// Check if helper socket exists
		if _, err := os.Stat("/run/shrink-your-shit/helper.sock"); err != nil {
			return "manual", runtime.GOOS
		}
	}
	return "app", runtime.GOOS
}

// shareToJSON converts a Share to the API response map.
func shareToJSON(store *jobs.Store, sh jobs.Share) map[string]any {
	status := "waiting_setup"
	var reason any = nil
	mode, _ := detectMode()
	if mode == "app" {
		status = "not_connected"
	}
	// Look up mount status if applicable
	mounts, err := store.ShareMountsByShare(sh.ID)
	if err == nil && len(mounts) > 0 {
		m := mounts[0]                  // primary mount
		status = m.Status
		if m.Error != "" {
			reason = map[string]any{"code": "mount_failed", "message": m.Error}
		}
		if m.Status == "connected" {
			status = "connected"
			reason = nil
		}
	}
	addr := sh.Address
	// Normalise backslashes for display
	addr = strings.ReplaceAll(addr, "/", "\\")
	hasPw := sh.HasPassword
	return map[string]any{
		"id": strconv.FormatInt(sh.ID, 10),
		"name": sh.Name,
		"type": sh.Type,
		"address": addr,
		"guest": sh.Guest,
		"username": nilIfEmpty(sh.Username),
		"hasPassword": hasPw,
		"status": status,
		"since": nil,
		"reason": reason,
		"path": "/var/lib/syp/shares/" + sanitiseName(sh.Name),
		"libraries": []any{},            // TODO D4: libraries using this share
		"setup": setupForMode(store, sh),
	}
}

// sanitiseName makes a share name safe as a directory name.
func sanitiseName(name string) string {
	r := strings.ToLower(name)
	r = strings.ReplaceAll(r, " ", "-")
	var out []rune
	for _, c := range r {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	return string(out)
}

// setupForMode returns the setup object (for manual/docker modes) or nil.
func setupForMode(store *jobs.Store, sh jobs.Share) any {
	mode, _ := detectMode()
	switch mode {
	case "manual":
		return map[string]any{
			"kind": "command",
			"text": fmt.Sprintf("sudo mount -t %s -o username=%s,vers=3.1.1,soft,noexec %s /var/lib/syp/shares/%s",
				sh.Type, sh.Username, sh.Address, sanitiseName(sh.Name)),
			"removeText": fmt.Sprintf("sudo umount /var/lib/syp/shares/%s", sanitiseName(sh.Name)),
		}
	case "docker":
		return map[string]any{
			"kind": "compose",
			"text": fmt.Sprintf("  %s:\n    driver_opts:\n      type: %s\n      device: %q\n      o: credentials=/path/to/credentials-file,vers=3.1.1,soft",
				sanitiseName(sh.Name), sh.Type, sh.Address),
			"removeText": fmt.Sprintf("Remove the %q volume from your compose file and restart.", sanitiseName(sh.Name)),
		}
	}
	return nil
}