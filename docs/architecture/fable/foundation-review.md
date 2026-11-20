# shrink-your-shit: foundation review

Read against `origin/dev` at commit `294e3d88` (2026-11-20), read-only. Citations are repo-relative
with line numbers from that commit; commit hashes are from `origin/dev`'s history. The companion
`app-map.md` holds the full pipeline and state map; this document only repeats what the argument needs.
Anything I could not confirm from code or history is marked **UNVERIFIED**.

---

## 1. Summary, one page

**What the app does well.** The safety story is real and well built: a private staging folder, a
journaled swap with a kept original, rename-without-replace everywhere, fsync before the rename,
identity checks on both files right before the swap, sidecar bookkeeping, crash recovery at start, and
a verify step that never lets a damaged or truncated file replace an original. The SQLite store with
numbered migrations and automatic backups is sound. None of that needs rewriting.

**What is wrong with the foundation.** The conversion pipeline is built as *predict, run, then
check the prediction*. Before ffmpeg runs, the code writes down exactly what the output must look like
(`rules.Plan.Expect`: picture width to the pixel, bit depth, codec names, container alias, duration,
frame count, timing offsets). It does this *before it knows which encoder will run*. Then a separate
check (`Expect.Compare`) refuses any output that differs from the prediction, and reports every
difference as a safety refusal ("the original was kept"). Each encoder family rounds, names and
converts things slightly differently, so each new family or each new source kind turns a harmless
difference into a refused job. The fixes land as one more special case in the predictor, one more
tolerance in the checker, or one more place that decides "is this file 10-bit".

Around that core, four more patterns feed the patch stream:

1. Knowledge about encoders is spread over eight places in four packages, and the remote worker has
   its own copy of the fallback ladder.
2. The decision to convert is made at scan time against the flow as it was then; the worker runs
   whatever the flow is *now*, and the preview compiles a third time. Migrations rewrite flows in SQL
   and delete file decisions to force re-decisions.
3. Timers and regular expressions over ffmpeg's text stand in for signals ffmpeg already provides:
   a 10-minute stall clock with a 1-hour "finishing" allowance, ~15 regexes deciding "damaged",
   string tables deciding "fall back to the processor" and the user's failure sentence.
4. One verdict ("kept original") covers three different situations: the output is broken, the
   output differs from a prediction, or a check could not run. The user words, retry policy and
   workflow failure ports cannot tell them apart.
5. Savings and counts are reconstructed from six columns on `jobs`, scanner-set marks and the
   `files` table's deliberate "size before" meaning, in 70 lines of SQL fragments.

**Evidence in numbers** (my own count, not the manager's): since 2026-11-13 `dev` has 620 non-merge
commits; 165 have fix-like subjects by keyword; `internal/worker/worker.go` changed in 48 commits,
`verify.go` in 22, `rules/plan.go` in 19. Twelve of those commits, over three days, adjust how the
original's damage and frame count are read from ffmpeg text; five adjust what bit depth the output
will have; two in one day adjust the stall clock.

**What fixing it would mean.** Not a rewrite. The target is a single contract per job,
`intent → spec → run → measurement → verdict → commit`, where the *chosen encoder* renders both the
command and the expectation (with the tolerances it owns), verify measures the output and returns a
typed verdict (mismatch vs damaged vs could-not-check), and the remote worker runs the same code. That
is roughly nine slices of one to three days each, every one leaving `dev` shippable, with the first
two fixing the width-rounding bug (job 48) and the 10-bit family of bugs at their root. The swap,
journal, store, scanner guards, API and UI stay as they are.

---

## 2. The pipeline as it is now

The full map is in `app-map.md` §5c. The spine, with where each kind of knowledge lives:

```mermaid
flowchart LR
  SCAN[scan: Template.Compile → Skip?<br/>scanner.go:355] --> Q[(jobs row:<br/>path, duration, size)]
  Q --> W1[worker: Template.Compile again<br/>on the library's CURRENT flow<br/>worker.go:917, libraries.go:533]
  W1 --> EXP[Expect = prediction of the output<br/>plan.Expect worker.go:1006]
  EXP --> SNAP[(snapshot stored<br/>RecordPlan)]
  SNAP --> ENC[encoder chosen AFTER the prediction<br/>worker.go:1113-1145]
  ENC --> RUN[ffmpeg + 10-min stall clock<br/>encodeLocal]
  RUN --> LAD[fallback ladder on stderr text<br/>gpuFailed / FallsBackToProcessor]
  LAD --> DMG[damage from stderr regexes + lost-frame arithmetic<br/>worker.go:1244-1281]
  DMG --> VER[verify: Compare(output, prediction)<br/>→ one KeptError for every difference<br/>verify.go:170]
  VER --> COMMIT[journaled swap<br/>keep.go]
```

Three facts about this spine drive the rest of the document:

- `Expect` is computed at `worker.go:1006` and stored at `worker.go:1012-1019`; the encoder is chosen
  at `worker.go:1113-1145`. The prediction cannot know what the encoder will do.
- `Expect.Compare` (`rules/expect.go:166-256`) returns a list of sentences; `verify` turns any
  non-empty list into `kept("%s", …)` (`verify.go:170-171, 188-190`) with the one code
  `jobs.ReasonKeptOriginal` (`verify.go:75-77`).
- `TemplateFor` (`jobs/libraries.go:533-558`) reads the library's current `flows.template`; nothing
  on the job says which revision it was queued under except `workflow_rev`, which only "Convert
  again" reads.

---

## 3. Root causes, ranked by the bugs they produced

### RC1. The plan predicts what the encoder decides; the check refuses reality

**Mechanism.** `Plan.Expect` (`rules/plan.go:606-688`) fills `StreamExpect.Width/Height` from
`ScaledWidth` (`plan.go:1061-1066`: nearest even number), `BitDepth` from the source, `Codec` from
the plan's format, `Container` from `rules.Containers` alias strings, `HDR` from the source's
fields, duration and frame count from the source. `Expect.Compare` demands exact equality for width,
height, depth, codec, container and HDR strings (`expect.go:169, 269-273, 223-236`), and tolerances
only for duration, frames and timing.

The encoder is chosen later and renders its own command: the QSV full-GPU scale is
`scale_qsv=w=trunc(H*a/2)*2:h=H` (`plan.go:460-465`), which *truncates* where `ScaledWidth` *rounds*.
For 1920x1080 → 480p the prediction is 854 and QSV produces 852 (also 1280x720 and 3840x2160 → 480p,
and 1920x816 → 480p gives 1130 vs 1128; I computed both formulas for a dozen sizes). The check then
says "the picture size is 854x480 in the original but 852x480 in the new file" and keeps the
original. That is job 48. The truncating expression was added on 2026-11-20 by `0def124b` to fix a
different QSV failure (`w=-2` is refused by `scale_qsv`), and `Expect` was not told.

**Bugs it produced (commits on `dev`):**

| Commit / PR | Date | What was patched |
|---|---|---|
| job 48 (no fix at `294e3d88`) | 11-20 | width 854 predicted, 852 produced, original kept |
| `0def124b` | 11-20 | QSV scale expression; created the mismatch above |
| `5e683db3` (#498) | 11-20 | "a 10-bit source needs the encoder's 10-bit test, HDR or not": an 8-bit card produced 8-bit, `Expect.BitDepth` said 10, wasted encode then refused |
| `e1e433a9` (#507) | 11-20 | add `main10`/`p010` flags so a graphics encoder keeps 10-bit SDR |
| `a24894e7` | 11-20 | the "converting to … 10-bit" words must name the depth of the encoder that runs, not the plan's |
| `a7187734` (#483, R4), `75fe85b0` (#490) | 11-20 | decide HDR GPU vs processor before the encode; `hdrCarriers` list of families "proven" to keep mastering data (`rules/encoder.go:11-14`) |
| `589ef98b` (#480, R3) then `a1a0adb6` | 11-20 | predicted audio length and offset; faithful tracks failed; the same day the length check was narrowed to the original track only |
| `176f33f8`, `3415a1f6`, `2554b955`, `82451e5a`, `51357203`, `c59ca7c8` | 11-19/20 | the predicted frame count vs what ffmpeg outputs: mp4 edit lists, container vs stream duration, leading undecodable pictures (three iterations in one day) |
| `f94da767` (#481, P1) | 11-20 | verify read the file twice because the count had to be re-measured against the prediction |

**Why the checks cannot tell "different" from "broken".** `Compare` returns sentences; `verify`
wraps them all in one `KeptError` with code `kept_original`; `jobs.FailureCode` maps that to
`check_failed` whose user hint is "Pick a higher quality on the Convert step, or lower the check"
(`jobs/failure_codes.go:133-135`). For job 48 that hint is wrong and the retry policy (`retryIn`,
`applyFailure`) treats it like a damaged output.

**Structural fix.** Invert the contract: the *chosen* encoder renders both the command and the
expectation, and owns the tolerances that come from its own behaviour (even-width rounding to within
2 px of the aspect ratio, the depth it actually encodes, the container's alias name, the exact HDR
fields it passes through). Verify compares the output with that encoder-rendered expectation, and
the shape check becomes "invariants of the intent" (same aspect ratio within 2 px; height as asked;
depth ≥ the plan's; every intended track present with its language and channels) rather than
"equals the predicted integers". Where an encoder-caused difference is acceptable it is normalised
in the expectation, not refused by the check.

### RC2. Encoder knowledge is scattered, and the remote worker has its own copy

**Mechanism.** The answer to "which encoder, with what flags, and when to fall back" is computed in
these places at `294e3d88`:

| Concern | Sites |
|---|---|
| is this file 10-bit | `rules/plan.go:166-167` (`TenBit` set in a `defer`), `rules/plan.go:336` (`EncodeTenBit`), `worker/live.go:127` (`tenBitSource`), `remote/work.go:513-518` (`need10` recomputed from the probe), `web/v2workers.go:1194` (`candidateReject`, the log's reasons), `doctor/doctor.go:81` (`has10Bit`: which encoders get the 10-bit test) |
| which encoder for this job | `rules/encoder.go:51` (`EncoderFor`), `web/v2workers.go:325` (`primaryEncoder`), `web/v2workers.go:289` (`remoteMainEncoder`), `web/v2workers.go:1114` (`LocalEncoderFor`), `remote/work.go:507` (`encoderArgs`), `worker/worker.go:2159` (`graphicsArgs`, decides full vs hybrid) |
| per-family argument shapes | `rules/plan.go:342-443` (four `switch fam` sites plus VAAPI special cases at 397, 406, 418), `plan.go:460-487` (`gpuScale`, `gpuQuality`), `doctor/doctor.go:422-474` (`decodeArgs`, `encodeArgs`: a third rendering of the same device setup) |
| fallback ladder | `worker/worker.go:1172-1201` (full → hybrid → processor, local) and `remote/work.go:478-495` (hybrid → processor, remote), both keyed on `jobs.FallsBackToProcessor` string matching (`failure_codes.go:33-54`) |
| which families keep HDR metadata | `rules/encoder.go:14` hard-coded list, proven on one machine |

**Bugs it produced:** `#498`, `#507`, `a24894e7` (three fixes to the same 10-bit question in three
places); `0def124b` (QSV scale); `ddd0659a` (the full rung must step down on any decode-side
failure, because the string table could not tell a card's decode failure from a file's); `38048495`
(count a fallback only after the processor succeeds); `0c2c3d75` (keep the staged file on a GPU
retry); `32fcd26a` and `#448` (VAAPI device choice in Docker); `d66543b6` (log why a format's GPU
encoder was not used); `4f105f53` (remote worker's own GPU path, the duplicate ladder).

**Structural fix.** One package (`internal/encode`, name indicative) with one interface per
family: `Family{Name, Test(ctx) caps, Decodes(codec, depth), Render(spec) argv, Expect(spec, src),
Fallback(err, stderr) next}` implemented for cpu-x265/x264/svt/vpx, qsv, vaapi, nvenc, amf,
videotoolbox. The doctor's test encodes, the worker's argument rendering, the expectation (RC1), the
fallback ladder and the Doctor page's words all read the same table. The remote worker imports the
package and runs the same ladder; `remote/work.go` keeps only transport.

### RC3. Decide at scan time, run against whatever the flow is now

**Mechanism.** `Template.Compile` runs at six call sites (`scanner.go:355`, `worker.go:917`,
`rules/branch.go:222`, `rules/plan.go:150`, `preview/preview.go:233`, `preview/sample.go:36`). The
scanner records only a sentence and a code in `files.decision`/`files.reason`; the job row copies
`DurationSec` and `SizeBefore` (`scanner.go:365`); the worker compiles again from `TemplateFor`,
which is the library's *current* flow (`libraries.go:533-558`), with the person's *current*
languages injected (`libraries.go:169`). The workflow itself is stored twice: `flows.template` (the
engine's) and `workflow_revisions.doc` (the editor's), translated by `ToTemplate`/`FromTemplate`
(`workflow/preset.go:17, 139`), and four migrations rewrite templates in SQL (`migrate.go` steps 16, 17,
19, 38) while four (13, 16, 17, 19) delete `files` rows to force re-decisions.

**Bugs it produced:** worker-side re-skips bolted on because a queued job may no longer match its
flow: `worker.go:928-952` carries review notes `a09f6f5`, `7ab8f72`, `6cafc6f` (Dolby Vision, HDR
target, "setting cannot run"); `#453` (Convert again after a workflow change) and migration 51
(`workflow_rev` stamped so "earlier" can be told); `#471` (Convert again uses the current folder);
`#502` (the worker probed again what the scanner had probed, because nothing linked the job to its
probe); `#442`/`#431` (a restored original counted as converted); the `files.decision` text being
parsed by `LIKE` in migrations 7 and 13 (`migrate.go:107-121, 159-162`) and in the overview's
`latestEndsSQL` (`overview.go:86-90`).

**Structural fix.** A job references, by id, the workflow revision it was queued under and the probe
it was decided on; the worker compiles *that* revision (a republish explicitly requeues, which the
code already half-does by forgetting files). `files` is split into an observation (what the scanner
saw) and a decision (what was decided, by which revision, with a code only, never a sentence to be
parsed later). The workflow has one canonical form (the doc); the template is a derived cache keyed
by `logic_hash`, never edited by SQL.

### RC4. Timers and text matching stand in for ffmpeg's own signals

**Mechanism.** The encode runs at `-v error` with `-progress` (`plan.go:345`), so the only signals
are `out_time_us`, `frame`, `total_size` and whatever error text appears. On that the code builds:

- a stall clock: 10 min without `out_time_us` moving (`DefaultStallAfter`), a 1-hour allowance once
  the position is within 2 s of the duration (`DefaultFinishAfter`, `finishTolerance`, `reachedEnd`,
  `worker.go:229-243, 738-741`), a 30 s warning and a 60 s "quiet" warning;
- damage detection: `benignRe`, `fileDamageRe`, `pictureDamageRe`, `brokenSliceRe`
  (`verify.go:703-803`), plus "lost frames" arithmetic that needs `LeadingUndecodable`
  (`media.go:277-312`), `sourceFrameCount`'s three-way choice and a 1 % threshold;
- failure classification and the fallback decision: `classifyKeyLines` string tables
  (`failure_codes.go:33-54`), `FallsBackToProcessor`, the benign table (`jobs/benign.go`), the
  Opus parser regex and audio timing filter (`verify.go:260`, `playback_audio.go:71-96`);
- 35 regular expressions in `worker`, `jobs` and `media` in all.

Meanwhile the code *already* parses ffmpeg's structured end-of-run report (`media.InputStreamStats`,
`media/stats.go`: packets read, frames decoded, decode errors per input stream) but only for the
playback check, because the encode runs without `-loglevel level+verbose`.

**Bugs it produced:** the damage chain `e24e6fe2` (3 pictures), `f3a676bf` (1 %), `6266fc6d`,
`52ba74ac`, `f7b23610`, `3415a1f6`, `176f33f8`, `084c540a`, `2554b955`, `82451e5a`, `51357203`,
`c59ca7c8` (twelve commits between 11-18 and 11-20); playback false alarms `075256c8` (AAC priming),
`406198e0`, `727b2a18`, the Opus regex; `#508` with `55c41c83` and `950e20be` on the same day (the
finishing allowance, then bounding it); SSIM alignment `efe55779`, `1186b39d`, `26b44723`,
`ff4f5ca9`.

**Structural fix.** Run the encode with `-loglevel level+verbose -progress`, read `progress=end`
as the finishing signal (not a time window), read `InputStreamStats` for decoded frames and decode
errors (no regex arithmetic), and key the stall clock on *any* of `out_time_us`, `total_size` or
stderr lines advancing. Keep a small regex set only as a fallback for ffmpeg versions without the
report. Classify failures from the encoder abstraction (RC2), which knows what its own errors look
like, instead of a global string table.

### RC5. One verdict for three different situations

**Mechanism.** `KeptError` has one code (`verify.go:75-77`); `checkFailed` (a timeout or a tool
crash) is also `kept` plus retryable (`verify.go:55-62`); `sizeRule` returns a `SkippedError`
(`keep.go:259`); `keptVBRWords` rewrites the reason after the fact when the plan had a VBR limit
(`worker.go:1299-1303`); `applyFailure` chooses the workflow's `Check` policy for every `KeptError`
(`worker.go:436`); the Jobs page maps `kept_original` to `check_failed` with a quality hint
(`failure_codes.go:25-26, 133-135`).

**Bugs it produced:** job 48's user-facing sentence; `#495` (a bigger file must end "skipped", not
"failed"); `de2f6647` (a kept job must not get a failure summary); `a6772799`; the `a772397` review
items on VBR words; `#512`/`bec80900` (stale track statistics copied into the output were read back
as the output's figures).

**Structural fix.** Typed verdicts from verify: `Mismatch{field, planned, measured}` (the output
differs from the spec), `Damaged{where, evidence}` (the output does not play), `Unmeasurable{check,
cause}` (a tool failed or timed out), `SourceDamaged`, `Bigger`. Each has its own reason code, user
sentence, retry policy and workflow port. "Kept" stays the job status; the code says why.

### RC6. Savings and counts are reconstructed from scattered columns

**Mechanism.** `jobs/freed.go:40-74` defines `freedSQL`, `waitingSQL`, `goneSQL`, `couldFreeSQL`,
`savedSQL`, `grewSQL`, `grewNowSQL`, `leftNowSQL`, `freedAtSQL` over `jobs.size_before/after`,
`output_path`, `original_gone_at`, `original_changed`, `output_gone_at`, `originals.state`, a
"newest conversion per path" subquery and a "not restored" subquery; `files.size` deliberately keeps
the original's size after a replace (`scanner.go:320-331`, `overview.go:17-19`) so `BytesNow`
(`overview.go:51-53`) can add and subtract five figures; the scanner sets the gone/back marks each
pass; the live projected size uses a 16 MiB/3 % heuristic (`live.go:47-53`).

**Bugs it produced:** `#442`, `#431` (restored originals), `#403` (left outputs), review `ab99630`
(per-library reads), `d10d17e` (double counting after republish), `#459`/`9ba29057` (estimate),
`21777980` (own-output match), `#421` (overview moved to the background because the SQL got slow).

**Structural fix.** An append-only ledger of file-size facts (`observed`, `replaced`, `kept`,
`purged`, `restored`, `gone`, `changed`) written at the moment each fact is learned, and totals
computed from the ledger. The current SQL stays behind the same API until the two agree on the
existing databases.

### RC7. One 650-line function and a 20-callback worker

`Worker.process` is `worker.go:811-1459`; the `Worker` struct has 30 fields of which a dozen are
callbacks injected in `main.go:313-337`; the `Queue` interface has 30 methods (`worker.go:36-96`).
Not a bug source by itself, but every root cause above is a branch inside this function, which is why
48 commits since 11-13 touch the file and why the remote path (`RunRemote`) threads through it with
flags (`w.remote`, `renew`). The fix is structural only (stages with explicit inputs and outputs) and
should ride along with slices 1-3 rather than be its own project.

---

## 4. Target design

```mermaid
flowchart LR
  I[Intent<br/>workflow revision id + probe id<br/>stored on the job at enqueue] --> S[Spec<br/>encoder.Render(intent, source, caps)<br/>= argv + Expectation with the encoder's tolerances]
  S --> R[Run<br/>ffmpeg with structured progress and stats<br/>progress=end, InputStreamStats]
  R --> M[Measure<br/>probe + stream stats + SSIM<br/>pure functions over the output]
  M --> V[Verdict<br/>OK | Mismatch | Damaged | Unmeasurable | SourceDamaged | Bigger]
  V --> C[Commit<br/>unchanged: staging, journal, swap, sidecars]
  V -->|Mismatch on an encoder-owned field| S
  V -->|encoder failure| F[encoder.Fallback → next Spec]
```

1. **Intent** is immutable per job: the workflow revision, the probe, the output policy. A changed
   workflow requeues; it never changes a queued job's meaning.
2. **Spec** is rendered by the chosen encoder family from one table. The same object yields the
   command, the expectation and the words the log shows. The remote worker receives a Spec, not a
   Plan plus its own re-derivation.
3. **Run** reads ffmpeg's own signals. The stall clock asks "did anything advance", and finishing is
   `progress=end`, not a time window.
4. **Measure** produces facts (streams, sizes, decoded frames, decode errors, SSIM per place) without
   judging them.
5. **Verdict** is a typed comparison of facts against the Spec's expectation; each kind has one
   code, one sentence, one retry rule and one workflow port.
6. **Commit** is the existing code.
7. **Stats** read a ledger of facts written by scan, commit, purge, restore and the scanner's
   presence checks.

---

## 5. Migration plan, in slices that keep `dev` working

Sizes: S ≈ a day, M ≈ two to three days, L ≈ a week, for one builder with the existing tests. Order
matters for the first four; the rest can interleave with feature work.

| # | Slice | Size | What changes | What proves it |
|---|---|---|---|---|
| 1 | **Extract the encoder table.** Move `ffmpegArgs`'s family switches, `gpuScale`, `gpuQuality`, `withoutSoftwareParams`, `EncoderFor`, `GraphicsFamily/GraphicsEncoder`, `FallsBackToProcessor`'s encoder cases and the doctor's `encodeArgs`/`decodeArgs` into one package with one type per family. Byte-identical argv. | M | `rules/plan.go`, `rules/encoder.go`, `doctor/doctor.go`, `jobs/failure_codes.go` | the existing arg tests become golden tests over the new package; no behaviour change |
| 2 | **Expectation from the rendered spec.** Compute `Expect` *after* the encoder is chosen, from the family's `Expect(spec, src)`; the family declares width/height rounding and depth. Delete the pre-choice prediction at `worker.go:1006`; keep the snapshot but store the post-choice expectation. | S | `worker/worker.go`, `rules/plan.go` (`Expect`), `rules/expect.go` (an aspect tolerance) | job 48 passes; a test per family for 1920x1080 → 480p; `#498`/`#507` cases become table rows |
| 3 | **Typed verdicts.** `verify` returns a verdict type; `KeptError` carries a kind and code; `FailureCode`/`FailureWords` map each kind; `applyFailure` and `retryIn` branch on the kind; `keptVBRWords` and `sizeRule`'s `SkippedError` fold into kinds. | M | `worker/verify.go`, `worker/worker.go`, `jobs/failure_codes.go`, `web/v2failure.go`, UI words | every existing `kept` test asserts a kind; new user sentences reviewed |
| 4 | **Remote worker on the shared package.** `remote.encoderArgs` and its fallback loop are replaced by the package; the server sends a Spec; `CheckResult` stays. | S-M | `remote/work.go`, `web/workerremote.go` | the remote tests; one real remote run per family available |
| 5 | **Structured ffmpeg signals.** Encode with `-loglevel level+verbose`; `recordEncode` keeps error-level lines for the log as now; damage from `InputStreamStats` first, regexes as fallback; `progress=end` ends the stall clock; `FinishAfter` becomes a hard ceiling only. | M | `worker/worker.go`, `worker/verify.go` (damage), `media/media.go` | the damage test files from the 11-18..20 chain; a long-MP4 `+faststart` test |
| 6 | **Intent pinned at enqueue.** Job rows get `workflow_rev` (exists) and `probe_id`; `TemplateFor` reads the revision's template; a publish requeues affected queued jobs explicitly; the worker-side re-skips at `worker.go:928-952` become one "intent no longer valid" path. | M | `scanner`, `jobs/libraries.go`, `jobs/workflows.go`, `worker/worker.go` | migration test; "edit flow while queued" test |
| 7 | **Stage the worker.** Split `process` into `prepare`, `plan`, `encode`, `verify`, `commit` with explicit structs; `RunRemote` becomes a different `encode` stage, not flags. No behaviour change. | M | `worker/*.go` | existing worker tests unchanged |
| 8 | **One workflow representation.** The doc is canonical; `flows.template` becomes a cache column keyed by `logic_hash`; migrations that rewrote templates are replaced by doc migrations going forward (old steps stay as history). | M | `jobs/workflows.go`, `jobs/libraries.go`, `web/setting.go` (v1 writes go through `FromTemplate`) | revision tests; v1 setting save round-trips |
| 9 | **Stats ledger.** `file_facts` table written at scan/commit/purge/restore/gone; totals from it; old SQL kept behind a flag until a parity test over real databases passes. | L | `jobs/freed.go`, `overview.go`, `scanner.go`, `worker/keep.go` | parity on the existing backups; the `#442`/`#431`/`#403` cases |

Slices 1-2 are the smallest change that removes the class of bug the user asked about. Slices 3-5
remove most of the patch stream. Slices 6-9 are hygiene the patch stream will otherwise keep paying
for.

---

## 6. What not to rewrite, and why

- **The swap and the journal** (`worker/keep.go`, `RecoverSwaps`, `renameNoReplace`,
  `tempfile.go`'s staging and sync, `restore.go`, the sidecar bookkeeping): correct, tested, and the
  part users must never lose. Slice 7 moves it into a `commit` stage unchanged.
- **The store and its migrations** (`internal/jobs`): the schema is sane, the migration discipline
  is sound (append-only, backed up, one transaction). Slices 6, 8 and 9 add tables and columns; they
  do not restructure.
- **The scanner's guards and two-look rule**: careful and correct. Only the decision record changes
  shape (slice 6).
- **Verify's checks themselves**: the playback test, the SSIM alignment (seek margin, timestamp
  alignment, ±1-frame nudges), the HDR field checks. They are the *measurement*; what changes is who
  sets the expectation and what the result is called.
- **The API surface and the SPA**: v1 and v2 routes, the SSE stream, the job log model. New verdict
  codes are additive.
- **The remote worker's transport** (pairing, leases, resumable upload, path mappings): keep; only
  its encoder logic moves (slice 4).
- **Shares, helpers, update, backups, notifications, integrations**: untouched by this review.
- **`media.ParseProbe` and the probe cache**: keep; slice 6 only references probes by id.

---

## 7. Test matrix: what the root causes predict

No `FINDINGS.md` had landed when this draft was written (checked at 19:02 UTC); this section maps
the planned runs in the matrix to the causes above so the findings can be checked against it when
they land.

| Runs | Prediction from this review | Root cause |
|---|---|---|
| 480p on the GPU with QSV (runs 10, 12, 19, 32: HEVC/H.264 at 480p) | original kept, "picture size 854x480 vs 852x480" | RC1 (the job-48 mechanism); the same four runs would pass on the processor |
| 480p on the processor (runs 3, 11, 26, 34: VP9/AV1) | pass; `scale=-2:480` rounds like `ScaledWidth` | RC1 confirms the asymmetry |
| run 36 (H.264 keep, as-is audio, MKV) | skipped `already_in_format` | RC3's scan-time decision works as intended |
| H.264 keep/1080 with quality on the GPU (runs 1, 24, 25) | the video is *copied* only when nothing scales and no cap is set (`plan.go:897`); run 1 has a VBR cap so it re-encodes on `h264_qsv`; run 24 copies | the remux rule is in the compiler, not the encoder table (RC2) |
| VP9 with a bitrate level (runs 7, 22, 23, 28, 29, 33, 35) | `vp9Capped` rewrites `-b:v` to the cap (`plan.go:1297-1312`); outcome depends on libvpx accepting constrained quality with `-crf` | RC2: a per-encoder special case living in the generic renderer; **UNVERIFIED** outcome |
| quality 4-5 runs | may come out bigger; `ifBigger keep` ends them *skipped* with `new_file_bigger` | RC5 (the verdict kind exists only as a special case) |
| MP4 outputs with 37 text subtitles (runs 2, 10, 19, 31) | `mov_text` conversion; `Expect` sets `CheckDefault` only for non-`mov_text` (`plan.go:718-720`) | RC1: a per-container exception inside the prediction |
| long runs on the GPU | watch the finishing window: an MP4 output rewrites for `+faststart` after `progress` stops moving | RC4 |

When the findings land, each row above either confirms its cause or is evidence against this review;
both are useful.

---

## 8. Open questions and UNVERIFIED items

- Whether the VAAPI full-GPU path rounds width like QSV: `scale_vaapi` is given `w=-2`
  (`plan.go:461, 470`), so probably not; **UNVERIFIED** on hardware.
- NVENC, AMF and VideoToolbox never get the full-GPU shape (`plan.go:321`); their 10-bit SDR
  behaviour with `-pix_fmt p010le` is **UNVERIFIED**.
- Whether `Publish` forgets file decisions for every library on the flow (the v1 `SaveTemplate`
  path does; `Publish` at `jobs/workflows.go:275` was not read).
- The manager's count of "about 300 fixes" versus my keyword count of 165: the difference is
  classification, not disagreement; both say the same thing about the trend.
