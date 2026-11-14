#!/bin/sh
# shrink-your-shit installer for Linux and macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/PeiceOfToast/shrink-your-shit/main/install.sh | sh
#
# It downloads the program (checking it against the release's checksum file), installs ffmpeg if it is missing, sets the
# program up as a service that starts on boot (systemd on Linux, a launchd agent on macOS),
# starts it and prints the web address. Running it again upgrades to the newest release and
# keeps your settings. Options go after "sh -s --":
#
#   curl -fsSL <url>/install.sh | sh -s -- --library /srv/videos --addr 0.0.0.0:8484
#
#   --library DIR     folder to watch (repeat for several). Default: none; add folders on the web page.
#   --addr ADDR       web address (default 0.0.0.0:8484: other computers on the network reach it and sign in
#                     with the access token in the link shown at the end; 127.0.0.1:8484 is this computer only)
#   --version X.Y.Z   install that release instead of the newest
#   --no-start        install but do not start
#   --run             run in the foreground at the end (for containers / machines without a service manager)
#   --docker          run in a Docker container (compose file in /opt/shrink-your-shit) instead of as a service (Linux)
#
# Environment: SYP_BASE_URL (where releases are downloaded from; file:///some/dir works for offline
# or test use), SYP_LIBRARIES, SYP_ADDR.
set -eu

REPO=PeiceOfToast/shrink-your-shit
BASE_URL=${SYP_BASE_URL:-}
VERSION=
LIBS=${SYP_LIBRARIES:-}
FLAG_LIBS=
ADDR=${SYP_ADDR:-}
NO_START=
NETWORK_SHARES=
RUN_FG=
DOCKER=        # --docker: run the app in a container instead of as a service
UPDATED=        # set when an earlier install is found (this run upgrades it)
NEW_VERSION=
NS_LINE=        # the network-share line of the final output
NS_REASON=      # why the share helper could not be set up
TOKEN_OUT=      # the program's `token` output (its access token and sign-in address)
TOKEN_CMD=      # how to show the sign-in link again, for the final output

say() { printf '%s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# quiet_run WHAT COMMAND...: runs a package step with its output in a log file, so a successful run prints nothing.
# On failure it prints the log's last 20 lines and the log's path, then returns the command's status.
quiet_run() {
  q_what=$1; shift
  q_log=$(mktemp "${TMPDIR:-/tmp}/shrink-your-shit-install.XXXXXX") || die "could not make a log file to $q_what"
  "$@" >"$q_log" 2>&1 && { rm -f "$q_log"; return 0; }
  q_rc=$?
  printf 'ERROR: %s failed. Last 20 lines of its output (full log: %s):\n' "$q_what" "$q_log" >&2
  tail -n 20 "$q_log" >&2
  return "$q_rc"
}

usage() {
  cat <<'USAGE'
Usage: install.sh [--library DIR]... [--addr ADDR] [--version X.Y.Z] [--network-shares|--no-network-shares] [--no-start] [--run] [--docker]
  --library DIR    folder to watch (repeat for several); default: none, add folders on the web page
  --addr ADDR      web address, default 0.0.0.0:8484 (other computers on the network sign in with the access token);
                   127.0.0.1:8484 is this computer only
  --version X.Y.Z  install that release instead of the newest
  --network-shares / --no-network-shares   set up (default) or skip the SMB/NFS share helper
  --no-start       install but do not start
  --run            run in the foreground at the end (containers / no service manager)
  --docker         run in a Docker container instead of as a service (Linux; needs Docker and the Compose plugin)
Environment: SYP_BASE_URL (download location; file:///dir works), SYP_LIBRARIES, SYP_ADDR
USAGE
}

while [ $# -gt 0 ]; do
  case $1 in
    --library) [ $# -ge 2 ] || die "--library needs a folder"; FLAG_LIBS=${FLAG_LIBS:+$FLAG_LIBS:}$2; shift 2 ;;
    --addr) [ $# -ge 2 ] || die "--addr needs a value like 127.0.0.1:8484"; ADDR=$2; shift 2 ;;
    --version) [ $# -ge 2 ] || die "--version needs a number like 0.1.0"; VERSION=$2; shift 2 ;;
    --no-start) NO_START=1; shift ;;
    --run) RUN_FG=1; shift ;;
    --docker) DOCKER=1; shift ;;
    --network-shares) NETWORK_SHARES=1; shift ;;
    --no-network-shares) NETWORK_SHARES=0; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
done

[ -n "$FLAG_LIBS" ] && LIBS=$FLAG_LIBS   # flags beat the environment

# ---- what machine is this? ----
case $(uname -s) in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) die "this installer supports Linux and macOS. On Windows use the Windows installer (see the README)." ;;
esac
case $(uname -m) in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported CPU type '$(uname -m)' (supported: x86_64, arm64)" ;;
esac

# ---- where do releases come from? ----
if [ -z "$BASE_URL" ]; then
  if [ -n "$VERSION" ]; then
    BASE_URL=https://github.com/$REPO/releases/download/v${VERSION#v}
  else
    BASE_URL=https://github.com/$REPO/releases/latest/download
  fi
fi
BASE_URL=${BASE_URL%/}

fetch() { # fetch URL DEST
  case $1 in
    file://*) cp "${1#file://}" "$2" ;;
    *)
      if have curl; then curl -fsSL "$1" -o "$2"
      elif have wget; then wget -q -O "$2" "$1"
      else die "need curl or wget to download files; install one and run this again"
      fi ;;
  esac
}

sha256_of() {
  if have sha256sum; then sha256sum "$1" | cut -d' ' -f1
  elif have shasum; then shasum -a 256 "$1" | cut -d' ' -f1
  else die "need sha256sum or shasum to verify the download"
  fi
}

say "Shrink Your Shit installer"

# ---- privileges ----
SUDO=
if [ "$OS" = linux ]; then
  if [ "$(id -u)" -ne 0 ]; then
    if have sudo; then
      SUDO=sudo
    else
      die "this needs root to install system-wide. Run it as root, or install sudo first."
    fi
  fi
fi


# The address to ask "are you up?" on: the host part of the listen address; "all addresses"
# (0.0.0.0, ::, nothing) means ask on this computer.
check_host() {
  h=${1%:*}
  h=${h#[}
  h=${h%]}
  case $h in ''|0.0.0.0|::) h=127.0.0.1 ;; esac
  printf '%s' "$h"
}

health_check() {
  host=$(check_host "$PORT_ADDR")
  case $host in *:*) host="[$host]" ;; esac
  url="http://$host:${PORT_ADDR##*:}/healthz"
  i=0
  while [ $i -lt 20 ]; do
    if have curl; then curl -fsS -o /dev/null "$url" 2>/dev/null && return 0
    elif have wget; then wget -q -O /dev/null "$url" 2>/dev/null && return 0
    else return 2
    fi
    i=$((i + 1)); sleep 1
  done
  return 1
}

# token_out: the program's `token` output (its access token and sign-in address). Empty if it fails.
token_out() {
  if [ -n "${TOKEN_DATA:-}" ]; then
    $SUDO env SYP_ADDR="$PORT_ADDR" SYP_DATA="$TOKEN_DATA" "$BIN" token 2>/dev/null || true
  else
    $SUDO env SYP_ADDR="$PORT_ADDR" "$BIN" token 2>/dev/null || true
  fi
}

# lan_ip: this computer's address on the local network. The default route's address comes first, then the other
# interfaces', in interface order. Loopback and Docker or virtual bridge interfaces (docker*, br-*, veth*, virbr*) are
# skipped by name. Without the ip command, hostname -I is used, and its 172.16.0.0/12 addresses (where Docker puts its
# bridges) are skipped too: a link with a bridge address can't open from another computer.
# Empty when nothing usable is found, and the caller prints the program's own placeholder.
lan_ip() {
  cand=
  if have ip; then
    cand=$( { ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p'
              ip -4 -o addr show 2>/dev/null | awk '$2 !~ /^(lo|docker.*|br-.*|veth.*|virbr.*)$/ { sub(/\/.*/, "", $4); print $4 }'
            } ) || true
  else
    if have route && have ipconfig; then
      iface=$(route -n get default 2>/dev/null | sed -n 's/.*interface: //p' | head -n 1) || true
      if [ -n "$iface" ]; then cand=$(ipconfig getifaddr "$iface" 2>/dev/null) || true; fi
    fi
    if [ -z "$cand" ] && have hostname; then
      cand=$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -vE '^172\.(1[6-9]|2[0-9]|3[01])\.') || true
    fi
  fi
  ip=
  for c in $cand; do
    case $c in ''|127.*) continue ;; esac
    ip=$c
    break
  done
  printf '%s' "$ip"
}

# ready_block: the last thing the installer prints. Only what was found, and the link to open.
ready_block() {
  say ""
  if [ -n "${DOCKER_UPDATED:-}" ]; then say "Updated the container. Your settings are kept."
  elif [ -n "$UPDATED" ]; then say "Updated to ${NEW_VERSION:-the new release}. Your settings are kept."; fi
  if [ -n "$NS_LINE" ]; then say "$NS_LINE"; fi
  if [ "$OS" = linux ] && [ -z "${DOCKER_RUN:-}" ] && have docker; then say "Docker is installed here. To run Shrink Your Shit in Docker instead, run this again with --docker."; fi
  case $PORT_ADDR in
    127.*|localhost:*|\[::1\]:*)
      say "Listening on this computer only, so workers can't find it. To let other computers in, run this again with --addr 0.0.0.0:$PORT."
      say "Ready. Open this on this computer to finish setting up:"
      say "  http://127.0.0.1:$PORT/" ;;
    *)
      tok=$(printf '%s\n' "$TOKEN_OUT" | sed -n 's/.*[?&]token=\([^&]*\).*/\1/p' | head -n 1)
      q=
      if [ -n "$tok" ]; then q="?token=$tok"; fi
      ip=$(lan_ip)
      if [ -n "$ip" ]; then
        link="http://$ip:$PORT/$q"
      else # no default route: the program's own sign-in address, which names this computer's address
        link=$(printf '%s\n' "$TOKEN_OUT" | sed -n 's/^Sign-in address: //p' | head -n 1)
        link=${link:-"http://<this computer's address>:$PORT/"}
      fi
      say "Ready. Open this on any computer on your network to finish setting up:"
      say "  $link"
      say "This link signs you in. To see it again: $TOKEN_CMD token"
      say "There is no HTTPS: use it on a network you trust." ;;
  esac
  say "Shrink Your Shit replaces original video files after checking the result. Try it on a copy first."
}

# ---- graphics (Linux): one Found line per chip, the service user given the device's group ----
# SYP_DEV and SYP_SYS are for tests only (where /dev and /sys are).
# give_service_access NODE: add the service user to the group that owns NODE. On failure, print why, return 1.
give_service_access() {
  g=$(stat -c %G "$1" 2>/dev/null) || { printf '%s' "can't read the device's group"; return 1; }
  if [ "$g" = root ]; then printf '%s' "the device belongs to root"; return 1; fi
  if id -nG syp 2>/dev/null | tr ' ' '\n' | grep -qxF "$g"; then return 0; fi
  if have usermod; then
    out=$($SUDO usermod -aG "$g" syp 2>&1) || { printf '%s' "$out" | tr '\n' ' '; return 1; }
  elif have addgroup; then
    out=$($SUDO addgroup syp "$g" 2>&1) || { printf '%s' "$out" | tr '\n' ' '; return 1; }
  else
    printf '%s' "neither usermod nor addgroup is available"; return 1
  fi
}

# nvidia_card: is there an NVIDIA display device on the PCI bus (0x10de, class 0x03...)?
nvidia_card() {
  for v in "${SYP_SYS:-/sys}"/bus/pci/devices/*/vendor; do
    [ -e "$v" ] || continue
    [ "$(cat "$v" 2>/dev/null)" = 0x10de ] || continue
    case $(cat "$(dirname "$v")/class" 2>/dev/null) in 0x03*) return 0 ;; esac
  done
  return 1
}

gpu_access() {
  dri=${SYP_DEV:-/dev}/dri
  shown=
  for node in "$dri"/renderD*; do
    [ -e "$node" ] || continue
    vendor=$(cat "${SYP_SYS:-/sys}/class/drm/$(basename "$node")/device/vendor" 2>/dev/null || true)
    case $vendor in
      0x8086) name="Intel Quick Sync" ;;
      0x1002) name="AMD graphics" ;;
      *) continue ;;
    esac
    shown=1
    if reason=$(give_service_access "$node"); then
      say "Found: $name ($(basename "$node")). Using it."
    else
      say "Found: $name ($(basename "$node")), but couldn't give the service access to it: $reason. Converting on the processor for now."
    fi
  done
  if nvidia_card; then
    shown=1
    model=$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -n 1 | sed 's/^NVIDIA //') || true
    if [ -n "$model" ]; then
      say "Found: NVIDIA $model. Using it."
    else
      say "Found an NVIDIA card, but its driver isn't installed. Converting on the processor for now. Install the NVIDIA driver, then run this again."
    fi
  fi
  if [ -z "$shown" ]; then
    say "No graphics chip for video found. Converting on the processor: slower, but it works."
  fi
}

# ======================= Docker (--docker) =======================
# The app runs from the published image in a container, not as a service. The only file this writes on the computer is
# the compose file in $DOCKER_DIR. A re-run keeps that file, so its settings and data volume stay as they were.
DOCKER_DIR=${SYP_DOCKER_DIR:-/opt/shrink-your-shit}   # SYP_DOCKER_DIR is for tests only
DOCKER_IMAGE=ghcr.io/peiceoftoast/shrink-your-shit:latest

docker_mode() {
  [ "$OS" = linux ] || die "--docker is for Linux. On macOS, run this again without --docker."
  have docker || die "--docker needs Docker, and it isn't installed here. Install Docker, or run this again without --docker."
  $SUDO docker compose version >/dev/null 2>&1 || die "--docker needs the Docker Compose plugin (docker compose). Install it, or run this again without --docker."
  [ -z "$LIBS" ] || warn "--library and SYP_LIBRARIES are not used with --docker yet. Add the folders to $DOCKER_DIR/docker-compose.yml as volumes."
  file=$DOCKER_DIR/docker-compose.yml
  dtmp=$(mktemp -d)
  trap 'rm -rf "$dtmp"' EXIT

  # Graphics: Intel and AMD through /dev/dri, with each device's group; NVIDIA through the container runtime.
  : > "$dtmp/groups"
  gpus=
  dri=${SYP_DEV:-/dev}/dri
  for node in "$dri"/renderD*; do
    [ -e "$node" ] || continue
    vendor=$(cat "${SYP_SYS:-/sys}/class/drm/$(basename "$node")/device/vendor" 2>/dev/null || true)
    case $vendor in
      0x8086) name="Intel Quick Sync" ;;
      0x1002) name="AMD graphics" ;;
      *) continue ;;
    esac
    gid=$(stat -c %g "$node" 2>/dev/null) || continue
    grep -qx "      - \"$gid\"" "$dtmp/groups" && continue # another render node in this group already added it
    printf '      - "%s"\n' "$gid" >> "$dtmp/groups"
    gpus="${gpus:+$gpus, }$name"
  done
  nvidia=
  if nvidia_card; then
    model=$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -n 1 | sed 's/^NVIDIA //') || true
    if [ -n "$model" ]; then
      nvidia=1
      gpus="${gpus:+$gpus, }NVIDIA $model"
    fi
  fi
  if [ -n "$gpus" ]; then passed="$gpus passed through"; else passed="processor only"; fi

  {
    cat <<YML
# Written by install.sh --docker. A re-run keeps this file and updates the image. To change the GPU or other settings, delete this file and run install.sh --docker again.
services:
  shrink-your-shit:
    image: $DOCKER_IMAGE
    ports:
      - "8484:8484"
    volumes:
      - syp-config:/config
    restart: unless-stopped
YML
    if [ "$NETWORK_SHARES" != 0 ]; then cat <<YML
    # Lets the app mount SMB and NFS shares. It gives the container mount rights (close to privileged);
    # delete these lines if you don't use network shares.
    cap_add:
      - SYS_ADMIN
      - DAC_READ_SEARCH
    security_opt:
      - "apparmor:unconfined"
YML
    fi
    if [ -s "$dtmp/groups" ]; then
      printf '    devices:\n      - /dev/dri:/dev/dri\n    group_add:\n'
      cat "$dtmp/groups"
    fi
    if [ -n "$nvidia" ]; then cat <<YML
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]
YML
    fi
    printf 'volumes:\n  syp-config:\n'
  } > "$dtmp/compose.yml"

  if [ -f "$file" ]; then
    DOCKER_UPDATED=1
    say "Kept $file (the settings from the earlier run)."
  else
    $SUDO install -d -m 0755 "$DOCKER_DIR"
    $SUDO install -m 0644 "$dtmp/compose.yml" "$file"
    say "Wrote $file ($passed)."
  fi

  if [ -n "$NO_START" ]; then
    say "Installed, not started (--no-start). Start it with: docker compose -f $file up -d"
    exit 0
  fi
  # Pull the newest image. If the pull fails (offline, or not published yet), start the copy already here.
  $SUDO docker compose -f "$file" pull >"$dtmp/pull.log" 2>&1 || warn "could not pull the newest image; starting the copy already on this computer."
  say "Starting the container..."
  quiet_run "starting the container" $SUDO docker compose -f "$file" up -d
  PORT_ADDR=0.0.0.0:8484
  PORT=8484
  if health_check; then :; else
    rc=$?
    [ "$rc" = 2 ] || die "the container did not answer on port $PORT. See: docker compose -f $file logs"
  fi
  TOKEN_OUT=$($SUDO docker compose -f "$file" exec -T shrink-your-shit shrink-your-shit token 2>/dev/null) || true
  TOKEN_CMD="sudo docker compose -f $file exec shrink-your-shit shrink-your-shit"   # ready_block adds: token
  if [ "$NETWORK_SHARES" = 0 ]; then
    NS_LINE="Network shares: off (--no-network-shares)."
  else
    NS_LINE="Network shares: ready (the container can connect SMB and NFS shares itself)."
  fi
  DOCKER_RUN=1
  ready_block
  exit 0
}

if [ -n "$DOCKER" ]; then docker_mode; fi


# ---- ffmpeg ----
ffmpeg_ok() { have ffmpeg && have ffprobe; }

install_ffmpeg() {
  ffmpeg_ok && return 0
  if [ "$OS" = darwin ]; then
    have brew || die "ffmpeg is missing and Homebrew is not installed. Install Homebrew from https://brew.sh, then run this again."
    quiet_run "installing ffmpeg with Homebrew" brew install ffmpeg
  elif have apt-get; then
    quiet_run "updating the package lists" $SUDO apt-get update -qq
    quiet_run "installing ffmpeg" $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ffmpeg ca-certificates
  elif have dnf; then
    quiet_run "installing ffmpeg" $SUDO dnf install -y ffmpeg || die "dnf could not install ffmpeg. On Fedora/RHEL enable RPM Fusion (https://rpmfusion.org) first, or install ffmpeg yourself, then run this again."
  elif have pacman; then
    quiet_run "installing ffmpeg" $SUDO pacman -S --noconfirm --needed ffmpeg
  elif have apk; then
    quiet_run "installing ffmpeg" $SUDO apk add --no-cache ffmpeg
  elif have zypper; then
    quiet_run "installing ffmpeg" $SUDO zypper --non-interactive install ffmpeg || die "zypper could not install ffmpeg. Enable the Packman repository (https://en.opensuse.org/Additional_package_repositories) or install ffmpeg yourself, then run this again."
  else
    die "ffmpeg is missing and no supported package manager was found (apt, dnf, pacman, apk, zypper, brew). Install ffmpeg yourself, then run this again."
  fi
  ffmpeg_ok || die "ffmpeg still not found after installing it. Check the messages above."
}

install_ffmpeg
ffmpeg -hide_banner -encoders 2>/dev/null | grep -q libx265 \
  || die "the installed ffmpeg cannot encode HEVC (no libx265). Install a full ffmpeg build and run this again. On Fedora/RHEL that means enabling RPM Fusion (https://rpmfusion.org) and running: sudo dnf swap ffmpeg-free ffmpeg --allowerasing"

# ---- download and verify ----
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
name=shrink-your-shit_${OS}_${ARCH}.tar.gz
fetch "$BASE_URL/$name" "$tmp/$name" || die "could not download $BASE_URL/$name. Is there a published release yet? (--version X.Y.Z picks one; SYP_BASE_URL points elsewhere)"
fetch "$BASE_URL/checksums.txt" "$tmp/checksums.txt" || die "could not download $BASE_URL/checksums.txt, so the download cannot be verified"
want=$(grep "  $name\$" "$tmp/checksums.txt" | cut -d' ' -f1 || true)
[ -n "$want" ] || die "checksums.txt has no entry for $name"
got=$(sha256_of "$tmp/$name")
[ "$want" = "$got" ] || die "checksum mismatch for $name (expected $want, got $got). Not installing."
mkdir "$tmp/x"
tar -xzf "$tmp/$name" -C "$tmp/x" --strip-components=1 --no-same-owner
[ -f "$tmp/x/shrink-your-shit" ] || die "the download did not contain the program"

NEW_VERSION=$("$tmp/x/shrink-your-shit" -version 2>/dev/null | awk '{print $2}') || true
PORT_ADDR=${ADDR:-0.0.0.0:8484}

# ======================= Linux =======================
if [ "$OS" = linux ]; then
  BIN=/usr/local/bin/shrink-your-shit
  ENV_FILE=/etc/default/shrink-your-shit
  DATA_DIR=/var/lib/shrink-your-shit
  UNIT=/etc/systemd/system/shrink-your-shit.service
  TOKEN_DATA=$DATA_DIR
  TOKEN_CMD='sudo shrink-your-shit'
  if [ -f "$ENV_FILE" ]; then UPDATED=1; fi

  if ! id syp >/dev/null 2>&1; then
    if have useradd; then
      $SUDO useradd --system --user-group --home-dir "$DATA_DIR" --shell /usr/sbin/nologin syp
    elif have adduser; then # Alpine / busybox
      $SUDO addgroup -S syp 2>/dev/null || true
      $SUDO adduser -S -D -H -h "$DATA_DIR" -s /sbin/nologin -G syp syp
    else
      die "can't create the service user 'syp': neither useradd nor adduser was found"
    fi
  fi
  $SUDO install -d -o syp -g syp -m 0750 "$DATA_DIR"
  gpu_access

  # Library: given, or kept from a previous install, or none (the web page's first run asks for it).
  if [ -z "$LIBS" ] && [ -f "$ENV_FILE" ]; then
    LIBS=$(sed -n 's/^SYP_LIBRARIES=//p' "$ENV_FILE" | tail -n 1)
  fi
  OLDIFS=$IFS; IFS=:
  for d in $LIBS; do
    [ -d "$d" ] || die "library folder '$d' does not exist"
    if have runuser && ! $SUDO runuser -u syp -- test -w "$d" 2>/dev/null; then
      warn "the service user 'syp' cannot write to $d. Fix with: sudo chown -R syp:syp '$d'  (or add syp to the folder's group)"
    fi
  done
  IFS=$OLDIFS

  # Settings file: created once; later runs change only the values you pass.
  newenv=$tmp/env
  if [ -f "$ENV_FILE" ]; then cp "$ENV_FILE" "$newenv"; else : > "$newenv"; fi
  setkv() { grep -v "^$1=" "$newenv" > "$newenv.n" || true; printf '%s=%s\n' "$1" "$2" >> "$newenv.n"; mv "$newenv.n" "$newenv"; }
  haskv() { grep -q "^$1=" "$newenv"; }
  if [ -n "$LIBS" ] && { [ -n "$FLAG_LIBS" ] || [ -n "${SYP_LIBRARIES:-}" ] || ! haskv SYP_LIBRARIES; }; then setkv SYP_LIBRARIES "$LIBS"; fi
  if [ -n "$ADDR" ] || ! haskv SYP_ADDR; then setkv SYP_ADDR "$PORT_ADDR"; fi
  haskv SYP_DATA || setkv SYP_DATA "$DATA_DIR"
  PORT_ADDR=$(sed -n 's/^SYP_ADDR=//p' "$newenv" | tail -n 1)
  PORT=${PORT_ADDR##*:}
  $SUDO mkdir -p "$(dirname "$ENV_FILE")"   # Alpine has no /etc/default
  $SUDO install -m 0644 "$newenv" "$ENV_FILE"

  $SUDO install -m 0755 "$tmp/x/shrink-your-shit" "$BIN"

  if [ -d /run/systemd/system ] && have systemctl; then
    cat > "$tmp/unit" <<UNIT
[Unit]
Description=shrink-your-shit video re-encoder
After=network-online.target
Wants=network-online.target

[Service]
User=syp
Group=syp
EnvironmentFile=$ENV_FILE
ExecStart=$BIN
Restart=on-failure
RestartSec=5
Nice=10
IOSchedulingClass=idle
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
UNIT
    $SUDO install -m 0644 "$tmp/unit" "$UNIT"

    # ── Network shares helper (user review 11-13 item D) ──────────────────
    # A small root helper that can only mount SMB/NFS shares in /var/lib/syp/shares. Opt-in. An upgrade keeps
    # the earlier choice: only --no-network-shares or answering "no" removes it.
    HELPER_DIR=/usr/local/libexec/shrink-your-shit
    HELPER_BIN=$HELPER_DIR/syp-network-helper
    HELPER_UNIT=/etc/systemd/system/syp-network-helper.service
    # Set up by default, without a question. The helper runs as root but can only mount shares in /var/lib/syp/shares.
    if [ -z "$NETWORK_SHARES" ]; then NETWORK_SHARES=1; fi
    if [ "$NETWORK_SHARES" = 1 ] && [ ! -f "$tmp/x/syp-network-helper" ]; then
      NS_REASON="this release has no network-share helper"
      NETWORK_SHARES=0
    fi
    if [ "$NETWORK_SHARES" = 1 ]; then
      # The helper refuses a shares folder anyone but root can write (a planted symlink could redirect a mount).
      $SUDO install -d -o root -g root -m 0755 /var/lib/syp /var/lib/syp/shares "$HELPER_DIR"
      $SUDO install -o root -g root -m 0755 "$tmp/x/syp-network-helper" "$HELPER_BIN"
      if ! have mount.cifs || ! have mount.nfs; then
        if have apt-get; then
          quiet_run "updating the package lists" $SUDO apt-get update -qq || true
          quiet_run "installing cifs-utils and nfs-common" $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends cifs-utils nfs-common \
            || NS_REASON="could not install cifs-utils and nfs-common"
        elif have dnf; then
          quiet_run "installing cifs-utils and nfs-utils" $SUDO dnf install -y cifs-utils nfs-utils || NS_REASON="could not install cifs-utils and nfs-utils"
        elif have yum; then
          quiet_run "installing cifs-utils and nfs-utils" $SUDO yum install -y cifs-utils nfs-utils || NS_REASON="could not install cifs-utils and nfs-utils"
        elif have apk; then
          quiet_run "installing cifs-utils and nfs-utils" $SUDO apk add --no-cache cifs-utils nfs-utils || NS_REASON="could not install cifs-utils and nfs-utils"
        else
          NS_REASON="no package manager found for cifs-utils and the NFS client tools"
        fi
      fi
      # No sandboxing on purpose: ProtectSystem/ProtectHome/PrivateTmp/PrivateMounts would give the helper its
      # own mount namespace, and the shares it mounts would be invisible to the app (orch-lead review of D2).
      cat > "$tmp/helper-unit" <<HLPUNIT
[Unit]
Description=Shrink Your Shit network share helper (mounts SMB/NFS shares in /var/lib/syp/shares)
After=network-online.target
Wants=network-online.target
Before=shrink-your-shit.service

[Service]
Type=simple
User=root
ExecStart=$HELPER_BIN -user syp
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
HLPUNIT
      $SUDO install -m 0644 "$tmp/helper-unit" "$HELPER_UNIT"
      $SUDO systemctl daemon-reload
      $SUDO systemctl enable syp-network-helper >/dev/null 2>&1
      $SUDO systemctl restart syp-network-helper # an upgrade runs the new helper
    elif [ -f "$HELPER_UNIT" ] || [ -f "$HELPER_BIN" ]; then
      # Asked to remove it. The shares folder and whatever is mounted there are left alone.
      $SUDO systemctl disable --now syp-network-helper >/dev/null 2>&1 || true
      $SUDO rm -f "$HELPER_UNIT" "$HELPER_BIN"
      say "Removed the network-share helper. Shares already mounted stay mounted until the next restart."
    fi

    $SUDO systemctl daemon-reload
    if [ -n "$NS_REASON" ]; then
      NS_LINE="Network shares: not set up ($NS_REASON). You can still use folders this computer already sees."
    elif [ "$NETWORK_SHARES" = 1 ]; then
      NS_LINE="Network shares: ready (the app can connect SMB and NFS shares itself)."
    else
      NS_LINE="Network shares: off (--no-network-shares)."
    fi
    if [ -n "$NO_START" ]; then
      $SUDO systemctl enable shrink-your-shit >/dev/null 2>&1
      say "Installed, not started (--no-start). Start it with: sudo systemctl start shrink-your-shit"
      exit 0
    fi
    $SUDO systemctl enable shrink-your-shit >/dev/null 2>&1
    $SUDO systemctl restart shrink-your-shit
    if health_check; then :; else
      rc=$?
      [ "$rc" = 2 ] || die "the service did not answer on port $PORT. See: journalctl -u shrink-your-shit -n 50"
    fi
    TOKEN_OUT=$(token_out)
    ready_block
    exit 0
  fi

  # No systemd (a container, Alpine/OpenRC, WSL without systemd...): the program is installed, no service.
  say "No systemd here, so no service was set up. The program is installed at $BIN."
  if [ -n "$RUN_FG" ]; then
    say "Running it now (Ctrl+C stops it) at http://$(check_host "$PORT_ADDR"):$PORT"
    say "(--run starts it as the user running this installer, not as 'syp', so files it replaces will be owned by that user.)"
    while IFS='=' read -r k v; do
      case $k in ''|'#'*) continue ;; esac
      export "$k=$v"
    done < "$ENV_FILE"
    rm -rf "$tmp"; trap - EXIT   # exec replaces this shell, so clean up first
    exec "$BIN"
  fi
  say "Start it with:  set -a; . $ENV_FILE; set +a; $BIN"
  say "(or run this installer again with --run)"
  exit 0
fi

# ======================= macOS =======================
BIN_DIR=$HOME/.local/bin
BIN=$BIN_DIR/shrink-your-shit
DATA_DIR=$HOME/Library/Application\ Support/shrink-your-shit
PLIST=$HOME/Library/LaunchAgents/dev.shrink-your-shit.plist
LOG=$HOME/Library/Logs/shrink-your-shit.log
TOKEN_CMD=$BIN
if [ -f "$PLIST" ]; then UPDATED=1; fi

# Settings from a previous install are kept unless a flag says otherwise (upgrade = re-run).
plist_get() { # plist_get KEY  (reads our own plist, where each key and its string share a line)
  [ -f "$PLIST" ] || return 0
  sed -n "s|.*<key>$1</key><string>\(.*\)</string>.*|\1|p" "$PLIST" | head -n 1 \
    | sed -e 's/&lt;/</g' -e 's/&gt;/>/g' -e 's/&amp;/\&/g'
}
if [ -z "$LIBS" ]; then LIBS=$(plist_get SYP_LIBRARIES); fi
if [ -z "$ADDR" ]; then
  old_addr=$(plist_get SYP_ADDR)
  [ -z "$old_addr" ] || PORT_ADDR=$old_addr
fi
PORT=${PORT_ADDR##*:}
OLDIFS=$IFS; IFS=:
for d in $LIBS; do [ -d "$d" ] || die "library folder '$d' does not exist"; done
IFS=$OLDIFS

mkdir -p "$BIN_DIR" "$DATA_DIR" "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"
install -m 0755 "$tmp/x/shrink-your-shit" "$BIN"
# Downloads from the internet get a quarantine flag that triggers Gatekeeper; this program is
# unsigned and was fetched by this script, so remove the flag for our own binary.
xattr -d com.apple.quarantine "$BIN" 2>/dev/null || true

esc() { printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }
FFDIR=$(dirname "$(command -v ffmpeg)")
cat > "$PLIST" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>dev.shrink-your-shit</string>
  <key>ProgramArguments</key><array><string>$(esc "$BIN")</string></array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>SYP_LIBRARIES</key><string>$(esc "$LIBS")</string>
    <key>SYP_ADDR</key><string>$(esc "$PORT_ADDR")</string>
    <key>SYP_DATA</key><string>$(esc "$DATA_DIR")</string>
    <key>PATH</key><string>$(esc "$FFDIR"):/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>$(esc "$LOG")</string>
  <key>StandardErrorPath</key><string>$(esc "$LOG")</string>
</dict>
</plist>
PL

if [ -n "$NO_START" ]; then say "Installed, not started (--no-start). Start it with: launchctl bootstrap gui/$(id -u) $PLIST"; exit 0; fi
launchctl bootout "gui/$(id -u)/dev.shrink-your-shit" 2>/dev/null || true
# bootout can still be finishing when bootstrap runs, which fails with an I/O error: retry briefly.
i=0
until lc_err=$(launchctl bootstrap "gui/$(id -u)" "$PLIST" 2>&1); do
  i=$((i + 1))
  [ $i -lt 5 ] || die "launchd would not load the service ($lc_err). Is this a normal desktop login session? See also $LOG"
  sleep 1
done
if health_check; then :; else
  rc=$?
  [ "$rc" = 2 ] || die "the service did not answer on port $PORT. See $LOG"
fi
TOKEN_DATA=$DATA_DIR
TOKEN_OUT=$(token_out)
ready_block
