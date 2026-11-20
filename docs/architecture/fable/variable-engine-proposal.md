# shrink-your-shit: a variable-driven engine (proposal)

A proposal for restructuring the job engine around variables. Our side is `origin/dev` at `294e3d88`,
cited as `path:line`. It builds on `app-map.md` and `foundation-review.md` in this folder.
**UNVERIFIED** marks what could not be confirmed. (A section of study notes was removed from this public
copy.)

---

## 1. Summary for the user

**What our app would look like restructured around variables.** The scanner would still find and probe files
and the swap would still be ours. What changes is the middle: a job carries a *run table* of named
values (the probe's facts, the workflow's settings, the worker's capabilities, then every decision and
measurement as it is made), and a *command model* (streams with operations) that steps edit. "Convert
video" would no longer compute fixed flags; it would publish intent variables (format, quality, speed,
max height). A separate "choose encoder" step would read the intent, the source facts and the worker's
capability table, and publish the chosen encoder profile. The *profile*, one per encoder family, renders
the ffmpeg flags from variables, declares what it will produce (width rounding, bit depth) and what
counts as its own failure. The check step compares measurements with that declaration and publishes a
typed verdict. Everything the run decided is on the job as variables with "set by", so the log, the
remote worker and the Jobs page read the same facts.

**What we already have.** The workflow document with variables and the `{{name}}` resolver for four
text fields (#497, #504), the per-step job log with decisions (L1-L3), the encoder ladder with a
one-hour pause (P4), audio track titles (#506), the size rule (R8). Those are the first third of this
design. What is missing is the run table as a first-class object that steps read *and write*, encoder
settings that come from variables through a profile instead of from a 100-line argument function, an
expectation rendered by the chosen profile, and typed verdicts.

**What fixing it buys.** The width-rounding refusal (job 48), the 10-bit chain and the frame-count
chain in the foundation review all come from the plan predicting what the encoder decides. In this
design the encoder profile is the only thing that says what it will produce, and it says it *after* it
is chosen. The remote worker stops carrying a copy of the encoder logic. The log, which today reads
decisions back out of the code's control flow, becomes a dump of the run table.

---

## 3. The gap against our app at `294e3d88`

| Capability | Ours | Status | Evidence |
|---|---|---|---|
| A run table of file/library/tool facts, dumped at start | `vars.Source.Values()` computes 29 built-in names per job; the worker logs them in a `variables` section with start and end values and "set by" (`worker/variables.go:22-32, 136-157`); `rules.Snapshot.Variables` stores them on the job (`rules/plan.go:772, 778-784`). | **have, read-only** | values are computed once from the probe and plan; no step can add a name; only `target_codec` has a "set by" (`worker/variables.go:53`) |
| Steps read variables from the table | Only four text fields resolve `{{name}}`: output name, folder, .srt name, track title (`worker/worker.go:990-1072`, `vars.OutputName/Folder/SrtNames/Resolve`). Every other step config is a literal (`workflow/preset.go:175-255` reads literals only). | **partial** | `workflow.Variable` exists with type/value/options/`TemplateParam` (`workflow/schema.go:66-75`) but no config field of a convert/audio/subtitle/container step takes a reference |
| Steps *publish* variables for later steps | No mechanism. Decisions are `rules.StepDecision` sentences (`rules/steps.go:18-22`) written to the log, not values another step can read. | **missing** | `Plan.Steps` are words (`rules/plan.go:38`) |
| Variable scopes: global, library, workflow, step | Workflow scope only (`Doc.Variables`). Languages are a global setting injected into every template (`jobs/libraries.go:169`), the one "global variable" we have, by hand. | **partial** | the team's workflow-architecture W2/W3 plan the other scopes; not merged |
| A shared command model with per-stream operations and "changed" marks | `rules.Plan` is close: `Video VideoEncode`, `Tracks []TrackOp` (drop/convert/add/extract per stream), `Container`, `OutputDir`, `SrtNames` (`rules/plan.go:21-69`, `rules/audio.go:17-38`). But it is produced by one function (`compileSelectors`, `plan.go:826-996`) in one pass, not built up by steps, and `ffmpegArgs` reads it in one pass (`plan.go:342-443`). | **partial** | the model exists; the step-by-step editing of it does not |
| Encoder chosen by test encodes in order, recorded on the stream | Capability tests at startup per encoder, 8-bit and 10-bit (`doctor/doctor.go:242-270, 348-361`), results in memory and `encoder_tests`; per job `rules.EncoderFor` picks from the worker's main encoder (`rules/encoder.go:51-80`); the choice is logged (`web/v2workers.go:1130-1188`). | **have, better** (once per worker, not per file) | but the choice happens *after* `Expect` is built (`worker/worker.go:1006` vs `1113`) |
| Decoder chosen from the encoder family and available filters; pixel format decided there | `graphicsArgs` asks the doctor whether the family decodes this codec at this depth (`worker/worker.go:2159-2173`, `doctor/doctor.go:368-417`), then `FFmpegArgsFullGPU` or hybrid (`rules/plan.go:304-325`); pixel formats are hard-coded per family inside `ffmpegArgs` (`plan.go:388-424`). | **have** | spread over three files; not recorded as values |
| Encoder flags built from variables through a per-family profile | None. Flags are `switch` branches in `ffmpegArgs` and `gpuQuality` (`plan.go:370-384, 475-487`); the doctor's test flags are a second copy (`doctor.go:453-474`); the remote worker a third decision path (`remote/work.go:507-528`). | **missing** | foundation review RC2 |
| Per-stream argument templates with index placeholders resolved at build time | `trackArgs` computes output indexes by walking `AudioOrder`/`SubtitleOrder` (`plan.go:531-600`). Same effect, done in code. | **have** | |
| Analysis passes that publish measurements (loudness, crop) | None. No loudness or crop step. The frame-count and first-keyframe reads are analysis passes, but their results are local variables (`worker/worker.go:903-913, 1249-1260`). | **missing** | team's F4/F5 are planned, not merged |
| Numbered exits per step, wired in a graph | Registry ports exist: success, skipped, error, yes/no (`workflow/registry.go:63-70`); only checks' and converts' error ports are honoured (`workflow/branches.go:57-61`, `not_runnable_yet`); the compile demands the fixed preset order (`workflow/preset.go:156-158`). | **partial** | |
| Fallback as a graph branch | Ours is a ladder inside the job: full GPU → hybrid → processor, one verify, one save (`worker/worker.go:1172-1201`). | **different, better** |one verify and one save |
| Measured values written into the final command (loudness) | None. | **missing** | |
| Re-probe of the output, facts replace the source's | `verify` probes the output and keeps it (`worker/verify.go:154-163`); stored for the Files view (`worker.go:486-490`). Source facts stay as the job's "before" (`Snapshot.Source`). | **have, better** (both kept) | |
| Per-step log with model, decision, exit, duration | Sections per step with one decision line (`writeStepSections`, `worker/joblog.go:51-67`), run steps with timings (`jobs.RunStep`), summary with passes/bytes/slowest (L2), encoder candidates (L3). | **have** | the model is not printed because there is no model object to print |
| Replace gated by size only | Ours: shape, playback, frame count, SSIM, source unchanged, then the size rule (`verify.go:144-201`, `keep.go:245-266`). | **different, better** | |

**Already done from the merged work.** Variables API and name rules (#497), editor cards (#500, #503),
templates in four fields with provenance in the log (#504), track titles (#506), step sections and
summary counters (L1-L3), the encoder ladder and decode probe (P4), the 10-bit gate (#498, #507), stale
statistics cleared (#512). **Not done:** steps reading typed variables in non-text fields; steps
publishing variables; global and library scopes; encoder profiles; a run-time command model; measured
values; typed verdicts.

---

## 4. The proposed design

### 4.1 The variable model

**Scopes, from most to least specific.**

| Scope | Lives in | Written by | Read by | Exists today |
|---|---|---|---|---|
| `run` | the job: `rules.Snapshot.Variables` extended with `SetBy` and `Scope`; in memory as `RunState.Vars` | the engine and steps: probe facts, decisions, encoder choice, measurements, outputs | every later step, the log, the Jobs page, the remote worker | partly (`RunVariable{Name, Start, End, ChangedBy}` `rules/plan.go:778-784`) |
| `step` | a node's config literal | the person, in the step form | that step | yes |
| `workflow` | `Doc.Variables` | the person, in the Variables card | any step of that workflow via `{{name}}` | yes (#497, #504) |
| `library` | a `variables` JSON column on `libraries` | the person, on the library | workflows that mark the variable `templateParam` | no (team W3) |
| `global` | `app_meta.settings` key `workflow_variables` | Settings > Workflows | any workflow | no (team W2); `languages` is the hand-made precedent |

**Resolution order for a name read by a step:** `run` (if a step already published it) → the step's
own literal → `workflow` → `library` (only for `templateParam` names) → `global`. A name nobody
defines is the existing `missing_variable` issue at validate and publish; at run time it resolves to
empty and is listed once in the log (the rule `vars.Resolve` already follows, `vars/vars.go:151-180`).

**Names.** Keep `workflow.NamePattern` (`workflow/variables.go:18`: lowercase, digits, underscore, 40
chars). Reserved prefixes the person cannot define: `source_`, `encode_`, `measure_`, `check_`,
`output_` (plus today's flat built-ins, kept as they are for compatibility, `workflow/variables.go:31-62`).

**Types.** Today's `text | number | boolean | choice | secret` (`workflow.Variable.Type`) plus `list`
(languages, codecs). A typed config field accepts a literal or exactly one reference; the resolved value
is validated against the field's schema like a literal (`workflow.checkConfig`, `workflow/validate.go:182-219`).
String interpolation stays limited to text fields, as #504 does.

**Defaults.** A variable's declared `Value` is its default; a library or global value overrides it only
when `TemplateParam` is true (field exists, unused: `workflow/schema.go:74`).

**Secrets.** Never published into `run`; never resolvable into a command argument; resolvable only in
fields a step definition marks as a secret sink (none of the engine's steps today; integrations later).
The log shows the name and "Hidden" (`worker/variables.go:140-143` already does).

**Provenance.** Every `run` value carries `SetBy` (step id or `engine`) and `Scope`. The log's variables
section prints `name = value (set by Step 3: Convert video)`, generalising today's single `changedByStep`.

### 4.2 Encoder settings as variables

**Intent variables**, published by the Convert step from its config (today's `Selectors` fields): `video_format`
(hevc/h264/av1/vp9), `video_quality` (1-5), `video_speed`, `video_max_height`, `video_rate_control`,
`video_level_kbps`, `video_peak_kbps`, `video_crf` (the derived CRF the slider table gives,
`rules/plan.go:855-861`), `hardware_preference` (auto / hardware_only / cpu_only; the team's W7).

**Source variables**, published by the Probe step: `source_video_codec`, `source_width`, `source_height`,
`source_bit_depth`, `source_hdr` (none/hdr10/hlg/dv), `source_mastering` (boolean), `source_cll`
(boolean), `source_fps`, `source_duration`, `source_frames` and `source_frames_exact`, `source_rotation`,
per-track lists for audio and subtitles.

**Capability variables**, published by the engine from the worker's capability record:
`worker_encoders` (passed list), `worker_encoders_10bit`, `worker_decodes` (family×codec×depth),
`worker_hdr_carriers`, `worker_main_encoder`, `worker_encoder_paused_until`.

**Encoder profiles.** One table in a new package (`internal/encode`, name indicative), one entry per
family, each with:

| Field | Content | Today's source |
|---|---|---|
| `Family` | cpu_x265, cpu_x264, cpu_svtav1, cpu_vpx, qsv, vaapi, nvenc, amf, videotoolbox | `rules/plan.go:261-294` |
| `Test` | the one-frame test recipe, 8-bit and 10-bit | `doctor/doctor.go:453-474` |
| `Decodes(codec, depth)` | whether this family's hardware decoder can take the source | `doctor/doctor.go:368-417` |
| `Args(spec)` | the argument template: device init, hwaccel, codec flag, quality flag, preset, pixel format, scale filter, upload filter; every value a `{{encode_*}}` or `{{video_*}}` reference | `rules/plan.go:342-443, 460-487` |
| `Produces(spec, source)` | the expectation it commits to: width rounding (nearest even vs truncation), bit depth produced, HDR fields carried, container names | today scattered: `ScaledWidth` `plan.go:1061`, `EncodeTenBit` `plan.go:336`, `hdrCarriers` `encoder.go:14` |
| `OwnFailure(stderr, exit)` | which failures are the encoder's (fall back) vs the file's or the disk's | `jobs/failure_codes.go:33-54`, `worker/worker.go:2180-2199` |
| `Rungs(spec, caps)` | the ladder this family offers: full GPU, hybrid, none | `worker/worker.go:2159-2173` |

**The Choose-encoder step** (new, explicit, always on, zero duration) reads intent + source +
capability variables and publishes: `encode_family`, `encode_encoder` (ffmpeg's name or the CPU
encoder), `encode_decoder` (family or `software`), `encode_rung` (full / hybrid / cpu), `encode_ten_bit`,
`encode_hdr` (carried / dropped / none), `encode_quality_flag` (the family's own quality value),
`encode_reason` (one sentence: why this one, why not the others; today's candidate lines,
`web/v2workers.go:1130-1188`). The rules are today's `rules.EncoderFor` plus the ladder, moved into the
profile table and reading variables.

**QSV / VAAPI / NVENC / AMF / CPU choices and fallbacks.** The step builds an ordered list of rungs from
`hardware_preference` and the worker's capabilities: `[qsv full, qsv hybrid, cpu]` for a QSV worker
that decodes this codec; `[nvenc hybrid, cpu]` for NVENC; `[cpu]` for cpu_only. The Run step publishes
`measure_exit_code`, `measure_stderr_class` and, on an encoder-owned failure, the engine re-enters Choose
with `encode_rung` advanced; the same Verify and Save steps run whichever rung produced the file. This
keeps the fallback *inside* the job (our approach) while making each rung visible as values. The
three-failures pause stays as a capability variable (`worker_encoder_paused_until`).

**10-bit and HDR.** `source_bit_depth` and `source_hdr` come from the probe once. The profile's
`Test` says whether 10-bit passed; `Produces` says what depth comes out; `encode_ten_bit` and
`encode_hdr` are published once, by Choose. Nothing else in the code decides depth. The "converting to
10-bit" words, the Expect's depth and the remote worker's `need10` all read the same variable.

### 4.3 The step contract

```go
// Step is one node of the workflow at run time. Pure except through RunState.
type Step interface {
    Reads() []string                // variable names it reads (validation checks they exist by then)
    Publishes() []string            // variable names it may set (reserved prefixes only for engine steps)
    Apply(ctx, cfg Config, st *RunState) Outcome
}

type RunState struct {
    Vars  VarTable        // run-scope values with SetBy and Scope
    Model CommandModel    // streams with ops and "changed", global options, ordered filters (team's L4)
    Facts Measurements    // what analysis and run passes measured
}

type Outcome struct {
    Port     string         // success | skipped | error (| match | else for checks)
    Decision string         // one sentence for the log (today's StepDecision)
    Set      []string       // names published in this call (the log's "Set {{x}} = y" lines)
    Verdict  *Verdict       // Check step only: OK | Mismatch | Damaged | Unmeasurable | SourceDamaged | Bigger
}
```

Steps and what they publish, in the preset order:

| Step | Reads | Publishes | Touches the model |
|---|---|---|---|
| Probe (engine) | path | `source_*` | creates the stream rows |
| Leave some files alone | `source_*`, config | decision only | marks the run skipped |
| Convert video | config, `source_*` | `video_*` intent | video row: op = encode (no flags yet) |
| Audio | config, `source_audio_*`, `languages` | `audio_kept`, `audio_converted`, `audio_dropped`, `audio_added` | audio rows: keep / convert / drop / add |
| Subtitles | config, `source_subtitle_*` | `subtitle_kept`, `subtitle_files` | subtitle rows |
| File type | config, `source_container` | `output_container` | global option |
| Choose encoder (engine) | `video_*`, `source_*`, `worker_*` | `encode_*` | video row: profile + flags |
| Build command (engine) | the model | `encode_command` | renders argv from the profile and the rows |
| Run (engine) | `encode_command` | `measure_*`: exit, frames decoded, decode errors, bytes written, speed, duration | writes the staged file |
| Measure (engine) | the staged file | `measure_*`: probe facts of the output, SSIM per place | reads only |
| Check the result | `measure_*`, the profile's `Produces` | `check_verdict`, `check_reason`, `check_*` per check | verdict |
| Save the result | config, `check_verdict`, `output_*` | `output_path`, `new_size_bytes`, `saved_percent` | the swap (unchanged code) |

The existing `compileSelectors` becomes the first implementation of the five middle steps in one
adapter (the team's W10 idea), so every stored workflow produces the same model on day one.

### 4.4 How this fixes the foundation review's root causes

| Root cause (`foundation-review.md` §3) | What changes |
|---|---|
| RC1 prediction vs measurement | `Expect` is produced by the chosen profile's `Produces` *after* Choose, from `encode_*` variables; the width-rounding and depth cases become table rows per family. Job 48 passes because QSV's profile declares truncation. |
| RC2 scattered encoder knowledge | One profile table; the doctor's tests, the worker's rendering, the expectation, the fallback classification and the remote worker read it. The 10-bit question is answered once, as `encode_ten_bit`. |
| RC3 decide at scan, run against the current flow | The run table is seeded from the job's pinned workflow revision and probe id; the worker never re-reads the live flow. A republish requeues. |
| RC4 timers and text matching | `measure_*` values come from ffmpeg's structured report (frames decoded, decode errors) and `progress=end`; the stall clock reads "did any measurement advance". |
| RC5 one verdict for three situations | `Verdict` is typed; each kind has a code, a sentence, a retry rule and a port; the workflow's error links read the kind. |
| RC6 stats from scattered columns | `output_*` and `measure_*` are facts with provenance; the ledger in the review's slice 9 is their persistence. |
| RC7 the 650-line function | Steps with `Apply` replace the branches of `Worker.process`; the commit stage keeps today's code. |

---

## 5. Updated app map for the new design

```mermaid
flowchart LR
  subgraph Store["store (SQLite)"]
    JOB[(job: revision id, probe id,<br/>run table snapshot, verdict)]
    REV[(workflow_revisions)]
    PROBES[(probes)]
    CAPS[(encoder_tests / workers)]
  end
  SCAN[scanner: find, settle, probe, decide-to-queue] --> JOB
  SCAN --> PROBES
  DOC[doctor: capability tests per family] --> CAPS
  subgraph Worker["worker: one job"]
    RS[RunState<br/>• Vars (run table)<br/>• CommandModel (stream rows + ops + filters)<br/>• Measurements]
    S0[Probe → source_*] --> RS
    S1[Leave alone?] --> RS
    S2[Convert video → video_*] --> RS
    S3[Audio → audio_*] --> RS
    S4[Subtitles → subtitle_*] --> RS
    S5[File type → output_container] --> RS
    S6[Choose encoder → encode_*] --> RS
    S7[Build command → encode_command] --> RS
    S8[Run ffmpeg → measure_*] --> RS
    S9[Measure output → measure_*] --> RS
    S10[Check the result → check_verdict] --> RS
    S11[Save the result → output_*] --> RS
  end
  PROF[encoder profiles<br/>internal/encode: Test, Decodes, Args, Produces, OwnFailure, Rungs]
  REV -- "pinned revision + its variables" --> RS
  PROBES -- "pinned probe" --> RS
  CAPS -- "worker_* variables" --> RS
  PROF --> S6
  PROF --> S7
  PROF --> S10
  S8 -- "encoder-owned failure: next rung" --> S6
  RS -- "snapshot + log sections" --> JOB
  RS -- "the same RunState, serialised" --> RW[remote worker]
  RW --> PROF
  LOG[job log = run table dump + per-step sections] --> UI[Jobs page / SPA]
  JOB --> LOG
  GLOB[(global + library variables)] -- "resolved at job start" --> RS
```

**Plain words.** The scanner still finds files and decides what to queue, but the job now pins the
workflow revision and the probe it was decided on. A worker builds one `RunState` for the job: the run
table seeded from the pinned revision's variables, the global and library scopes, the probe and the
worker's capabilities. Steps run in order and each reads values, edits the command model and publishes
values. The encoder profile table is consulted three times: to choose (with the rungs), to render the
command, and to say what the output must look like. The Run step publishes measurements; Check compares
them with the profile's declaration and publishes a typed verdict; Save is today's swap. The job keeps
the run table as its snapshot; the log is a dump of it plus the per-step sections we already have; the
remote worker receives the same `RunState` and runs the same steps from Choose onward.

**Variable flow through one job**, as a sequence:

```mermaid
sequenceDiagram
  participant E as engine
  participant V as run table
  participant M as command model
  participant P as encoder profiles
  E->>V: seed: file, library, workflow, worker_*, global/library values
  E->>V: probe → source_*
  E->>M: one row per source stream
  Note over V,M: Convert video publishes video_* ; Audio/Subtitles/File type mark rows and publish *_kept/_converted
  E->>P: Choose(video_*, source_*, worker_*)
  P-->>V: encode_family, encode_encoder, encode_decoder, encode_rung, encode_ten_bit, encode_hdr, encode_reason
  E->>P: Args(model, V) → encode_command
  E->>E: run ffmpeg (structured progress and end-of-run stats)
  E->>V: measure_exit, measure_frames_decoded, measure_decode_errors, measure_bytes, measure_speed
  alt encoder-owned failure and a rung is left
    E->>P: Choose again with encode_rung advanced
  end
  E->>V: measure_* of the output (probe, SSIM per place)
  E->>P: Produces(encode_*, source_*) → expectation
  E->>V: check_verdict (OK | Mismatch | Damaged | Unmeasurable | SourceDamaged | Bigger), check_*
  E->>V: output_path, new_size_bytes, saved_percent (Save)
  E->>E: snapshot V on the job; log = V dump + step sections
```

---

## 6. Migration plan

Sizes as in the foundation review (S ≈ a day, M ≈ two to three, L ≈ a week). "FR n" is slice n of
`foundation-review.md` §5; "W n" is the team's `workflow-architecture.md` slice.

| # | Slice | Size | Overlaps | What proves it |
|---|---|---|---|---|
| V1 | **Run table as an object.** Introduce `RunState.Vars` in the worker; seed it from today's `vars.Source`; every existing decision site publishes its value with `SetBy` (skip reason, remux, encoder, tracks, container); `Snapshot.Variables` gains `SetBy`/`Scope`; the log prints the table. No behaviour change. | M | FR 7 (staging the worker) in part; L2/L3 done | log golden; snapshot round-trip |
| V2 | **Typed references in step configs.** Whole-field `{{name}}` in number/enum fields of Convert/Audio/Subtitles/File type, resolved at validate (scope check) and at job start (`run` + workflow scope); `missing_variable` issue. | M | W1 | table tests; a workflow with no references compiles to the same fingerprint |
| V3 | **Encoder profile table.** Move `ffmpegArgs`'s family switches, `gpuScale`, `gpuQuality`, `EncoderFor`, the doctor's test recipes and `FallsBackToProcessor`'s encoder cases into `internal/encode`, one type per family. Byte-identical argv. | M | **FR 1** | the existing arg tests as goldens over the new package |
| V4 | **Choose-encoder as a step publishing `encode_*`.** The worker's choice block (`worker.go:1113-1145`) and `graphicsArgs` become the step; `tenBitSource`, `EncodeTenBit`, `candidateReject` and the remote `need10` read `encode_ten_bit`. | S-M | FR 2 (first half), #498/#507 consolidation | the 10-bit cases as table rows; job log shows `encode_*` |
| V5 | **Expectation from the profile.** `Produces(spec, source)` replaces the pre-choice `plan.Expect` for the video stream's width/height/depth/HDR; `Expect.Compare` gains a 2-px aspect tolerance only where the profile declares it. | S | **FR 2** | job 48's case per family; the matrix's 480p runs |
| V6 | **Typed verdicts as `check_*` variables.** `Verdict` kinds, codes, sentences, retry rules, workflow ports. | M | **FR 3** | every `kept` test asserts a kind |
| V7 | **Command model built by steps.** `CommandModel` with stream rows, ops, "changed", global options and an ordered filter list; `compileSelectors` wrapped as one adapter producing it; `Args` renders from the model. | M-L | W10, team's L4 | every stored workflow's fingerprint unchanged (team's W6 golden folder) |
| V8 | **Remote worker runs the same steps.** Serialise `RunState` to the lease plan; delete `remote.encoderArgs` and its fallback loop. | S-M | **FR 4** | the remote tests; one real run per family available |
| V9 | **Measurements from structured signals.** Run with `-loglevel level+verbose`; `measure_frames_decoded`/`measure_decode_errors` from the end-of-run report; `progress=end` ends the finishing window; the regex damage classifier becomes a fallback. | M | **FR 5** | the 11-18..20 damage sample set |
| V10 | **Global and library scopes.** `workflow_variables` in settings, `libraries.variables`, `TemplateParam` enforcement, "affects N workflows" preview. | M | W2, W3 | lookup-order tests; migration test |
| V11 | **Pin intent on the job.** `probe_id` on jobs; `TemplateFor` reads the pinned revision; a publish requeues explicitly; the worker's re-skips at `worker.go:928-952` become one "intent no longer valid" path. | M | **FR 6** | "edit flow while queued" test |
| V12 | **Analysis steps that publish.** Optional loudness measure (`measure_loudness_*`) and black-bar detect (`measure_crop`) as steps whose values the model's filters read. Off by default. | M each | team's F4, F5 | real-ffmpeg samples |

Order: V1 → V2 → V3 → V4 → V5 (the width and 10-bit class is gone after V5) → V6 → V7 → V8 → V9 →
V10 → V11 → V12. V1-V3 and V7 are the "restructure"; the rest are the foundation review's slices
expressed in variables. Each leaves `dev` shippable: V1 and V3 change no behaviour, V2 and V10 are
additive, V4-V6 change verdicts only where today's are wrong.

---

## 7. Risks, and what to keep

**Keep as it is (our current approach is already the right one):**

- **Probing** with a JSON probe, a first-frame HDR read and a cache (`media/media.go:212-231`).
- **Fallback inside the step:** rungs run inside the convert step with one verify and one save
  (`worker/worker.go:1172-1201`), matching the team's W7 decision.
- **Verify before replace:** shape, playback, frames, SSIM and source identity are checked first, and
  the size rule is applied after (`verify.go:144-201`, `keep.go:245-266`).
- **No pointless re-encodes:** the video is copied when the format already matches with no scale or cap
  (`rules/plan.go:897-905`), and the file is skipped when nothing changes (`plan.go:986-992`).
- **A clean log** with no pictures, base64 or repeated dumps: L1-L3 already require this.
- **No external metadata lookups in the conversion path.** Stale statistics are cleared in the same
  ffmpeg run (#512).
- **Owner changes through system calls,** not a shell string (`setOwner`, `worker/owner_unix.go`,
  **UNVERIFIED**).
- **Encoder tests once per worker,** cached, with a pause after repeated failures. Publish the results as
  `worker_*` variables.

**Risks of the design itself:**

- **Provenance hides.** A value that came through four scopes is hard to debug unless every read says
  where it came from. Mitigation: `SetBy`/`Scope` on every run value and the log line per publish (V1).
- **Noise.** A run table with 80 names gets noisy. Mitigation: the log groups by
  prefix and hides unchanged names behind the download (the L1 download already separates them).
- **Fingerprint drift.** Moving flags into profiles must not change any existing job's `plan_fingerprint`
  or argv. Mitigation: V3's golden tests over the arg test corpus (the team's reviewer dumps of 9,360
  cases for P4 A1 are the model).
- **Two representations during the transition.** `rules.Plan` and the `CommandModel` coexist until V7.
  Mitigation: the adapter produces the model from the Plan, and `Args` reads only the model from V7 on.
- **Secrets in templates.** A `{{secret}}` in an output name would leak to disk. Mitigation: secret
  sinks are an allow-list on the step definition; everything else refuses at validate.
- **The remote worker's version skew.** A worker on an older build cannot run new steps. Mitigation: the
  lease carries a `RunState` schema version; an old worker gets the rendered command only (today's path)
  until updated.

---

