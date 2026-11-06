# Status and roadmap

True for: `main` after PR #4 (slices 1-4). The "verified" claims say how and by whom; anything
marked **never verified** has not been seen working by anyone. Update this file when that changes.

## What exists

| Slice | What | How it landed |
|---|---|---|
| 1 | Go foundation: scanner, rule (H.264 > 4000 kbps -> HEVC), SQLite job queue, built-in ffmpeg worker, embedded web page, AGPL-3.0 | commits on `main` directly (no PR) |
| 2 | Cross-platform: user config data dir, finding ffmpeg/ffprobe, Windows rename retry, hidden-folder skipping, per-OS README notes | PR #1 |
| 3 | GitHub Actions CI: tests on Linux/Windows/macOS, 5-target cross-compile, Docker build + smoke test | PR #2 |
| 4 | Dockerfile, compose example, Debian/Ubuntu install script and systemd unit, `docs/proxmox.md`, `SYP_ADDR` / `SYP_LIBRARIES` | PR #3, docs follow-up PR #4 |
| 5 | These docs (DEVELOPMENT, ARCHITECTURE, STATUS) | this change |

## What has been verified, and how

- **Linux, real ffmpeg 6.1 and 8.0:** end-to-end conversions of generated test files (mkv with
  subtitles/attachments, mp4, avi, ts, mov, wmv); H.264 to HEVC, roughly a third of the size,
  streams and permissions kept; Ctrl+C mid-encode requeues the job; `docker stop` the same.
  Run by the builder and independently by a QA pass for each slice.
- **Docker 29.1:** image builds, container healthy, real conversion from a mounted folder,
  named volume for `/config`, clean stop, compose file accepted, `compose up` worked
  (QA pass). The root-owned bind-mount failure was reproduced.
- **GitHub Actions, real runners** (read with `gh run view` while writing this): `go vet` and
  `go test` passed on **ubuntu and windows** for the PR runs of slices 3 and 4, and on
  **macOS** for slice 3's PR run (the macOS job for the PR #4 run was still queued when
  checked). All five cross-compile builds and the Docker job passed. This also shows
  `setup-go` can fetch the Go version in `go.mod`.
- `install.sh` passes `shellcheck` 0.10; the systemd unit passes `systemd-analyze verify`.

## Never verified

- A real Windows or macOS **run** of the program (CI only runs the unit tests there, which use
  a fake ffmpeg). Includes: the Windows replace-retry against a genuinely locked file, SQLite on
  a Windows path, UNC and long paths, Ctrl+C on Windows.
- `deploy/install.sh` actually executing on a Debian/Ubuntu machine (it was only linted and read).
- Every Proxmox step in `docs/proxmox.md`: LXC bind mounts and the UID shift, running the
  service in an unprivileged LXC, Docker inside an LXC (nesting/keyctl), Proxmox's OCI image
  import.
- The `winget install Gyan.FFmpeg` package id in the README, and `brew install ffmpeg`.
- Hardware or very large libraries: nothing here has run on more than a handful of small files.

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
- **One rule, one target:** H.264 above a bitrate to HEVC. No other codecs, no per-library rules,
  no flow builder.
- Failed jobs are not retried automatically (manual Retry button).
- Subtitle/attachment copying relies on keeping the input's container; data streams are dropped.
- `go test -race` has not been run (needs cgo).
- No release binaries or published container image yet.

## Planned next

Roughly in the order discussed, none started:

1. Windows installer / packaged builds.
2. Remote workers (the `worker.Queue` interface was shaped for this; needs an HTTP pull API).
3. Authentication.
4. Hardware encoding (GPU passthrough on Proxmox).
5. A visual flow/rule builder and more rules than "H.264 to HEVC".
6. Published release binaries and container image.
