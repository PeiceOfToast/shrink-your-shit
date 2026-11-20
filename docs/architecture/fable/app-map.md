# shrink-your-shit: application map

Read against `origin/dev` at commit `294e3d88` (2026-11-20). Every box and arrow below names the
file or function that implements it, repo-relative, so a checker can open it. Line numbers are from
that commit. Anything I could not confirm from the code is marked **UNVERIFIED**. The map describes
what the code does, not what the design documents say it should do; where the two differ, the code
wins here.

Conventions: `pkg/file.go:NN` is a line; `Type.Method` is a function; "the store" is `internal/jobs.Store`,
the one SQLite-backed object every other package talks to.

---

## 1. Top level

```mermaid
flowchart LR
  subgraph Process["one Go process: main.go"]
    SC[scanner<br/>internal/scanner]
    ST[(store: SQLite<br/>internal/jobs)]
    POOL[worker pool<br/>internal/worker]
    WEB[HTTP API + embedded SPA<br/>internal/web]
    DOC[doctor: tool + encoder tests<br/>internal/doctor]
    RULES[rules: flow → Plan → ffmpeg args + Expect<br/>internal/rules]
    WF[workflow: node graph ↔ rules.Template<br/>internal/workflow]
    MEDIA[media: ffprobe/ffmpeg wrapper<br/>internal/media]
    PREV[preview: dry run<br/>internal/preview]
    NOTIFY[notify: webhook/chat<br/>internal/notify]
    INTEG[integrations: media-manager rescans<br/>internal/integrations]
    UPD[update: release check + helper client<br/>internal/update]
    REM[remote: worker mode client<br/>internal/remote]
  end
  UI[Browser SPA<br/>web/src]
  FF[ffmpeg / ffprobe]
  GPU[GPU: QSV / VAAPI / NVENC / AMF / VideoToolbox]
  FS[(library folders, .syp-trash,<br/>staging folders)]
  SHARES[(NFS / SMB mounts)]
  NETHELP[syp-network-helper (root)<br/>cmd/syp-network-helper]
  UPDHELP[syp-update-helper (root)<br/>cmd/syp-update-helper]
  RWORKER[remote worker:<br/>shrink-your-shit worker]
  EXT[webhooks, chat, media managers, release host]

  UI -- "/api/*, /api/v2/*, SSE /api/v2/events" --> WEB
  WEB --> ST
  WEB --> DOC
  WEB --> PREV
  WEB -- "ScanNow / PauseNow / StopFile" --> SC
  WEB -- "RunRemote(job)" --> POOL
  SC -- "probe cache, files, jobs" --> ST
  SC -- Template.Compile --> RULES
  SC -- Probe --> MEDIA
  POOL -- "Claim / Progress / Complete / Keep / Fail…" --> ST
  POOL -- "Compile, FFmpegArgs*, Expect" --> RULES
  POOL -- "Probe, Transcode, Capture" --> MEDIA
  POOL -- "Encoder(), GraphicsDecodes()" --> DOC
  POOL -- "Tell(job)" --> INTEG
  WF <-- "ToTemplate / FromTemplate" --> RULES
  WEB --> WF
  MEDIA --> FF
  FF --> GPU
  FF --> FS
  POOL --> FS
  SC --> FS
  FS --- SHARES
  WEB -- "unix socket" --> NETHELP
  NETHELP --> SHARES
  UPD -- "unix socket" --> UPDHELP
  UPD --> EXT
  NOTIFY --> EXT
  INTEG --> EXT
  RWORKER -- "/api/worker/v1/*" --> WEB
  RWORKER --> REM
```

**Plain words.** Everything runs in one Go process started by `main.go`. The scanner walks library
folders, probes new videos with ffprobe, compiles the library's flow into a Plan for each file and
queues a job. The worker pool claims jobs from the SQLite store, compiles the same flow again, picks an
encoder, runs ffmpeg into a private staging folder, checks the result against an `Expect` it built
before encoding, and swaps the file into place through a journaled rename. The web server serves one
single-page app and two JSON API generations (`/api` v1 and `/api/v2`), plus a separate worker API
(`/api/worker/v1`) that a copy of the same binary in worker mode talks to from another computer. Two
small root helpers exist only on Linux installs: one mounts network shares, one swaps the binary on
update. The doctor package runs real test encodes to learn which encoders work on this machine; the
worker asks it before every job.

---

## 2. Components, one line each

| Component | What it owns | Entry points |
|---|---|---|
| `main.go`, `platform.go` | flags and env, data folder lock, store open with migration/backup, crash recovery (swaps, staging, sidecars, requeue), starts scanner, pool, web, purge, update check, lease sweeper | `main.go:49-430` |
| `workercmd.go`, `joinedworker.go` | `shrink-your-shit worker`: pair/join a server, run the remote work loop; a server whose role becomes "worker" runs the same loop in-process | `workerCommand`, `joinedWorker.SetRole` |
| `token.go`, `doctor.go`, `restore.go`, `announce.go`, `datalock.go` | CLI subcommands (`token`, `doctor`, `restore`), mDNS announce, the `syp.lock` file lock | |
| `internal/scanner` | walks started libraries each interval, file guards (symlink, hard link, still-changing two-look), probe cache, Plan compile for the skip decision, enqueue; marks left originals/outputs gone or back | `Scanner.ScanOnce`, `scanFile`, `Guard`, `Probe` |
| `internal/rules` | the Plan compiler (slider/selectors/legacy/branches → `Plan`), the ffmpeg argument renderer for CPU and four GPU families, the `Expect` contract and its `Compare`, audio/subtitle track planning, bitrate ladder, skip words, language canon | `Template.Compile`, `Plan.FFmpegArgs*`, `Plan.Expect`, `Expect.Compare`, `EncoderFor` |
| `internal/media` | `Exec` seam over ffprobe/ffmpeg, probe JSON → `Info`/`Stream`, first-frame HDR side data, `-progress` parsing, ffmpeg end-of-run stream stats, file identity/ctime/link count, VAAPI device choice | `Probe`, `ParseProbe`, `TranscodeLines`, `CountVideoPackets`, `InputStreamStats` |
| `internal/jobs` | the SQLite store: schema + 56 migrations, job state machine and claims with leases, files/probes, libraries/flows, workflow revisions, originals journal, sidecars, job logs/steps/moves/stats, events feed, workers/pairings/path mappings, shares, backups, encoder health, overview totals SQL | `Store.*` (one type, ~30 files) |
| `internal/worker` | the job runner: pre-checks, Plan+Expect+snapshot, encoder choice and GPU fallback ladder, encode with stall watchdog, source-damage reading, verify (shape, playback, SSIM), size rule, permissions/owner/fsync, swap with kept original, Convert again, sidecars, retries, remote-job join, live progress registry, restore and purge of kept originals | `Worker.RunOne`, `Worker.process`, `Worker.verify`, `keepAndReplace`, `RecoverSwaps`, `Purge`, `RunRemote` |
| `internal/workflow` | the v2 workflow document (nodes/edges/variables), node registry with JSON-schema-like configs, validation, `Doc ↔ rules.Template` mapping, branch compilation, variable name rules | `FromTemplate`, `ToTemplate`, `Validate`, `Definitions` |
| `internal/doctor` | converter checks, per-encoder test encodes (8-bit and 10-bit), GPU decode probe, folder checks, health report, Doctor page model | `Checker.Tools`, `Checker.Decodes`, `Checker.Folder`, `Checker.API` |
| `internal/web` | router and auth, v1 API (libraries, flows, settings, activity, workers, preview, files), v2 API (overview, jobs, job log, workflows, workers, shares, setup, backups, doctor, notify, integrations, variables, events SSE), worker API, share mounting via helper, GPU load readers, worker view and encoder choice glue | `Handler` (`web.go:28`) |
| `internal/web/next` | embedded Vite build of the SPA (`go:embed`) | `next.Files()` |
| `web/src` | React SPA: Dashboard, Jobs, Workflows (stack + graph editors), Libraries, Workers, Settings, Shares, Backup, Doctor, Enroll/Start, SignIn; SSE client; mock mode | `App.tsx`, `router.tsx`, `live.ts` |
| `internal/preview` | dry run of a setting over a library (counts, skip reasons, saving range, sample) and "first files" lister | `Previewer`, `FirstLister` |
| `internal/remote` | the worker-mode client: pair/join, heartbeat with load and capabilities, lease loop, plan fetch, source download or shared-folder read, convert with its own GPU fallback, local `worker.CheckResult`, resumable upload, report | `Client.Work`, `Client.convert`, `encoderArgs` |
| `internal/integrations` | tell media managers a file was replaced; instances with sealed API keys, path mappings, failing state | `Service.Tell` |
| `internal/notify` | webhook/chat notifications from the events feed, background with retries | `Notifier.Run` |
| `internal/update` | release check, update marker/result files, helper and Windows watcher clients, DB restore on rollback | `Run`, `Settle`, `RestoreDB` |
| `internal/discover` | mDNS announce and browse | |
| `internal/sysload` | CPU/memory meter (Linux) | |
| `internal/vars` | `{{name}}` template values for output names, .srt names, track titles | `Source.Values`, `OutputName`, `SrtNames` |
| `internal/pathmap`, `internal/helper` | server↔worker path mapping; client for the network helper socket | |
| `cmd/syp-network-helper` | root daemon: validated NFS/CIFS mount/unmount/status over a unix socket | |
| `cmd/syp-update-helper` | root daemon: download, verify, swap binary, health-gated rollback | |
| `Dockerfile`, `docker-entrypoint.sh`, `run-as-syp.sh` | image on Debian with ffmpeg and GPU runtimes; starts as root to run the network helper, then drops to uid 1000 with the `/dev/dri` groups | |
| `install.sh`, `installer/` | Linux/macOS one-line installer and service unit; Windows installer | |

---

## 3. Who calls whom, and what flows

| From → To | Call | Data |
|---|---|---|
| main → store | `jobs.OpenAs`, `RecoverSwaps`, `RequeueRunning`, `EnsureLibraries` | opens DB, finishes interrupted swaps, requeues `running` jobs |
| main → scanner | `scanTargets(store)` (`main.go:475`) | started libraries with their flow's `rules.Template`, re-read every pass |
| scanner → store | `GetFile`, `PutFile`, `GetProbe`/`PutProbe`, `HasOpenJob`, `IsOwnOutput`, `Enqueue`, `AddStep`, `SetLibraryScanned`, `SetOriginalGone/Back`, `SetOutputGone/Back` | `files` row per path (decision text + reason code), `probes` row, new `jobs` row with `DurationSec`, `SizeBefore` copied from the probe/stat (`scanner.go:365`) |
| scanner → rules | `Template.Compile(info)` (`scanner.go:355`) | Plan: only `Skip`/`SkipCode`/`Reason`/`Decisions`/`Path` are used here |
| pool → worker | `Pool.New(n)` builds a `Worker` with 20+ injected callbacks (`main.go:313-337`) | store as `Queue`, `TemplateFor`, `RunInfoFor`, doctor encoder funcs, `ShareDown`, `OnDone` |
| worker → store (`Queue` interface, `worker.go:36-96`) | `Claim`, `LeaseOf/RenewLease/HoldsLease`, `Progress`, `RecordPlan`, `AppendLogLine`, `RecordRunStep`, `AddStep`, `RecordJobMove`, `Complete/Fail/Keep/Skip/RetryLater/Release/FallBack/UserStopped`, `EncoderFell/Worked/Paused`, `OutputFolder`, `PutProbe`, `GetProbe`, `IsOwnOutputExact`, `OutputOwner` | job state transitions, Plan snapshot JSON, log lines, step summaries, moves, counters |
| worker → store (`Journal`, `SidecarJournal`, `againStore`) | `BeginSwap/FinishSwap/SetSwapState`, `AddSidecars/FinishSidecars/DropSidecar`, `AgainFrom/OriginalByID/Reconverted` | `originals` and `sidecars` rows |
| worker → rules | `Template.Compile`, `CompileBranch`, `Plan.Expect`, `Plan.FFmpegArgs`, `FFmpegArgsWith`, `FFmpegArgsFullGPU`, `ReplaceName`, `StagedSrt`, `SkipWords` | Plan, Expect, argv |
| worker → media | `scanner.Probe` (cache-aware) for the source, `media.Probe` for the output, `TranscodeLines`, `CountVideoPackets`, `LeadingUndecodable`, `Exec.Capture` for playback and SSIM runs, `FileState`, `LinkCount` | `Info`, progress callbacks, stderr text |
| worker → doctor (via web glue) | `Encoder(codec, need10, hdr)` = `web.LocalEncoderFor` → `rules.EncoderFor` over `capabilities(doctor.Tools)`; `GraphicsDecodes` = `doctor.Checker.Decodes` | encoder name or "" (processor) plus a note; whether the GPU decodes this codec/depth |
| worker → integrations | `OnDone` → `Service.Tell(jobID, from, final)` | rescan requests after a replace |
| web → scanner | `ScanNow`, `NextScan`, `SetEvery`, `Scanning` | |
| web → pool | `RequeueJob`, `PauseNow`, `StopFile`, `RunRemote` | |
| web → workflow | `FromTemplate`, `ToTemplate`, `Validate`, `Definitions`, `FillCommitDefaults` | workflow Doc ↔ `rules.Template` |
| web → store | everything else: libraries, flows, revisions, settings (`app_meta.settings`), activity, overview totals, events, workers, shares, backups, encoder tests, path mappings | |
| web ↔ remote worker | `/api/worker/v1/{pair,join,heartbeat,lease,leases/{l}/{plan,source,result,progress,note,report,release},paths/{t}/result}` (`workerapi.go`, `workerremote.go`) | capabilities JSON, lease with `primaryEncoder`, `rules.Snapshot` as the plan, original bytes down, new file bytes up, progress with `outTimeUs`, report with stderr/steps/args |
| remote worker → worker pkg | `worker.CheckResult(snapshot, src, out)` (`remotejob.go:204`) | the same `verify` as the server, on the worker's own copy |
| browser → web | fetch with session cookie; SSE `GET /api/v2/events` replays the `events` table by `seq` (`v2events.go`) | |
| web → helper | unix socket JSON `{cmd: mount|unmount|status}` (`internal/helper`) | mount results into `share_mounts` |
| update → helper | unix socket `{cmd: install|rollback|status}` | version only, never a path |

---

## 4. External edges

| Edge | Where | Notes |
|---|---|---|
| **ffprobe** | `media.Probe` (`media.go:212`): `-show_format -show_streams -show_chapters`, plus a first-frame `-show_frames` read for HEVC/AV1/PQ/HLG sources; `CountVideoPackets` (`-count_packets`); `LeadingUndecodable` | Output bounded at 8 MB. Probe JSON cached in `probes` keyed by path+size+mtime, with `ProbeVersion` 7 (`media.go:207`) |
| **ffmpeg encode** | `Plan.ffmpegArgs` (`plan.go:342-443`): `-progress pipe:1 -nostats -v error`, `-map 0 -map -0:d -c copy -max_muxing_queue_size 4096`, video encoder per family, `+faststart` for MP4 family, stale-stat metadata clears, extra outputs for .srt files | run by `media.TranscodeLines` under the stall watchdog |
| **ffmpeg checks** | playback: `-loglevel level+verbose -xerror -copyts … -f null -` (`verify.go:298-309`); SSIM: two inputs with `setpts/trim` and `ssim=stats_file=-` (`verify.go:619-660`); `-filters` for the ssim check; `-encoders` list | each with its own timeout |
| **ffmpeg test encodes** | `doctor.encodeArgs` (`doctor.go:453`): one black frame per encoder, 8-bit and 10-bit; `decodeArgs` for the GPU decode probe | 15 s / 30 s timeouts; results cached 10 min and stored in `encoder_tests` |
| **GPU** | arguments only: `-init_hw_device qsv=qsv:hw` / `vaapi=va:<render node>`, `-hwaccel` for the full pipeline, `scale_qsv` / `scale_vaapi`, per-family quality flags (`plan.go:347-353, 412-424, 475-487`); render node chosen by PCI vendor (`media/vaapi.go`); GPU use read from `nvidia-smi`, `gpu_busy_percent`, i915 PMU (`web/gpuload.go`, `gpu_sysfs.go`) | Docker image joins `/dev/dri` groups at start (`run-as-syp.sh`, **UNVERIFIED**: not read) |
| **Filesystem** | library folders (walk, Lstat, `O_NOFOLLOW` open, link count, ctime); staging folder `.<name>.<rand>.syp-tmp/new.<ext>` beside the original or in the output folder (`tempfile.go`); `.syp-trash/<name>.<jobid><ext>` kept originals (`keep.go`); `renameNoReplace` (renameat2 / MoveFileEx / link+unlink), fsync of file and folders (`tempfile.go:330-391`); free-space checks | |
| **Shares** | `shares`/`share_mounts` tables; mounts under `/mnt/<name>` or the shares folder via the root helper; `web.WatchShares` pauses a library whose mount stops answering (`libraries.share_paused_id`); worker treats a failure on a down share as "put back", not failed (`worker.go:411-423`) | **UNVERIFIED**: `web/v2shares.go`, `shareoffline.go` not read in full |
| **Browser / API** | v1 `/api/*` (older pages' contract, still served) and v2 `/api/v2/*` (the SPA's contract); every write must be JSON and pass CSRF; token or session cookie beyond loopback (`auth.go`); `/healthz` public | the SPA uses v2 for Dashboard/Jobs/Workflows/Workers and some v1 routes (libraries, settings, preview) |
| **Auth** | one shared token in `<data>/access-token`; session cookie = HMAC of the token; worker API uses per-worker bearer tokens stored hashed in `workers.token_hash`; pairing codes hashed in `worker_pairings`; join requests in `join_requests` | |
| **Backups** | pre-migration copy `<db>.bak-v<N>` (newest 3) and JSON backups in `<data>/backups/` (`jobs/backup.go`, `web/v2backup.go`); restore replaces data in one transaction after a `before_import` backup | |
| **Update** | `update.Run` asks the release host daily; Linux root helper swaps the binary; marker `update.json` + `update-result.json` in the data folder; a DB from a newer version is refused and the pre-upgrade copy put back (`main.go:441-471`) | |
| **Notifications / integrations** | HTTP to user-configured endpoints; integration keys sealed with the share-password cipher whose key lives in `$HOME/.config/shrink-your-shit/share-key` (`jobs/shares.go:45-61`) | |
| **mDNS** | `discover.Announce` when the UI listens beyond loopback | |

---

## 5. Main flows

### 5a. Adding a library or a share

```mermaid
sequenceDiagram
  participant UI
  participant WEB as web (libraries.go / v2libraries.go / v2shares.go)
  participant DOC as doctor
  participant ST as store
  participant HELP as syp-network-helper
  UI->>WEB: POST /api/libraries/check {path}
  WEB->>DOC: Checker.Folder(path)
  DOC-->>UI: blockers / notes / videoCount (RealPath, SystemFolder, writeProbe, walk, free space, Tools)
  UI->>WEB: POST /api/libraries {name, path, flowId | none}
  WEB->>ST: AddLibraryOnFlow / AddLibraryWithoutFlow (libraries.go:229-296)
  Note over ST: libraries row, started=0; path stored as its real path; overlap refused (path_overlaps)
  UI->>WEB: POST /api/libraries/{id}/scan (start)
  WEB->>ST: StartLibrary (refused for a system folder or no flow)
  WEB->>WEB: ScanNow()
  UI->>WEB: POST /api/v2/shares {type, address, credentials}
  WEB->>ST: SaveShare (password sealed with share-key)
  WEB->>HELP: {cmd: mount} over unix socket (app mode on Linux only)
  HELP-->>WEB: mounted / error → share_mounts.status
  Note over WEB: RemountShares at start; WatchShares pauses libraries on a dead mount
```

1. The folder check runs the same `doctor.Checker.Folder` the health report uses (`doctor.go:486`).
2. A library is a `libraries` row with `flow_id` nullable since migration 56; a library with no workflow is never scanned (`main.go:499-503`) and `Enqueue` refuses jobs for it in the same SQL statement (`jobs.go:228-229`).
3. A new library starts stopped; `StartLibrary` sets `started=1`; the scanner reads started libraries on every pass through `scanTargets`.
4. Shares: record, mount through the root helper, remount at start, watch for outages. **UNVERIFIED** in detail: I did not read `v2shares.go`/`shareoffline.go` line by line.

### 5b. Scanning

```mermaid
flowchart TD
  A[Run: every scanEvery or ScanNow<br/>scanner.go:462] --> B[Targets(): started libraries + flow template<br/>main.go:475]
  B --> C[WalkDir each library<br/>scanner.go:125]
  C --> D{Ignore list / SkipDir / IsVideo}
  D -->|video| E[Guard: symlink? hard-linked?<br/>scanner.go:413]
  E -->|skip code| F[recordSkip → files row]
  E -->|ok| G[MinAge + two-look rule<br/>observe/ready scanner.go:243-269]
  G -->|still changing| F
  G -->|settled| H{GetFile: same size+mtime<br/>and decided?}
  H -->|yes| Z[nothing]
  H -->|no| I{HasOpenJob / IsOwnOutput / empty?}
  I -->|own output| Z
  I -->|empty| J[EnqueueFailed]
  I -->|no| K[Probe via cache<br/>scanner.Probe → media.Probe]
  K -->|error| L[files: unreadable]
  K --> M[Template.Compile(info)<br/>scanner.go:355]
  M -->|Skip| N[files: skip + SkipCode]
  M -->|convert| O{IntakePaused?}
  O -->|no| P[Enqueue job: path, DurationSec, SizeBefore<br/>+ steps found/checked<br/>scanner.go:365-381]
  P --> Q[files: queued]
  C --> R[after the walk: SetLibraryScanned,<br/>forgetUnseen, markOriginalsGone, markOutputsGone]
```

1. A pass re-reads libraries and their flow templates from the DB (`scanTargets`), so a flow edit takes effect on the next pass.
2. File guards run before any read: links are never followed, hard-linked files are skipped (`Guard`, `scanner.go:413`).
3. A file is decided only when seen unchanged twice at least `-settle` apart; the first look is in memory only (`firstLook`, lost on restart).
4. Unchanged files (same size and mtime as the `files` row) are not re-probed unless their record is a guard, still-changing or stranded.
5. The probe goes through the `probes` cache; the worker later reads the same cache (`worker.go:2253-2258`).
6. `Template.Compile` decides skip vs convert; the Plan's other fields are thrown away here and recompiled by the worker.
7. The job row copies the probe's duration and the stat's size; these are the numbers progress percent, the stall "finishing" test and the totals later use.
8. After each full walk the scanner marks left-in-place originals and outputs as gone or back, which the savings totals read (`markOriginalsGone`, `markOutputsGone`).

### 5c. A job from start to end

The worker has one code path (`Worker.process`, `worker.go:811-1459`) with the encoder chosen inside it. The diagram shows the common spine; the encoder paths branch at step 9.

```mermaid
flowchart TD
  C[Claim: one SQL statement, lease id<br/>jobs.go:318] --> R[runClaimed: lease renewer, start lines<br/>worker.go:340]
  R --> P1[templateFor(job) = library's CURRENT flow<br/>TemplateFor libraries.go:533]
  P1 --> P2[againSource: original or its kept copy]
  P2 --> P3[Lstat/statOpen/FileState/LinkCount<br/>removeLeftoverTemps]
  P3 --> P4[probeSource via probe cache<br/>worker.go:885]
  P4 --> P5[preflight: free space ≥ size×1.1+64MB]
  P5 --> P6[frame count: container's or CountVideoPackets]
  P6 --> P7[plan := tmpl.Compile(src); fallback branch if FallbackOf<br/>worker.go:917-923]
  P7 --> P8{DolbyVisionHold / plan.Skip?}
  P8 -->|skip| S1[SkippedError → Store.Skip]
  P8 --> P9[runValues: {{name}} vars; outName; srt names]
  P9 --> P10[exp := plan.Expect(src, packets)<br/>worker.go:1006]
  P10 --> P11[RecordPlan: Snapshot{Plan, Expect, Verify, Source}<br/>worker.go:1012-1019]
  P11 --> P12[newName by output mode; name-taken checks; preflight on output disk]
  P12 --> P13[newStaged: private folder + exclusive file]
  P13 --> E0[args := plan.FFmpegArgs(src,tmp)  (processor)]
  E0 --> E1{w.Encoder(codec, need10, hdr)<br/>→ rules.EncoderFor}
  E1 -->|processor| ENC
  E1 -->|graphics encoder| E2{EncoderPaused?}
  E2 -->|paused| ENC
  E2 --> E3[graphicsArgs: GraphicsDecodes? → FFmpegArgsFullGPU<br/>else FFmpegArgsWith<br/>worker.go:2159]
  E3 --> ENC[encodeLocal: TranscodeLines + stall watchdog<br/>worker.go:2202]
  ENC --> F1{gpu && gpuFailed?}
  F1 -->|full rung failed| F2[truncate tmp; hybrid args; encode again]
  F2 --> F1
  F1 -->|hybrid failed| F3[truncate tmp; processor args; encode again<br/>EncoderFell counts]
  F3 --> V0
  F1 -->|no| V0[recordEncode; sourceGone?; damage from stderr + lost frames<br/>worker.go:1203-1281]
  V0 -->|damaged| K1[kept]
  V0 --> V1[sourceUnchanged; stage.intact]
  V1 --> V2[verify: stat, probe out, Expect.Compare(shape), decodeCheck, frame count, ssimCheck<br/>verify.go:144-201]
  V2 -->|KeptError| K1
  V2 --> V3[sizeRule (KeepIfBigger) → SkippedError]
  V3 --> C1[chmod, chtimes, setOwner, fsync file + srt files]
  C1 --> C2{ctx / StopRequested}
  C2 --> C3[installSubtitles (renameNoReplace, recorded first)]
  C3 --> C4{mode}
  C4 -->|convert again| C5[replaceOwnOutput]
  C4 -->|replace, keepDays>0 or renaming| C6[keepAndReplace: journal row → original to .syp-trash → new file in → FinishSwap<br/>keep.go:120]
  C4 -->|replace, 0 days| C7[moveIntoPlace (os.Rename)]
  C4 -->|beside / folder| C8[moveOverOwn / moveNoClobber]
  C5 --> D1
  C6 --> D1
  C7 --> D1
  C8 --> D1[PutProbe(final); steps verified/replaced; Complete; OnDone → integrations<br/>worker.go:481-510]
```

**Steps, as numbered in the code's order:**

1. **Claim** (`Store.Claim`, `jobs.go:318`): oldest queued job by priority, not stop-requested, not before `retry_at`, library started and not share-paused, worker record enabled; sets `running`, `started`, `lease_id`, `lease_expires`. One statement, so parallel workers never collide.
2. **Template** (`TemplateFor`, `libraries.go:533`): the library's *current* `flows.template`, with the person's languages injected (`GetFlow`, `libraries.go:153-170`). A flow edited after the job was queued changes what runs.
3. **Source checks** (`worker.go:831-862`): link, identity through an open handle, change time, hard links, leftover temp folders.
4. **Probe** (`worker.go:885`): the scanner's cached probe when size and mtime match, else ffprobe again.
5. **Preflight** free space (`verify.go:123`).
6. **Frame count** (`worker.go:903-913`): the container's own count when exact, else a full packet read.
7. **Plan** (`worker.go:917`): `Template.Compile(src)`; a fallback branch replaces it for a V6b retry.
8. **Expect and snapshot** (`worker.go:1006-1019`): `plan.Expect(src, packets)` predicts streams, codecs, picture size (`ScaledWidth`), bit depth, container, HDR fields, duration, frame count; the snapshot (Plan, Expect, Verification, Source probe, variables) is stored on the job before anything is written. **The encoder is not chosen yet at this point.**
9. **Encoder choice** (`worker.go:1113-1145`): `w.Encoder` → `web.LocalEncoderFor` → `rules.EncoderFor(codec, primaryEncoder, need10, hdr, caps)`; a paused encoder (three failures) falls to the processor; `graphicsArgs` asks the doctor whether the family decodes the source and renders full-GPU or hybrid arguments. Four shapes of argv come out of `Plan.ffmpegArgs` (`plan.go:342`): processor; GPU encode with processor decode (hybrid); full GPU (QSV/VAAPI only); video copy (remux). NVENC/AMF/VideoToolbox only ever get the hybrid shape (`plan.go:321`).
10. **Encode** (`encodeLocal`, `worker.go:2202`): ffmpeg with `-progress`; progress drives `Store.Progress`, the live registry (`live.go`), the 10 % log lines and the stall watchdog (`watchStall`, `worker.go:745`), which stops the job after 10 min without `out_time_us` moving, or after 1 h once the position reached the file's length ("finishing", `reachedEnd`, `worker.go:738`).
11. **Fallback ladder** (`worker.go:1172-1201`): on a GPU failure classified by `jobs.FallsBackToProcessor` (string matching on stderr, `failure_codes.go:33-54`), full GPU → hybrid → processor, truncating the same staged file; `EncoderFell` counts toward a one-hour pause of that encoder.
12. **Source damage** (`worker.go:1244-1281`): regexes over ffmpeg's stderr plus "lost frames" (frames expected minus frames ffmpeg reported) → kept if above 1 % of frames or any file-level message.
13. **Verify** (`Worker.verify`, `verify.go:144`): exists, non-empty, probe, `Expect.Compare` (container, stream counts and per-stream codec/language/channels/size/depth/flags/timing, HDR fields, chapters, duration ±2 %), playback test (whole file up to 3 h, else one-minute samples every 10 min) which also yields the frame count (±1 %), then SSIM at three 10 s places with ±1-frame nudges, threshold from the slider position or `-min-ssim`.
14. **Size rule** (`keep.go:245`): bigger than the original and `KeepIfBigger` → ends as a *skipped* file with code `new_file_bigger`.
15. **Commit** (`worker.go:1318-1458`): permissions, mtime, owner, fsync, stop check, context made uncancellable, sidecars placed, then one of four moves; replace with retention goes through the journaled `keepAndReplace` (`keep.go:120-239`).
16. **Record** (`runClaimed`, `worker.go:481-510`): the output's probe is stored for the Files view, steps are written, `Complete` sets `done`, `size_after`, `output_mtime`, `original_disk`; `OnDone` tells integrations.
17. **Any error** goes back through `runClaimed` (`worker.go:385-480`): stop/pause/stall decide first, then share-down → put back, `SkippedError` → skip, workflow failure policy (`applyFailure`), retryable → `RetryLater` (3 tries, 5 m/30 m or 30 m/2 h), `KeptError` → `kept`, `placeError` → failed replace step, else `failed`.

**Per encoder path, what differs:**

| Path | argv builder | decode | pixel format / depth flags | scale filter | known expectation gaps |
|---|---|---|---|---|---|
| processor (libx265/libx264/libsvtav1/libvpx-vp9) | `ffmpegArgs(gpu="")` `plan.go:370-384` | software | HDR: `-pix_fmt yuv420p10le -profile main10 -x265-params`; 10-bit SDR: nothing added (encoder keeps depth) | `scale=-2:H` | none known |
| hybrid (processor decodes, GPU encodes: nvenc/qsv/vaapi/amf) | `FFmpegArgsWith` `plan.go:304` | software | `-profile main10` + `-pix_fmt p010le` (not VAAPI); VAAPI adds `format=nv12|p010,hwupload` | `scale=-2:H` before upload | VAAPI/NVENC/AMF 10-bit **UNVERIFIED** on real hardware; the code says HDR on NVENC/AMF goes to the processor (`encoder.go:14`) |
| full GPU (qsv/vaapi decode + encode) | `FFmpegArgsFullGPU` `plan.go:319` | `-hwaccel qsv|vaapi` | depth from the decoder's frames | `scale_qsv=w=trunc(H*a/2)*2:h=H` or `scale_vaapi=w=-2:h=H:format=…` (`plan.go:460-471`) | QSV width rounds differently from `ScaledWidth` (854 vs 852 for 1920x1080 → 480p): verify refuses the file |
| video copy (remux) | `ffmpegArgs` with `Video.Copy` `plan.go:363-367` | none | none | none | SSIM skipped (`VideoCopied`) |
| remote worker | `remote.encoderArgs` `work.go:507` (hybrid only, no full GPU, own fallback at `work.go:478-495`) | software on the worker | as hybrid | as hybrid | the worker's checks run on its copy; the server re-checks shape and a sampled decode |

### 5d. A workflow edit

```mermaid
sequenceDiagram
  participant UI as SPA (WorkflowStack / WorkflowGraph)
  participant WEB as web/v2workflows.go
  participant WF as workflow pkg
  participant ST as store
  UI->>WEB: GET /api/v2/workflows/{id} (doc, layout, etag, validation)
  WEB->>ST: Draft / LatestRevision
  UI->>WEB: PUT draft {doc, layout, If-Match etag}
  WEB->>WF: Validate(doc) → issues, summaries
  WEB->>ST: SaveDraft (workflow_revisions status=draft)
  UI->>WEB: POST publish
  WEB->>WF: ToTemplate(doc, currentTemplate) → rules.Template
  WEB->>ST: Publish(flowID, etag, template): revision numbered, flows.template replaced, libraries' files forgotten, scanner woken
  Note over ST: the worker reads flows.template at run time; queued jobs keep workflow_rev for "Convert again" comparisons
```

1. Two representations exist: the v2 `Doc` (nodes/edges, stored per revision in `workflow_revisions.doc`) and the engine's `rules.Template` (stored in `flows.template`). `FromTemplate` and `ToTemplate` (`workflow/preset.go:17, 139`) translate between them; the template is what scanner and worker run.
2. Old v1 setting endpoints (`web/setting.go`) still write templates directly; `EnsureRevision` makes a revision from a template changed that way (`jobs/workflows.go:135`).
3. A publish resets the affected libraries' file decisions so the next scan re-decides them (`SetFlowTemplate`/`SaveTemplate`, `libraries.go:696, 856`; **UNVERIFIED** which of them the v2 publish calls, the signature `Publish(flowID, etag, t)` is in `workflows.go:275`).
4. Workflows with "Check the file" steps compile to a `Branches` template (`workflow/branches.go`), each branch ending in a straight template compiled the same way.

### 5e. Restart or update: what is restored

```mermaid
flowchart TD
  A[start] --> B[lockDataDir syp.lock]
  B --> C[ReadMarker update.json]
  C --> D[openStore: migrate + pre-upgrade backup;<br/>newer DB → put back .bak-v and record result]
  D --> E[RecoverSwaps: originals rows 'moving'/'restoring' → finish, roll back, or needs_attention<br/>keep.go:290]
  E --> F[SweepStaging + SweepNextTo: remove our staging folders]
  F --> G[RecoverSidecars: .srt files placed before a swap that did not happen]
  G --> H[RequeueRunning: running → queued; finish requested stops]
  H --> I[ImportRunLogs (old files → DB), purge kept originals, prune logs/events]
  I --> J[EnsureLibraries from flags]
  J --> K[doctor.Converter; encoder test in background; SetAvailableFormats]
  K --> L[scanner.Run; lease sweeper; pool.Run; notifier; web server]
  L --> M[/healthz ok → update.Settle; RemountShares; WatchShares]
```

1. In-memory state is lost on restart: scanner first-looks (files wait `-settle` again), live progress, doctor's test results (re-run in the background), preview and first-files caches, path-test timers, remote-run joins (a remote worker's lease ends and the job is requeued by the lease sweeper, `main.go:278-293`).
2. Durable state is reconciled in the order above; a job that was mid-encode simply runs again from the start.
3. Paused/enabled flags, retry waits (`retry_at`), stop requests, encoder pauses (in `app_meta`) all survive.

---

## 6. Where state lives

### 6a. Database tables (SQLite, `<data>/syp.db`, WAL, one write connection)

| Table (migration) | Owner / writers | Readers | Holds |
|---|---|---|---|
| `files` (1,7,31,48) | scanner (`PutFile`), worker `Skip`/`UserStopped`, restore | scanner, Skipped list, overview totals (`SUM(size)` as "bytes before", `overview.go:31`) | per path: size, mtime, codec, decision text, reason code, job_id, decided_at. Keeps the *original's* size after a replace on purpose |
| `jobs` (1,3,4,7,9,12,21,27-30,34,49-52) | scanner enqueue; worker transitions; web retry/stop/priority | everything | status, progress, reason, error + error_code, worker, duration, size_before/after, output_path/mtime, plan JSON + fingerprint, started, attempts, retry_at, stop_requested, priority, original_gone_at/changed/disk, route, fallback, lease, again_from, output_gone_at, workflow_rev, subtitle_files |
| `probes` (5) | scanner, worker (source and output) | scanner, worker, preview, Files view | ffprobe JSON per path at size+mtime |
| `libraries` (3,15,25,26,41,56) | web | scanner targets, claims (`libraryOnSQL`) | name, path, flow_id (nullable), started, last_scan, override_folder, priority, share_paused |
| `flows` (3,20) | web (v1 setting, v2 publish), migrations 13/16/17/19/38 rewrite templates in SQL | `TemplateFor`, scanner, preview | name, template JSON (the engine's truth), graph (unused), description/status/favourite/tags |
| `workflow_revisions` (20) | web v2 | web v2, `RunInfoFor` | doc JSON, layout, logic/template hashes, etag, draft/published |
| `originals` (6,8,11) | worker swap journal, restore, purge | recovery, Done/Kept lists, totals (`keptNowSQL`), Convert again | path, trash, staged, sizes, mtimes, sample hashes, state, delete_on, new_path, aside |
| `sidecars` (10) | worker | recovery | .srt files placed before a swap |
| `job_steps` (18,28) | worker, scanner, store | Jobs timeline, overview counts | step words, node/port |
| `events` (21-23,32) | SQL triggers on `jobs` + `RecordEvent` | SSE stream, daily counts, notifier, `lostInARow` | status changes with reason code and message |
| `job_run_steps`, `job_log_lines`, `job_failures`, `job_run_stats`, `job_run_moves` (39,53,54,55) | worker | job log page | encode/check/replace steps, every log line with section and level, failure summary, counters, file moves |
| `app_meta` (2,14,36,…) | many | many | `install` kind, `settings` JSON (the whole Settings object), `paused`/intake flags, notices, `encoderHealth:<worker>`, update check cache, schema markers |
| `workers`, `worker_pairings`, `worker_paths` (24,35-37,42-44) | web, worker API | claims (`workerRecordSQL`), Workers page, lease | local and remote worker records, enabled flag, primary encoder, capabilities JSON, token hash, last seen |
| `join_requests` (45) | worker API, web | | pending joins |
| `encoder_tests` (46) | doctor `OnTested`, remote reports | Doctor/Workers pages | per computer, per run, per encoder pass/fail |
| `shares`, `share_mounts` (40,47) | web | scanner targets (waiting mounts), purge, worker ShareDown | share records (sealed passwords), mount status |

### 6b. Config

| Setting | Where |
|---|---|
| flags and env (`-data`, `-addr`, `-library`, `-crf`, `-decode-check`, `-min-ssim`, `-scan-every`, `-settle`, `-stall-after`, `-ffmpeg`, `-trusted-proxies`) | `main.go:68-87`; Docker sets `SYP_DATA`, `SYP_ADDR`, `SYP_INSTALL` |
| the Settings object (parallelJobs, keepOriginalsDays, logKeepDays, languages, quiet hours, scanEvery, update checks, …) | `app_meta.settings` JSON (`web/settings.go:71`), read on every use (`LoadSettings`) |
| per-worker config (name, slots, primary encoder) | `workers` table |
| flow templates and workflow docs | `flows`, `workflow_revisions` |
| remote worker credential | `<data>/worker/worker.json` (mode 600) |
| integrations | a file document loaded by `integrations.Service.load` (**UNVERIFIED** path; `integrations.go:226`) |

### 6c. Files on disk (outside the DB)

| File | Purpose |
|---|---|
| `<data>/syp.db` + `-wal`/`-shm`, `<data>/syp.db.bak-v<N>`, `<data>/backups/*.json` | database, pre-upgrade copies, user backups |
| `<data>/syp.lock` | exclusive lock: one service per data folder (`datalock.go`) |
| `<data>/access-token` | the shared UI token |
| `<data>/update.json`, `<data>/update-result.json` | update in progress / last result |
| `<data>/logs/` | legacy run logs, imported once into SQLite |
| `$HOME/.config/shrink-your-shit/share-key` (or `/etc/shrink-your-shit/share-key` as root) | key sealing share passwords and integration keys |
| `<library>/.<name>.<rand>.syp-tmp/new.<ext>` (+ `sub.N.srt`) | staging; swept at start |
| `<library>/.syp-trash/<name>.<jobid><ext>` | kept originals |
| `<worker data>/work/<leaseid>/{source.*, new.*}` | remote worker's downloads and outputs |

### 6d. In-memory caches and registries (lost on restart)

| State | Where | Who depends on it |
|---|---|---|
| scanner `firstLook` map (two-look rule), `scanning` counts, `nextScan` | `scanner.go:70-72` | settle rule, library card |
| live encode registry `live.jobs` (progress, projected size, encoder words) | `worker/live.go:35` | overview, events, Workers page |
| worker `filterMemo`, `phase`, stall/quiet atomics, `verified` probe, `check` score, `failures`, `lease`, `remote` | `Worker` struct `worker.go:122-216` | one job at a time per worker |
| doctor `tools`, `hw`, `probes`, `decodeOK`, `refreshing` | `doctor.go:113-128` | encoder choice for every job (`LocalEncoderFor` reads `ToolsNow` each time) |
| store `removing` map (libraries being removed) | `jobs.go:106` | claims |
| preview results, first-files walks | `preview` package | setting page |
| `workerLoads`, `workerHolds` sync.Maps, `remoteRuns` by lease, `pathTests`, `joinBook`, `gpuUse` cache, `statsCache`/`libraryTotals` caches | `web/workerapi.go:437-440`, `workerremote.go:30`, `workerpaths.go:43`, `joinrequests.go:63`, `gpuload.go:37`, `v2overview.go:269,359` | Workers/Dashboard pages |
| `rules.AvailableFormats` / `availableEncoders` | `rules/formats.go:34-52` | validation of workflow formats |

### 6e. Which of these is the source of truth for what

| Fact | Source of truth in the code | Copies that can drift |
|---|---|---|
| what a file *is* | the `probes` row at (path, size, mtime), version-gated by `ProbeVersion` | `jobs.duration`/`size_before` (copied at enqueue), `files.codec`, the snapshot's `Source`, `media.Info.VideoCodec` vs `Video()` |
| whether a file should convert | the scanner's `Template.Compile` at scan time, recorded as text in `files.decision` | the worker recompiles against the *current* flow; the preview compiles a third time; migrations delete `files` rows to force re-decisions |
| how a file is converted | the worker's Plan at run time (`RecordPlan` snapshot) | the Expect inside the same snapshot is computed before the encoder is chosen |
| what the output must look like | `Snapshot.Expect` | the encoder's real output (width rounding, depth, container alias names) |
| job status | `jobs.status` with transitions enforced in `Store.transition` | `events` (triggers), `job_steps`, `live` registry, `files.decision` for skipped/stopped jobs |
| a worker's encoders | the doctor's last test run (memory) and `encoder_tests` (history) | `workers.capabilities` for remote workers; `primaryEncoder` derived on every read |
| space saved / to free | SQL over `jobs` + `originals` + scanner marks (`freed.go:40-74`) | `files.size` as "bytes before", `output_gone_at`, `original_gone_at`, `original_changed`, `newestSQL`, restored originals; the live projected size |
| the workflow | `flows.template` for the engine; `workflow_revisions` for the editor | the two are kept equal by `ToTemplate` at publish and `EnsureRevision` after a v1 save |
| whether a job still runs | `jobs.lease_expires` renewed by progress and a ticker | worker `current` field; remote `judging` state |

---

## 7. UNVERIFIED and not read

- `internal/web/v2shares.go`, `shareoffline.go`, `shares_*.go`, `workerpaths.go`: described from their package comments and call sites, not read line by line.
- `internal/web/setting.go`, `flows.go`, `libraries.go` (v1 handlers): described from `docs/ARCHITECTURE.md` and the store functions they call.
- `web/src` screens: only `router.tsx`, `nav.ts`, `api.ts`, `live.ts` were read; the screen list comes from the file names.
- `run-as-syp.sh`, `install.sh`, `installer/`: not read.
- `internal/update` beyond the package comments; `cmd/*` helpers beyond their header comments.
- Real behaviour of VAAPI, NVENC, AMF and VideoToolbox 10-bit paths: only the arguments were read; the code itself marks NVENC/AMF HDR as unproven (`rules/encoder.go:11-14`).
