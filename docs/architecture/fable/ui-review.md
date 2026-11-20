# UI design review: making the web app look refined

Author: solo-analyst@fable-solo (an independent review seat). Date: 2026-11-20. Review only: no code changes.

What was reviewed: `web/src` at origin/dev `294e3d88`, the design docs in `DESIGN/`, and the screenshot sets under `slices/11-ui-web/` (pass6, pass5, plex). All screenshots are older than that commit; where a screenshot and the code disagree, the code is quoted. Sizes come from `styles/tokens.css` and `styles/shell.css`, not from measuring a live page. Patterns from public UI conventions were used as inspiration and are described here in our own words.

Sections: 1 summary · 3 critique per screen · 4 proposed changes · 5 mockups · 6 ordered slices. (Section 2, a private comparison, is not included here.)

---

## 1. Summary

**Where ours falls short.** Our app is honest and well worded (plain English everywhere, states that say what to do, no fake data, good accessibility work). It is also already structurally close (rail, top bar, command palette, tabs with counts, drawer, stack and graph views). What it lacks is visual rhythm:

- **Tables where rows should be.** Libraries, Jobs and Workflows are 44 px table rows spread across eight columns at 1440 px (`shell.css:155`, `Libraries.tsx:134`, `Jobs.tsx:191`). With two libraries the page is 90% empty; with twenty it is a spreadsheet. Nothing anchors the eye at the start of a row.
- **A bimodal type scale.** Base text is 13.5 px, page titles 20 px, chips 11.5 px (`tokens.css:92-95`, `shell.css:375`), then one 44 px headline on the dashboard (`shell.css:724`). Everything is small except one thing that is huge. The design system asked for 16/24/18 with nothing under 13 (`DESIGN/DESIGN-SYSTEM.md` lines 17-18); the live tokens drifted away from it without a recorded decision.
- **Colour switched off.** The clean pass made idle status pills neutral (`shell.css:125-132`), which is right, but nothing was switched on in exchange: the active nav item is a 3 px bar and a raised background, progress is a 6 px bar only on the Running tab, "Done" rows are grey pills with grey text. The pages look monochrome even when the system is busy.
- **The dashboard talks instead of showing.** A 44 px "Nothing saved yet" headline with three competing figures under it, two charts with legends and footnotes, two tables and a two-sentence explanatory paragraph (`Overview.tsx:149-163`, `pass6/after/dashboard-near-1440-dark.png`). Three tiles and two lists would say the same things.
- **Editors sit inside the page.** The graph canvas is a bordered box of at most 720 px inside the normal content column, with the palette and inspector as sibling cards (`shell.css:598-600`, `:700-701`). It reads as a widget, not a workspace. The stack view's header row carries eight controls of equal weight.
- **Loading is a word.** Every page shows "Loading…" as a sentence (`Libraries.tsx:129`, `pass6/after/states-dashboard-loading-1440-dark.png`) and then jumps to its layout. Empty and error states are good (icon, one sentence, one button).

**Top 5 changes** (details and priorities in section 4):

| # | Change | Why it matters most | Screens | Priority / effort |
|---|---|---|---|---|
| 1 | One list-row grammar: type tile, title, chip line, right rail, progress underline | Every list looks finished and scannable at any width; ends the sparse-table problem | Libraries, Jobs, Workflows, Workers, Dashboard lists | P1 / M |
| 2 | Dashboard as tiles plus live rows: three stat tiles with sparklines, a "Converting now" list with labelled sub-columns, libraries as savings bars | The first screen stops looking like a report and starts looking like a control room | Dashboard | P1 / M |
| 3 | Type and spacing scale: base 14, title 24 with icon, section 16, chips 13 minimum, rows 52, a 4-point spacing scale recorded in tokens | Removes the "everything is tiny" feel; fixes the drift from the design system | All | P1 / S |
| 4 | Colour for "alive" only: filled active nav item, accent progress line, green saved figures, tinted run/ok/bad pills with icons; everything idle stays neutral | Pages read at a glance without becoming loud | All | P2 / S |
| 5 | Editors as workspaces: full-height canvas under a slim toolbar, grouped palette with search, one primary button; stack view toolbar grouped left/right | The two most complex screens stop feeling embedded | Workflows (graph and stack) | P2 / M |

Also worth doing soon: skeleton loading that matches the final layout (P2/S), first run without the navigation rail (P2/S), a shorter phone dashboard (P3/S).

---

## 3. Critique per screen

Each screen: visual hierarchy, spacing and alignment, typography, colour and dark/light, states, consistency, density, wording. File references are `web/src/...` at `294e3d88`; screenshot references are under `slices/11-ui-web/`.

### 3.1 Overview (dashboard)

Evidence: `pass6/after/dashboard-near-1440-dark.png`, `-light.png`, `-390-dark.png`, `dashboard-empty-1440-dark.png`, `states-dashboard-loading-1440-dark.png`; `screens/Overview.tsx:139-163, 188-212`; `styles/shell.css:723-767`.

- **Hierarchy.** The 44 px headline (`shell.css:724`) is the largest text in the product and it is a sentence ("Nothing saved yet"). Under it three figures compete in 13.5 px (frees by date, could free, added by bigger files; `Overview.tsx:150`). The red health banner sits above all of this (`Overview.tsx:143`). Then "Last 30 days" with two charts, two tables and the note. Nothing says "look here first". Proposed order: tiles, live rows, libraries, then charts.
- **Spacing.** Sections are 20 px apart (`shell.css:733`) while the content gap is 16.8 px (`shell.css:75`): close enough that section boundaries disappear. The two charts share a 32 px gutter; the chart footnote line adds a fourth text size.
- **Typography.** 44 / 20 / 16 / 13.5 / 12 px (`shell.css:724-730`, `tokens.css:92-95`). The big number is right; the rest is one step too small for a page that is read from a metre away.
- **Colour.** Light and dark are both coherent (`tokens.css:7-48`). Freed/waiting/left-in-place use three greens and greys that are hard to separate in the light theme (`dashboard-near-1440-light.png`). Failed figure is red text (`shell.css:730`), good.
- **States.** Loading is the word "Loading…" (`states-dashboard-loading-1440-dark.png`); the loaded page is 1100 px tall, so the jump is large. Empty is good: "Add a library to start." and "Charts start after two days of conversions." (`dashboard-empty-1440-dark.png`, `Overview.tsx:163`).
- **Consistency.** The Workers table here uses a "Ready" pill with no icon; the Workers page uses "Running · 0 of 1 busy" with an icon for the same machine (`sweep-dashboard-workers-1440-dark.png` vs `qa437d/workers-1440-dark.png`). The CPU·GPU cell shows "14% · —" here and "12% · none" in another shot of the same column.
- **Density.** Two tables with one or two rows each at 1440 px, each spanning 1160 px, with four to six columns: the sparse-table problem.
- **Wording.** Excellent and too much. The Libraries note (`Overview.tsx:212` area, visible at the bottom of `dashboard-near-1440-dark.png`) is two sentences explaining a column; it belongs in the "?" help, not on the page. "failed in 24 h" and "frees by Nov 26" are good.
- **Dead styles.** A KPI-tile system exists in CSS (`shell.css:147-151`, `:412-418`, `:443`) but the overview uses `.headline`/`.stats` instead; the tile CSS is the right starting point for change 2.

### 3.2 Libraries (list and detail)

Evidence: `pass6/after/qa446/list-1440-dark.png`, `list-390-dark.png`, `qa439/list-real-1440-dark.png`, `qa439/lib2-general-1440-{dark,light}.png`, `lib2-output-390-dark.png`, `qa445/dialog-1440-dark.png`, `states-libraries-{empty,error-down}-1440-dark.png`; `screens/Libraries.tsx:108-155, 240-249`; `shell.css:357-404`.

- **Hierarchy.** Page head (icon, 20 px title, "?"), toolbar, table. Fine, but the table header row (12 px grey) is the only thing that separates toolbar from data; the primary "Add library" button is 32 px tall (`shell.css:367`), smaller than the search box next to it.
- **Spacing.** Table rows are 44 px with two lines of text inside (`shell.css:155`, `Libraries.tsx:143`): the second line sits 2 px above the row border. The `.list` border-top/bottom (`shell.css:357`) gives the table no left or right edge, so at 1440 it floats.
- **Typography.** Name 13.5 px bold, path 12 px mono, workflow summary 12 px, numbers 13.5 px. Nothing above 20 px. Column headers 12 px grey.
- **Colour.** Status pill neutral grey with a check icon ("Watching", `Libraries.tsx:63`), "1 failed" in red bold under it (`Libraries.tsx:155`). The unwritable warning is a bold red sentence inside the Name cell (`Libraries.tsx:141`): at 1440 it is the loudest thing on the page, louder than the failed count, and it repeats on the detail page header and twice under the Folder field (`lib2-general-1440-dark.png`).
- **States.** Empty: icon, "No libraries yet. A library is a folder that is watched for videos.", Add library (good; `Libraries.tsx:129`). Error: icon, sentence, Try again (good). Loading: "Loading…" text.
- **Consistency.** Toolbar buttons are ghost 32 px; on Workflows the same toolbar has the same shape (good); on Jobs the toolbar's right side holds filters styled as buttons (not good, see 3.4).
- **Density.** Eight columns for seven facts, two of which (Priority "Normal", Last scan) are rarely the reason to look. At 390 the row is right: title, one muted line, pill, chevron (`list-390-dark.png`).
- **Wording.** "Watching" is a good status word. The detail page's Priority hint (`lib2-general-1440-dark.png`) is a 2-line sentence under a select that most people never change; move it to the "?" help.
- **Detail page.** The two-column body (form, first-5 files) is the best-looking screen in the app in light mode (`lib2-general-1440-light.png`). The "Convert them again…" offer (`qa445/dialog-1440-dark.png`) is a bordered panel with a bold question, a sentence and one primary: this is the right shape for every inline confirmation in the app.

### 3.3 Workflows (list, stack editor, graph editor)

Evidence: `pass6/after/qa431-9b9421/after/workflows-1440-light.png`, `pass5/after/workflows-list-1440-dark.png`, `workflows-list-390-dark.png`, `pass5/before/workflow-editor-1440-light.png` (2026-11-19 19:07, older than code), `states-workflows-*`; `screens/Workflows.tsx:117-140`; `screens/WorkflowStack.tsx:246-300`; `screens/WorkflowGraph.tsx:337-363`; `shell.css:451-481, 597-632, 699-720`.

- **Hierarchy (list).** Tabs Workflows / Templates, toolbar, table. The star column (`shell.css:407`) puts a 32 px empty-looking button before every name. "Recent runs" is the most informative cell and sits in the middle in 12 px grey.
- **Hierarchy (stack).** The header row holds Rename, a Published pill, "v1 published", Graph, undo, redo, Check, Save draft, Publish (`workflow-editor-1440-light.png`; the code adds a Variables view since). Nine controls at equal weight; Publish is the only primary, which is right, but it is at the far end of a row that also contains the title. "Check: no problems" in green under it is a status line styled as a success message.
- **Stack rows.** Number, icon, bold name, grey summary, right column "Always on" (text) or a switch, chevron (`WorkflowStack.tsx:259-273`, `shell.css:463-468`). Good. The mix of a word and a switch in the same column makes the column ragged; a disabled switch with "Always on" as a title, or a lock icon (which `shell.css:455` already defines), would align it.
- **Graph (from code).** `.graphwrap` is a 3-column grid of cards (260 / 1fr / 300 px) with the canvas capped at `min(72vh, 720px)` (`shell.css:598-600`, `:700-701`); the page header and 20 px page padding stay around it. Node cards are 188 px with a left colour bar, 13 px title, 11.5 px summary and 12 px port labels (`shell.css:602-611`). Zoom controls are a small pill bottom-left (`shell.css:621`); minimap bottom-right, hidden on phones (`shell.css:627-628`). The palette items are 6 px-padded buttons with 11.5 px small text (`shell.css:704-707`). All of this is competent and all of it is small.
- **Typography.** 13 / 12 / 11.5 / 11 / 10.5 px inside the graph (`shell.css:602-616`). Five sizes under 13 px in one component.
- **Colour.** Category colours via `--cat-*` on the left bar and icon only; the node body is the same surface as every card. The failure edge is dashed red (`shell.css:618`), good. The problem tag is a warning-coloured pill over the node (`shell.css:614`), good.
- **States.** Workflows empty: icon, "No workflows yet. A workflow says what happens to each file.", New workflow (good). Graph on a phone: a one-line note "Editing works best on a larger screen" (`WorkflowGraph.tsx:337`), good.
- **Wording.** Step names and summaries are the best copy in the product ("Leave some files alone: Skips Dolby Vision files…", "Check the result: The new file must play through, look like the original and hold what you chose").

### 3.4 Jobs and the job log

Evidence: `pass6/after/qa445/jobs-done-1440-light.png`, `jobs-done-1440-dark.png` (same layout), `qa446/list-390-dark.png` (Libraries; the Jobs phone list follows the same rules), `pass5/after/jobs-drawer-failed-1440-dark.png`, `jobs-drawer-done-1440-light.png`, `jobs-drawer-skipped-390-dark.png`, `sweep-jobs-drawer-1440-dark.png`, `plex/1440-dark-jobs_tab_completed_job_48165.png` (2026-11-19 07:45, older), `states-jobs-empty-1440-dark.png`; `screens/Jobs.tsx:21, 63-66, 163-205, 265-281, 354-378, 483-523`; `shell.css:537-552, 582-589`.

- **Hierarchy (list).** Tabs with counts (good), then a toolbar with bulk actions left and three filters right, then the table. The filters "Original in the trash" and "Original left in place" are styled as 32 px buttons (`jobs-done-1440-light.png`) and read as actions; they are toggles.
- **Rows.** File name bold, a muted line "Movies · Arrival (2016) · HEVC 1080p", a Status pill, a Details sentence, Worker, Finished (`Jobs.tsx:191-205`). The progress bar only renders when `j.progress` exists (`Jobs.tsx:63`), so on Waiting, Done and Failed there is no visual at all; on Running it is a 6 px bar in the Details column.
- **Spacing.** Rows 50 px with two text lines; the Details column is 470 px wide and the Worker column is often empty, so the row is a long thin strip with text at the left and a time at the right.
- **Typography.** 13.5 bold / 12 muted / 12 pill / 12 details. Same as everywhere.
- **Colour.** "Done" pill green-tinted with green text (`shell.css:130`); "Failed" red-tinted (`:131`); "Skipped" neutral. This is the right vocabulary; it is only missing from the lists that are not Jobs.
- **Drawer.** 520 px (`shell.css:201`), header with name (16 px, `shell.css:542`), a status pill, "ID n" with copy, then the primary action (Retry / Convert again), then sections whose headings are 13.5 px grey (`shell.css:544`): "What the check found" (a note box with a 3-row key/value table), File, Workflow, Timeline (`jobs-drawer-failed-1440-dark.png`). The timeline is excellent: icon, sentence, time, and a "History: Waiting 06:52 PM → Running → Failed" line. The Log section has a search field, Copy log, Download the full log, then collapsible step sections with Done pills. Weak points: section headings are smaller than body text; the Source path in mono wraps mid-path; the File key/value list uses a 150 px label column (`shell.css:182`) so "Original: In the trash for 7 more days, then deleted · Get original back" wraps awkwardly.
- **"Original kept" pill on a failed job** (`jobs-drawer-failed-1440-dark.png`): the pill is a warning colour but says something reassuring; the row in the list says "Failed". Reading the drawer alone, the state is unclear until the "Why it failed" heading.
- **States.** Empty: icon, "No jobs yet. Jobs start when a library finds videos.", Add library (good, `Jobs.tsx:185`). Log loading: "Loading the log…" (`Jobs.tsx:367`).
- **Phone.** The drawer is a full-screen sheet with a back arrow (`jobs-drawer-skipped-390-dark.png`), hierarchy clear, "Why" first. Good.
- **Wording.** "11 GB → 5.9 GB, 48% smaller · original in the trash 7 more days" is exactly the right sentence; it just wants to be two chips and a number rather than prose.

### 3.5 Workers

Evidence: `pass6/after/qa437d/workers-1440-dark.png`, `workers-390-dark.png`, `states-workers-*`; `screens/Workers.tsx:60-83, 124-139, 176-178, 300-355`; `shell.css:559-580`.

- **Hierarchy.** Header: title, "?", "Workers on · 1 of 1" switch, "Add a worker". One card at 380 px wide in a 1160 px column (`shell.css:560` grid `minmax(340px,1fr)`), so with one worker 65% of the page is empty.
- **Encoder tiles.** Six tiles in a 2-column grid inside the card (`Workers.tsx:80`, `shell.css:567-578`): the lit one has an accent border and "Works · Used first" in green; the five others are dashed, grey, with a sentence ("No Intel graphics in this computer") and the codec names struck through (`shell.css:577`). Strike-through reads as "removed", the dashed border as "drop target", and together they make a healthy single-CPU machine look like a page of errors.
- **Typography.** Card title 15 px, facts 12 px, tiles 12 px, codec names 12 px.
- **Colour.** Good restraint: one accent border, one green word.
- **States.** Empty / loading / error exist (`states-workers-*`). The "Stop" question with two radios (`Workers.tsx:116-119`) is the right pattern.
- **Phone.** The card is full width, tiles 2-up, all readable (`workers-390-dark.png`).
- **Wording.** "last heard 1 s ago", "1 file at once · Change", "This worker uses the processor (CPU)." are good. "Not on this computer" × 3 and "No … in this computer" × 3 say the same thing six ways.

### 3.6 Settings

Evidence: `pass6/after/settings-appearance-1440-dark.png`, `pass5/after/settings-general-1440-dark.png`, `settings-processing-1440-light.png`, `settings-health-1440-dark.png`, `settings-safety-390-dark.png`; `App.tsx:36`; `screens/SettingsMore.tsx:59-153, 198`; `shell.css:170-175, 595, 648`.

- **Hierarchy.** Title, ten tabs, cards. The active tab is bold with a 3 px underline (`shell.css:172`). Cards have a 15 px bold title in a header strip and 16 px padding.
- **Spacing.** Cards are 20 px apart; fields 12 px apart; a card with one select and one hint (Processing, `settings-processing-1440-light.png`) is 250 px tall in a 1160 px column. The Access token field is 900 px wide for a 32-character value (`settings-general-1440-dark.png`).
- **Typography.** As everywhere. Hints 12 px grey under 13.5 px controls.
- **Colour.** Radios use the accent (`shell.css:347`). Light mode is clean.
- **States.** Health is a model page: summary card, then grouped OK/Fail/Not present pills with a sentence each and a "Fix:" line (`settings-health-1440-dark.png`).
- **Consistency.** Save sits inside each card on General and Appearance, but Processing (`settings-processing-1440-light.png`) saves on change with no button, and Safety has Save under one field (`settings-safety-390-dark.png`).
- **Density.** Right for a settings page. Ten tabs are the upper limit; on a phone the strip scrolls with an edge shade (`shell.css:595`, `settings-safety-390-dark.png` shows "nce … Processing Safety Connections No" cut at both ends, which is the intended hint but looks like clipping).
- **Wording.** Good: "Kept in this browser. Another browser or computer keeps its own.", "0 means the original is deleted right after the new file has been checked."

### 3.7 First run and sign-in

Evidence: `proof/ui-v2/shots-bfa8a12/start-step1-{1440,390}.png` (2026-11-11; older than the code, which since added the progress bar and the health-checks link); `screens/Start.tsx:38-77, 128, 175-186`; `screens/SignIn.tsx:24-38`; `shell.css:338-340`.

- **Hierarchy.** "Set up Shrink Your Shit" (20 px) with "0 of 3 done" on the right, one sentence, a 6 px progress bar, step 1 as an open card, steps 2 and 3 as dimmed collapsed cards. Clear. The 236 px rail with six destinations and the search field remain visible (the shot and `Shell.tsx:103-132` agree), so a first-time user sees eleven things to click of which one matters.
- **Spacing.** The step card is 880 px wide at 1440 (the content column with no max-width), so the folder input is 850 px wide.
- **Typography.** Step titles 15 px in a card header; the folder check result in green 13.5 px bold.
- **States.** The folder check has inline pass/fail lines and a "Files in this folder / Is this the right folder?" table (`Libraries.tsx:240-249`): good. Step 3 ends with "Start the worker" (primary) and "Not yet" plus a sentence saying what each does (`Start.tsx:185-186`): good.
- **Sign-in.** Title, one sentence, token field with Show, Sign in, "Where do I find it?" disclosure with per-OS lines (`SignIn.tsx:27-38`). Correct and plain; it is an ordinary page rather than a centred card, which is fine.

### 3.8 Mobile (390 px)

Evidence: `pass6/after/dashboard-near-390-dark.png`, `qa446/list-390-dark.png`, `qa439/lib2-output-390-dark.png`, `qa437d/workers-390-dark.png`, `pass5/after/settings-safety-390-dark.png`, `workflows-list-390-dark.png`, `jobs-drawer-skipped-390-dark.png`, `proof/ui-v2/shots-bfa8a12/start-step1-390.png`; `shell.css:212-235, 387-404, 433-440, 752-767`.

- **Top bar.** Menu button, page title, search icon, theme icon (`shell.css:227-235`): right.
- **Lists.** Rows stack to a bold title, one muted line (up to 4 lines), a pill and a chevron; the whole row is the link (`shell.css:387-404`). Right.
- **Dashboard.** 1875 px tall: headline 36 px, four numbers 2×2, both charts in full with legends and footnotes, Workers as a stacked table, Converting now, Libraries as four-line stacked rows, the note. The charts alone are 700 px. Fold them.
- **Library detail.** The breadcrumb title, four header buttons on two rows, the red warning sentence, tabs, then the form; the folder input clips its value (`lib2-output-390-dark.png`). The two red check lines under the Folder field take 160 px.
- **Settings tabs.** One scrolling row with edge shades (`shell.css:648`); the first and last visible tabs are cut mid-word, which is the shade doing its job but looks like a bug.
- **Workers, Jobs drawer, first run.** All fine at 390.
- **Targets.** 44 px on coarse pointers throughout (`shell.css:496-510`). Good.

### 3.9 Cross-cutting

- **Icons.** 72 inline SVG symbols in `icons.svg`, stroke 1.75 at 18 px (`shell.css:15`): consistent and light; keep.
- **Pills.** Four states (run, ok, bad, warn) plus neutral (`shell.css:126-132`): the vocabulary is complete; the lists just do not use it for their most important column.
- **Buttons.** `.btn` 36 px, `.btn.sm` 30 px, toolbar buttons 32 px (`shell.css:86-96`, `:367-370`): three heights in one toolbar row (Add library 32, search 32, "?" 24).
- **"Sample data" pill.** Warning-tinted pill next to the title on mock pages (`PageHead.tsx:9-13`): good replacement for the old prototype banner.
- **Light theme.** Borders `#D8E0EB` on white (`tokens.css:40`) are faint; table rows nearly merge (`jobs-done-1440-light.png`). Everything else holds.

---

## 4. Proposed changes (public-safe)

This section can be copied into an issue or a design note. It names no other product. "Row grammar", "stat tile" and "progress underline" are our names for the patterns.

Priority: P1 do first, P2 next, P3 when convenient. Effort: S under a day, M one to three days, L more.

### 4.1 Changes

| # | What | Why | Screens | Pri | Eff |
|---|---|---|---|---|---|
| C1 | **One list-row grammar.** A row is: a 40 px square *type tile* on the left (folder icon for a library; codec and resolution for a file, e.g. "HEVC / 1080p"; a machine icon for a worker), a bold title, under it one *chip line* of small neutral chips grouping related facts (library · workflow · worker), a right rail with the time or size and one status pill, and a 3 px *progress underline* along the bottom edge while the row is live. Rows are 56 to 64 px; no table header; columns only on the Waiting tab (position). On phones the same row loses the right rail into a third line. | Ends the sparse-table look at 1440 and the eight-column read; every list looks the same, so each one is learned once. | Libraries, Jobs, Workflows, Workers, the Dashboard's lists | P1 | M |
| C2 | **Dashboard as stat tiles plus live rows.** Replace the 44 px headline band with three tiles across the top: *Space saved* (big number, "per day" line, 30-day sparkline of freed bytes), *Files converted* (number, per day, sparkline with done/skipped/failed stacked), *Right now* (converting · waiting · failed in 24 h, with a small "Workers on" switch). Under them "Converting now" as C1 rows with three labelled sub-values (Step · Speed · Time left) and the progress underline. Then "Libraries" as one row per library with a thin saved-share bar and the saved bytes right-aligned. The two day charts move to the bottom under "Last 30 days", collapsed on phones. Drop the explanatory paragraph into the "?" help. | The first screen shows numbers before sentences and live work before history; the honest split (freed / waiting / left in place) stays, as the tile's secondary line and the chart. | Dashboard | P1 | M |
| C3 | **Type scale and spacing scale.** Body 14 px, small 12.5 px, chips 12.5 px minimum, section titles 16 px, page titles 24 px with the page icon, display numbers 32 px (tiles only). Spacing tokens `--s1..--s7` = 4/8/12/16/24/32/48; content gap 24 between sections, 16 inside cards, page padding 24 at 1100 px and up. List rows 56 px (compact density 48). | Removes the "everything is small except one thing" feel and records the density decision that `tokens.css` made silently. | All | P1 | S |
| C4 | **Colour for "alive" only.** Active nav item: accent-tinted background plus the existing bar, bold label. Progress underline and running pill: accent. Saved figures and Done: success colour (text, not fill). Failed: error. Everything idle (Watching, Ready, Skipped, OK) stays neutral. Pills always carry an icon plus a word (already the rule). | The page reads at a glance: colour is where work or trouble is. | All | P2 | S |
| C5 | **Page header.** One 56 px row: page icon in a 32 px tile, 24 px title, optional one-line subtitle in muted text, actions right-aligned with exactly one primary. The top bar keeps search and theme. The "?" help stays, 24 px, after the title. | Gives every page the same anchor at the top-left; today the title is 20 px and the top bar above it is mostly empty. | All | P1 | S |
| C6 | **Toolbar discipline.** One toolbar height (36 px). Filters are segmented toggles or chips, not buttons; bulk actions appear only while rows are selected (replace the toolbar's left side with "N selected · Retry · Cancel"). Search right-aligned, 240 px. | Buttons that are really toggles ("Original in the trash") stop looking like actions. | Jobs, Libraries, Workflows | P2 | S |
| C7 | **Skeleton loading.** Each page's loading state draws grey blocks in the shape of its loaded layout (header, three tile outlines, four row outlines) instead of the word "Loading…". | No layout jump, and the page looks like itself from the first frame. | Dashboard, Jobs, Libraries, Workflows, Workers | P2 | S |
| C8 | **Job drawer hierarchy.** Header: type tile, file name (16 px), status pill, size delta ("11 GB → 5.9 GB · 48% smaller") as the one big fact; sections as cards with 15 px titles; "What the check found" as a two-column table; the File section with a 120 px label column and paths that wrap at slashes. For a failed job the header pill says "Failed" and the "Original kept" fact moves into the first line of the body. | The drawer's content is already the best in class; its headings are smaller than its body text. | Jobs | P2 | S |
| C9 | **Workers: calmer encoder tiles.** Show encoders as a chip row ("Processor · Works · Used first", "Intel · Not found", "NVIDIA · Not found"). No strike-through, no dashed boxes; a found-but-failed encoder gets a warning pill. The worker card gets a 2-column layout at 1440 (facts left, encoders right) so one worker fills the page. | Six dashed boxes with struck-through codecs make a healthy machine look broken. | Workers | P2 | S |
| C10 | **Editors as workspaces.** Graph view: the canvas fills the viewport below a 48 px editor toolbar (name on the left; Check, Save draft, Publish on the right; undo/redo and view switch in the middle), with the palette as a collapsible 240 px column and the inspector as a 320 px column, both inside the editor frame rather than as cards on the page. Node cards: 200 px, 14 px title, a category-coloured header strip, 12.5 px minimum text. Stack view: the same toolbar; the step rows as C1 rows with the number as the tile. | The two most complex screens stop looking embedded in a page. | Workflows | P2 | M |
| C11 | **First run without the rail.** On `/start` and `/signin`, hide the navigation rail and the search field (keep the brand and the theme button), centre a 720 px column, show a three-step stepper above the open step. | A first-time user sees one thing to do. The design already says every page goes to `/start` until setup is done. | First run, Sign-in | P2 | S |
| C12 | **Phone dashboard.** Tiles 2 × 2 (the third tile splits), "Converting now" and "Libraries" rows, charts behind a "Last 30 days" disclosure, the note in the help. | The phone page is 1875 px tall today. | Dashboard (390) | P3 | S |
| C13 | **Inline warnings once.** A library's folder problem appears once per page: as the row's status pill ("Can't write" with a warning icon) in the list, and as one line under the Folder field on the detail page (not also in the header). | The same red sentence is shown three times on one screen today. | Libraries | P3 | S |
| C14 | **Light theme borders.** Raise `--border` in light mode one step (about `#CCD5E1`) so table rows and cards keep their edges. | Rows nearly merge on white. | All (light) | P3 | S |
| C15 | **Settings as a grouped list** when the tab count passes ten: a left list with groups (General, Processing, Safety, Connections, System) and the content on the right; until then keep the tabs. | Ten tabs is the limit of a strip; two more screens are planned. | Settings | P3 | M |

### 4.2 Design-token table

The live source is `web/src/styles/tokens.css` (the v2 theme file, `DESIGN/v2/THEMES.md`). `DESIGN/DESIGN-SYSTEM.md` is the older note for the previous UI, kept here as the reference point because it is the last written type and spacing decision. Values marked "none" do not exist as a token today.

| Token | DESIGN-SYSTEM.md | tokens.css at 294e3d88 | Proposed | Note |
|---|---|---|---|---|
| `--fs` (body) | 16 px | 13.5 px (`:92`) | 14 px | 16 was never built; 13.5 reads small at a metre; 14 keeps density |
| `--fs-sm` | 14 px ("small"), 13 px minimum | 12 px (`:93`) | 12.5 px | chips and hints; nothing below 12.5 anywhere (today 11.5, 11, 10.5 in `shell.css:375, 602-616`) |
| `--fs-lg` (section, card title) | 18 px (h2) | 16 px (`:94`) | 16 px | keep |
| `--fs-xl` (page title) | 24 px (h1) | 20 px (`:95`) | 24 px | with the page icon tile |
| `--fs-display` | none | 44 px hard-coded (`shell.css:724`) | 32 px, tiles only | one display size, used in stat tiles and nowhere else |
| `--fw-strong` | bold | 600 (`:96`) | 600 | keep; no 700 |
| `--s1..--s7` | 4/8/12/16/24/32/48 | none (`--gap` 12, `--pad` 16, `--page-pad` 20 at `:99`) | 4/8/12/16/24/32/48 | add; map `--gap`→`--s3`, `--pad`→`--s4`, section gap `--s6`, page pad `--s6` ≥ 1100 px, `--s3` on phones |
| `--row-h` | 44 px targets | 44 px (`:99`) | 56 px list rows, 44 compact, 48 table rows | rows carry two lines plus a chip line |
| `--chip-h` | badge 13 px text | 20 px (`:97`) | 22 px | with 12.5 px text |
| `--radius` / `--radius-sm` | 8 px | 8 / 6 px (`:86-87`) | 8 / 6 | keep |
| `--shadow` | none | none (`:88`) | none for cards; `--shadow-pop` for popovers and drawers | popovers already use inline shadows (`shell.css:57, 191, 201`); name the one value |
| `--nav-w` | no sidebar in v1 | 236 px (`:101`) | 220 px (collapsed 64) | with 14 px labels 220 is enough; or keep 236 and reclaim nothing |
| `--topbar-h` | top bar | 56 px (`:101`) | 56 px | keep; move the page title into it only if C5's header is not adopted |
| `--bg --sidebar --surface --raised` | `--bg --surface --surface-2` | four dark and four light values (`:9-12`, `:34-37`) | keep; add `--surface-2` as an alias of `--raised` for chips | the four-step ladder is what gives depth; use it on chips and tiles |
| `--border` (light) | `--border` | `#D8E0EB` (`:40`) | `#CCD5E1` | C14 |
| `--border-strong` | `--border-strong` | `#6E7F96` / `#7D8BA0` (`:16`, `:41`) | keep | 3:1 outlines |
| `--accent --on-accent --accent-text` | `--accent --accent-text` | `#6C8CFF`, `#0D1117`, `#7C99FF` dark (`:17-20`) | keep | presets at `:71-81` |
| `--accent-soft` | `--accent-soft` | none (built inline as `color-mix(... 14%)` in `shell.css:128`) | `color-mix(in srgb, var(--accent) 14%, transparent)` | name it; same for `--ok-soft`, `--warn-soft`, `--bad-soft` (DESIGN-SYSTEM's `-soft` family) |
| `--success --warning --error` + `-text` | `--ok --warn --bad` + `-soft` | present (`:21-23`, `:46-48`, text variants per THEMES.md) | keep names; add the `-soft` fills | the vocabulary is complete; the lists do not use it |
| `--focus` | 3 px ring | 2 px ring, offset 2 (`shell.css:14`) | keep 2 px | WCAG 2.2 AA |
| `--motion` | none required | 150 ms (`:105`) | keep | reduced-motion handled |
| `--font` | system stack | system stack (`:89`) | keep | no web fonts |

### 4.3 Clean-room check of this section

The proposals section (from the "## 4." heading to the end of the file) was searched for other products' names:

It returned no matches.

(empty result. The bracket in the pattern stops the command line matching itself; the same command and its live output are at the end of the file.)

---

## 5. Mockups

Simple self-contained HTML in `ui-mockups/` (our token names and our palette, no external assets; each opens in a browser and follows the system theme):

| File | Screen | What it shows |
|---|---|---|
| `ui-mockups/dashboard.html` | Dashboard (C2, C3, C4, C5) | Header with icon tile; three stat tiles with sparklines; "Converting now" rows with Step · Speed · Time left and a progress underline; "Libraries" rows with a saved-share bar; the 30-day charts as a collapsed section |
| `ui-mockups/libraries.html` | Libraries list (C1, C6, C13) | Row grammar: folder tile, name, chip line, right rail with the status pill, a "Can't write" warning as the pill, the toolbar with one primary and segmented filters; the phone variant at the bottom |
| `ui-mockups/jobs.html` | Jobs list and drawer (C1, C6, C8) | Running and Done rows with codec/resolution tiles, chips, the size delta, progress underlines; the drawer with a tile header, the size delta as the big fact, card sections |
| `ui-mockups/workflow-editor.html` | Workflow editor shell (C10) | A 48 px editor toolbar with grouped controls and one primary; the stack view rows with number tiles; a graph frame sketch with palette and inspector columns inside the frame |

ASCII of the two most important ones, for the reader without a browser:

```
Dashboard (1440)
┌ rail ┐┌──────────────────────────────────────────────────────────────────────────┐
│      ││ [search………………………………]                                           (theme) │
│ Dash ││──────────────────────────────────────────────────────────────────────────│
│ Jobs ││ [▦] Dashboard                      Workers on · 1 of 1  [⟳ Scan all]     │
│ Work ││                                                                          │
│ Libs ││ ┌ Space saved ───────┐ ┌ Files converted ───┐ ┌ Right now ─────────────┐ │
│ Wkrs ││ │ 1.2 TB   ▁▂▃▅▆▇█  │ │ 1,284    ▂▃▃▅▆▇█  │ │ 2 converting · 640 wait │ │
│ Sett ││ │ 38 GB per day      │ │ 70 per day         │ │ 2 failed in 24 h        │ │
│      ││ │ 3 MB waiting · 2 MB│ │ 1 skipped · 2 fail │ │ Workers on ● 1 of 1     │ │
│      ││ └────────────────────┘ └────────────────────┘ └─────────────────────────┘ │
│      ││ Converting now                                                  All jobs │
│      ││ ┌──────────────────────────────────────────────────────────────────────┐ │
│      ││ │[HEVC] Arrival.2016.1080p.mkv      Step Convert · Speed 2.7× · 12 min │ │
│      ││ │[1080p] Movies · Shrink videos · This computer            Running 48% │ │
│      ││ │━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━         │ │
│      ││ └──────────────────────────────────────────────────────────────────────┘ │
│      ││ Libraries                                                  Libraries page │
│      ││ [▭] Movies     Shrink videos · 640 waiting · scanned 1:30 AM    820 GB   │
│      ││     ▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬░░░░░░░░░░   1 failed │
│      ││ [▭] TV Shows   Shrink videos · 360 waiting · scanned 1:30 AM    310 GB   │
│      ││ ▸ Last 30 days (charts)                                                  │
└──────┘└──────────────────────────────────────────────────────────────────────────┘

Jobs row (1440)                                     Jobs row (390)
┌─────────────────────────────────────────────┐     ┌──────────────────────────┐
│[HEVC ] Arrival.2016.1080p.BluRay.mkv  2:12 AM│     │[HEVC] Arrival.2016…mkv   │
│[1080p] Movies · Arrival (2016) · 11 GB→5.9 GB│     │       Movies · 48% smaller│
│        ● Done  48% smaller  trash 7 more days│     │       ● Done  2:12 AM   ›│
└─────────────────────────────────────────────┘     └──────────────────────────┘
```

---

## 6. Ordered slices for ui-shop

Small, in dependency order; each names its check. "Before/after" means screenshots at 1440 and 390 in both themes under `slices/11-ui-web/<pass>/{before,after}/`, taken on the same fixture (the pass6 mock data is enough).

| # | Slice | Depends on | Effort | How we'll know it's better |
|---|---|---|---|---|
| U1 | **Tokens** (C3, C14): `--fs` 14, `--fs-sm` 12.5, `--fs-xl` 24, `--fs-display` 32, `--s1..--s7`, `--row-h` 56/48, `--chip-h` 22, the `-soft` fills, light `--border`; replace every hard-coded px under 13 in `shell.css` with a token | none | S | Before/after of Libraries, Jobs, Dashboard. In devtools, no rendered text under 12.5 px on any of the seven pages (count with a one-line script over `getComputedStyle`). axe: 0 contrast violations in both themes. No horizontal scroll at 390. |
| U2 | **Page header** (C5): icon tile, 24 px title, subtitle slot, actions right, one primary | U1 | S | Before/after of all seven pages at 1440: header height identical on every page; exactly one `.btn.primary` in each header; the "?" help stays where it was. |
| U3 | **Row grammar on Libraries** (C1, C13): tile, title, chip line, right rail, status pill incl. "Can't write"; drop Priority and Last scan into the chip line; keep selection checkboxes behind a "Select" toggle | U1 | M | Before/after at 1440 and 390. At 1440 a library reads in two lines and the row is ≤ 64 px; no table header; the folder warning appears once per page (grep the DOM for the warning text: 1 hit on the list, 1 on the detail). |
| U4 | **Row grammar on Jobs** (C1, C6): codec/resolution tile, chips, the size delta as the right-rail fact, progress underline on live rows, filters as segmented toggles, bulk actions only while selecting | U3 | M | Before/after of each tab at 1440 and 390. The Running tab shows every running job's percent without opening a row; the Waiting tab keeps the position column; the "Original in the trash" filter is visibly a toggle (aria-pressed) and not a `.btn`. |
| U5 | **Row grammar on Workflows and Workers** (C1, C9) | U3 | S | Before/after. Workflows: "Recent runs" is the second line, the star is a 24 px icon inside the row. Workers: no `text-decoration: line-through` anywhere; one worker fills ≥ 80% of the content width at 1440. |
| U6 | **Dashboard tiles** (C2 part 1): three stat tiles with sparklines replace the headline band; the four plain numbers fold into the tiles; charts move under "Last 30 days" at the bottom | U1, U2 | M | Before/after at 1440 and 390. Above the fold at 1440×900 you see the tiles, "Converting now" and the first library row; no text larger than 32 px; the honest split (freed / waiting / left in place) is still on the page (tile sub-line and chart legend). |
| U7 | **Dashboard live rows and library bars** (C2 part 2): "Converting now" as rows with Step · Speed · Time left and the underline, driven by the existing SSE progress; "Libraries" as rows with a saved-share bar | U4, U6 | M | With a running job (fixture or dogfood), the percent, step and speed update in place without a reload (watch the DOM for 30 s). Each library row shows a bar whose width equals saved ÷ (saved + size now) to within 1%. |
| U8 | **Colour pass** (C4): active nav tint, accent underline, success-coloured saved figures, neutral idle pills | U3-U7 | S | Greyscale screenshots of Dashboard and Jobs still readable (every status has a word and an icon). Count of accent-filled elements per page ≤ 3 (nav item, primary button, progress). |
| U9 | **Skeleton loading** (C7) on Dashboard, Jobs, Libraries, Workflows, Workers | U2-U6 | S | Throttled network screenshots: the y-position of the first row and the header differs by < 8 px between the loading and the loaded frame. No "Loading…" string on those pages. |
| U10 | **Job drawer** (C8) | U4 | S | Before/after of a Done, a Failed and a Skipped job at 1440 and 390. Section titles ≥ 15 px; the size delta is the largest text in the drawer header; a failed job's header pill reads "Failed". |
| U11 | **Editor shell** (C10): toolbar, full-height canvas, palette and inspector inside the frame, node sizes | U1, U2 | M | Before/after at 1440 (graph and stack) and 390 (stack). At 1440 the canvas height equals the viewport minus header and toolbar (no page scroll with the canvas open); exactly one primary (Publish); no text under 12.5 px in a node. |
| U12 | **First run and sign-in without the rail** (C11) | U2 | S | At 1440 and 390, `/start` and `/signin` show no `nav.rail` and no search field; Tab order goes brand → theme → first field; keyboard-only completion of step 1 still works. |
| U13 | **Phone dashboard** (C12) | U6, U7 | S | At 390 with the pass6 "near" fixture, page height < 1,300 px; the charts open from a disclosure; tiles are 2×2. |
| U14 | **Settings grouped list** (C15), only when an eleventh tab lands | U2 | M | At 1440 a left list with groups and the active item; at 390 the list collapses to a select; every existing settings route still resolves. |

Checks that apply to every slice: `DESIGN/ACCEPTANCE.md` A11Y-1 (contrast), A11Y-2 (keyboard), A11Y-5 (390 px, no horizontal scroll), and the existing before/after screenshot habit of `slices/11-ui-web`.

---

