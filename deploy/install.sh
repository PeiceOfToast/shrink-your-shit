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

MAX_BYTES=536870912 # 512 MiB: no download may be larger
FFMPEG_DIR=/opt/syp/ffmpeg
DATA_DIR=/var/lib/syp
BIN=/usr/local/bin/syp
UNIT=/etc/systemd/system/syp.service
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
  say "This is an update: the service stops, $DATA_DIR is copied to $DATA_DIR.bak-<time>, then the program and unit are replaced. Data is kept."
else
  say "This is a first install: a user 'syp', $DATA_DIR, $BIN, $UNIT, and ffmpeg 8 in $FFMPEG_DIR."
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

# GPU helpers: only packages the enabled sources already offer. A missing one is a note, not a failure.
gpu_pkgs=""
for p in intel-media-va-driver intel-media-va-driver-non-free mesa-va-drivers vainfo; do
  if apt-cache policy "$p" 2>/dev/null | grep -q 'Candidate: [^ (]'; then
    gpu_pkgs="$gpu_pkgs $p"
  fi
done
if [ -n "$gpu_pkgs" ]; then
  # shellcheck disable=SC2086
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $gpu_pkgs || say "note: GPU helper packages did not install; syp will use the processor."
else
  say "note: no GPU helper packages are offered by this system's enabled sources; syp will use the processor."
fi
say "note: NVIDIA drivers are not installed by this script. See your distribution's driver documentation."

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
  if [ -d "$DATA_DIR" ]; then
    stamp="$(date -u +%Y%m%dT%H%M%SZ)"
    say "Backing up $DATA_DIR to $DATA_DIR.bak-$stamp"
    cp -a "$DATA_DIR" "$DATA_DIR.bak-$stamp"
  fi
fi

install -d -m 0750 -o syp -g syp "$DATA_DIR"
chown -R syp:syp "$DATA_DIR"

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
ProtectHome=read-only

[Install]
WantedBy=multi-user.target
EOF
mv -f "$UNIT.new" "$UNIT"
systemctl daemon-reload
systemctl enable syp >/dev/null
systemctl restart syp

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
