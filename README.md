# shrink-your-shit

> **⚠️ Work in progress. Not finished and not ready for use.**
> Expect breakage, missing features and changes without notice. It re-encodes and
> can replace your video files, so do not point it at media you can't afford to lose.


Free, open-source tool that shrinks your video library. Point it at a folder: new
video files are checked with `ffprobe`, and the ones that match a simple rule
(default: **H.264 at 4000 kbps or more → HEVC**) are re-encoded with `ffmpeg` in the
background. A small web page shows the queue and progress.

Licensed under **AGPL-3.0-or-later** (see `LICENSE`). No paid tiers, ever.

## Install (one step)

**Linux and macOS** (also a Proxmox/Debian container): paste this into a terminal.

```
curl -fsSL https://raw.githubusercontent.com/PeiceOfToast/shrink-your-shit/main/install.sh | sh
```

It downloads the program (and compares it with the checksum file published beside it, which
catches a damaged download, not a compromised release), installs ffmpeg if you don't have it,
sets the program up as a service that starts on boot, starts it, and prints the web address.
Run the same line again later to upgrade; your settings are kept. It uses `sudo` on Linux
and tells you when it does; on macOS it installs for your user only (no sudo).

Point it at your videos, and (on a server you will open from another computer) let the
network in. There is no login yet, so only do that on a network you trust:

```
curl -fsSL https://raw.githubusercontent.com/PeiceOfToast/shrink-your-shit/main/install.sh | sh -s -- --library /path/to/videos --addr 0.0.0.0:8484
```

On macOS the service is a login item (a launchd agent), so the installer must be run in a
normal desktop session (Terminal.app), not over plain SSH; the service then starts at login.
The program is unsigned: macOS or Windows may warn the first time it runs.

> **Not working yet:** the installer downloads a published release, and none has been
> published. Until the first release exists the Linux/macOS installer stops with "could not
> download" and the Windows download link gives a "not found" page; use the manual steps below
> instead. The macOS installer has never been run on a Mac, and the Windows installer has only run
> in GitHub's automated tests, not yet on someone's own PC.

**Windows:** download **[shrink-your-shit-setup.exe](https://github.com/PeiceOfToast/shrink-your-shit/releases/latest/download/shrink-your-shit-setup.exe)**
and run it. It installs for your user only (no administrator prompt), asks which folder holds your
videos, downloads ffmpeg if you don't have a suitable one (about 115 MB, once), starts the program,
adds a Start Menu shortcut, starts it again whenever you log in, and opens the web page
(http://127.0.0.1:8484). Uninstall from Settings > Apps. Run the new setup file again to upgrade;
your settings are kept. For scripts: `shrink-your-shit-setup.exe /VERYSILENT /LIBRARY=D:\Videos`.

The Windows installer is **unsigned**, so Windows SmartScreen will say "Windows protected your
PC": click **More info**, then **Run anyway**. That warning is about the file having no paid
publisher signature, not about anything found in it. The same goes for Chrome or Edge saying the
file "isn't commonly downloaded".

The program **replaces your original video files** once it has checked the result is good.
Start with a copy of some videos. Other install options (Docker, Proxmox): [docs/proxmox.md](docs/proxmox.md).

## Manual / advanced

You need ffmpeg (which includes ffprobe) and Go 1.26+ to build. It builds without a C
compiler, and the same source builds for Linux, Windows and macOS (Intel and ARM).

1. Install ffmpeg:
   - **Linux / WSL2:** `sudo apt install ffmpeg`
   - **macOS:** `brew install ffmpeg`
   - **Windows:** `winget install Gyan.FFmpeg`, or put `ffmpeg.exe` and `ffprobe.exe` in the
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
  Hidden files and folders (names starting with `.`, such as macOS `._name.mp4` or
  `.Trash`) and the Synology `@eaDir` and Windows `$RECYCLE.BIN` folders are ignored.
- Files being copied in are skipped until they have not changed for `-settle`
  (15 seconds by default); on a slow network share raise it.
- File permission bits are copied on Linux/macOS. Windows has no equivalent, so the
  new file just gets the folder's normal permissions.

**Proxmox / Docker / a Linux server:** see [docs/proxmox.md](docs/proxmox.md). The web page
only listens on this computer by default (`127.0.0.1`). `-addr 0.0.0.0:8484` (or `SYP_ADDR`)
lets other machines in. There is no login yet, so only do that on a network you trust.

## Is it safe for my files?

Yes by design. The new file is written next to the original under a temporary
name (`name.syp-tmp.mkv`). Only when ffmpeg finishes successfully is it moved into
place. If anything fails, the original is untouched and the job shows as *failed*
with a Retry button.

`-output` decides what happens on success:
- `replace` (default): the new HEVC file takes the original's name.
- `beside`: keep the original and write `name.hevc.mkv` next to it.

The first video stream becomes HEVC; audio, subtitles, attachments (fonts) and extra
streams are copied unchanged. The new file keeps the original's container (`.mkv` stays
`.mkv`). Data streams (e.g. timecode) are dropped. Before replacing anything the program
checks that the result is readable, about the same length, and **smaller** than the original;
otherwise the original is kept and the job fails.

## Settings

Run `./shrink-your-shit -h` for all of them. The main ones:

| Flag | Default | Meaning |
|---|---|---|
| `-library` | (required) | folder to watch (repeatable; or env `SYP_LIBRARIES`) |
| `-data` | user config folder | where the database lives |
| `-from-codec` | `h264` | only convert this video codec |
| `-min-bitrate` | `4000` | only convert files at or above this video bitrate (kbps) |
| `-crf` | `26` | quality: lower is better and bigger |
| `-preset` | `medium` | x265 speed: `ultrafast` … `veryslow` |
| `-output` | `replace` | `replace` or `beside` |
| `-scan-every` | `30s` | how often to look for new files |
| `-settle` | `15s` | skip files changed this recently (still copying) |
| `-addr` | `127.0.0.1:8484` | web UI address (env `SYP_ADDR`) |

## For developers

```
go build ./... && go test ./...
```

- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md): build, test, run, CI, how changes are made
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): packages, job lifecycle, settings, where to add things
- [docs/STATUS.md](docs/STATUS.md): what's done, what has never been verified, known limits, next steps
- [docs/proxmox.md](docs/proxmox.md): Docker and Proxmox LXC
