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
network in. Other machines then sign in with an access token; the installer ends by printing an
address that already carries it (see [Signing in](#signing-in)):

```
curl -fsSL https://raw.githubusercontent.com/PeiceOfToast/shrink-your-shit/main/install.sh | sh -s -- --library /path/to/videos --addr 0.0.0.0:8484
```

On macOS the service is a login item (a launchd agent), so the installer must be run in a
normal desktop session (Terminal.app), not over plain SSH; the service then starts at login.
The program is unsigned: macOS or Windows may warn the first time it runs.

> **Not working yet:** the installer downloads a published release, and none has been
> published. Until the first release exists the Linux/macOS installer stops with "could not
> download" and the Windows download link gives a "not found" page; use the manual steps below
> instead. The macOS installer has never been run on a Mac. The Windows installer passed an automated
> silent install / convert a video / upgrade / uninstall test on a GitHub Windows runner, but it has
> **not** been tried on a real PC.

**Windows:** download **[shrink-your-shit-setup.exe](https://github.com/PeiceOfToast/shrink-your-shit/releases/latest/download/shrink-your-shit-setup.exe)**
and run it. It installs for your user only (no administrator prompt), asks which folder holds your
videos, downloads ffmpeg if you don't have a suitable one (about 115 MB, once), starts the program,
adds a Start Menu shortcut, starts it again whenever you log in, and opens the web page
(http://127.0.0.1:8484). Uninstall from Settings > Apps (your job database in
`%LOCALAPPDATA%\shrink-your-shit\data` is left behind; delete that folder if you want it gone). Run the new setup file again to upgrade;
your settings are kept. For scripts: `shrink-your-shit-setup.exe /VERYSILENT /LIBRARY=D:\Videos`.

The Windows installer is **unsigned**, so Windows SmartScreen will say "Windows protected your
PC": click **More info**, then **Run anyway**. That warning is about the file having no paid
publisher signature, not about anything found in it. The same goes for Chrome or Edge saying the
file "isn't commonly downloaded". On Windows 11, if **Smart App Control** is turned ON, it can block
an unsigned program outright, with no "Run anyway" option; you would have to turn Smart App Control
off (Windows Security > App & browser control) to install.

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
3. Open http://127.0.0.1:8484. The first run shows your folder, lets you choose how much to
   shrink, and starts it when you press Start; nothing is converted before that. Stop with Ctrl+C
   (works on every OS); a job in progress is put back in the queue and starts over next time.

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
- **HDR and Dolby Vision videos are left as they are** (skipped as "HDR or Dolby Vision"), whatever the
  setting, unless a Standard setting turns "Skip HDR" off. Converting them would drop their HDR
  information and can make the picture look washed out. Before this version an existing library could
  convert them; it no longer does.
- Files being copied in are skipped until they have not changed for `-settle`
  (15 seconds by default); on a slow network share raise it.
- File permission bits are copied on Linux/macOS. Windows has no equivalent, so the
  new file just gets the folder's normal permissions.

**Proxmox / Docker / a Linux server:** see [docs/proxmox.md](docs/proxmox.md). The web page
only listens on this computer by default (`127.0.0.1`). `-addr 0.0.0.0:8484` (or `SYP_ADDR`)
lets other machines in; they sign in with the access token.

## Signing in

On this computer only (the default, `127.0.0.1`) there is nothing to sign in to. As soon as the
program listens beyond this computer (`-addr 0.0.0.0:8484`, the container, a Proxmox LXC), it makes a
random access token, keeps it in its data folder (file `access-token`, readable only by the user it
runs as), and asks for it on every page that shows or changes anything. The installer ends by
printing a sign-in address with the token in it; opening it signs that browser in. To see the token
and address again:

| Where it runs | Command |
|---|---|
| Linux service | `sudo shrink-your-shit token` |
| macOS | `shrink-your-shit token` |
| Docker | `docker exec <container name> shrink-your-shit token` |
| Windows | `shrink-your-shit token` |

Settings can make a new token, which signs out every other browser. Scripts send the token as
`Authorization: Bearer <token>`. There is no HTTPS, so the token crosses the network in plain text:
use it on a network you trust, never on the internet.

## Checking the setup

`shrink-your-shit doctor` checks the converter (ffmpeg, ffprobe, the HEVC encoder, the picture check),
the data folder and every library folder, and says in plain words what to fix. It asks the running
program, so the folder checks are made as the user the service runs as:

| Where it runs | Command |
|---|---|
| Linux service | `sudo shrink-your-shit doctor` |
| macOS | `shrink-your-shit doctor` |
| Docker | `docker exec <container name> shrink-your-shit doctor` |
| Windows | `shrink-your-shit doctor` |

It exits with 1 if something needs fixing. The web page shows the same checks under Settings > System,
and checks a folder as soon as you choose it. On Linux the service runs as the user `syp`: a folder it
cannot write needs `sudo chown -R syp:syp /path/to/videos` (or `syp` in the folder's group with group
write access).

## Is it safe for my files?

Yes by design. The new file is written next to the original under a temporary
folder (like `.name.4185293.syp-tmp/`) that, on Linux and macOS, only you can open. Only when ffmpeg finishes successfully is it moved into
place. If anything fails, the original is untouched and the job shows as *failed*
with a Retry button.

`-output` decides what happens on success:
- `replace` (default): the new HEVC file takes the original's name. The original is **kept for 7
  days** in a hidden `.syp-trash` folder next to it (Settings > Safety, "Keep originals"; 0 days
  deletes it at once, as versions before this one did). This is the default for new installs and
  for upgrades alike: it changes nothing about what is converted or how, it only keeps more. Kept
  originals use disk space until they are deleted, so the space a conversion saves is only freed
  then. The move into `.syp-trash` is a rename on the same disk (no copy) and is recorded first, so
  a crash or power cut at any moment leaves either the original or the new file in place.
  To get an original back: Activity > Kept originals, "Get original back", or in a terminal
  `shrink-your-shit restore <path of the converted file>` (`--force` if you changed the converted
  file since and still want the original; it works only while the service is stopped, and says so). The file is then left alone by later scans.
- `beside`: keep the original and write `name.hevc.mkv` next to it.

The first video stream becomes HEVC; audio, subtitles, attachments (fonts) and extra
streams are copied unchanged. The new file keeps the original's container (`.mkv` stays
`.mkv`). Data streams (e.g. timecode) are dropped. Before replacing anything the program
checks that the new file has the same streams (audio, subtitles, chapters...), the same length and frame count, plays
without decode errors, looks like the original, and is **smaller**. If any check fails, or cannot run, the original
is kept and the job shows "original kept" with the reason in plain words. It also needs free disk space about the
size of the original.

## Settings

Run `./shrink-your-shit -h` for all of them. The main ones:

| Flag | Default | Meaning |
|---|---|---|
| `-library` | none | folder to watch, added as a library if no library has it yet (repeatable; or env `SYP_LIBRARIES`). Libraries can also be added on the web page; with neither, the program starts with none |
| `-data` | user config folder | where the database lives |
| `-from-codec` | `h264` | only convert this video codec |
| `-min-bitrate` | `4000` | only convert files at or above this video bitrate (kbps) |
| `-crf` | `26` | quality: lower is better and bigger |
| `-preset` | `medium` | x265 speed: `ultrafast` … `veryslow` |
| `-output` | `replace` | `replace` or `beside` |
| `-decode-check` | `auto` | play the new file through a decoder before replacing the original: `auto` (completely up to 3 hours long, else at 3 places) or `always` |
| `-min-ssim` | `0.95` | how similar the new picture must be to the original (0.90-1.0); an untuned starting value |
| `-scan-every` | `30s` | how often to look for new files |
| `-settle` | `15s` | skip files changed this recently (still copying) |
| `-addr` | `127.0.0.1:8484` | web UI address (env `SYP_ADDR`) |

On a new install, libraries from `-library` wait for the first run on the web page, where you
choose their setting and press Start. `-from-codec`, `-min-bitrate`, `-crf`, `-preset` and
`-output` only matter for an install upgraded from a version before libraries: they are the
settings its libraries keep converting with. Each library keeps its own settings (its flow) after
that: changing these flags later does not change libraries that already exist.

## For developers

```
go build ./... && go test ./...
```

- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md): build, test, run, CI, how changes are made
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): packages, job lifecycle, settings, where to add things
- [docs/STATUS.md](docs/STATUS.md): what's done, what has never been verified, known limits, next steps
- [docs/proxmox.md](docs/proxmox.md): Docker and Proxmox LXC
