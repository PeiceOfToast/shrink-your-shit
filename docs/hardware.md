# Graphics cards we have tested

Test results for hardware encoding on real machines. Each was run inside a fresh Proxmox container with the GPU
passed through, encoding a 1080p H.264 test clip to 8-bit HEVC with ffmpeg; the output had to read back as HEVC
written by that card's encoder, decode with no errors and give back its pictures. A blank cell means that
combination hasn't been tested yet; nothing here says a blank one fails.

| GPU | Method | Ubuntu 24.04 | Debian 13 | H.264 | HEVC | HEVC 10-bit | AV1 |
|---|---|---|---|---|---|---|---|
| AMD Radeon Vega (Ryzen 9 5900HX iGPU, "Renoir/Cezanne") | VAAPI (Mesa radeonsi) | | works | | yes | | |
| Intel UHD 630 (Core i9-9900, gen9) | QSV | works | not available | | yes | | |
| Intel UHD 630 (Core i9-9900, gen9) | VAAPI | | works | | yes | | |
| NVIDIA RTX 2070 SUPER (Turing) | NVENC | | works | | yes | | |

None of these cards has a hardware AV1 encoder — Renoir/Cezanne and Turing both predate AV1, and so does gen9 —
so an AV1 job runs on the processor. A card that can't encode a format is skipped the same way.

## What each needs inside the machine

- **AMD (VAAPI):** `ffmpeg`, `vainfo`, `mesa-va-drivers`. Tested on Debian 13 with Mesa 25.0. Check with
  `vainfo --display drm --device /dev/dri/renderD128`.
- **Intel QSV:** Ubuntu 24.04 with `intel-media-va-driver-non-free` and `libmfx1` (universe/multiverse). Older
  Intel chips like gen9 have no QSV runtime on Debian 13; use VAAPI there. On Debian 13, VAAPI on those chips
  needs `intel-media-va-driver-non-free` from the non-free section: the installer prints the two steps but never
  edits the apt sources itself.
- **NVIDIA (NVENC):** the NVIDIA driver must already be installed on the machine (in a container, the same
  version as the host, without its kernel module). The app never installs it.

## Containers on Proxmox

Pass the GPU's render node (`/dev/dri/renderD128`) into the container, plus the `/dev/nvidia*` nodes for NVIDIA.
An unprivileged container works when the device is passed with `mode=0666`.

*Last tested 2026-11-28.*
