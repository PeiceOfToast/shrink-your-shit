// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import "sort"

// EncoderSpeed is how fast one encoder runs on one worker, as its last finished conversions measured it
// (F7): the median realtime multiple and the median frames per second over the samples kept, and how many
// conversions that is. FPS is 0 when no sample said it (an older remote worker reports none).
type EncoderSpeed struct {
	Worker  string
	Encoder string
	Speed   float64
	FPS     float64
	Samples int
}

// encoderSpeedScan is how many of the newest finished encodes EncoderSpeeds reads. It runs on every
// workers GET (the Overview and Jobs pages poll it every 15 s), so it must not walk the whole history;
// a pair's own newest `each` conversions are far newer than this, so the medians do not change. A var,
// so a test can set it low and see the bound.
var encoderSpeedScan = 5000

// EncoderSpeeds returns, per worker and encoder, the medians over that pair's last `each` finished
// conversions: 'done' jobs whose encode step recorded a speed. The newest conversions are the ones kept
// when a pair has more. A copy and a conversion nothing measured recorded no speed, so they are not here.
func (s *Store) EncoderSpeeds(each int) ([]EncoderSpeed, error) {
	rows, err := s.db.Query(`SELECT j.worker, st.encoder, st.speed, st.fps
FROM job_run_steps st JOIN jobs j ON j.id = st.job_id
WHERE st.name = 'encode' AND st.speed > 0 AND j.status = ?
ORDER BY j.id DESC LIMIT ?`, Done, encoderSpeedScan) // newest first, so a pair past `each` keeps its newest conversions
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type pair struct{ worker, encoder string }
	speeds := map[pair][]float64{}
	fps := map[pair][]float64{}
	var order []pair
	for rows.Next() {
		var p pair
		var speed, f float64
		if err := rows.Scan(&p.worker, &p.encoder, &speed, &f); err != nil {
			return nil, err
		}
		if len(speeds[p]) == 0 {
			order = append(order, p)
		}
		if len(speeds[p]) < each {
			speeds[p] = append(speeds[p], speed)
			if f > 0 {
				fps[p] = append(fps[p], f)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]EncoderSpeed, 0, len(order))
	for _, p := range order {
		out = append(out, EncoderSpeed{Worker: p.worker, Encoder: p.encoder,
			Speed: median(speeds[p]), FPS: median(fps[p]), Samples: len(speeds[p])})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Worker != out[j].Worker {
			return out[i].Worker < out[j].Worker
		}
		return out[i].Encoder < out[j].Encoder
	})
	return out, nil
}

// median is the middle value; with an even count, the mean of the two middle ones. 0 for no values.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	x := append([]float64(nil), xs...)
	sort.Float64s(x)
	n := len(x)
	if n%2 == 1 {
		return x[n/2]
	}
	return (x[n/2-1] + x[n/2]) / 2
}
