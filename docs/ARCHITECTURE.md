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
| `internal/scanner` | every `-scan-every` (or at once on `ScanNow`), walks the folders of the **started** libraries (`Targets`, read from the database each pass), probes new or changed video files, asks each library's own rule, queues jobs tagged with the library id. Skips hidden files and folders (names starting with `.`), `@eaDir`, `$RECYCLE.BIN`, `System Volume Information`, files changed within `-settle`, and our own temp and output files (names whose part before the extension ends in `.syp-tmp` or `.hevc`, e.g. `a.hevc.mkv`). The **file guards** (slice 23, `scanner.Guard`, shared with the Preview) come before any probe: a link is never followed or converted (one leading outside the library is skipped as `symlink`, one inside is not looked at since its real file is converted under its own name); a file with more than one name is skipped as `shared_file`; and a file is decided only when unchanged since the previous look at least `-settle` earlier (`still_changing` in between). The first look is kept in memory with the file's identity (device and inode or the Windows file ID, size, modification and change time), so a file made again under the same name starts its wait afresh, and looks of files not seen in a whole pass are dropped. Guards are evaluated on every scan, and every file the scan sees is observed (guarded ones too), so a file that stops being shared is decided only once `-settle` has passed since it was first seen in that state. Library folders at a system folder are never scan targets. The worker checks again: it does not follow a link (Lstat, then an open with `O_NOFOLLOW`, or a reparse-point check on Windows), refuses a shared file at the start, and re-checks the link count right before the swap |
| `internal/rules` | `Rule.Match(info)`: source codec and minimum video bitrate; returns a reason string shown in the UI. `flow.go`: a flow's `Template` (kind `slider`, `legacy`, later `selectors`/`custom`, with params and a compiler version) and `Template.Settings()`, the one mapping from a flow to what the scanner and worker use (rule, CRF, preset, output; there is no minimum saving); the slider table (`Slider`, positions 1-5) lives here |
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
rule the scanner matches with and the CRF, preset and output mode the worker uses
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
the video encode (`libx265`, CRF, preset, tag; a VBV bitrate cap and a scale-down height from the
selectors), the output mode and the new file's type (`Container`, beside mode only), and the threshold (an SSIM gate, 0 = `-min-ssim`; there is no minimum saving: a checked conversion is always kept, even if not smaller, USER-REVIEW 2026-11-07). The scanner compiles a Plan
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

A **slider** flow compiles TIERS-PLAN.md 2.1's recipe, which is the Standard selectors at that
position's values (`DefaultSelectors`, one compiler: `compileSelectors`): HEVC at the position's CRF
(one lower up to 720p, one higher at 2160p), preset medium, the source's bit depth, resolution and
container, every audio track and subtitle copied, the position's SSIM gate. It
converts H.264, MPEG-2, MPEG-4, VC-1 and WMV (D5) whatever their bits per pixel (always standardize:
the "already small" skip is gone);
HEVC, AV1 and VP9 are skipped as already efficient, and so is a file whose subtitles its container
cannot hold. **HDR and Dolby Vision files are skipped (`hdr_dv`, D3) by every flow** since slice 25a,
legacy ones included where the rule would have converted them: converting drops their HDR information,
which can wash the picture out. The main video is encoded even when cover art comes first
(`VideoEncode.Index`; legacy keeps `v:0`). All the numbers are in one table, `rules.Slider`, untuned (D8).

A **selectors** flow (`internal/rules/selectors.go`, slice 25a) stores the Standard choices
(UI-API-CONTRACT.md addenda 2, 9, 13, 14) and compiles them with the same recipe:
- Speed faster/normal/slower is x265 fast/medium/slow.
- Resolution keep/1080/720/480 scales down only (`scale=-2:H`, CRF by the new height). `Expect` then
  wants that picture size, and the picture comparison scales the original to it.
- maxBitrate is a VBV cap (maxrate = value, bufsize = 2x).
- File type keep/mkv/mp4. Beside: `name.hevc.mkv`. Replace (25a-2): the new file takes the original's name
  with the new extension (`movie.mp4` becomes `movie.mkv`; an extension that differs only in case is no
  change), through the journaled swap below, and the setting answers a warning that files are renamed
  (other apps may lose track of them). Either way an existing file at the new name is never replaced (the
  job fails with a plain reason, before converting and again at the rename). A track the new type cannot
  hold skips the file with a reason (`cannot_keep_track`).
- Ignore is a list of folders, files or patterns inside the library that the scanner and the
  Preview do not look at.
- Skip HDR (on by default; off converts HDR and the setting answers a warning). There is no minimum
  saving and no Skip small ("always standardize", USER-REVIEW 2026-11-07): GET no longer returns
  `minSavingPct` or `skipSmall`; a PUT that sends them is accepted and they are ignored (for one
  release). Migration 13 queued again, once, the jobs kept only for saving too little (matched by their
  two sentences: a kept job stores no reason code) and forgot the `already_efficient`,
  `unknown_bitrate` and `would_not_shrink` file decisions, so the next scan queues those files. A legacy
  flow (command-line flags) keeps its explicit `-min-bitrate`.
- AV1 is 400 `not_available`, nothing written.
- A selectors setting equal to a position is saved as the slider (kind derived, addendum 6).
- **Audio and subtitles** (slice 25b, `internal/rules/audio.go`), as `Plan.Tracks` (one op per track
  dropped, converted or saved as a file; the rest copied):
  - Audio `my_languages` keeps tracks in the person's languages and untagged ones. If none would be
    left, all are kept (no choice drops all audio). With no languages chosen nothing is dropped.
  - Audio `aac`, `eac3` and `opus` convert at a fixed bitrate per channel (AAC 64k, Opus 48k, E-AC-3 96k,
    capped 640k). A track is never re-encoded into its own codec or made bigger: a lossless track is
    always converted, a lossy one only if its bitrate is known to be higher, and a lossy one of unknown
    bitrate is copied. `lossless_only` converts only lossless tracks (TrueHD, DTS-HD MA, FLAC, ALAC,
    PCM), to E-AC-3. No downmix.
  - Subtitles `my_languages` follow the same language rule. `text_only` drops picture subtitles.
  - Subtitles `srt` save text subtitles beside the new file as `<new name>.<lang>[.forced][.n].srt`,
    written by the same ffmpeg run into the staging folder. They are checked to exist, recorded first
    (`sidecars`, migration 10: path, size, time and full SHA-256, and the new video's size and sample hash), put in
    place with `renameNoReplace` before the swap, and removed again only while still exactly ours if the
    swap does not happen. `RecoverSidecars` at start ends a crash in between: the new video in place
    keeps them, otherwise our own files go. A name
    already taken fails the job before converting. Picture subtitles stay inside. A later restore of the
    original (slice 18) leaves the .srt files.
  - `Expect` lists exactly the tracks that remain (dropped ones absent, converted ones in their new
    codec), so verify refuses anything else. The new-type check looks at what remains.
  - Languages are a program setting (`languages` on `/api/settings`, checked against the full ISO 639-2
    list from the Library of Congress (`rules/iso639.go`), stored in one form: `rules.CanonLang` maps every
    639-1 code and the 639-2/T forms, so `fra` and `fre` are one language). The store fills them into every flow it reads
    (`Template.Languages`, never stored in the flow). A selectors body with "my languages" and no
    languages gives 400 `languages_needed`, and any unknown field (e.g. `audioBitrate`) gives 400 `invalid`.

The library's **setting** (`internal/web/setting.go`) is its flow seen through the editors:
`GET /api/settings/slider`, `GET/PUT /api/libraries/{id}/setting`, `POST .../setting/replace-custom`.
GET carries the derived `kind`, the selector values for slider and selectors flows, and on every answer
`availableFormats`, `availableAudio`, `availableSubtitles`, `availableFileTypes` (per afterConverting)
and `warnings`. A PUT with only `quality` saves the slider; any selector field makes it a selectors save.
A legacy or custom flow is never overwritten (a new flow named after the library is made; the old one
stays in Flows); a flow other libraries share needs `sharedFlowChoice` (`change_both` or `make_copy`,
"<library> (own copy)"), else 409 `flow_shared` with `usedBy`. Every change forgets the affected
libraries' files and wakes the scanner. `GET /api/flows/{id}` describes slider, selectors and legacy
flows in plain words, raw numbers only in `detail`.

## Setup checks (the doctor)

`internal/doctor` is the one set of checks behind three places (FEATURE-PLAN FR-3):
`POST /api/libraries/check {path}` (first-run checks a folder as soon as it is chosen),
`GET /api/system/health` (Settings > System) and `shrink-your-shit doctor`.

- **Converter:** ffmpeg and ffprobe found (with versions), libx265, the `ssim` filter (verify needs
  it). Hardware HEVC encoders are only a note, and only those that encode a test frame here count:
  converting always uses the processor for now (slice 37).
- **Folder:** a folder chosen through a link is checked as the folder it points to (`jobs.RealPath`,
  as it is stored); it is not a system folder, `/` or a whole home folder (`jobs.SystemFolder`, blocker
  `system_folder`; the store refuses such a library too, however it would be made, moved or started); it opens; it is writable by the user the program runs as (a temp file is created and
  removed, so the check is the service's own); it holds videos (counted with the scanner's rules); its
  disk has room to convert the largest one (`worker.SpaceNeeded`, the worker's own preflight rule);
  files shared with another folder are a note saying they will be left alone (link counts on Linux,
  macOS and Windows); originals in a `.syp-trash` folder that the database does not know (kept by an earlier setup whose
  database was reset or lost: never purged, not under Kept originals) are a note per folder,
  `orphaned_originals`, with count, size and path (`doctor.OrphanedOriginals`, which slice 28's
  uninstall summary is to reuse); they are only reported, never deleted, renamed or moved; folders inside that cannot
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
`{"quality":1..5}`; Standard's selectors are accepted while they are at that position's own values
(addendum 6: such a setting is the slider position), any other choice is 400 `not_available` until
slice 25. It lists the folder like the scanner (same
skipped folders and files), probes each video through the probe cache (`probes` table, migration 5:
ffprobe's answer per path while size and modification time stay the same; the scanner fills it too)
and compiles the setting's Plan for it, so the counts come from the scanner's own decision.
`skipped` counts the Plan's `SkipCode` (`already_hevc`, `format_not_converted`, `no_video`,
`cannot_keep_track`, `setting_cannot_run`, `already_efficient` for a legacy flow's `-min-bitrate` only,
`unreadable`). Files with more than one link stay in `canShrink`, are counted in `sharedNoSaving`
and are left out of the range (until slice 12 skips them as `shared_file`).

The range (v1, orch-lead's ruling 2026-11-07): per converted file, its video bytes times its
position's band in `rules.Slider` (`SaveLowPct`-`SaveHighPct`: Smallest 15-60%, Smaller 12-57%,
Balanced 10-55%, Better quality 8-40%, Best quality 5-25%; a legacy flow uses the position with the
nearest CRF). The bands follow the published reports in TIERS-PLAN.md and are not measured on real
files yet (D8). Audio, subtitles and the container are copied, so they never count. A source within
1.5x of its position's old eligibility threshold (`rules.NearMinBPP`, kept for the estimate only; a
legacy flow: its bitrate) counts
from 0 at the low end (`preview.Saving`); there is no minimum saving, so such a file is converted all the
same and counted. Content like film grain or noise can need more data in the new format than
before; the range overshoots there, and a short sample encode is the planned fix. The result is kept in memory per
library and setting with a signature of the listing: the same setting over the same files answers at
once; a change starts a new computation, shown as `stale` with `status: computing` until it is ready.
A request waits up to 2 s, then answers `computing` with `progress {done, total}` and the page asks
again. Probes run as many at a time as there are CPUs (at least 2), through the same cache as the
scanner, so Start does not probe the files again.
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
   chapter count); duration within 2%; video frame count within 1%. There is **no size check**: a new file that
   is not smaller is kept all the same ("always standardize"); its before and after sizes are on the
   job and in the log. Then the **playback test** (`-decode-check auto|always`; there is no "off":
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
   at most 6 hours); a stuck call ends as `kept` with "took too long". These are guesses.
   **Fail-safe:** a check that errors or cannot run (no `ssim` filter in this ffmpeg, free space
   unreadable) is a refusal, never a pass.
4. Replace: the temp file is renamed over the original (retried for ~10 s on Windows, where
   another program can hold the file open). With `-output beside` it becomes `name.hevc.<ext>`
   and the original stays.
5. Any refusal or failure removes the temp file and leaves the original untouched: `kept` for a
   refused result, `failed` for a crash (the UI offers Retry; neither is retried automatically).
   The job's detail, which the page shows, is plain words written where the failure happens
   (`failed(plain, err)` and `kept(...)` in `internal/worker`; `plainFailure` covers a full disk, a
   permission refusal and anything else). It names no paths, tools or raw errors (COPY-1), and the raw
   error goes to the log, which Settings > System shows (`TestFailedDetailsArePlain`).

`Expect` (`worker/expect.go`) is "what the new file must look like": a list of stream expectations
in output order, chapters, duration, frame count. `DefaultExpect(source)` builds it
for what the worker does today (keep everything except data streams, first video stream becomes
HEVC at the same size and depth). Later work that changes what the worker does (other codecs, dropped
tracks) should fill `Expect` differently and leave `verify` alone.

Shutdown: on Ctrl+C/SIGTERM the running job is released back to `queued`, and `main` waits for
the worker before closing the database. On startup, jobs left `running` by a crash are
requeued, after any interrupted swap is ended (below).

## Keeping originals (slice 17)

In replace mode with `keepOriginalsDays` > 0 (default 7) the swap is (`internal/worker/keep.go`):
1. make `.syp-trash` beside the file (cannot: the job is `kept`, nothing moved); record the
   original's sampled hash (SHA-256 of its size and three 1 MiB samples: start, middle, end; cheap
   on a 50 GB file, and what slice 18 checks a restore against);
2. **journal**: an `originals` row (migration 6) with the paths, sizes, hash and state `moving` is
   committed before anything is renamed;
3. the original is renamed to `.syp-trash/<name>.<job id><ext>` (`.2`, `.3`... if taken; never
   overwritten), only if it is still the file that was read;
4. the checked new file is renamed to the original's name (if that fails, the original is renamed
   back);
5. the row becomes `kept` with `delete_on` = now + the setting; the job is done.

`worker.RecoverSwaps` runs at startup before the requeue: for each row still `moving`, if the original
(by size and hash) is at its name, nothing moved (rolled back, the job runs again); if it is in the
trash and the new file at its name, the swap is finished and the job marked done; if it is in the
trash and nothing at its name, it is put back. Anything else is left alone and marked
`needs_attention`. Jobs still `running` whose row is `kept` are marked done, so a crash between the
swap and the job record never converts the new file again. 0 days: one rename, as before.

**A new file type in replace mode (25a-2):** the row also records `new_path` (migration 11; `''` for
the same name), the name the new file takes. The same steps run with the new file going to `new_path`
without ever replacing a file there (`renameNoReplace`; taken: the original goes back and the job fails
with design's sentence). Recovery and restore look for the converted file at `new_path` and put the
original back at `path`, never over a file that took that name meanwhile (then `needs_attention`). A
change of name is always journaled; with 0 days the kept original is deleted right after the swap
(state `purged`). Done and Kept items carry `newPath`; "Convert again" on a record of a name that a
conversion renamed away answers 409 `renamed`.
`GET /api/activity/done` says per job whether the original is `kept` (with `keptId`, `keptDaysLeft`)
or `deleted`; `GET /api/activity/kept` lists the kept ones. `freedBytes` counts originals already
gone, `waitingBytes` those still kept (contract F1). `.syp-trash` is hidden, so the scanner, the Preview
and the setup checks skip it.

**Getting an original back (slice 18, `internal/worker/restore.go`):** `POST /api/kept/{keptId}/restore`
(the page's "Get original back") or `shrink-your-shit restore [--force] <kept id | path>`, which asks the
running service (`--force` only on the command line, on the database directly, holding the data folder's lock
`<data>/syp.lock` for the whole restore; the service holds that lock for its whole life (flock on
Linux and macOS, LockFileEx on Windows), so a forced restore never runs beside it and a second service
on the same data folder refuses to start; the API never forces). The forced restore uses the installed
service's data folder, found the same way as the token command (`installedDataDir`: SYP_DATA, the Linux
settings file, the macOS LaunchAgent, the Windows launcher), or `--data <folder>` for a service started
with its own `-data`; with neither, or with no database there, it refuses instead of guessing (a
different folder's lock would not stop the service). Refused, with nothing moved, if the kept original is missing or not the recorded file
(size + sampled hash), or if the converted file changed since it was made (size, modification time and
sampled hash recorded at the swap, migration 7; `--force` on the command line only). Otherwise, on the
same journal row: state `restoring` with a free name for the converted file allocated and recorded
(migration 8; never a fixed name), the converted file checked again and renamed there, the original
renamed back, the converted file deleted only if it is still the recorded one (a forced restore of an
edited file keeps it there). Every rename refuses to replace a file (`renameNoReplace`: renameat2
RENAME_NOREPLACE on Linux, MoveFileEx without replace on Windows, else link then unlink): a file that
appears at the name meanwhile stops the restore with nothing deleted. Then the file record's decision set to `restored` (the
scanner and the Preview skip it until "Convert again" clears it), state `restored`, and the empty
`.syp-trash` removed. `RecoverSwaps` finishes a `restoring` row at startup: the original back finishes
it, the converted file still in place means nothing moved (kept), an empty name puts the original
back. The answer says "identity confirmed" when size, time and samples match: a sampled hash cannot
prove every byte.

**Purge:** at startup and every hour, kept originals whose `kept_at` + `keepOriginalsDays` (the setting
now, so lowering it applies at the next purge; the lists show the same date) has passed are deleted,
only if the file at the recorded trash path is still the kept original: size, modification time and
sampled hash (otherwise it is left alone and the row marked `needs_attention`); the row becomes `purged`, its saving counts as freed, and an empty
`.syp-trash` is removed. Nothing else in `.syp-trash` is ever deleted. Library records do not matter.

## Activity (slice 21b)

`internal/web/activity.go`, read-only over the store (`internal/jobs/activity.go`), all lists
`{items, total}` with `limit`/`offset` (default 50):
- `GET /api/activity/summary`: `freedBytes` (original minus new over done jobs whose original is
  gone) and `waitingBytes` (the same while it is kept in `.syp-trash`), running and queued counts, and
  `counts` of skipped files and of failed or kept jobs. The only source of savings numbers (F1).
- `GET /api/activity/queue`: running jobs with `percent` (the worker's progress), `phase` (`checking`
  once the new file is written), `etaSeconds` from the time since the job started (`jobs.started`,
  migration 7) and the share done, or null; `upNext` counts queued jobs per library with the
  `queueOrder` setting.
- `GET /api/activity/failed`: failed jobs and jobs a check kept (`kept`), with a plain title and the
  reason; Retry is `POST /api/jobs/{id}/retry`.
- `GET /api/activity/skipped?reason=`: file records the scanner skipped, with the reason as a code
  (`files.reason`, migration 7: the Plan's `SkipCode`, `unreadable`, or slice 18's `restored`) and its
  plain words (`rules.SkipWords`). `POST /api/skipped/{id}/convert-again` forgets the record and wakes
  the scanner, which looks at the file afresh (and may skip it again for the same reason).
- `POST /api/system/backup` answers 501 `not_available` until slice 38.

## Workers: nothing converts until one is started (S2, `internal/web/workers.go`)

USER-REVIEW 2026-11-07: "i dont want it to just start before the user wants". Today there is one worker,
this computer's (`id` `this`): the worker pool below. Started or stopped is the `paused` flag, which
`Claim` honours in the same statement, so no job is taken while it is stopped; scanning and the Preview go
on. Migration 14 stops it on every install, new or upgraded; an upgraded install whose worker was running
(and that had libraries or jobs) gets a one-time notice (`stopped_by_update`, `stoppedByUpdate` in the API)
that starting a worker or `POST /api/workers/notice/dismiss` ends for good, together with
`basic_changed_by_update` (`basicFlowsChanged`, which S4's migration sets). `GET /api/workers` gives
`{workers: [{id, name, host, state stopped|idle|converting|stopping (quiet_hours, unreachable: later),
filesAtOnce, converting [{jobId, path, percent, etaSeconds}], quietUntil (null until quiet hours), gpu}],
stoppedByUpdate, basicFlowsChanged}`; `gpu` is doctor's hardware test, `{found, vendor}` (vendor NVIDIA,
AMD or Intel when the working encoder names one, else null), or null when the checks are not available.
`POST /api/workers/{id}/start`; `POST /api/workers/{id}/stop` with `when` `after_current` (default: the
flag only, running files finish, state `stopping`) or `now` (also `PauseNow`: they go back to the queue,
originals untouched); `PATCH /api/workers/{id}` with `filesAtOnce` (1-8, stored as `parallelJobs`, which
the pool follows). Activity's Pause/Resume stay as aliases (Resume also ends the notice) until the page
moves; `GET /api/activity/summary` adds `workersRunning` (bool).

## Pause, stop and the stall watchdog (slice 24)

- **Workers:** `worker.Pool` runs as many workers as the `parallelJobs` setting (1 by default, at most
  8), reading it every 2 s: a new worker starts at once; one too many finishes its current file first.
  Workers still finishing a file after the setting went down are taken back first when it goes up
  again, so no more files convert at once than the setting (and 8) allows.
  `Store.Claim` is one statement, so two workers never take the same job; it takes none while paused
  and none whose stop was requested. A worker claims and registers the job as its current one under its
  own lock, so a pause or a stop that looks for the job waits for that and finds it.
- **Pause all** (`POST /api/queue/pause`): the `paused` flag in `app_meta` (survives a restart) makes
  workers take no job, and every running conversion is stopped through the job's cancel path and put
  back in the queue (original untouched, staging folder removed). A worker checks the flag again after
  its claim, before converting starts. Scanning goes on. `POST
  /api/queue/resume` clears it. `GET /api/activity/summary` `paused` is the flag.
- **Stop this file** (`POST /api/jobs/{id}/stop`, `Store.RequestStop`, one transaction): a queued job
  ends at once; a running one is only marked (`jobs.stop_requested`, migration 9) and its worker is
  stopped the same way as a pause. The worker ends it, and it checks the mark again right before the
  swap, so a stop that reached no worker still prevents the swap. Either way the job record goes and
  the file is recorded as skipped with reason `user_stopped`, which the scanner honours (like
  `restored`) until Convert again. The answer says what happened: 200 once it has ended as stopped,
  409 `already_finished` if it was already being put in place and ended done, and 202
  `{"status":"stopping"}` if it has not ended within 30 s. A finished job: 409 `not_stoppable`. A marked job left by a stopped
  program ends as stopped at the next start.
- **Removing a library** (`DELETE /api/libraries/{id}`): `Store.BeginRemove` marks it, in memory only,
  as being removed: no job of it is claimed and it cannot be started. Nothing is written, so nothing needs
  undoing or recovering after a crash. Its running jobs are put back in the queue the way a pause does
  (`RequeueJob`: original untouched). `DeleteLibrary` removes the library and its waiting jobs in one
  transaction, and only when none of its jobs runs (else `ErrLibraryBusy`); the handler retries for at
  most 5 s. A conversion already inside its swap finishes first, so the answer comes after it; with
  retention > 0 its original is kept and restorable. Any other outcome (timeout: 409 `busy`; a store
  error) leaves everything as it was, and the in-memory mark is released on every exit path.
- **Swap window:** once the new file has passed its checks the rest of the job runs with a context
  that cannot be cancelled, so a stop or pause arriving then lets the swap finish and the job ends
  done, never half swapped.
- **Stall watchdog:** during the encode, a job whose progress has not moved for `-stall-after` (default
  10 minutes) is stopped and fails: "Stopped: no progress for 10m0s. The original is untouched; press
  Retry to try again." The checks after the encode keep their own time limits (slice 10).

## Failures that may pass (DD-2, `internal/worker/retry.go`)

Some failures pass on their own: a check that timed out or crashed, a full disk (while converting, or
too little free space to start), free space that could not be checked, ffmpeg stopped by the system (a
signal: out of memory), the original changing while it converts (still downloading), the new file or a
subtitle file not movable (busy, locked), an old temp file that is busy (not a permission error), the
database busy. Those sites wrap their usual `FailedError`/`KeptError` in `retryable(err, kind)`; the
plain words stay. `RunOne` puts such a job back in the queue (`RetryLater`: `attempts`+1, `retry_at`,
the reason kept on it) while it has tries left: 3 tries in all, waits of 5 then 30 minutes, or 30
minutes then 2 hours for disk space. `Claim` takes no job before its `retry_at` (migration 12), so the
wait survives a restart. Each try starts from the beginning: the original is read again, nothing of
the last try is reused, and the original is never touched. The last try ends Failed or Kept as before,
with `attempts` recorded. Everything else ends at once (name collisions, refused results, damaged or
unreadable or missing originals, a library gone). The stall watchdog, "Stop this file", pause and
shutdown are never retried. The person's Retry, and Convert again (a new job), start with no tries
counted. `GET /api/activity/failed` items carry `attempts`; `GET /api/activity/queue` lists the
waiting jobs in `retrying` (`jobId`, `library`, `path`, `retryAt`, `attempts`, `detail`), and they
still count in `upNext`.

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
