// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
	"github.com/PeiceOfToast/shrink-your-shit/internal/settings"
)

// keepDaysNow is processing.keepOriginalsDays as it is now: the trash days a replaced original is kept. A read that
// fails gives the store's default (7), never 0, so no line can say "trash for 0 days" by mistake.
func (s *Server) keepDaysNow(rs *Deps) int {
	doc, err := settings.Load(context.Background(), rs.DB, s.settingsOptions(rs))
	if err != nil {
		return safety.DefaultKeepDays
	}
	return doc.Processing.KeepOriginalsDays
}

// keepDaysRead is processing.keepOriginalsDays and whether it read. A read that fails says so; the caller decides what a
// line says then (the summary leaves its replace line empty, rather than name a number it does not know).
func (s *Server) keepDaysRead(rs *Deps) (int, bool) {
	doc, err := settings.Load(context.Background(), rs.DB, s.settingsOptions(rs))
	if err != nil {
		return 0, false
	}
	return doc.Processing.KeepOriginalsDays, true
}

// validateEnv is the env a flow is checked with: its words read the setting when they are written.
func (s *Server) validateEnv(rs *Deps) flow.Env {
	return flow.Env{KeepDays: func() int { return s.keepDaysNow(rs) }}
}

// checkEnv is validateEnv for a handler that may run before the server is ready: the empty env until then.
func (s *Server) checkEnv() flow.Env {
	if rs := s.ready.Load(); rs != nil {
		return s.validateEnv(rs)
	}
	return flow.Env{}
}
