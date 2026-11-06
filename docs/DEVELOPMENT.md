# Development guide

True for: `main` at the time this file was written (after PR #4). Commands below were run
on Linux (WSL2/Ubuntu) unless marked otherwise; anything not run is marked **not run**.
When this file and the code disagree, trust the code and fix this file.

## What you need

| Tool | Version | Why |
|---|---|---|
| Go | 1.26 or newer | `go.mod` says `go 1.26.0`; that is the minimum `modernc.org/sqlite` needs. Official `golang` Docker images do not auto-download toolchains, so the `Dockerfile` base (`golang:1.26-alpine`) must be kept in step with `go.mod`. |
| ffmpeg + ffprobe | any recent (tested 6.1 and 8.0) with libx265 | the real encoder. **Unit tests do not need it** (they use a fake runner); only manual end-to-end runs do. |
| Docker | optional (tested 29.1) | to build/run the container image |
| shellcheck, actionlint | optional | lint `install.sh`, `scripts/build-release.sh` and the workflows in `.github/workflows/` |

`go` and `ffmpeg`/`ffprobe` must be on your `PATH`. For the run-it-for-real steps, port 8484
must be free (change it with `-addr`).

No C compiler is needed: the SQLite driver is pure Go and all builds use `CGO_ENABLED=0`.
Install Go from https://go.dev/dl/ (a tarball unpacked anywhere is enough; no root needed).

## Build, vet, test

```
go build -o shrink-your-shit .
go vet ./...
go test -count=1 ./...
```

All three were run and pass on Linux. `go test -race` needs cgo, so it is **not run** here.

### Cross-compile matrix (what CI also does)

```
for t in linux/amd64 linux/arm64 windows/amd64 darwin/arm64 darwin/amd64; do
  GOOS=${t%/*} GOARCH=${t#*/} CGO_ENABLED=0 go build ./... && \
  GOOS=${t%/*} GOARCH=${t#*/} CGO_ENABLED=0 go vet ./...
done
```

Run and passing for all five. `go vet` also compiles the tests for each target. Compiling is
not running: the Windows and macOS tests ran for the first time in CI (see
[STATUS.md](STATUS.md) for what is known).

## Run it for real

```
./shrink-your-shit -library /path/to/COPY-of-videos -data ./data -scan-every 5s
# web page: http://127.0.0.1:8484   (JSON: /api/status)
```

The program **replaces originals by default**. Always develop against a copy (or pass
`-output beside`). Stop with Ctrl+C; a running job goes back to the queue.

### Making a test video

No sample files are stored in the repo; generate them with ffmpeg's built-in test source.
Work in a **scratch folder**, and make the library a **separate folder holding only the final
file**: anything in the library that matches the rule gets converted, including intermediates.
This makes a 12-second 720p H.264 file at 8 Mbps with audio and a subtitle track (large
enough to match the default rule, which needs H.264 of at least 4000 kbps):

```
mkdir -p scratch/lib && cd scratch
ffmpeg -f lavfi -i testsrc2=duration=12:size=1280x720:rate=30 \
       -f lavfi -i sine=duration=12 -c:v libx264 -b:v 8M -c:a aac in.mp4
printf '1\n00:00:00,000 --> 00:00:02,000\nhi\n' > s.srt
ffmpeg -i in.mp4 -i s.srt -map 0 -map 1 -c copy -c:s srt lib/movie.mkv
touch -d '1 minute ago' lib/movie.mkv      # GNU/Linux syntax; macOS: touch -t "$(date -v-1M +%Y%m%d%H%M.%S)" lib/movie.mkv
```

The last line backdates the file: the scanner ignores files changed in the last `-settle`
(15 s). The macOS form was **not run** (no Mac available). Then run the program with
`-library scratch/lib` (see above). Use `-preset ultrafast` to make the encode quick. Expected result (observed many times):
the file becomes HEVC, roughly a third of the size, with audio and subtitles kept, and the
job shows `done`. Check with
`ffprobe -v error -show_entries stream=codec_name -of csv=p=0 scratch/lib/movie.mkv`.

### In Docker

```
docker build -t shrink-your-shit .
docker run -d --name syp -p 8484:8484 -v syp-config:/config -v /path/to/COPY:/media shrink-your-shit -preset ultrafast
docker inspect --format '{{.State.Health.Status}}' syp     # "healthy" after ~10-30s
docker stop syp                                           # exits 0, well under a second
```

Run end to end (build, healthy, real conversion, clean stop). More detail and the Proxmox
steps are in [proxmox.md](proxmox.md). Use a **named volume** for `/config`: a host folder
Docker creates itself is root-owned and the non-root program cannot open its database.

## Releases and the installer

`scripts/build-release.sh <version>` builds the release files into `dist/` (archives for the 5
platforms plus `checksums.txt`) exactly as the release workflow does. To test `install.sh` without
a published release, build a release, serve it as a local folder, and run the installer in a clean
container (this is how it was tested):

```
sh scripts/build-release.sh 0.0.1-test
docker run --rm -v "$PWD/install.sh:/install.sh:ro" -v "$PWD/dist:/release:ro" -v "$PWD/media:/media" \
  debian:12-slim sh -c 'SYP_BASE_URL=file:///release sh /install.sh --library /media --run'
```

(`--run` starts the program in the foreground because a plain container has no systemd.) The
systemd path was tested the same way in a privileged `jrei/systemd-debian:12` container. Neither
has been run against a real GitHub release.

## Continuous integration

`.github/workflows/ci.yml`, triggered on pull requests and pushes to `main` and `dev`:

| Job | Checks |
|---|---|
| `test` (ubuntu, windows, macos) | `go vet ./...` and `go test -count=1 ./...` on each OS |
| `build` (5 targets) | `go build ./...` and `go vet ./...` with `CGO_ENABLED=0` for linux amd64/arm64, windows amd64, darwin arm64/amd64 |
| `docker` | `docker build`, then starts the container and curls `/api/status` |

Go version comes from `go.mod`. Actions are pinned to major versions. Check a PR's status
with `gh pr checks <number>`; what has been observed so far is in [STATUS.md](STATUS.md).

## How changes have been made

- **One concern per pull request**, one branch per concern, small commits.
- Work was done in **slices** (see [STATUS.md](STATUS.md)); later slices were *stacked* on
  earlier branches and the PRs merged in order, rebasing when an earlier one merged first.
- Every slice was checked by a builder, a separate reviewer reading the diff, and a QA pass that
  ran the real binary with real ffmpeg. Findings were fixed in new commits.
- Say what you ran and what you could not run. "Compiles for Windows" is not "works on Windows".
- Unit tests must not need ffmpeg: use `media.FakeExec` (see `internal/media/fake.go`).
- Keep the repository free of personal paths, emails and secrets: it is public.
