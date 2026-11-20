# Flow builder: cards people chain into a logic flow

Status: design, 2026-11-20, from the research analyst seat (fable-solo). Evidence base: origin/dev `e006050c`. This is the plan for USER TASK 5: "we have the dropdowns but the flow builder does not have any options. The pieces should be cards people can chain together and build a logic flow from, each card with pieces they can chain. Use the same cards for the dropdown editor."

Read with `docs/architecture/app-map.md` (the pipeline) and `docs/architecture/foundation-review.md` (root causes RC1 to RC7). The workflow engine study notes (in the mission workspace, outside the repo) define slices W1 to W11; section 8 says which of those this plan replaces, reuses or leaves alone.

## 0. In one page

**What is wrong today.** A workflow is a document of nodes and edges (`internal/workflow/schema.go`), and the editor has a graph view and a steps view over it. But the registry holds ten node types (`internal/workflow/registry.go:13-25`), eight of which are the fixed "Shrink videos" chain, and the server tells the editor that no step can be added or moved (`internal/workflow/validate.go:40-52`). The graph is a drawing of the dropdowns. The only logic a person can build is "Check the file → Yes/No → Leave the file alone".

**What this plan builds.** A **card** is a step type with a contract the engine and both editors understand: what it does (`runsAs`), where it may sit (`phase`), what it publishes, what state the file is in on each of its outputs, and whether this build can run it. A **catalog** of cards covers what a mature automation product's building blocks cover: questions that send a file one of two ways, actions that change variables, names, folders, tags and logs, cards that tell other programs, cards that shape the one conversion, cards that check and save, and cards that end a run. People chain cards in the graph; the steps view shows the same chain as a list; the dropdown editor becomes a filtered steps view that shows only the cards that shape the conversion. **One document, one compile, three views.**

**How it runs.** A job is one *run* over the published document. The engine walks it in five phases: *before* (questions and actions on the original, nothing on disk changes), *convert* (every picture-shaping card compiles into one plan and one ffmpeg process, as today), *check* (the result is verified, as today), *save* (the result is committed, as today), *after* (actions on the saved result and messages to other programs; a failure here never undoes the save). The compile already produces a `rules.Template` of branches (`internal/workflow/branches.go`); this plan adds *before* and *after* card lists to each branch and a small run package that executes them. The worker gains two call sites.

**What stays exactly as it is.** The document schema, every stored workflow and revision, every preset, the plan fingerprints, the eight conversion steps and their order, the publish and draft API, the variables work (#497, #504, #511). New cards are new node types; nothing existing is renamed, retyped or removed.

**Order of work.** FB0 this document. FB1 the card contract, the first catalog (questions, variables, notes, tags, ends) and the *before* phase. FB2 the *after* phase (notifications, media server, downloader, sidecar files, checksums, commands). FB3 the steps view for questions and after-cards, and the dropdown view as a filter. FB4 the remaining flow cards (convert again later, hand to another workflow, random split, wait). FB5 picture-shaping cards beyond today's (crop, loudness, frame rate, bit depth), which wait for the engine's "steps add to the plan" slice. FB6 the editor shell (toolbar, full-height canvas, palette and settings inside the frame).

## 1. The card model

A card is a `StepDefinition` (`internal/workflow/registry.go:31-53`) with six new fields. Everything the registry already has stays: `type`, `typeVersion`, `category`, `name`, `description`, `icon`, `inputs`, `outputs`, `configSchema` (JSON Schema with the `x-` hints the forms read), `alwaysOn`, `requires`, `runsWith`, `sideEffects`, `source`, `readOnly`.

```go
// workflow.StepDefinition, added fields
RunsAs    string         `json:"runsAs"`    // input | question | action | plan | verify | commit | end
Phase     []string       `json:"phase"`     // where it may sit: before | convert | after (a plan card: convert only)
Publishes []Published    `json:"publishes"` // variables it sets for later cards ({{name}})
Reads     []string       `json:"reads"`     // variables it must have (checked at publish; "" means none)
FileOn    map[string]string `json:"fileOn"` // per output port: original | staged | saved | none
Available bool           `json:"available"` // this build runs it; false: it can be drafted, not published

type Published struct {
    Name  string `json:"name"`  // a NamePattern name
    Type  string `json:"type"`  // string | number | bool | list
    Words string `json:"words"` // what it holds, for the picker
}
```

### 1.1 Kinds (`runsAs`)

| Kind | What it does | Ports (outputs) | Example |
|---|---|---|---|
| `input` | where files come from | `success` | Library files |
| `question` | answers Yes or No about the file, the run or a variable; changes nothing | `match` (Yes), `else` (No) | Check the file, Check a variable |
| `action` | does one thing: sets a variable, writes a note, renames, moves, runs a command, tells another program | `success` (Done), `error` (Didn't work) | Set a variable, Tell the media server |
| `plan` | shapes the one conversion | `success`, plus the existing `skipped` and `error` on Convert video | Convert video, Audio, Subtitles, File type |
| `verify` | checks the result against the original | `success`, `error` | Check the result |
| `commit` | saves the result and decides the original's fate | `success`, `error` | Save the result |
| `end` | ends the run with a verdict | none | Leave the file alone, Stop as failed, Convert again later |

### 1.2 Phases and what may follow what

A run has five phases in this order: **before → convert → check → save → after**. Each card declares the phases it may sit in. Validation reads the document as a graph and checks, for every edge, that the phase does not go backwards and that the card the edge leads to may sit where it lands:

- `before`: questions, actions that change no file (set a variable, add a note, tag, wait, ask the downloader for details), ends.
- `convert`: plan cards only, contiguous, in the preset's order until the engine's "steps add to the plan" slice lands (W10; then free order). A question inside this stretch stays refused (`branch_in_conversion`), because it would split one ffmpeg process in two.
- `check`: Check the result. Always present on a converting path (`path_without_save` enforces the end; the compile enforces Check before Save).
- `save`: Save the result.
- `after`: actions on the saved result (checksum, write a sidecar file, copy to a folder, run a command, tell the media server, tell the downloader, send a notification), ends.

`fileOn` says what state the file is in on each output port, so validation can refuse a card that would read a staged file as if it were saved, and so the editor can show "on this port the original is untouched" on unwired ports (W9's rule 4: an outcome with no link keeps the original).

### 1.3 Published variables and reads

Cards publish into the run's own scope, the same `{{name}}` namespace the Save step's fields already read (`internal/workflow/variables.go:35-66` lists the built-ins; #511 resolves them at run time). Rules:

- A published name follows `NamePattern` and may not be a built-in; the registry refuses a definition that breaks this at start-up (a test covers every card).
- Later cards may read it in any `x-variables` field and in the question card "Check a variable".
- Order matters: a `{{name}}` that is published only by a card *after* the reader is an `unknown_variable` at publish time, with the card named ("{{tag}} is set by 'Ask the downloader', which comes after this step").
- Names published by `before` cards are part of the job's variable list in the job log, with "set by <card>" as the source (the log already shows each variable's source since #504).

The scope chain for global and library variables (W1 to W3) stays the engine team's work and is not needed for any card here: a run's own scope sits in front of it when it lands.

### 1.4 Availability and the honest refusal

`Available: false` cards can be placed and saved as drafts, so people can design ahead, but Publish refuses the document with `not_runnable_yet` naming the card ("'Crop black bars' can't run in this version. Remove it, or keep the workflow as a draft."). The palette shows them dimmed with "coming later". Nothing is shown as a disabled control with no reason (the design rule in `DESIGN/v2/IA.md`, "No fake functionality").

### 1.5 Verdicts

Every port that is not wired has a meaning the card declares, and it is always the safe one:

| Card kind | Unwired `error` / `else` means | Job state |
|---|---|---|
| question `else` | validation error: every answer must lead somewhere (today's rule) | — |
| action in `before` | the run stops, original untouched | failed, "<card> didn't work" |
| action in `after` | the run goes on; the job ends "done, with a warning" | completed, warning recorded |
| plan / verify / commit `error` | today's behaviour (stop, original untouched; or the card's `onError`) | failed |
| end | n/a | the end's verdict: skipped, failed, or queued again |

## 2. The catalog, v1

Groups are the steps view's groups (`web/src/stepEdits.ts:6`), with one new group. Names are ours. "Exists" is the state at `e006050c`. "Slice" is where it becomes real. Capabilities were taken as a checklist from what a mature plugin catalog offers (conditions on file and media facts, variables, naming and moving, logging and tagging, media-server and downloader hooks, notifications, checksums, archives, external commands, sub-flows and loops, scripting); we name and shape each in our own words and leave out what our product does not need (books, images, comics, PDFs, audio-only products, scripting in a browser).

### 2.1 Start

| Card | Type | What it does | Exists | Slice |
|---|---|---|---|---|
| Library files | `input.library` | every video in the libraries that use this workflow; exclude patterns | yes | — |

### 2.2 Questions (Yes / No)

| Card | Type | Asks | Publishes | Exists | Slice |
|---|---|---|---|---|---|
| Check the file | `filter.video` | codec, bitrate, resolution, HDR, Dolby Vision, frame rate, size, file type; **new:** bit depth, audio codecs, audio channels, audio languages, subtitle languages, running time, container has chapters | — | yes (8 of 15 conditions) | FB1 extends |
| Check the file name | `check.name` | the name or the full path contains / starts with / ends with / matches a pattern (glob or regular expression); case choice | — | no | FB1 |
| Check the file's age | `check.age` | created or changed before / after a date, or older / younger than N days | — | no | FB1 |
| Check a variable | `check.variable` | a `{{name}}` is set / equals / contains / is more than / is less than a value | — | no | FB1 |
| From this library | `check.library` | the file comes from one of the chosen libraries | — | no | FB1 |
| Has a tag | `check.tag` | the file carries one of the chosen tags | — | no | FB1 (with Tag the file) |
| Shares its data with another file | `check.hardlinks` | the original has other hard links (the scanner already knows; `rules.SkipSharedFile`) | — | no | FB1 |
| Random split | `check.random` | sends a chosen share of files down Yes (for trying a setting on some files) | — | no | FB4 |
| Compare the result | `check.result` | after the conversion: new file smaller than / bigger than / within N % of the original; new file's running time within N % | — | no (the Save step's "if bigger" covers the one case) | FB4 (phase `after` the check, before save; needs W9's state rule) |

Every question has one form rule: every condition set must hold for Yes; a question with no condition is refused (`empty_check`, exists).

### 2.3 Variables, notes and tags (actions that change no file)

| Card | Type | What it does | Publishes | Exists | Slice |
|---|---|---|---|---|---|
| Set a variable | `action.set_variable` | sets `{{name}}` to a value or a template of other variables | the name | no | FB1 |
| Add a note to the log | `action.note` | writes a line (with variables) into the job log at this point | — | no | FB1 |
| Tag the file | `action.tag` | adds or removes tags on the file's record (shown in Jobs, filterable) | `tags` | no | FB1 (needs a `tags` column on jobs; a small store change, coordinated) |
| Wait | `action.wait` | pauses the run for N seconds or until a time of day (quiet hours per workflow) | — | no | FB4 |
| Ask the downloader for details | `action.lookup_downloader` | asks the connected downloader (Settings > Connections) for the title, year, season, episode, original language of this file | `title_clean`, `year`, `season`, `episode`, `original_language` | no (the connection exists) | FB2 |

### 2.4 Picture, sound and file (plan cards: the one conversion)

| Card | Type | What it does | Exists | Slice |
|---|---|---|---|---|
| Leave some files alone | `filter.video_skips` | the preset's built-in skips | yes | — |
| Convert video | `video.convert` | format, quality, resolution, bitrate, speed, rate control; **later:** hardware choices (W7) | yes | — |
| Audio | `audio.tracks` | which sound tracks, in which form | yes | — |
| Subtitles | `subtitles.tracks` | which subtitles, as tracks or files | yes | — |
| File type | `container.output` | the new file's type | yes | — |
| Crop black bars | `video.crop` | detects and removes letterbox bars (engine F4) | no | FB5, after W10 |
| Even out loudness | `audio.loudness` | two-pass loudness normalisation (engine F5) | no | FB5, after W10 |
| Frame rate | `video.framerate` | keep, or at most N fps | no | FB5, after W10 |
| Bit depth | `video.bitdepth` | keep, 8-bit, 10-bit | no | FB5, after W10 |
| Earlier install's settings | `video.legacy_settings` | read-only migration card | yes | — |

Plan cards never run alone: validation requires the stretch to start with Leave some files alone and Convert video and to be followed by Check the result and Save the result, as today (`internal/workflow/preset.go:156-158`). Until W10 the stretch keeps the preset's order; the steps view says so on the row ("Set place").

### 2.5 Check and save

| Card | Type | What it does | Exists | Slice |
|---|---|---|---|---|
| Check the result | `verify.output` | plays through, looks like the original, holds what was chosen | yes | — |
| Save the result | `output.commit` | where it goes, the original's fate, names, "if bigger" | yes | — |

### 2.6 After the save (actions on the saved result)

| Card | Type | What it does | Publishes | Exists | Slice |
|---|---|---|---|---|---|
| Tell the media server | `action.media_server` | asks the connected media server(s) to look at the folder (today this happens for every job when a connection is on) | — | the connection exists | FB2 (the card makes it per workflow; the global switch stays as the default when no workflow has the card) |
| Tell the downloader | `action.downloader` | asks the connected downloader to re-read the file (same as above) | — | the connection exists | FB2 |
| Send a notification | `action.notify` | sends a message (with variables) to one of the notification targets in Settings > Notifications; a kind: info / good / warning / bad | — | targets exist | FB2 |
| Write a file beside the result | `action.sidecar` | writes a text file next to the new file (a template with variables; for example a .nfo or a note) | — | no | FB2 |
| Checksum the result | `action.checksum` | computes a checksum of the new file (SHA-256; MD5 and SHA-1 as choices) | `checksum` | no | FB2 |
| Copy the result to a folder | `action.copy` | copies the new file to another folder (same sub-folders), with a name template | `copy_path` | no | FB2 |
| Run a command | `action.command` | runs a program with the new file's path and variables as arguments; the exit code chooses Done / Didn't work; output goes to the job log. Off until the person turns on "Workflows may run commands" in Settings > Safety, because a command runs as the app's user. | `command_output` | no | FB2 |
| Add a note to the log, Tag the file, Set a variable | as in 2.3 | also allowed here | | | FB1 |

### 2.7 Ends

| Card | Type | Verdict | Exists | Slice |
|---|---|---|---|---|
| Leave the file alone | `flow.end` | skipped, with the words given | yes | — |
| Stop as failed | `flow.fail` | failed, with the words given; the original untouched | no | FB1 |
| Convert again later | `flow.requeue` | the file goes back to the queue after N minutes/hours, at most K times (for "the downloader hasn't finished yet" cases) | no | FB4 |
| Hand to another workflow | `flow.goto` | the file continues in another published workflow (no loops: refused if it leads back) | no | FB4 |

### 2.8 Not in v1, and why

- **Scripting cards** (a code box that returns an output number): a tempting general escape hatch, but it can't be validated, can't be explained in a job log in plain words, and runs arbitrary code on the person's machine. "Run a command" with the safety switch covers the real need (call your own tool). Revisit when there is a request that no card covers.
- **Loops over folders or lists**: our unit of work is one file; the scanner is the loop.
- **Image, book, document, audio-only cards**: not this product.
- **Sub-flows**: `Doc.Subflows` is reserved in the schema; "Hand to another workflow" (FB4) covers reuse without nesting.

## 3. How branches and outputs connect

Nothing changes in the document: an edge joins an output port of one node to the `in` port of another (`schema.go:57-64`). What changes is what the compile accepts.

Today the compile walks from Library files through questions to one of: a converting stretch, or an end (`branches.go:123-173`). Each path becomes a `rules.Branch{Checks, Then, End, OnFailure}`. This plan extends the branch, not the document:

```go
// rules.Branch, added
Before []Card // actions on the path before the conversion, in order
After  []Card // actions after the save, in order
// rules.BranchCheck.Check becomes a Question: today's VideoCheck plus the new question kinds
// rules.Card is a node's type, id, name and config, carried to the run
```

Routing at job time (`rules.route`, `branch.go:109-126`) stays "first path whose questions all answer as it needs"; the questions gain kinds (file facts, name, age, variable, library, tag, hard links). Before-actions run in path order once the path is chosen; they can publish variables that later questions on the same path read (a Set a variable before a Check a variable), so routing is evaluated card by card along the path rather than all questions first. After-actions run after the commit.

Failure ports: an action's `error` port may lead to an end or to another action in the same phase (validation refuses a jump to an earlier phase). A plan card's `error` and `skipped` ports keep today's rules (`branches.go:56-61`, `failureOf`), and W9 (the engine team) lifts them per step as it declares the file state on each port; the `fileOn` field is where that declaration lives, so W9 and this plan use one mechanism.

Edges from a question's two ports to the same card are allowed (both answers continue the same way; useful for "tag, then continue either way").

## 4. One model, three views

The three views read the same document and write it through the same functions (`web/src/stepEdits.ts`: `order`, `insertAfter`, `moveNode`, `deleteNode`).

### 4.1 The graph

Everything: cards, ports, edges, branches, ends. The palette lists the catalog by group with search; cards that this build can't run are dimmed with "coming later" and can still be placed as a draft (1.4). Dropping a card on an edge inserts it there. The settings panel sits inside the editor frame (the shell change in `RESEARCH/fable-solo/ui-review.md` C10 and the design in `RESEARCH/fable-solo/designs/screens/workflow-graph.html`; slice FB6).

### 4.2 The steps view

The steps view is the **main path**: from Library files along `success` and `match` (Yes) ports to Save the result and the after-cards. It shows every card on that path as a numbered row with its summary. The server's `stackable` flag (`validate.go:170`) is widened: a document is stackable when every node is on the main path or is an end reached from a question's `else` port. The rows:

- A **question** row reads "Only if: <summary>" and carries an **Otherwise** control: "Leave the file alone" (the default), "Stop as failed", or "Another path (opens the graph)". Changing it rewires the `else` edge to an end node, or opens the graph with the question selected.
- **Plan** rows are today's rows (switch or "Always on", chevron to the form).
- **After** rows sit under Save the result with a thin divider "After the file is saved".
- The add menu groups the catalog as the palette does and inserts into the main path after the chosen row, within the phase rules (the server's `addSteps` answers per type with the reason when a card can't go there).

A document that is not stackable (a second converting path, an action chain off an `else` port) shows the steps view read-only with one line "This workflow has more than one path. Edit it in the graph." (today's behaviour for `stackable: false`, kept).

### 4.3 The dropdown view (the simple editor)

The dropdown view is the steps view filtered to the **plan cards** of the main path, each shown as its form inline, with the question and action rows folded into one line each ("3 rules before converting · 2 steps after saving · open the steps view"). It is the same component with a filter, so there is nothing to keep in sync. The first-run wizard's step 2 and the library page's "Edit this workflow" open this view; the Workflows page opens the steps view; the graph is one click away from either.

### 4.4 Summaries and words

Every card's summary line is server-rendered (`validate.go:332`, `StepSummaries`), as today, so the three views and the job log say the same thing. New cards add their summaries there, from their config, in plain words ("Only if the file name contains 'Remux'", "Tell the media server: Living room", "Send a notification: 'Done: {{title}}' to Phone").

## 5. How saved workflows keep working

- **Documents.** `SchemaVersion` stays 1. No node type, port, config key or edge shape changes. New cards are new types. A document from any earlier build validates and compiles exactly as before: the compile's new fields (`Before`, `After`) are empty for it, and `rules.route` with only `VideoCheck` questions is today's `route`.
- **Presets and templates.** `Preset()` is unchanged. The built-in template list gains none in FB1. `FromTemplate` and the read-only `video.legacy_settings` card are untouched.
- **Fingerprints.** A straight workflow's `Template` and `Plan` are byte-for-byte what they were (the added fields are `omitempty`). A branched workflow with no new cards produces the same `Branches`.
- **The migration test (FB1 brings it, as the task requires).** `internal/workflow/golden_test.go` loads every built-in preset, a legacy revision-1 document (`input.library` + `video.legacy_settings` with a stored template), a straight V1 document, a V6a branched document with an end, and a V6b document with a fallback, from `testdata/golden/*.json`; each must validate with no errors and compile to the stored template hash in `testdata/golden/*.hash`. A change to a hash fails the test unless the hash file changes in the same PR. This is W6's golden folder; if the engine team lands W6 first, FB1 adds its documents to their folder instead.
- **Drafts.** A draft with a card this build can't run stays a draft (1.4); it is never rewritten.
- **Jobs in flight.** A job carries its revision; a publish never changes a running job (today's rule).

## 6. The run engine

New package `internal/flow` (no storage, no HTTP): the pieces the worker calls.

```go
type State struct {           // one run's state
    Vars  map[string]string   // built-ins + workflow variables + what cards published
    Facts media.Info          // the probe (before) or the result's probe (after)
    Path  []string            // node ids walked, for the job log
    Notes []Note              // what each card did, in words, for the log
}
type Env struct {             // what actions may touch, given by the worker
    Notify      Notifier      // notify.Service's send
    MediaServer Teller        // integrations' "look at this folder"
    Downloader  Teller        // integrations' "re-read this file"
    Exec        Exec          // for Run a command, nil when the safety switch is off
    Tag         func(jobID int64, add, remove []string) error
    Log         func(jobID int64, line string)
}
func Before(ctx, env, st *State, cards []rules.Card) (verdict Verdict, err error)
func After(ctx, env, st *State, cards []rules.Card) (warnings []string)
func Ask(q rules.Question, st *State) (yes bool, why string)
```

The worker's two call sites (coordinated with the engine team, who own `internal/worker` today):

1. After the probe and routing (`internal/worker/worker.go:926-936`): `flow.Before` on the chosen branch's `Before` cards. A `Verdict` of skip/fail ends the job as the corresponding end card would; a published variable joins the job's variable values (`worker.go:169`, the W4b list).
2. After `OnDone` (`worker.go:508`): `flow.After` on the branch's `After` cards. Warnings become the job's "done, with a warning" (the pill exists in the drawer).

Each card's run is recorded as a step in the job log (`Queue.AddStep`), with its words, so the timeline reads "Only if the file name contains 'Remux' → Yes", "Set {{profile}} = remux", "Told the media server (Living room)".

## 7. Slices

Small PRs, one each, to dev. Each: tests, the clean-room grep in the PR description, screenshots at 1440 and 390 where a screen changes, a review from the engine team (Go) or the screen team (web).

| # | Slice | Packages | Size | Done when |
|---|---|---|---|---|
| FB0 | this document | docs | S | draft PR up; manager told |
| FB1a | card contract: the six fields on `StepDefinition`, filled for today's ten types; a registry test that every card's ports, phases and published names are consistent; `GET /step-definitions` serves them; the golden test folder | workflow, web (types only) | S | `go test ./internal/workflow/...` green; goldens stored |
| FB1b | questions and before-actions: Check the file's seven new conditions, Check the file name, Check the file's age, Check a variable, From this library, Shares its data with another file, Set a variable, Add a note to the log, Stop as failed; validation's phase rules; `addSteps` and `stepPlaces` real for these cards; compile to `Branch.Before` and `Question` | workflow, rules (branch.go only), flow (new) | M | unit tests per card; golden unchanged; a document with each card validates, publishes and compiles |
| FB1c | the worker's first call site (`flow.Before`), the job-log steps and the variable hand-off | worker (one hook, with the engine team) | S | a real run on a fixture video takes a Yes path, publishes a variable, and the Save step's name template uses it |
| FB1d | editor: the add menu and palette offer the FB1 cards; question rows with "Otherwise"; before-cards in the steps view | web | M | screenshots 1440/390 of a workflow with a question and a note; typecheck and build green |
| FB2a | after-actions: Tell the media server, Tell the downloader, Send a notification, Write a file beside the result, Checksum the result, Copy the result to a folder; the worker's second call site; "done, with a warning" | workflow, flow, worker (one hook) | M | each card tested against a fake env; a real run writes a sidecar and a checksum |
| FB2b | Run a command with the Safety switch; Ask the downloader for details (publishes title/year/season/episode/language) | flow, settings, integrations (read-only use) | M | the switch off refuses publish with the reason; on, a command's exit code picks the port |
| FB2c | editor: after-rows under Save; tags in Jobs rows and the "Has a tag" filter | web, jobs (a `tags` column) | M | screenshots; migration test for the column |
| FB3 | the dropdown view as the filtered steps view; first run and the library page open it | web | M | one component; the old dropdown page removed; screenshots |
| FB4 | Convert again later, Hand to another workflow, Random split, Wait, Compare the result | workflow, flow, jobs (requeue with delay) | M | tests; the goto loop check |
| FB5 | plan cards beyond today's: Crop black bars, Even out loudness, Frame rate, Bit depth | rules, workflow | L | after the engine team's W10 (steps add to the plan); not before |
| FB6 | editor shell: toolbar, full-height canvas, palette and settings inside the frame (ui-review C10 / U11) | web | M | screenshots at 1440 and 390; one primary button |

Order: FB0 → FB1a → FB1b → FB1c → FB1d → FB2a → FB2b → FB2c → FB3 → FB6 → FB4 → FB5.

## 8. Overlap with queued work

- **W4 (variables panel and picker), W4a/W4b (merged):** reused as they are. Cards publish into the same namespace; the picker lists published names with "set by <card>" once FB1b lands (one list from the server, `GET /workflow/variables`).
- **W1, W2, W3 (variable scopes):** the engine team's; untouched. The run scope from cards sits in front of the chain when it exists. "Set a variable" writes the run scope only.
- **W5 (export/import), W6 (migrations and goldens):** W6's golden folder is the one FB1a creates or joins; export/import carries new cards as ordinary nodes.
- **W7 (hardware choices on Convert), W8 (size rule):** the engine team's, on existing cards; no overlap.
- **W9 (error links from more steps):** the `fileOn` declaration (1.2) is the mechanism W9 needs; the engine team lifts plan cards' ports, this plan lifts action cards' ports. Agreed split, by card kind.
- **W10 (steps add to the plan):** the engine team's; FB5 waits for it. Until then plan cards keep the preset order and the steps view says "Set place".
- **W11 (add, reorder and drag steps):** this plan's FB1d and FB6 take it, since the manager moved the editor to this seat; ui-shop keeps U1 to U10, U12, U13 of the UI overhaul.
- **Engine Drop 2 (F1 to F9):** F4 (crop) and F5 (loudness) become FB5's cards once W10 lands; their engine halves stay the engine team's.
- **Connections (media server, downloader) and Notifications:** reused through their services; the cards are per-workflow switches over the same machinery. When no workflow has the card, the global setting behaves as today.

## 9. Open decisions for the user

1. **Run a command** ships behind a Safety switch, off by default (2.6). Confirm, or leave it out of v1.
2. **Tags** live on the job record (2.3); they are shown in Jobs and can be filtered. Confirm the name "tag" (the alternative is "label").
3. **The dropdown view** becomes the steps view filtered to the conversion cards (4.3), and the first-run wizard opens it. Confirm that the first-run wizard may show the "3 rules before converting" line, or hide questions entirely there.
4. **Scripting cards** are left out (2.8). Confirm.

## 10. Clean room

Capabilities were taken from public documentation as a checklist; names, words, keys, ports and groupings here are ours. The PR description carries the grep for the reference's name and for the distinctive strings of the private study material, with its empty result.
