#!/bin/sh
# shrink-your-shit installer for Debian 12, Debian 13 and Ubuntu 24.04 (amd64 and arm64), with systemd.
#
#   wget -qO- https://github.com/PeiceOfToast/shrink-your-shit/releases/download/rebuild/install.sh | sh
#   (as a user without root: wget -qO- <the same URL> | sudo sh)
#
# SYP_BASE_URL (optional) overrides the default address; it must hold checksums.txt and syp-linux-<arch>. Each download is checked against
# checksums.txt before it is put in place. ffmpeg 8 is the pinned BtbN static build below; the distro ffmpeg is
# never installed. This script never edits apt sources and never installs an NVIDIA driver. A re-run updates
# the program and keeps /var/lib/syp, after a backup copy.
set -eu

# ffmpeg 8: BtbN's static build n8.0.1 (the same build as the CI pin in .github/workflows/ci.yml). The amd64 hash
# is the CI pin. The arm64 hash was taken from the first download of this asset on 2026-11-27; the release does
# not publish a second source for it. BtbN prunes old autobuild releases, so these URLs may go away around
# 2027-11-30; if one does, pin a newer build here and in CI together.
FFMPEG_AMD64_URL="https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2025-11-30-12-53/ffmpeg-n8.0.1-17-g27a297f186-linux64-gpl-8.0.tar.xz"
FFMPEG_AMD64_SHA256="cab6b5dd9cede8a8db8891e28f2fec2ddfd6401785328d139cbcfd23af9f691e"
FFMPEG_ARM64_URL="https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2025-11-30-12-53/ffmpeg-n8.0.1-17-g27a297f186-linuxarm64-gpl-8.0.tar.xz"
FFMPEG_ARM64_SHA256="1ccfba1f73a498694f32fa834c21bd74ba29cdb108265b886c6513020745e2e6"

# libva 2.22, shipped beside ffmpeg (S3-1c). The pinned static ffmpeg 8 needs libva 2.22 at run time; Ubuntu 24.04
# ships 2.20, which aborts every VAAPI and QSV encoder. These are Debian trixie's libva2 and libva-drm2 2.22.0-3
# packages, from snapshot.debian.org by content hash (the live pool can move; the bytes cannot). The .deb is checked;
# its libraries are extracted, not installed.
LIBVA2_AMD64_URL="https://snapshot.debian.org/file/c5b976a61cf1857d1fde4c8efbf67e9fd7e2aed4"
LIBVA2_AMD64_SHA256="b76bdd330de47a826698aaed10f53435b703e9a7d4415dd68269c97709f46a9b"
LIBVADRM_AMD64_URL="https://snapshot.debian.org/file/2a6e89bd7812649824f83a53385a3dbeb0ff54f1"
LIBVADRM_AMD64_SHA256="5dce5007ddc0ce87a61db4d71476ce1a5a135737b5185ce2e8b7067342fcafc6"
LIBVA2_ARM64_URL="https://snapshot.debian.org/file/8632b4e2968a41f0891e9b8f4e45917f19c9aff1"
LIBVA2_ARM64_SHA256="7a5e7cbaaddf1f39973c10f9daae6769b704e0d6d72fe752d64003929272cbcf"
LIBVADRM_ARM64_URL="https://snapshot.debian.org/file/232c0c99171658ed064bb3fa8704ff5b1a4c7f18"
LIBVADRM_ARM64_SHA256="18e556968a0f4bffd321d67b01fa5e445177c0849bef96a741aa7ed3a11dcc8b"

MAX_BYTES=536870912 # 512 MiB: no download may be larger
FFMPEG_DIR=/opt/syp/ffmpeg
DATA_DIR=/var/lib/syp
BIN=/usr/local/bin/syp
UNIT=/etc/systemd/system/syp.service
# The root mount helper (S3-7): socket-activated, so it runs only while a share is being mounted.
MOUNT_SOCKET=/run/syp/mount.sock
MOUNT_SERVICE=/etc/systemd/system/syp-mount.service
MOUNT_SOCKET_UNIT=/etc/systemd/system/syp-mount.socket
HELPER_DIR=/var/lib/syp-helper
# Outside $DATA_DIR on purpose: the app's user owns that folder and could swap anything inside it.
SHARES_DIR=/var/lib/syp-shares
PORT=8484

say() { printf '%s\n' "$*"; }
die() { printf 'install: %s\n' "$*" >&2; exit 1; }

# need_root: the installer writes to system folders.
if [ "$(id -u)" -ne 0 ]; then
  die "run this as root, for example: curl -fsSL \$SYP_BASE_URL/install.sh | sudo SYP_BASE_URL=\$SYP_BASE_URL sh"
fi

# The rolling pre-release the rebuild branch publishes to. SYP_BASE_URL overrides it (a test folder, a mirror).
BASE="${SYP_BASE_URL:-https://github.com/PeiceOfToast/shrink-your-shit/releases/download/rebuild}"
BASE="${BASE%/}"

# Which system is this?
[ -r /etc/os-release ] || die "this system has no /etc/os-release; supported: Debian 12, Debian 13, Ubuntu 24.04"
# shellcheck disable=SC1091
. /etc/os-release
case "${ID:-}:${VERSION_ID:-}" in
  debian:12|debian:13|ubuntu:24.04) ;;
  *) die "unsupported system: ${PRETTY_NAME:-${ID:-unknown} ${VERSION_ID:-}}. Supported: Debian 12, Debian 13, Ubuntu 24.04." ;;
esac

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  *) die "unsupported processor: $(uname -m). Supported: x86_64 and aarch64." ;;
esac

[ -d /run/systemd/system ] || die "systemd is not running here. This installer needs systemd."

if [ "$ARCH" = amd64 ]; then
  FF_URL="$FFMPEG_AMD64_URL"; FF_SHA="$FFMPEG_AMD64_SHA256"
else
  FF_URL="$FFMPEG_ARM64_URL"; FF_SHA="$FFMPEG_ARM64_SHA256"
fi

UPDATE=no
if [ -x "$BIN" ] || [ -f "$UNIT" ]; then
  UPDATE=yes
fi

say "shrink-your-shit installer: $PRETTY_NAME, $ARCH"
if [ "$UPDATE" = yes ]; then
  say "This is an update: the services stop, $DATA_DIR is copied to $DATA_DIR.bak-<time>, then the program and units are replaced. Data is kept."
else
  say "This is a first install: a user 'syp', $DATA_DIR, $BIN, the units, the root mount helper's folders, and ffmpeg 8 in $FFMPEG_DIR."
fi
say "Downloads come from $BASE. Each one is checked against checksums.txt before it is used."

# A tool the script needs may be missing on a minimal install. Install only those, never change sources.
need_pkgs=""
for cmd in curl xz tar sha256sum; do
  command -v "$cmd" >/dev/null 2>&1 || case "$cmd" in
    curl) need_pkgs="$need_pkgs curl" ;;
    xz) need_pkgs="$need_pkgs xz-utils" ;;
    *) ;;
  esac
done
if [ -n "$need_pkgs" ]; then
  say "Installing:$need_pkgs"
  apt-get update -qq
  # shellcheck disable=SC2086
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $need_pkgs ca-certificates
fi

WORK="$(mktemp -d /tmp/syp-install.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT

# fetch URL OUT: download with a size cap, to a temp file that is only renamed after the checks pass.
fetch() {
  curl -fsSL --max-filesize "$MAX_BYTES" -o "$2" "$1" || die "download failed: $1"
}

# verify FILE SHA256 NAME: stops the install with the checksum message when the hash differs.
verify() {
  got="$(sha256sum "$1" | cut -d' ' -f1)"
  if [ "$got" != "$2" ]; then
    rm -f "$1"
    die "checksum does not match for $3; nothing was installed. Expected $2, got $got."
  fi
}

say "Fetching checksums.txt"
fetch "$BASE/checksums.txt" "$WORK/checksums.txt"
expect="$(awk -v n="syp-linux-$ARCH" '$2 == n { print $1 }' "$WORK/checksums.txt")"
[ -n "$expect" ] || die "checksums.txt has no entry for syp-linux-$ARCH"

say "Fetching syp-linux-$ARCH"
fetch "$BASE/syp-linux-$ARCH" "$WORK/syp"
verify "$WORK/syp" "$expect" "syp-linux-$ARCH"

# ffmpeg: left alone when the installed copy was made from the pinned archive.
MARK="$FFMPEG_DIR/.archive-sha256"
if [ -x "$FFMPEG_DIR/ffmpeg" ] && [ -x "$FFMPEG_DIR/ffprobe" ] && [ -f "$MARK" ] && [ "$(cat "$MARK")" = "$FF_SHA" ]; then
  say "ffmpeg 8 is already at the pinned build; leaving it alone."
else
  say "Fetching ffmpeg 8 (pinned build)"
  fetch "$FF_URL" "$WORK/ffmpeg.tar.xz"
  verify "$WORK/ffmpeg.tar.xz" "$FF_SHA" "the ffmpeg archive"
  mkdir "$WORK/ff"
  tar -xJf "$WORK/ffmpeg.tar.xz" -C "$WORK/ff"
  ffdir="$(find "$WORK/ff" -maxdepth 2 -type d -name bin | head -n 1)"
  if [ -z "$ffdir" ] || [ ! -x "$ffdir/ffmpeg" ] || [ ! -x "$ffdir/ffprobe" ]; then
    die "the ffmpeg archive has no ffmpeg and ffprobe"
  fi
  install -d -m 0755 "$FFMPEG_DIR"
  install -m 0755 "$ffdir/ffmpeg" "$FFMPEG_DIR/ffmpeg.new"
  install -m 0755 "$ffdir/ffprobe" "$FFMPEG_DIR/ffprobe.new"
  mv -f "$FFMPEG_DIR/ffmpeg.new" "$FFMPEG_DIR/ffmpeg"
  mv -f "$FFMPEG_DIR/ffprobe.new" "$FFMPEG_DIR/ffprobe"
  printf '%s\n' "$FF_SHA" > "$MARK"
fi

# libva 2.22 beside ffmpeg. Media starts ffmpeg and ffprobe with this folder on LD_LIBRARY_PATH, never the service.
# The copies keep their symlinks, and the copyright notice goes with them. A re-run leaves them alone when the pins match.
if [ "$ARCH" = amd64 ]; then
  L2_URL="$LIBVA2_AMD64_URL"; L2_SHA="$LIBVA2_AMD64_SHA256"
  LD_URL="$LIBVADRM_AMD64_URL"; LD_SHA="$LIBVADRM_AMD64_SHA256"
else
  L2_URL="$LIBVA2_ARM64_URL"; L2_SHA="$LIBVA2_ARM64_SHA256"
  LD_URL="$LIBVADRM_ARM64_URL"; LD_SHA="$LIBVADRM_ARM64_SHA256"
fi
LIBDIR="$FFMPEG_DIR/lib"
LIBMARK="$LIBDIR/.libva-sha256"
LIBSET="$L2_SHA $LD_SHA"
if [ -f "$LIBMARK" ] && [ "$(cat "$LIBMARK")" = "$LIBSET" ] && [ -e "$LIBDIR/libva.so.2" ] && [ -e "$LIBDIR/libva-drm.so.2" ]; then
  say "libva 2.22 is already at the pinned build; leaving it alone."
else
  say "Fetching libva 2.22 (pinned Debian packages)"
  fetch "$L2_URL" "$WORK/libva2.deb"
  verify "$WORK/libva2.deb" "$L2_SHA" "libva2 2.22"
  fetch "$LD_URL" "$WORK/libva-drm2.deb"
  verify "$WORK/libva-drm2.deb" "$LD_SHA" "libva-drm2 2.22"
  mkdir "$WORK/libva"
  dpkg-deb -x "$WORK/libva2.deb" "$WORK/libva"
  dpkg-deb -x "$WORK/libva-drm2.deb" "$WORK/libva"
  install -d -m 0755 "$LIBDIR"
  find "$WORK/libva/usr/lib" -name 'libva*.so*' -exec cp -P {} "$LIBDIR/" \;
  if [ ! -e "$LIBDIR/libva.so.2" ] || [ ! -e "$LIBDIR/libva-drm.so.2" ]; then
    die "the libva packages have no libva.so.2 and libva-drm.so.2"
  fi
  if [ -f "$WORK/libva/usr/share/doc/libva2/copyright" ]; then
    cp "$WORK/libva/usr/share/doc/libva2/copyright" "$LIBDIR/LICENSE-libva"
  fi
  printf '%s\n' "$LIBSET" > "$LIBMARK"
fi

# What cards this machine has, from the device nodes it was given: inside a container /sys shows the host's PCI
# devices, so /sys is only read to name the driver behind a render node. NVIDIA's driver makes /dev/nvidia*, and
# /dev/dri/renderD* is Intel or AMD by its DRM driver (i915/xe, amdgpu/radeon) or, when that is unreadable, its
# vendor id. The probe decides later what actually works; these notes only say what is here and what a person can
# do about it.
gpu_vendors=""
add_gpu() { case " $gpu_vendors " in *" $1 "*) ;; *) gpu_vendors="$gpu_vendors $1" ;; esac; }
for n in /dev/nvidia0 /dev/nvidiactl; do
  [ -e "$n" ] && add_gpu nvidia
done
for r in /dev/dri/renderD*; do
  [ -e "$r" ] || continue
  node="$(basename "$r")"
  drv="$(sed -n 's/^DRIVER=//p' "/sys/class/drm/$node/device/uevent" 2>/dev/null)"
  vendor="$(cat "/sys/class/drm/$node/device/vendor" 2>/dev/null)"
  case "$drv:$vendor" in
    i915:*|xe:*|*:0x8086) add_gpu intel ;;
    amdgpu:*|radeon:*|*:0x1002) add_gpu amd ;;
  esac
done
# An NVIDIA card is only usable once its driver is installed, and this script never installs one. The PCI bus is
# how a card with no driver is still found, so the person is told there is one to give a driver to.
pci_nvidia=no
for d in /sys/bus/pci/devices/*; do
  case "$(cat "$d/class" 2>/dev/null)" in 0x030*) ;; *) continue ;; esac
  [ "$(cat "$d/vendor" 2>/dev/null)" = 0x10de ] && pci_nvidia=yes
done
# In a container the PCI bus shows the host's cards, which this machine cannot be given by a driver; that note is
# only for a machine of its own.
in_container=no
if [ -e /.dockerenv ] || [ -e /run/systemd/container ]; then in_container=yes; fi
has_gpu() { case " $gpu_vendors " in *" $1 "*) return 0 ;; esac; return 1; }

# Intel's generation from its Core name: i7-1165G7 is 11th, i7-6700K is 6th. A name the numbering does not read
# (Xeon, Celeron) answers nothing, and then nothing is claimed about Quick Sync.
intel_generation() {
  name="$(sed -n 's/^model name[[:space:]]*: //p' /proc/cpuinfo 2>/dev/null | head -n 1)"
  case "$name" in
    *"i3-"*|*"i5-"*|*"i7-"*|*"i9-"*) ;;
    *) return 1 ;;
  esac
  num="$(printf '%s\n' "$name" | sed -n 's/.*i[3579]-\([0-9][0-9]*\).*/\1/p')"
  [ -n "$num" ] || return 1
  case "$(printf '%s' "$num" | cut -c1-2)" in
    1[0-9]) printf '%s\n' "$(printf '%s' "$num" | cut -c1-2)" ;;
    *) printf '%s\n' "$(printf '%s' "$num" | cut -c1)" ;;
  esac
}

# GPU helpers: only packages the enabled sources already offer, and only for a card this machine has. An AMD-only
# machine is never asked for the Intel driver (L-HW-22). Each is installed in its own call, so one that cannot
# install (a conflict, a missing source) is a note and never takes the others with it. The free and the non-free
# Intel iHD drivers conflict, so the free one is left out when the non-free one is offered. QSV on Ubuntu needs
# libmfx1 with the non-free driver (L-HW-21).
gpu_pkgs=""
offer() {
  if apt-cache policy "$1" 2>/dev/null | grep -q 'Candidate: [^ (]'; then
    gpu_pkgs="$gpu_pkgs $1"
  fi
}
if has_gpu intel; then
  offer intel-media-va-driver
  offer intel-media-va-driver-non-free
  offer libmfx1
fi
if has_gpu intel || has_gpu amd; then
  offer mesa-va-drivers
  offer vainfo
fi
case "$gpu_pkgs" in
  *intel-media-va-driver-non-free*)
    kept=""
    # shellcheck disable=SC2086
    for p in $gpu_pkgs; do
      [ "$p" = intel-media-va-driver ] || kept="$kept $p"
    done
    gpu_pkgs="$kept" ;;
esac
if [ -n "$gpu_pkgs" ]; then
  for p in $gpu_pkgs; do
    if DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$p" >/dev/null 2>&1; then
      continue
    fi
    say "note: $p did not install; syp will use the processor for that card."
    # The free Intel driver was left out for the non-free one. When that fails, the free one is the fallback, so a
    # failure never leaves the Intel chip with no VA-API driver at all.
    if [ "$p" = intel-media-va-driver-non-free ] && apt-cache policy intel-media-va-driver 2>/dev/null | grep -q 'Candidate: [^ (]'; then
      DEBIAN_FRONTEND=noninteractive apt-get install -y -qq intel-media-va-driver >/dev/null 2>&1 || say "note: intel-media-va-driver did not install either; syp will use the processor for Intel."
    fi
  done
elif [ -z "$gpu_vendors" ]; then
  say "note: no Intel, AMD or NVIDIA display device was found; syp will use the processor."
else
  say "note: no GPU helper packages are offered by this system's enabled sources; syp will use the processor for the card."
fi

# The notes a person can act on, per vendor. Nothing here installs or enables anything beyond the helpers above:
# the script never edits apt sources and never installs an NVIDIA driver (L-INST-8).
if has_gpu intel && [ "${ID:-}" = debian ] && [ "${VERSION_ID:-}" = 13 ] && ! dpkg -s intel-media-va-driver-non-free >/dev/null 2>&1; then
  say "note: GPU encoding on Intel needs the non-free Intel media driver (intel-media-va-driver-non-free), and"
  say "      Debian 13 offers it only in its non-free section. To enable it:"
  say "  1. add non-free to your apt sources (Debian 13: the Components: line of /etc/apt/sources.list.d/debian.sources;"
  say "     older layout: the deb line in /etc/apt/sources.list)"
  say "  2. run: sudo apt-get update && sudo apt-get install intel-media-va-driver-non-free"
  say "      Until then syp converts with the processor."
fi
if has_gpu intel && [ "${ID:-}" = debian ] && [ "${VERSION_ID:-}" = 13 ]; then
  if gen="$(intel_generation)" && [ "$gen" -lt 11 ]; then
    say "note: this Intel chip is older than Tiger Lake, and Debian 13 has no Quick Sync runtime for it; syp uses the"
    say "      card through VA-API instead (L-HW-21)."
  fi
fi
if has_gpu nvidia; then
  nv=""
  if [ -r /proc/driver/nvidia/version ]; then
    nv="$(sed -n 's/.*[[:space:]]\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9.]*\)[[:space:]].*/\1/p' /proc/driver/nvidia/version | head -n 1)"
  fi
  if [ -z "$nv" ] && command -v nvidia-smi >/dev/null 2>&1; then
    nv="$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -n 1)"
  fi
  if [ -n "$nv" ]; then
    say "note: NVIDIA card found; driver $nv is loaded, and syp will use the card's encoder through it."
    say "      This script never installs a driver."
  else
    say "note: NVIDIA card found and its driver is loaded; syp will use the card's encoder through it. This script"
    say "      never installs a driver."
  fi
elif [ "$pci_nvidia" = yes ] && [ "$in_container" = no ]; then
  say "note: an NVIDIA card is present but no driver is loaded; syp will use the processor for it. This script never"
  say "      installs a driver; install one from your distribution's driver packages if you want the card used."
fi
if has_gpu amd; then
  say "note: AMD card found; its encoder comes through mesa (mesa-va-drivers), and the Intel media driver is not"
  say "      needed on this machine (L-HW-22)."
fi

# The system user and the data folder.
if ! id -u syp >/dev/null 2>&1; then
  useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin syp
fi
for g in render video; do
  if getent group "$g" >/dev/null 2>&1; then
    usermod -aG "$g" syp
  fi
done

if [ "$UPDATE" = yes ]; then
  if systemctl is-active --quiet syp 2>/dev/null; then
    say "Stopping syp"
    systemctl stop syp
  fi
  # The helper is socket-activated, so it is often not running; stop it when an old copy is.
  if systemctl is-active --quiet syp-mount.service 2>/dev/null; then
    say "Stopping the mount helper"
    systemctl stop syp-mount.service
  fi
  if [ -d "$DATA_DIR" ]; then
    stamp="$(date -u +%Y%m%dT%H%M%SZ)"
    say "Backing up $DATA_DIR to $DATA_DIR.bak-$stamp"
    # The shares' mount points are not under here, so a copy never descends into a network folder.
    cp -a "$DATA_DIR" "$DATA_DIR.bak-$stamp"
  fi
fi

install -d -m 0750 -o syp -g syp "$DATA_DIR"
chown -R syp:syp "$DATA_DIR"
# The mount helper's folders. The shares' points are root's to write and the syp user's to read: kept
# outside $DATA_DIR, whose owner can swap anything inside it, so the helper can trust the folder.
install -d -m 0750 -o root -g syp "$SHARES_DIR"
# The helper's own record folder, root's alone: a stale record can never unmount a hand mount.
install -d -m 0700 -o root -g root "$HELPER_DIR"

install -m 0755 -o root -g root "$WORK/syp" "$BIN.new"
mv -f "$BIN.new" "$BIN"

cat > "$UNIT.new" <<EOF
[Unit]
Description=shrink-your-shit
After=network-online.target
Wants=network-online.target

[Service]
User=syp
Group=syp
Environment=SYP_FFMPEG_DIR=$FFMPEG_DIR
ExecStart=$BIN -data $DATA_DIR -listen 0.0.0.0:$PORT
Restart=on-failure
NoNewPrivileges=yes
ProtectSystem=full

[Install]
WantedBy=multi-user.target
EOF
mv -f "$UNIT.new" "$UNIT"

# The root mount helper. systemd owns the socket (root:syp 0660), so the app's user can ask and nobody
# else can; the service is socket-activated and runs as root because mounting is a privileged call. The
# helper's own checks are the second gate (SO_PEERCRED: root or syp). No PrivateNetwork: cifs and nfs
# are kernel mounts whose network belongs to the machine's namespace.
cat > "$MOUNT_SOCKET_UNIT.new" <<EOF
[Unit]
Description=shrink-your-shit mount helper socket

[Socket]
ListenStream=$MOUNT_SOCKET
SocketMode=0660
SocketUser=root
SocketGroup=syp
RuntimeDirectory=syp
# The app's user must be able to traverse /run/syp to reach the socket; the socket itself is 0660 root:syp.
RuntimeDirectoryMode=0755
RemoveOnStop=yes

[Install]
WantedBy=sockets.target
EOF
mv -f "$MOUNT_SOCKET_UNIT.new" "$MOUNT_SOCKET_UNIT"

cat > "$MOUNT_SERVICE.new" <<EOF
[Unit]
Description=shrink-your-shit mount helper
Requires=syp-mount.socket
After=syp-mount.socket

[Service]
# No User=: root is the default, and mounting is a privileged call.
ExecStart=$BIN helper
# The mount programs resolve a share's host name over ordinary internet sockets (measured: with
# AF_UNIX alone, getent hosts fails inside this unit); raw and packet sockets stay out.
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
NoNewPrivileges=yes
# No ProtectSystem=/ProtectHome=/ReadWritePaths= here. Each of them makes systemd give this unit a
# private mount namespace, and a mount made there lives only in that namespace: measured on Debian 12,
# the helper answered ok while the app saw an empty folder (the service's / is shared,slave). A mount
# helper must act in the host's mount namespace, so path protection is not available to it.
EOF
mv -f "$MOUNT_SERVICE.new" "$MOUNT_SERVICE"

systemctl daemon-reload
systemctl enable syp >/dev/null
systemctl restart syp
# Restarting the socket also restarts a running helper (the service Requires it), so an update never
# leaves an old helper answering on a replaced socket.
systemctl enable syp-mount.socket >/dev/null
systemctl restart syp-mount.socket

say "Waiting for syp to answer"
ok=no
i=0
while [ "$i" -lt 30 ]; do
  if curl -fsS -o /dev/null "http://127.0.0.1:$PORT/api/v2/health" 2>/dev/null; then
    ok=yes
    break
  fi
  i=$((i + 1))
  sleep 1
done
if [ "$ok" != yes ]; then
  say "syp did not answer within 30 seconds. The last 30 log lines:"
  journalctl -u syp -n 30 --no-pager || true
  exit 1
fi

ip="$(hostname -I 2>/dev/null | awk '{ print $1 }')"
[ -n "$ip" ] || ip="<this machine's address>"
say "syp is running. Open http://$ip:$PORT to finish setting up."
say "syp runs as user syp: give it access to your video folders (e.g. add syp to your group, or use a folder outside your home)"
