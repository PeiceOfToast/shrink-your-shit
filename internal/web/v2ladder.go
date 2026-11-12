// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// v2Ladder serves the bitrate ladder the engine uses (user review 11-12 item 1): each format's Low,
// Medium and High for each resolution, in Mbps, and where the picture starts to look rough, so the
// page shows the real numbers for the resolution chosen (480 is the "sd" row).
func v2Ladder(api *http.ServeMux) {
	api.HandleFunc("GET /api/v2/bitrate-ladder", func(w http.ResponseWriter, r *http.Request) {
		rows := []map[string]string{}
		for _, row := range rules.LadderRows {
			rows = append(rows, map[string]string{"row": row, "label": rules.RowLabel(row)})
		}
		formats := map[string]any{}
		for _, f := range []string{"hevc", "h264", "av1", "vp9"} {
			lf := f
			if _, ok := rules.Ladder[f]; !ok {
				lf = "hevc" // VP9 has no column: it uses HEVC's, as the engine does
			}
			byRow := map[string]map[string]float64{}
			for _, row := range rules.LadderRows {
				byRow[row] = map[string]float64{"low": rules.Ladder[lf]["low"][row], "medium": rules.Ladder[lf]["medium"][row],
					"high": rules.Ladder[lf]["high"][row], "rough": rules.Rough[lf][row]}
			}
			formats[f] = byRow
		}
		writeJSON(w, map[string]any{"unit": "Mbps", "rows": rows, "formats": formats,
			"resolutionRow": map[string]string{"1080": "1080", "720": "720", "480": "sd"},
			// the rough-picture warning's room for HDR or 10-bit and for 50-60 fps (not the levels)
			"roughAllowance": map[string]float64{"hdr": 1.25, "highFps": 1.4}})
	})
}
