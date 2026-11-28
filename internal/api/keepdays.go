// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"

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
