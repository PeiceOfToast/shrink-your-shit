# Architecture

True for: `main` after PR #4. It is a map, not the territory: read the code before relying
on a detail here.

One Go program (`main.go`) wires together a scanner, a SQLite-backed job queue, a worker
that runs ffmpeg, and a small web server. Everything runs in one process.

```
library folders --scanner--> files table + jobs table (SQLite) <--worker (pulls jobs, runs ffmpeg)
                                      ^
                                      +-- web server (status page, /api/status, retry)
```

## Packages

| Path | Job |
|---|---|
| `main.go`, `platform.go` | flags and env, finding ffmpeg/ffprobe, default data dir, starts the scanner, worker and web server, handles Ctrl+C/SIGTERM |
| `internal/scanner` | walks library folders every `-scan-every`, probes new or changed video files, asks the rule, queues jobs. Skips hidden files and folders (names starting with `.`), `@eaDir`, `$RECYCLE.BIN`, `System Volume Information`, files changed within `-settle`, and our own temp and output files (names whose part before the extension ends in `.syp-tmp` or `.hevc`, e.g. `a.hevc.mkv`) |
| `internal/rules` | `Rule.Match(info)`: source codec equals `-from-codec` and video bitrate at least `-min-bitrate`; returns a reason string shown in the UI. Only one rule exists today |
| `internal/media` | wraps ffprobe and ffmpeg behind the `Exec` interface (`OSExec` is real, `FakeExec` is for tests); parses probe JSON into `Info` (container, duration, bitrate, plus a per-stream `Streams` list: codec, language, flags, video size/fps/bit depth/HDR and Dolby Vision markers, audio channels, subtitle text-vs-bitmap, attachments, chapter count; helpers `Video()`, `Audio()`, `IsHDR()`, `HasBitmapSubs()`...); builds the ffmpeg arguments (`HEVCArgs`); parses `-progress` output |
| `internal/jobs` | SQLite store (pure-Go `modernc.org/sqlite`, no cgo): `files` (what the scanner last saw) and `jobs` tables, and the job state machine |
| `internal/worker` | `Worker` pulls jobs through the `Queue` interface and runs the transcode. The built-in worker is one instance; remote workers would implement `Queue` over HTTP |
| `internal/web` | embedded static page (`go:embed`, plain HTML/JS, no build step) and a JSON API |

## Job lifecycle

States: `queued -> running -> done | failed`. Legal moves are in `internal/jobs/jobs.go`
(`running -> queued` is allowed for shutdown, `failed -> queued` for retry).
`Store.Claim` picks the oldest queued job; `MaxOpenConns(1)` keeps claims race-free.

1. Scanner probes a file; if the rule matches it enqueues a job (never a second one while one
   is open for the same path). Unchanged files (same size and mtime) are not re-probed.
2. Worker claims the job and runs ffmpeg into `name.syp-tmp.<ext>` next to the original, same
   container as the input. First video stream to HEVC; everything else copied except data
   streams.
3. Safety checks before anything is replaced (`worker.process`/`verify`): ffmpeg succeeded; the
   output is non-empty and probes as a readable video; its duration is within 2% of the
   source; it is **smaller** than the original; the source file did not change while encoding.
4. Replace: the temp file is renamed over the original (retried for ~10 s on Windows, where
   another program can hold the file open). With `-output beside` it becomes `name.hevc.<ext>`
   and the original stays.
5. Any failure removes the temp file, leaves the original untouched and marks the job `failed`
   (the UI offers Retry; failed jobs are never retried automatically).

Shutdown: on Ctrl+C/SIGTERM the running job is released back to `queued`, and `main` waits for
the worker before closing the database. On startup, jobs left `running` by a crash are
requeued.

## Database schema and migrations

The schema version is SQLite's `PRAGMA user_version`. `internal/jobs/migrate.go` holds an ordered list
of numbered steps; on every open the program runs the steps newer than the database's version, all in
one transaction (a failing step rolls everything back and the program refuses to start). Before
upgrading an existing database it saves a copy next to it as `syp.db.bak-v<old version>` (newest 3
kept). The backup needs free disk space about the size of the database; if it cannot be made
(for instance a full disk) the upgrade does not happen and the program says so. A database from a newer program is refused with "this database is from a newer
shrink-your-shit; upgrade the program" and is not touched. Databases created before migrations
existed (version 0, tables already there) are adopted as version 1.

**To change the schema:** add a step at the END of `migrations` with the next number and write it so
it only needs to run once (`ALTER TABLE ... ADD COLUMN`, `CREATE TABLE`). Never edit or reorder a step
that has been released. Add a test that opens a database at the previous version (see
`migrate_test.go`, which builds the pre-migration schema by hand) and checks the data survives.

## Configuration surface

Flags (see `go run . -h`) with environment equivalents:

| Setting | Flag | Env | Default |
|---|---|---|---|
| Folders to watch | `-library` (repeatable) | `SYP_LIBRARIES` (`:` separated; `;` on Windows) | none (required) |
| Database folder | `-data` | `SYP_DATA` | user config dir + `shrink-your-shit` |
| Web address | `-addr` | `SYP_ADDR` | `127.0.0.1:8484` |
| ffmpeg / ffprobe | `-ffmpeg` / `-ffprobe` | `SYP_FFMPEG` / `SYP_FFPROBE` | PATH, then next to the program |
| Rule and quality | `-from-codec`, `-min-bitrate`, `-crf`, `-preset` | | `h264`, 4000, 26, `medium` |
| Output | `-output replace\|beside` | | `replace` |
| Timing | `-scan-every`, `-settle` | | 30s, 15s |

The web UI has **no authentication**; binding to anything but loopback logs a warning.

## Where to add things

- **A new rule or codec target:** `internal/rules` (matching) and `internal/media.HEVCArgs`
  (the ffmpeg arguments). The worker currently always targets HEVC.
- **A remote worker:** implement `worker.Queue` over HTTP and add endpoints in `internal/web`;
  keep `internal/jobs` as the single owner of state transitions.
- **A new setting:** add the flag in `main.go` (plus env fallback in `platform.go` if useful) and
  document it in the README table.
- **Anything that runs ffmpeg:** go through `media.Exec` so it stays testable without ffmpeg.
- **A new OS-specific behaviour:** prefer a runtime check or a small injectable function
  (see `renameRetry`) over build tags, so it is testable on Linux.
