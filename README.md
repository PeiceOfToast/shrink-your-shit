# shrink-your-shit

> **⚠️ Work in progress. Not finished and not ready for use.**
> Expect breakage, missing features and changes without notice. It re-encodes and
> can replace your video files, so do not point it at media you can't afford to lose.


Free, open-source tool that shrinks your video library. Point it at a folder: new
video files are checked with `ffprobe`, and the ones that match a simple rule
(default: **H.264 above 4000 kbps → HEVC**) are re-encoded with `ffmpeg` in the
background. A small web page shows the queue and progress.

Licensed under **AGPL-3.0-or-later** (see `LICENSE`). No paid tiers, ever.

## Run it

You need ffmpeg (which includes ffprobe) and Go 1.22+ to build. It builds without a C
compiler, and the same source builds for Linux, Windows and macOS (Intel and ARM).

1. Install ffmpeg:
   - **Linux / WSL2:** `sudo apt install ffmpeg`
   - **macOS:** `brew install ffmpeg`
   - **Windows:** `winget install ffmpeg`, or put `ffmpeg.exe` and `ffprobe.exe` in the
     same folder as `shrink-your-shit.exe`.
2. Build and start (Windows: `go build -o shrink-your-shit.exe .` and run
   `shrink-your-shit.exe -library "D:\Videos"`):
   ```
   go build -o shrink-your-shit .
   ./shrink-your-shit -library /path/to/your/videos
   ```
3. Open http://127.0.0.1:8484 to watch the queue. Stop with Ctrl+C (works on every OS); a
   job in progress is put back in the queue and starts over next time.

Add `-library` more than once to watch several folders.

**Where things live.** The database goes in your user config folder
(`~/.config/shrink-your-shit` on Linux, `~/Library/Application Support/shrink-your-shit`
on macOS, `%AppData%\shrink-your-shit` on Windows). Change it with `-data` or the
`SYP_DATA` environment variable. ffmpeg is looked for on `PATH`, then next to the
program; point at it explicitly with `-ffmpeg` / `-ffprobe` or `SYP_FFMPEG` /
`SYP_FFPROBE`.

**Per-OS notes.**
- Windows: if a media player or antivirus has the original open when the new file is
  ready, replacing it is retried for about 10 seconds, then the job fails safely and
  can be retried. Very long paths (over 260 characters) are not specially handled.
- macOS/Windows file names are case-insensitive; `.MKV` and `.mkv` are both recognised.
  Hidden files (names starting with `.`, such as macOS `._name.mp4`) are ignored.
- Files being copied in are skipped until they have not changed for `-settle`
  (15 seconds by default); on a slow network share raise it.
- File permission bits are copied on Linux/macOS. Windows has no equivalent, so the
  new file just gets the folder's normal permissions.

## Is it safe for my files?

Yes by design. The new file is written next to the original under a temporary
name (`name.syp-tmp.mkv`). Only when ffmpeg finishes successfully is it moved into
place. If anything fails, the original is untouched and the job shows as *failed*
with a Retry button.

`-output` decides what happens on success:
- `replace` (default): the new HEVC file takes the original's name.
- `beside`: keep the original and write `name.hevc.mkv` next to it.

Tip: `.mkv` files work best. Audio and subtitles are copied unchanged; `.mp4`/`.mov`
files with text/image subtitles will fail safely (original untouched). Attachments
(fonts, cover art) and extra video streams are not carried over.

## Settings

Run `./shrink-your-shit -h` for all of them. The main ones:

| Flag | Default | Meaning |
|---|---|---|
| `-library` | (required) | folder to watch |
| `-data` | user config folder | where the database lives |
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
