# Status and roadmap

True for: `main` after PR #7 (slices 1-7a) plus the one-step installer (slice 7b, this change). The "verified" claims say how and by whom; anything
marked **never verified** has not been seen working by anyone. Update this file when that changes.

## What exists

| Slice | What | How it landed |
|---|---|---|
| 1 | Go foundation: scanner, rule (H.264 at 4000+ kbps -> HEVC), SQLite job queue, built-in ffmpeg worker, embedded web page, AGPL-3.0 | commits on `main` directly (no PR) |
| 2 | Cross-platform: user config data dir, finding ffmpeg/ffprobe, Windows rename retry, hidden-folder skipping, per-OS README notes | PR #1 |
| 3 | GitHub Actions CI: tests on Linux/Windows/macOS, 5-target cross-compile, Docker build + smoke test | PR #2 |
| 4 | Dockerfile, compose example, Debian/Ubuntu install script and systemd unit, `docs/proxmox.md`, `SYP_ADDR` / `SYP_LIBRARIES` | PR #3, docs follow-up PR #4 |
| 5 | Docs (DEVELOPMENT, ARCHITECTURE, STATUS) | PR #5 |
| 6 | Workflow that publishes a multi-arch image to GHCR on version tags (never run) | PR #6 |
| 7a | Workflow + `scripts/build-release.sh` that build the 5 binaries and attach them to a GitHub Release on version tags (never run), `-version` flag | PR #7 |
| 7b | `install.sh` at the repo root: one-line installer for Linux/macOS (replaces the old `deploy/install.sh`) | PR #8 |
| 7c | Windows installer (`installer/windows`, Inno Setup), built and tested on a Windows runner in CI and attached to releases | this change |

## What has been verified, and how

- **Linux, real ffmpeg 6.1 and 8.0:** end-to-end conversions of generated test files (mkv with
  subtitles/attachments, mp4, avi, ts, mov, wmv); H.264 to HEVC, roughly a third of the size,
  streams and permissions kept; Ctrl+C mid-encode requeues the job; `docker stop` the same.
  Run by the builder and independently by a QA pass for each slice.
- **Docker 29.1:** image builds, container healthy, real conversion from a mounted folder,
  named volume for `/config`, clean stop, compose file accepted, `compose up` worked
  (QA pass). The root-owned bind-mount failure was reproduced.
- **GitHub Actions, real runners** (read with `gh run view`, as of 2026-11-06): in the latest completed
  runs (PR runs for slices 3 and 4, and pushes to `main`) every job succeeded: `go vet` and
  `go test` on **ubuntu, windows and macOS**, all five cross-compile builds, and the Docker
  job. A newer run on `main` was still queued when checked. This also shows `setup-go` can
  fetch the Go version in `go.mod`. CI runs the unit tests only (fake ffmpeg), so it says
  nothing about real encodes on Windows or macOS.
- **`install.sh` in clean Debian 12 containers**, against a locally built release served with
  `SYP_BASE_URL=file://...`: ffmpeg installed from apt, checksum verified, a real file converted;
  in a container running systemd the service was created, enabled, restarted after a kill and
  after a container restart; re-run kept settings; non-root with sudo works; failures (no sudo,
  tampered download, missing release, missing library folder, unsupported CPU) give clear errors.
  `shellcheck` 0.10 is clean.

## Windows installer: what has and has not been proven

**Proven:** the installer builds on a GitHub `windows-latest` runner, and `installer/windows/smoke-test.ps1`
passed there (run 42282614770 at commit `6c5e88d`, all jobs green): silent install, ffmpeg download with
the SHA-256 check, the web page answering, a real conversion to HEVC, running the installer again
(upgrade), and uninstall (program, autostart entry and shortcut gone, page stopped).

**Not proven, and not covered by that test:**
- the GUI wizard (the folder page, the download progress page): the test runs `/VERYSILENT`;
- the Start Menu shortcut being *used*, and the program starting at logon (only that the entries exist);
- the "no administrator prompt" claim: the CI runner's user is an administrator, so a standard user
  was never tried;
- Mark-of-the-Web, SmartScreen, Defender, and Windows 11 Smart App Control (a downloaded copy is never
  what the runner tests);
- library paths with non-ASCII characters or network (UNC) paths: the launcher script is written in
  the system's ANSI code page;
- upgrading from an older real install: the test only upgrades over the same build and does not
  check that the web address setting (`SYP_ADDR`) was kept;
- a custom `/ADDR` with a LAN address: the launcher checks `127.0.0.1`, so it would think the page is
  down (the app itself would still run);
- other Windows versions or machines: only `windows-latest` (a Server edition), x64;
- the VBScript launcher's future (below);
- `shrink-your-shit-setup.exe` is not listed in `checksums.txt` (built by a different job);
- uninstall leaves the job database in `%LOCALAPPDATA%\shrink-your-shit\data` on purpose.

The pinned ffmpeg download (Gyan 9.0.2 essentials, SHA-256 recorded when the installer was written)
was extracted and run by the CI test.

- **Windows launcher depends on VBScript (`wscript`)**, which Microsoft is phasing out (optional feature
  on Windows 11 24H2, removal announced), and a hidden `cmd` started from a script is a pattern some
  antivirus flags. A GUI-subsystem build (`-ldflags -H=windowsgui`) or a direct hidden launch would replace it.
- **Windows launcher decision:** the installer keeps the VBScript launcher for now (smallest untested
  surface for the first CI run); a GUI-subsystem build with direct launch would also need a `-log`
  flag and console attachment for `-version`/`-h`, so it is a follow-up once CI is green.
- **The Windows download page** in the installer is not exercised by CI (silent mode skips it).

## Never verified

- A real Windows or macOS **run** of the program (CI only runs the unit tests there, which use
  a fake ffmpeg). Includes: the Windows replace-retry against a genuinely locked file, SQLite on
  a Windows path, UNC and long paths, Ctrl+C on Windows.
- `install.sh` on **macOS** (launchd agent, Homebrew, quarantine flag), on Fedora/Arch/Alpine/openSUSE
  package managers, downloading from real GitHub release URLs (no release exists yet), and
  inside a real Proxmox LXC.
- The two release workflows (`release-binaries.yml`, `release-image.yml`) on GitHub: they have
  only been linted with `actionlint` and their steps run by hand.
- Every Proxmox step in `docs/proxmox.md`: LXC bind mounts and the UID shift, running the
  service in an unprivileged LXC, Docker inside an LXC (nesting/keyctl), Proxmox's OCI image
  import.
- The `winget install Gyan.FFmpeg` package id in the README, and `brew install ffmpeg`.
- Hardware or very large libraries: nothing here has run on more than a handful of small files.

**Database upgrades (slice 8):** schema versioning with automatic backup is in; run on a database
written by the previous build, with real jobs, it upgrades in place, keeps the data, and the new
binary refuses a database marked as newer (checked with the real binaries). An older binary does not
refuse a newer database (it predates the check), so downgrading after an upgrade is unsupported;
restore the `.bak-v<N>` file if you must. An upgrade needs free disk space about the size of the
database for its backup (a full disk blocks the upgrade, with a clear message); the time and space for a
very large database were not measured.

## Known limitations and follow-ups

Collected from the reviews; none is fixed.

- **Crash between rename and "done":** if the process dies after the new file replaced the
  original but before the job is marked done, the next start requeues it and re-encodes the HEVC
  file. The "output must be smaller" check should make that fail safely, but this was reasoned
  about, not tested.
- **Windows read-only originals:** a read-only original may make the replace-rename fail (a
  suspicion, never tried).
- **Windows long paths (>260 chars) and UNC paths** are not specially handled.
- **Network shares:** scan stability on slow shares is untested; `-settle` is the only guard
  against files still being copied. A file that probes fine while half-copied could be queued
  (the worker refuses to replace a file that changed during encoding).
- **SQLite settings** (`WAL`, `busy_timeout`) are set once and rely on `MaxOpenConns(1)`.
- **No authentication.** Anyone who can reach the web page can see file names and retry jobs.
  Do not expose it to the internet.
- **Software encoding only** (x265); no GPU/hardware encoding.
- **One rule, one target:** H.264 at or above a bitrate to HEVC. No other codecs, no per-library rules,
  no flow builder.
- Failed jobs are not retried automatically (manual Retry button).
- Subtitle/attachment copying relies on keeping the input's container; data streams are dropped.
- `go test -race` has not been run (needs cgo).
- No release has been published: the release workflows exist but no version tag has been pushed, so
  the installer has nothing to download and the GHCR package does not exist (and will start private).

## Planned next

Roughly in the order discussed, none started:

1. Windows installer / packaged builds.
2. Remote workers (the `worker.Queue` interface was shaped for this; needs an HTTP pull API).
3. Authentication.
4. Hardware encoding (GPU passthrough on Proxmox).
5. A visual flow/rule builder and more rules than "H.264 to HEVC".
6. Cut the first release (push a version tag), make the GHCR package public, and run the installer against it.
