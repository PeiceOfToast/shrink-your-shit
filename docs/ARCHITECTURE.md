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

States: `queued -> running -> done | failed | kept`. Legal moves are in `internal/jobs/jobs.go`
(`running -> queued` is allowed for shutdown, `failed -> queued` and `kept -> queued` for retry).
`Store.Claim` picks the oldest queued job; `MaxOpenConns(1)` keeps claims race-free. `failed` means
the work itself broke (ffmpeg crashed, the source vanished); `kept` means a safety check refused
the result and the original was deliberately left alone, with the reason in the job's `error` field
("Original kept: ..." in the UI).

1. Scanner probes a file; if the rule matches it enqueues a job (never a second one while one
   is open for the same path). Unchanged files (same size and mtime) are not re-probed.
2. Worker claims the job, probes the source again (`media.Info` with every stream), checks there is
   free disk space for the new file, and counts the source's video frames. From these it builds an
   `Expect` (below). Then it runs ffmpeg into a temp file next to the original, same container as
   the input. The temp file (`worker/tempfile.go`) has an unpredictable name
   (`name.<random>.syp-tmp.<ext>`), is created exclusively and with the original's permissions, and
   the worker remembers its identity (device and inode, or Windows file ID): if that name later
   refers to anything else, the result is not used. Leftover temp files from a crash (and the old
   fixed name `name.syp-tmp.<ext>`) are removed first; if one cannot be removed, the job fails
   before ffmpeg starts. First video stream to HEVC; everything else copied except data streams.
3. **Safety floor** (`worker/verify.go`; not removable by a flow, only the thresholds are tunable).
   The new file is compared with the `Expect`, never directly with the original. Cheap checks first:
   it exists and is not empty; it is readable; **stream parity** (same number of video, audio,
   subtitle and attachment streams, in order; same codec for copied streams, languages, channel
   counts, subtitle kinds and forced flags, attachment names, picture size, colour depth; same
   chapter count); duration within 2%; video frame count within 1%; **smaller** than the original
   (or by `MinSavings`). Then the **playback test** (`-decode-check auto|always`; there is no "off":
   the new file's video through ffmpeg's decoder with `-xerror`, completely up to 3 hours long, else
   three one-minute samples; any decode error fails), then the **picture comparison** (SSIM on three
   10-second samples at the start, middle and end; the worst must reach `-min-ssim`, default 0.95 and
   at least 0.90, an untuned starting value). The new file is sought to the sample time plus the
   difference between the two files' video start times (an encoder delays the first frame, and
   without this a good file scored 0.93), and if a sample still scores low it is re-measured one frame
   earlier and later, best counts. The source must not have changed during the encode: still the same
   file (identity), size and modification time, and on Linux and macOS the same change time (which
   catches an edit that keeps the size and puts the modification time back). That is checked before
   the expensive checks, so it is reported plainly, and again right before each rename attempt,
   together with the temp file's identity.
   **The original is checked too:** while converting, ffmpeg's error output is read, and decode errors
   in the original (corrupt data, not the harmless messages a recording that starts mid-picture-group
   prints) make the job `kept` with "the original file has errors; not converting it", so a damaged
   file is never silently replaced by a clean-looking copy of the damage.
   Every ffprobe/ffmpeg call in the checks has a time limit (1 minute for a probe, 10 minutes per
   sample, 30 minutes to count frames, 10 minutes plus twice the file's length for a full playback,
   at most 6 hours); a stuck call ends as `kept` with "timed out". These are guesses.
   **Fail-safe:** a check that errors or cannot run (no `ssim` filter in this ffmpeg, free space
   unreadable) is a refusal, never a pass.
4. Replace: the temp file is renamed over the original (retried for ~10 s on Windows, where
   another program can hold the file open). With `-output beside` it becomes `name.hevc.<ext>`
   and the original stays.
5. Any refusal or failure removes the temp file and leaves the original untouched: `kept` for a
   refused result, `failed` for a crash (the UI offers Retry; neither is retried automatically).

`Expect` (`worker/expect.go`) is "what the new file must look like": a list of stream expectations
in output order, chapters, duration, frame count, minimum savings. `DefaultExpect(source)` builds it
for what the worker does today (keep everything except data streams, first video stream becomes
HEVC at the same size and depth). Later work that changes what the worker does (other codecs, dropped
tracks) should fill `Expect` differently and leave `verify` alone.

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
