// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/PeiceOfToast/shrink-your-shit/internal/share"
)

// The shares group (page 09's Add: Test the connection, Save, Try again, Remove). The password
// goes in and never comes back: a share answers hasPassword only. The JSON shapes live here alone,
// so the UI contract is one file to change.

// shareRow is page 09's Share. Its id is a decimal string, as every id in types.ts is.
type shareRow struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Address     string  `json:"address"`
	Username    string  `json:"username"`
	Guest       bool    `json:"guest"`
	HasPassword bool    `json:"hasPassword"`
	Connected   bool    `json:"connected"`
	Point       string  `json:"point,omitempty"`
	Since       *string `json:"since,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

// sharesRow is GET /shares: the list and how this computer mounts shares.
type sharesRow struct {
	Mode       string     `json:"mode"`
	ModeReason string     `json:"modeReason,omitempty"`
	Items      []shareRow `json:"items"`
}

// shareTestRow is Test the connection's answer: {ok: true}, or {ok: false, reason, message}.
type shareTestRow struct {
	OK      bool   `json:"ok"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// shareInput is Add's form and Test's. Type is optional: the address decides it.
type shareInput struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Address  string `json:"address"`
	Username string `json:"username"`
	Domain   string `json:"domain"`
	Password string `json:"password"`
	Guest    bool   `json:"guest"`
}

func (in shareInput) input() share.Input {
	return share.Input{
		Name: in.Name, Type: in.Type, Address: in.Address, Username: in.Username,
		Domain: in.Domain, Password: in.Password, Guest: in.Guest,
	}
}

func shareRowOf(sh share.Share) shareRow {
	row := shareRow{
		ID:          strconv.FormatInt(sh.ID, 10),
		Name:        sh.Name,
		Type:        string(sh.Type),
		Address:     share.Address{Type: sh.Type, Source: sh.Address, Sub: sh.Sub}.String(),
		Username:    sh.Username,
		Guest:       sh.Guest,
		HasPassword: sh.HasPassword,
		Connected:   sh.Connected,
		Point:       sh.Point,
		Reason:      sh.Reason,
	}
	if sh.Since != nil {
		at := rfc3339(*sh.Since)
		row.Since = &at
	}
	return row
}

// shares is the service, or nil after writing a 500 for a server built without one.
func (s *Server) shares(w http.ResponseWriter) *share.Service {
	rs := s.ready.Load()
	if rs.Shares == nil {
		internalError(w)
		return nil
	}
	return rs.Shares
}

func (s *Server) getShares(w http.ResponseWriter, r *http.Request) {
	svc := s.shares(w)
	if svc == nil {
		return
	}
	list, err := svc.List(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	out := sharesRow{Mode: svc.Mode(), ModeReason: svc.ModeReason(), Items: []shareRow{}}
	for _, sh := range list {
		out.Items = append(out.Items, shareRowOf(sh))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) postShare(w http.ResponseWriter, r *http.Request) {
	svc := s.shares(w)
	if svc == nil {
		return
	}
	var body shareInput
	if !decode(w, r, &body) {
		return
	}
	sh, err := svc.Create(r.Context(), body.input())
	if err != nil {
		writeShareError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, shareRowOf(sh))
}

func (s *Server) testShare(w http.ResponseWriter, r *http.Request) {
	svc := s.shares(w)
	if svc == nil {
		return
	}
	var body shareInput
	if !decode(w, r, &body) {
		return
	}
	result, err := svc.Test(r.Context(), body.input())
	if err != nil {
		writeShareError(w, err)
		return
	}
	out := shareTestRow{OK: result.OK}
	if !result.OK {
		out.Reason, out.Message = result.Reason, result.Message
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) reconnectShare(w http.ResponseWriter, r *http.Request) {
	svc := s.shares(w)
	if svc == nil {
		return
	}
	id, ok := libraryID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That share doesn't exist.", "", 0)
		return
	}
	sh, err := svc.Reconnect(r.Context(), id)
	if err != nil {
		writeShareError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, shareRowOf(sh))
}

func (s *Server) deleteShare(w http.ResponseWriter, r *http.Request) {
	svc := s.shares(w)
	if svc == nil {
		return
	}
	id, ok := libraryID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "That share doesn't exist.", "", 0)
		return
	}
	if err := svc.Delete(r.Context(), id); err != nil {
		writeShareError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeShareError is the whole mapping from the shares package's errors to page 00's envelope.
func writeShareError(w http.ResponseWriter, err error) {
	var invalid *share.InvalidError
	if errors.As(err, &invalid) {
		writeError(w, http.StatusBadRequest, invalid.Code, invalid.Message, invalid.Field, 0)
		return
	}
	var mountErr *share.MountError
	if errors.As(err, &mountErr) {
		writeError(w, http.StatusConflict, "mount_failed", mountErr.Message, "", 0)
		return
	}
	switch {
	case errors.Is(err, share.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "That share doesn't exist.", "", 0)
	case errors.Is(err, share.ErrInUse):
		writeError(w, http.StatusConflict, "share_in_use", "A library uses this share. Remove the library first.", "", 0)
	default:
		internalError(w)
	}
}
