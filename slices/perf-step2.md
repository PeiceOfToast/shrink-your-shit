# Overview step 2 build: stale-while-revalidate (row 85ec9d3d)

Branch perf/overview-background from origin/dev ad6645f. No schema change, no migration, same SQL.
Numbers from this VM (slow: ffprobe segfaults here). Store: 200k jobs seeded by TestSeed200k, scratch, deleted after measuring.

## What changed

- `internal/web/swrcache.go` (new): generic `swrCache[T]`. Serves the last value while a live page's key has changed
  and the last compute was slow (>= 250 ms). One background compute at a time (single-flight), at most one queued.
  Minimum gap between background computes: max(10 s, 2 x last compute). Errors are logged; the last value stays.
- `internal/web/v2overview.go`: `statsCache` (overview sums) and `libraryTotals` (Libraries and Dashboard per-library
  totals, on the same request path) use it. Warm computes at server start. A live page is one asked for in the last 2 min.
- `internal/jobs/jobs.go`, `overview.go`: a second read-only handle `ro` (query_only, same WAL file) for overview reads.
  Without it, a slow overview read holds the single writer connection and stalls job progress writes (see below).
- `internal/web/swrcache_test.go` (new): four tests (stale value during a slow recompute, coalescing of key changes,
  first call fresh, error keeps the last value).

## Measured (PERF_STORE 200k, this VM)

Overview request, miss with a snapshot present (key changed, page live):
- 62.5, 77.4, 107.3, 88.2 ms over four events. Target was < 100 ms: three of four pass, one (107 ms) does not.
  The request is served from the old snapshot; the cost is the JSON and the cache check, not the SQL.
- Cold first request with no snapshot: 5715.9 ms (computed in the request while warm runs in the background).

Compute time (the lag after an event, before the new numbers are served):
- `takeStats` (TotalsOf, Days, Averages, DoneCount): 8059–8967 ms.
- `libraryTotalsOf` (all libraries): 1853–3255 ms.
- Lag bound after an event: the wait is max(10 s, 2 x last compute) from the last background start, then ~8–12 s of compute.
  On this VM that is roughly 20–30 s before the new numbers appear. Before this change, a live page waited for the
  compute in its own request (about 9 s per request).

Write latency during an overview loop (from the earlier measurement, same store):
- Without the `ro` handle: 65 progress writes in 20 s, p50 164.7 ms, p99 1651 ms, max 1655 ms.
- With the `ro` handle: 3769 writes, p50 0.08 ms, p99 0.19 ms, max 1.17 ms. Idle p50 0.19 ms.
- So a background compute does not block job progress writes at 200k.

## Tests and checks

- `go test ./internal/web/ ./internal/jobs/ -count=1`: both exit 0 (web 161 s, jobs 96 s).
- `TestOverviewNumbersMatchGolden`: PASS with the golden file from wt-perf (not on origin/dev, so it is not committed here).
- `-race`: NOT RUN. CGO_ENABLED=0 on this VM and there is no gcc, so the race detector is unavailable.

## Decisions and deviations

- The key includes the minute (`now.Unix()/60`), so a live page recomputes at most once a minute even with no events.
  This keeps purges and scans visible, which have no event. It is a deviation from "data changed" only in that sense.
- Small stores: a compute faster than 250 ms is done in the request, so a small store is never stale and tests stay exact.
- The overview's reads are not one snapshot transaction (same as before). A read transaction on `ro` would be a follow-up.
- Live pages show the value from before the last event for up to the lag bound above. The page refetches on its own timers,
  so the final numbers arrive within that bound.
