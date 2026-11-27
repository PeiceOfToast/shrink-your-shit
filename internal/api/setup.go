// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/system"
)

// setupBody is page 13's one call: the password, an optional first library, and the starting
// workflow the person picked.
type setupBody struct {
	Account struct {
		Password string `json:"password"`
	} `json:"account"`
	Library *struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		ShareID string `json:"shareId"`
	} `json:"library"`
	Starter *struct {
		ID      string         `json:"id"`
		Answers map[string]any `json:"answers"`
	} `json:"starter"`
}

// postSetup does page 13 in one all-or-nothing transaction: the password and its access token, the
// workflow made from the chosen starter, and the first library with that workflow. A failure in any
// of them leaves no account, no workflow and no library. After the commit the browser is signed in,
// and the library's first scan starts. scanStarted reports whether a scan was started.
func (s *Server) postSetup(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	var body setupBody
	if !decode(w, r, &body) {
		return
	}
	ctx := r.Context()

	if body.Starter == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Pick a starting workflow.", "starter", 0)
		return
	}
	starter, ok := flow.StarterByID(body.Starter.ID)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "That starting workflow doesn't exist.", "starter", 0)
		return
	}

	var lib *library.PreparedLibrary
	if body.Library != nil {
		if body.Library.ShareID != "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "Shares aren't available yet. Choose a folder on this computer.", "library.shareId", 0)
			return
		}
		prepared, err := rs.Catalog.PrepareLibrary(ctx, library.NewLibrary{Name: body.Library.Name, Path: body.Library.Path})
		if err != nil {
			writeSetupLibraryError(w, err)
			return
		}
		lib = &prepared
	}

	password, err := rs.Access.PreparePassword(ctx, body.Account.Password)
	switch {
	case errors.Is(err, system.ErrSetupDone):
		writeError(w, http.StatusConflict, "setup_done", "Setup is already done.", "", 0)
		return
	case errors.Is(err, system.ErrInvalidPassword):
		writeSetupPasswordError(w)
		return
	case err != nil:
		internalError(w)
		return
	}

	var libID int64
	err = rs.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := rs.Access.SetPasswordTx(ctx, tx, password); err != nil {
			return err
		}
		workflowID, _, err := rs.Workflows.CreateFromStarterTx(ctx, tx, starter.ID, starter.Name)
		if err != nil {
			return err
		}
		if lib != nil {
			lib.WorkflowID = &workflowID
			libID, err = rs.Catalog.AddLibraryTx(ctx, tx, *lib)
		}
		return err
	})
	switch {
	case errors.Is(err, system.ErrSetupDone):
		writeError(w, http.StatusConflict, "setup_done", "Setup is already done.", "", 0)
		return
	case err != nil:
		var pathErr *library.PathError
		switch {
		case errors.As(err, &pathErr), errors.Is(err, library.ErrPathAlreadyWatched):
			writeSetupLibraryError(w, err)
		case errors.Is(err, system.ErrInvalidPassword):
			writeSetupPasswordError(w)
		default:
			internalError(w)
		}
		return
	}

	cookie, err := rs.Access.CreateSession(ctx, r.UserAgent(), remoteHost(r))
	if err != nil {
		internalError(w)
		return
	}
	setSessionCookie(w, r, cookie, sessionMaxAge)
	scanStarted := libID != 0
	if scanStarted {
		if added, err := rs.Catalog.GetLibrary(ctx, libID); err == nil {
			startScan(rs, added)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"scanStarted": scanStarted})
}

// writeSetupLibraryError answers a refused first library with the field the form shows it under.
func writeSetupLibraryError(w http.ResponseWriter, err error) {
	message := "That folder can't be used. Choose another folder."
	var pathErr *library.PathError
	if errors.As(err, &pathErr) {
		message = pathErr.Error()
	} else if errors.Is(err, library.ErrPathAlreadyWatched) {
		message = "Another library already watches this folder. Choose a different folder."
	} else if errors.Is(err, library.ErrInvalidWatch) {
		message = "Pick hourly, daily or weekly."
	}
	writeError(w, http.StatusBadRequest, "invalid_request", message, "library.path", 0)
}

func writeSetupPasswordError(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "invalid_password", "A password needs at least 8 characters and at most 72 bytes.", "account.password", 0)
}

// startScan runs one library's scan off the request and wakes the worker when it has queued what it
// found. A scan can take minutes; the answer does not wait for it.
func startScan(rs *Deps, lib library.Library) {
	if rs.Scanner == nil {
		if rs.Wake != nil {
			go rs.Wake()
		}
		return
	}
	go func() {
		_, _ = rs.Scanner.Scan(context.Background(), lib)
		if rs.Wake != nil {
			rs.Wake()
		}
	}()
}
