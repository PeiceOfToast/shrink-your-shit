# Architecture

True for: `main` after PR #4. It is a map, not the territory: read the code before relying
on a detail here.

One Go program (`main.go`) wires together a scanner, a SQLite-backed job queue, a worker
that runs ffmpeg, and a small web server. Everything runs in one process.

```
library folders --scanner--> files table + jobs table (SQLite) <--worker (pulls jobs, runs ffmpeg)
                                      ^
                                      +-- web server (pages, /api behind the token, /healthz)
```

## Packages

| Path | Job |
|---|---|
| `main.go`, `platform.go` | flags and env, finding ffmpeg/ffprobe, default data dir, starts the scanner, worker and web server, handles Ctrl+C/SIGTERM |
| `internal/scanner` | every `-scan-every` (or at once on `ScanNow`), walks the folders of the **started** libraries (`Targets`, read from the database each pass), probes new or changed video files, asks each library's own rule, queues jobs tagged with the library id. Skips hidden files and folders (names starting with `.`), `@eaDir`, `$RECYCLE.BIN`, `System Volume Information`, files changed within `-settle`, and our own temp and output files (names whose part before the extension ends in `.syp-tmp` or `.hevc`, e.g. `a.hevc.mkv`) |
| `internal/rules` | `Rule.Match(info)`: source codec and minimum video bitrate; returns a reason string shown in the UI. `flow.go`: a flow's `Template` (kind `slider`, `legacy`, later `selectors`/`custom`, with params and a compiler version) and `Template.Settings()`, the one mapping from a flow to what the scanner and worker use (rule, CRF, preset, output, minimum saving); the slider table (`Slider`, positions 1-5) lives here |
| `internal/media` | wraps ffprobe and ffmpeg behind the `Exec` interface (`OSExec` is real, `FakeExec` is for tests); parses probe JSON into `Info` (container, duration, bitrate, plus a per-stream `Streams` list: codec, language, flags, video size/fps/bit depth/HDR and Dolby Vision markers, audio channels, subtitle text-vs-bitmap, attachments, chapter count; helpers `Video()`, `Audio()`, `IsHDR()`, `HasBitmapSubs()`...); parses `-progress` output |
| `internal/jobs` | SQLite store (pure-Go `modernc.org/sqlite`, no cgo): `files` (what the scanner last saw), `jobs`, `libraries` and `flows` tables, and the job state machine. `libraries.go`: library and flow records (`AddLibrary`, `EnsureLibraries` for `-library`, `DeleteLibrary`, `SettingsFor(job)`, per-library `Counts`) |
| `internal/worker` | `Worker` pulls jobs through the `Queue` interface and runs the transcode. The built-in worker is one instance; remote workers would implement `Queue` over HTTP |
| `internal/preview` | the Preview (dry run): for a library and a setting, counts, skip reasons and a saving range, plus one sample file in plain words; reads only |
| `internal/web` | embedded static page (`go:embed`, plain HTML/JS, no build step) and a JSON API |

## Libraries and flows

A **library** (`libraries` table: name, folder, flow, `started`) is a folder the program looks after.
Every library has exactly one **flow** (`flows` table: name, `template` JSON, `graph` for the later
flow engine), which says what to do with its files. Until the Plan compiler (slice 16) and the flow
engine (slice 30), a flow's behaviour is its template: `rules.Template.Settings()` turns it into the
rule the scanner matches with and the CRF, preset, output mode and minimum saving the worker uses
(`Worker.Settings`, looked up per job by `Store.SettingsFor`).

- A library added on the web page (`POST /api/libraries`) gets a slider flow at the chosen position
  and starts **not started**: nothing is scanned or queued until `POST /api/libraries/{id}/scan`.
- `-library`/`SYP_LIBRARIES` (and Docker's `/media`) add a library only when no library has that
  folder. What it gets depends on the install, decided once by migration 3 (`app_meta` `install`):
  - **fresh install** (a new database): the library is **not started** and gets a slider flow
    (Balanced) named after it; first-run shows it pre-filled and nothing is converted before Start
    (DESIGN/FIRST-RUN.md). The global `-crf`/`-preset`/... flags do not apply.
  - **upgrade** (a database that already held work): the library is started with a `legacy` flow
    holding the program's flags, so it converts as before; on the first start after the upgrade
    those flows are named `<folder> - Legacy`. Settings start with first-run done and the Standard
    control level (D13).
  Otherwise a flow is named after its library, a clash adding " 2", " 3" (flow names are unique in
  the database). A flow is never renamed automatically. Jobs made before libraries existed are
  linked to the library whose folder holds them.
- Deleting a library removes the record and its queued jobs, stops its running job (the worker
  drops it; the original is untouched) and keeps its flow and finished jobs.
- Library folders are stored as their real folder (links resolved: the scanner does not follow a link
  at the top of a tree) and may not be inside one another, whether added on the web page (409
  `path_overlaps`) or by `-library` (the folder is not added and a startup log line says why; the
  program still starts). A job is only added while its library exists and is started, checked in the
  same SQL statement, so a library removed during a scan gets no job afterwards.
- `freedBytes` counts only conversions that replaced their original (`beside` frees nothing).
- Every `/api` answer carries `X-SYP-API: 1`; ids in the API are strings.
- `GET /api/fs/dirs?path=` lists folder names (never files or sizes) for choosing a library's folder.
- Flows (`internal/web/flows.go`): list with who uses each (`usedBy`, `madeWith`), create (default
  template: slider Balanced), rename (the only way a flow is ever renamed), duplicate ("<name> (copy)"),
  delete (refused with `flow_in_use` while a library uses it). `PUT /api/libraries/{id}/flow` gives a
  library another flow, only when the `perLibraryFlow` setting is on; it forgets the scanner's
  records of that library's files and wakes the scanner, so files skipped under the old flow are
  looked at again. Import/export answer 501 until
  slice 30; `GET /api/flows/{id}` has no steps until slice 16.
- Global settings (`internal/web/settings.go`, stored as JSON in `app_meta` key `settings`):
  `GET/PATCH /api/settings`. A PATCH may hold any subset; the result is checked as a whole and saved
  only if all of it is valid. Read-only fields (`authRequired`, `trashPath`, `updateAvailable`) are
  ignored in a PATCH; `integrations` is refused until its slice. When settings are first created they
  follow the install kind (migration 3): a fresh install starts at Basic with first-run to do, even
  with libraries from `-library` or Docker's `/media`; an upgrade starts at Standard with first-run
  done (DESIGN/FIRST-RUN.md, D13). This slice stores
  `parallelJobs`, `quietHours`, `lowPriority`, `scanSchedule`, `queueOrder` and `keepOriginalsDays`;
  later slices use them.

## Plans

A **Plan** (`internal/rules/plan.go`) is what to do with one file, compiled from its library's flow
template and the file's probe: `Skip` (why not to convert it, the scanner's decision) or `Reason`,
the video encode (`libx265`, CRF, preset, tag; a bitrate cap can be added for slice 25), the output
mode, and the thresholds (minimum saving; an SSIM gate, 0 = `-min-ssim`). The scanner compiles a Plan
per file to decide; the worker compiles it again from the probe it takes, runs `Plan.FFmpegArgs`,
checks the result against `Plan.Expect` (slice 10's `Expect`, now in `internal/rules`) and stores a
`Snapshot` on the job (migration 4): the Plan (with the eligibility rule that decided it), the
Expect verification uses and the verification settings in force (SSIM threshold, playback-test
mode), plus a fingerprint of everything that can change eligibility, the encode or the verdict
(not the file's own streams or length). If the snapshot cannot be stored, the job fails before
anything is written. Whether to convert stays the
scanner's decision; the worker only refuses a flow that cannot say how. A legacy flow compiles to
exactly the arguments and decisions of the program before Plans (`TestLegacyPlanIsExactlyTodaysBehaviour`
over every probe in `internal/media/testdata`).

A **slider** flow compiles TIERS-PLAN.md 2.1's recipe (`compileSlider`): HEVC at the position's CRF
(one lower up to 720p, one higher at 2160p), preset medium, the source's bit depth, resolution and
container, every audio track and subtitle copied, the position's minimum saving and SSIM gate. It
converts H.264, MPEG-2, MPEG-4, VC-1 and WMV (D5) above the position's bits per pixel per frame;
HEVC, AV1 and VP9 are skipped as already efficient, and so is a file whose subtitles its container
cannot hold. HDR/Dolby Vision skips are slices 13/14 (`contentSkip`, one place for every template).
The main video is encoded even when cover art comes first (`VideoEncode.Index`; legacy keeps `v:0`).
All the numbers are in one table, `rules.Slider`, untuned (D8).

The library's **setting** (`internal/web/setting.go`) is its flow seen through the slider:
`GET /api/settings/slider`, `GET/PUT /api/libraries/{id}/setting`, `POST .../setting/replace-custom`.
A legacy or custom flow is never overwritten (a new flow named after the library is made; the old one
stays in Flows); a flow other libraries share needs `sharedFlowChoice` (`change_both` or `make_copy`,
"<library> (own copy)"), else 409 `flow_shared` with `usedBy`. Every change forgets the affected
libraries' files and wakes the scanner. `GET /api/flows/{id}` describes slider and legacy flows in
plain words, raw numbers only in `detail`.

## Setup checks (the doctor)

`internal/doctor` is the one set of checks behind three places (FEATURE-PLAN FR-3):
`POST /api/libraries/check {path}` (first-run checks a folder as soon as it is chosen),
`GET /api/system/health` (Settings > System) and `shrink-your-shit doctor`.

- **Converter:** ffmpeg and ffprobe found (with versions), libx265, the `ssim` filter (verify needs
  it). Hardware HEVC encoders are only a note, and only those that encode a test frame here count:
  converting always uses the processor for now (slice 37).
- **Folder:** a folder chosen through a link is checked as the folder it points to (`jobs.RealPath`,
  as it is stored); it opens; it is writable by the user the program runs as (a temp file is created and
  removed, so the check is the service's own); it holds videos (counted with the scanner's rules); its
  disk has room to convert the largest one (`worker.SpaceNeeded`, the worker's own preflight rule);
  hardlinked files are a note (link counts on Linux, macOS and Windows); folders inside that cannot
  be read are counted in a note, and free space that cannot be read is a note, never a silent pass. A read-only folder's message gives the fix for the system: the `syp`
  service user's `chown` on Linux, the container's user on Docker, Get Info on macOS, Security on Windows.
- `doctor` asks the running program for its health report (with the token, like `token`), so its
  folder checks are the service user's even under `sudo`; if nothing answers it checks the converter
  and data folder from the terminal and says which user it checked as. Exit status 1 if anything is bad.

If the converter is missing or incomplete, the program still starts so the web page and the health
report can say what is wrong, but it does not scan or convert until it is fixed and restarted.

## Preview (dry run)

`POST /api/libraries/{id}/preview {setting, sampleFile}` (`internal/preview`, UI-API-CONTRACT.md
"Preview", addenda 3, 5 and 12) says what a setting would do to a library before anything converts,
also for a library that is not started. An empty `setting` is the saved one; otherwise
`{"quality":1..5}` (the selectors come with slice 25). It lists the folder like the scanner (same
skipped folders and files), probes each video through the probe cache (`probes` table, migration 5:
ffprobe's answer per path while size and modification time stay the same; the scanner fills it too)
and compiles the setting's Plan for it, so the counts come from the scanner's own decision.
`skipped` counts the Plan's `SkipCode` (`already_efficient`, `already_hevc`,
`format_not_converted`, `no_video`, `unknown_bitrate`, `cannot_keep_track`, `setting_cannot_run`,
`unreadable`). Files with more than one link stay in `canShrink`, are counted in `sharedNoSaving`
and are left out of the range (until slice 12 skips them as `shared_file`).

The range (v1, bits per pixel): the new video gets 0.03 to 0.075 bits per pixel per frame at CRF 23,
halving every 6 CRF up, never more than the source; the rest of the file is copied and saves nothing;
a file whose best case misses the Plan's minimum saving counts as kept (0), one that may miss it
counts from 0 (`preview.Saving`). The numbers are untuned (D8). The result is kept in memory per
library and setting with a signature of the listing: the same setting over the same files answers at
once; a change starts a new computation, shown as `stale` with `status: computing` until it is ready.
A request waits up to 2 s, then answers `computing` and the page asks again. Probes run 4 at a time.
`sampleFile` must be a video inside the library folder after links are resolved (400
`outside_library` / `not_a_video`, 404 `not_found`); `sample.lines` are plain words.
`GET /api/libraries/{id}/files?q=` lists the videos whose path inside the library contains `q` (`{items:[{path,name,bytes}],total}`, `limit`/`offset`, default 50).

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
   the input, inside a private staging folder (`worker/tempfile.go`): `.name.<random>.syp-tmp/`
   next to the original, created fresh at mode 0700 (the leading dot makes the scanner skip it), with
   the file created exclusively at 0600. On Linux and macOS other users cannot reach it while it is
   written and checked; on Windows the mode is ignored and the folder inherits the media folder's
   access list (ACL), so it is only as private as that folder.
   The worker remembers the file's identity (device and inode, or Windows file ID): if that name later
   refers to anything else, the result is not used. The original's permissions are put on the file
   only after the checks, just before the rename, and the folder is removed on every path. Leftovers
   from a crash are removed first: a file under the old fixed temp name `name.syp-tmp.<ext>`, and a
   staging folder only if it holds nothing but our temp file (that file, then the empty folder;
   never a recursive delete). Anything else with a matching name is left alone and logged. If a
   removal fails, the job fails before ffmpeg starts. First video stream to HEVC; everything else copied except data streams.
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
   at least 0.90, an untuned starting value). Both files are read from 12 s before the sample
   with their real timestamps kept, put on one timeline by subtracting each file's video start time
   (an encoder delays the first frame, and without this a good file scored 0.93), and the same 10 s
   are cut out of both by time, so frames are paired by time and never by where a seek happened to
   land (in MPEG-TS, ffmpeg 7.1 starts an H.264 original at the next keyframe after a seek but the
   HEVC result exactly at it). If a sample still scores low it is re-measured with the new file one
   frame earlier and later, best counts. The source must not have changed during the encode: still the same
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
| Folders to watch | `-library` (repeatable): adds a started library with its own flow when no library has that folder; never changes or removes one | `SYP_LIBRARIES` (`:` separated; `;` on Windows) | none (libraries can be added on the web page) |
| Database folder | `-data` | `SYP_DATA` | user config dir + `shrink-your-shit` |
| Web address | `-addr` | `SYP_ADDR` | `127.0.0.1:8484` |
| ffmpeg / ffprobe | `-ffmpeg` / `-ffprobe` | `SYP_FFMPEG` / `SYP_FFPROBE` | PATH, then next to the program |
| Rule and quality, for libraries `-library` adds | `-from-codec`, `-min-bitrate`, `-crf`, `-preset` | | `h264`, 4000, 26, `medium` |
| Output, for libraries `-library` adds | `-output replace\|beside` | | `replace` |
| Timing | `-scan-every`, `-settle` | | 30s, 15s |

**Access** (`internal/web/auth.go`): one shared token. Listening beyond loopback with none stored makes
one (32 random bytes, URL-safe) in the data folder (`access-token`, mode 0600) and logs the sign-in
address once. Whenever a token is stored, every `/api/*` request except `POST /api/login` and
`/api/logout` needs it: a session cookie (`syp_session`, HttpOnly, SameSite=Strict, value an HMAC of
the token, so making a new token ends every session) or `Authorization: Bearer <token>`; otherwise
401 `{"error":{"code":"auth"}}`. On loopback with no token, `/api/*` is open. Every write
(POST/PUT/PATCH/DELETE) must be `application/json` and, unless it carries a Bearer token, pass Go's
`http.CrossOriginProtection` (Origin / Sec-Fetch-Site against Host), else 403 `csrf`; that also
applies on loopback, where another website could otherwise send writes to 127.0.0.1. Pages, `/assets/`
and `GET /healthz` (just `ok`, for health checks) are public. `shrink-your-shit token` prints the token
and sign-in address; it looks for the data folder in `SYP_DATA`, the Linux service's settings file and
`/var/lib/shrink-your-shit`, the Windows installer's folder, then the user's default.

## Where to add things

- **A new rule or codec target:** `internal/rules`: the template's `Compile` (what to convert, how) and `Plan.FFmpegArgs`
  (the ffmpeg arguments). The worker currently always targets HEVC.
- **A remote worker:** implement `worker.Queue` over HTTP and add endpoints in `internal/web`;
  keep `internal/jobs` as the single owner of state transitions.
- **A new setting:** add the flag in `main.go` (plus env fallback in `platform.go` if useful) and
  document it in the README table.
- **Anything that runs ffmpeg:** go through `media.Exec` so it stays testable without ffmpeg.
- **A new OS-specific behaviour:** prefer a runtime check or a small injectable function
  (see `renameRetry`) over build tags, so it is testable on Linux.
