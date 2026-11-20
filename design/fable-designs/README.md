# Shrink Your Shit: finished designs

High-fidelity designs of how the app should look after the proposals in `RESEARCH/fable-solo/ui-review.md` (C1 to C15) and its slices for ui-shop (U1 to U14). Made 2026-11-20 by the research analyst seat. Everything here is ours: our words, our names, our palette, invented sample data.

## How to open

- **Browser:** open `index.html`. It links every screen and variant. Each screen is a self-contained HTML page under `screens/` (no fonts, no network, no build step). The pages follow your system theme; add `?theme=light` or `?theme=dark` to force one, and `?state=<name>` to show a list state (for example `screens/jobs.html?state=failed`). Narrow the window below 640 px to see the phone layout.
- **Without a browser:** flip through `png/`. Every screen and state is rendered four ways: `<screen>-<state>-1440-dark.png`, `-1440-light.png`, `-390-dark.png`, `-390-light.png` (full page at 1440×900 and 390×844 viewports). 180 files.
- **Re-render:** `node render.js` from this folder, with the `playwright` package resolvable (for example `NODE_PATH=<a node_modules that has playwright>`) and, if headless Chromium's shared libraries are not installed system-wide, with them on `LD_LIBRARY_PATH`. It reads `manifest.json`.

## What each file is

| File | What it is |
|---|---|
| `index.html` | gallery of every screen, state and PNG |
| `assets/design.css` | the whole design system: tokens (both themes), shell, header, row grammar, pills, chips, tiles, cards, forms, drawer, steps, graph, skeletons, toasts, phone layout |
| `assets/shell.js` | reads `?theme=` and `?state=` and shows the matching block; no visual logic |
| `assets/icons.svg` | the inline icon sprite (stroke icons, 18 px) |
| `screens/*.html` | the screens, one file per screen with all of its states inside (`[data-state]` blocks) |
| `png/*.png` | the renders |
| `manifest.json` | the screen and state list the renderer and gallery use |
| `render.js` | the renderer |

## Screens, and which proposal and slice each one shows

| Screen | File | States | Proposals (ui-review.md §4) | Slices (§6) |
|---|---|---|---|---|
| Dashboard | `screens/dashboard.html` | busy, failures, empty, loading | C2 stat tiles and live rows, C3 scale, C4 colour for "alive", C5 header, C7 skeleton, C12 phone | U1 U2 U6 U7 U8 U9 U13 |
| Libraries list | `screens/libraries.html` | list, loading, empty, error | C1 row grammar, C5, C6 toolbar discipline, C7, C13 warning shown once | U1 U2 U3 U9 |
| Library detail | `screens/library.html` | general, warning, new | C3, C5, C13 | U2 U3 |
| Workflows list | `screens/workflows.html` | list, loading, empty, error | C1, C5, C6, C7 | U2 U5 U9 |
| Workflow: steps view | `screens/workflow-stack.html` | open (step 3 expanded), closed | C10 editor toolbar, C3 | U11 |
| Workflow: graph editor | `screens/workflow-graph.html` | one | C10 full-height frame, palette and settings inside it | U11 |
| Jobs list | `screens/jobs.html` | done, running, waiting, failed, skipped, loading, empty, error | C1, C4, C6 filters as toggles, C7 | U4 U8 U9 |
| Job drawer and log | `screens/job-drawer.html` | done, failed, skipped | C8 drawer hierarchy | U10 |
| Workers | `screens/workers.html` | list, loading, empty, error | C1, C9 calmer encoder chips, C7 | U5 U9 |
| Settings | `screens/settings.html` | general, appearance, processing, safety, health | C3, C5, (C15 stays as tabs until an eleventh tab) | U2 U14 |
| First run | `screens/start.html` | step1, step2, step3 | C11 no rail, centred column, stepper | U12 |
| Sign in | `screens/signin.html` | plain, wrong | C11 | U12 |
| Component sheet | `screens/components.html` | one | C1 row, pills, stat tiles, buttons, page header, inputs, notes, toasts, skeletons, empty/error, progress, timeline | U1 U2 |
| Token sheet | `screens/tokens.html` | one | C3 type scale, spacing, colours, radius, sizes (the values from §4.2); C14 light borders | U1 |

## Notes for whoever builds it

- Token names are the app's (`web/src/styles/tokens.css`); only the values changed, as listed in `screens/tokens.html` and `ui-review.md` §4.2. `assets/design.css` can be read as the target stylesheet, not dropped in as-is: the app's components are React, these are static pages.
- The sample data is invented (titles, sizes, times). Paths are generic (`/media/...`). No hostnames, tokens or addresses.
- Phone layouts are in the same files (media queries at 900 px and 640 px); the renders at 390 px show them.
