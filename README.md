# shrink-your-shit

Free, open-source tool that shrinks your video library. Point it at a folder: new
video files are checked with `ffprobe`, and the ones that match a simple rule
(default: **H.264 above 4000 kbps → HEVC**) are re-encoded with `ffmpeg` in the
background. A small web page shows the queue and progress.

Licensed under **AGPL-3.0-or-later** (see `LICENSE`). No paid tiers, ever.

## Run it (Linux or WSL2)

1. Install ffmpeg and Go 1.22+:
   ```
   sudo apt install ffmpeg
   ```
   (Go: https://go.dev/dl/)
2. Build and start:
   ```
   go build -o shrink-your-shit .
   ./shrink-your-shit -library /path/to/your/videos
   ```
3. Open http://127.0.0.1:8484 to watch the queue. Stop with Ctrl+C; unfinished jobs
   are queued again next start.

Add `-library` more than once to watch several folders. The database lives in
`./data` (change with `-data`).

## Is it safe for my files?

Yes by design. The new file is written next to the original under a temporary
name (`name.syp-tmp.mkv`). Only when ffmpeg finishes successfully is it moved into
place. If anything fails, the original is untouched and the job shows as *failed*
with a Retry button.

`-output` decides what happens on success:
- `replace` (default): the new HEVC file takes the original's name.
- `beside`: keep the original and write `name.hevc.mkv` next to it.

Tip: `.mkv` files work best, since they accept almost any audio/subtitle track.

## Settings

Run `./shrink-your-shit -h` for all of them. The main ones:

| Flag | Default | Meaning |
|---|---|---|
| `-library` | (required) | folder to watch |
| `-from-codec` | `h264` | only convert this video codec |
| `-min-bitrate` | `4000` | only convert files above this video bitrate (kbps) |
| `-crf` | `26` | quality: lower is better and bigger |
| `-preset` | `medium` | x265 speed: `ultrafast` … `veryslow` |
| `-output` | `replace` | `replace` or `beside` |
| `-scan-every` | `30s` | how often to look for new files |
| `-settle` | `15s` | skip files changed this recently (still copying) |
| `-addr` | `127.0.0.1:8484` | web UI address |

## For developers

```
go build ./... && go test ./...
```
Layout: `internal/media` (ffprobe/ffmpeg behind an `Exec` interface, so tests need no
ffmpeg), `internal/rules`, `internal/jobs` (SQLite store and job states),
`internal/scanner`, `internal/worker`, `internal/web`.

The worker only uses the `worker.Queue` interface, so remote workers can be added
later by implementing it over HTTP. The built-in worker is just one instance.
