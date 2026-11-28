# Graphics cards we have tested

Test results for hardware encoding on real machines. Each was run inside a fresh Proxmox container with the GPU
passed through, encoding a 5-second 1080p test clip with ffmpeg. A blank cell means that combination hasn't been
tested yet.

| GPU | Method | Ubuntu 24.04 | Debian 13 | H.264 | HEVC | HEVC 10-bit | AV1 |
|---|---|---|---|---|---|---|---|
| AMD Radeon Vega (Ryzen 9 5900HX iGPU, "Renoir/Cezanne") | VAAPI (Mesa radeonsi) | works | works | yes | yes | yes | no (chip has no AV1 encoder) |
| Intel UHD 630 (Core i9-9900, gen9) | QSV | works | not available | yes | yes | | no |
| Intel UHD 630 (Core i9-9900, gen9) | VAAPI | works | works | yes | yes | | no |
| NVIDIA RTX 2070 SUPER (Turing) | NVENC | works | works | yes | yes | | no (Turing has no AV1 encoder) |

When the GPU can't encode a format (AV1 on all of the above), the app uses the processor instead.

## What each needs inside the machine

- **AMD (VAAPI):** `ffmpeg`, `vainfo`, `mesa-va-drivers`. Works the same on Ubuntu 24.04 (Mesa 25.2) and
  Debian 13 (Mesa 25.0). Check with `vainfo --display drm --device /dev/dri/renderD128`.
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
