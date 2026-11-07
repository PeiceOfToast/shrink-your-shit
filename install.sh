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
#   --addr ADDR       web address (default 127.0.0.1:8484, this computer only; with 0.0.0.0:8484
#                     other machines sign in with an access token, shown at the end)
#   --version X.Y.Z   install that release instead of the newest
#   --no-start        install but do not start
#   --run             run in the foreground at the end (for containers / machines without a service manager)
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
RUN_FG=

say() { printf '%s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

usage() {
  cat <<'USAGE'
Usage: install.sh [--library DIR]... [--addr ADDR] [--version X.Y.Z] [--no-start] [--run]
  --library DIR    folder to watch (repeat for several); default: none, add folders on the web page
  --addr ADDR      web address, default 127.0.0.1:8484 (this computer only); beyond it, an access token is needed
  --version X.Y.Z  install that release instead of the newest
  --no-start       install but do not start
  --run            run in the foreground at the end (containers / no service manager)
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

# ---- privileges ----
SUDO=
if [ "$OS" = linux ]; then
  if [ "$(id -u)" -ne 0 ]; then
    if have sudo; then
      SUDO=sudo
      say "Not running as root: using sudo to install the program, its service and ffmpeg system-wide."
    else
      die "this needs root to install system-wide. Run it as root, or install sudo first."
    fi
  fi
fi

# ---- ffmpeg ----
ffmpeg_ok() { have ffmpeg && have ffprobe; }

install_ffmpeg() {
  ffmpeg_ok && return 0
  say "ffmpeg is not installed. Installing it..."
  if [ "$OS" = darwin ]; then
    have brew || die "ffmpeg is missing and Homebrew is not installed. Install Homebrew from https://brew.sh, then run this again."
    brew install ffmpeg
  elif have apt-get; then
    $SUDO apt-get update -qq
    $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ffmpeg ca-certificates
  elif have dnf; then
    $SUDO dnf install -y ffmpeg || die "dnf could not install ffmpeg. On Fedora/RHEL enable RPM Fusion (https://rpmfusion.org) first, or install ffmpeg yourself, then run this again."
  elif have pacman; then
    $SUDO pacman -S --noconfirm --needed ffmpeg
  elif have apk; then
    $SUDO apk add --no-cache ffmpeg
  elif have zypper; then
    $SUDO zypper --non-interactive install ffmpeg || die "zypper could not install ffmpeg. Enable the Packman repository (https://en.opensuse.org/Additional_package_repositories) or install ffmpeg yourself, then run this again."
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
say "Downloading $name from $BASE_URL ..."
fetch "$BASE_URL/$name" "$tmp/$name" || die "could not download $BASE_URL/$name. Is there a published release yet? (--version X.Y.Z picks one; SYP_BASE_URL points elsewhere)"
fetch "$BASE_URL/checksums.txt" "$tmp/checksums.txt" || die "could not download $BASE_URL/checksums.txt, so the download cannot be verified"
want=$(grep "  $name\$" "$tmp/checksums.txt" | cut -d' ' -f1 || true)
[ -n "$want" ] || die "checksums.txt has no entry for $name"
got=$(sha256_of "$tmp/$name")
[ "$want" = "$got" ] || die "checksum mismatch for $name (expected $want, got $got). Not installing."
mkdir "$tmp/x"
tar -xzf "$tmp/$name" -C "$tmp/x" --strip-components=1 --no-same-owner
[ -f "$tmp/x/shrink-your-shit" ] || die "the download did not contain the program"

PORT_ADDR=${ADDR:-127.0.0.1:8484}

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

# signin_url BASE: on loopback, BASE. Beyond it, the sign-in address the program's `token` command
# gives for the configured address (it made the token on its first start), with this machine's
# network address for "all addresses"; BASE if that fails.
signin_url() {
  case $PORT_ADDR in 127.*|localhost:*|\[::1\]:*) printf '%s' "$1"; return ;; esac
  if [ -n "${TOKEN_DATA:-}" ]; then
    u=$($SUDO env SYP_ADDR="$PORT_ADDR" SYP_DATA="$TOKEN_DATA" "$BIN" token 2>/dev/null | sed -n 's/^Sign-in address: //p')
  else
    u=$($SUDO env SYP_ADDR="$PORT_ADDR" "$BIN" token 2>/dev/null | sed -n 's/^Sign-in address: //p')
  fi
  printf '%s' "${u:-$1}"
}

finish() { # finish URL_NOTE
  say ""
  say "Installed. Open $1"
  say "Settings: $SETTINGS_NOTE"
  say "Note: shrink-your-shit REPLACES original video files (after checking the result). Try it on a copy first."
}

# ======================= Linux =======================
if [ "$OS" = linux ]; then
  BIN=/usr/local/bin/shrink-your-shit
  ENV_FILE=/etc/default/shrink-your-shit
  DATA_DIR=/var/lib/shrink-your-shit
  UNIT=/etc/systemd/system/shrink-your-shit.service
  SETTINGS_NOTE="$ENV_FILE (then: sudo systemctl restart shrink-your-shit)   Logs: journalctl -u shrink-your-shit -f"

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

  # Library: given, or kept from a previous install, or none (the web page's first run asks for it).
  if [ -z "$LIBS" ] && [ -f "$ENV_FILE" ]; then
    LIBS=$(sed -n 's/^SYP_LIBRARIES=//p' "$ENV_FILE" | tail -n 1)
  fi
  [ -n "$LIBS" ] || say "No library folder given: add your video folders on the web page."
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
    $SUDO systemctl daemon-reload
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
    host=127.0.0.1
    case $PORT_ADDR in 127.*|localhost:*) ;; *) host=$(hostname -I 2>/dev/null | cut -d' ' -f1); host=${host:-this-machine} ;; esac
    finish "$(signin_url "http://$host:$PORT")"
    case $PORT_ADDR in
      127.*|localhost:*) say "It only listens on this computer. For other machines on your network: run this again with --addr 0.0.0.0:$PORT (they then sign in with an access token)." ;;
      *) say "That address carries the access token. To see it again: sudo shrink-your-shit token. There is no HTTPS: use it on a network you trust." ;;
    esac
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
SETTINGS_NOTE="$PLIST (then: launchctl kickstart -k gui/$(id -u)/dev.shrink-your-shit)   Log: $LOG"

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
[ -n "$LIBS" ] || say "No library folder given: add your video folders on the web page."
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
finish "$(signin_url "http://127.0.0.1:$PORT")"
case $PORT_ADDR in 127.*|localhost:*) ;; *) say "It is reachable from other machines, which sign in with the access token in that address. To see it again: shrink-your-shit token. There is no HTTPS: use it on a network you trust." ;; esac
