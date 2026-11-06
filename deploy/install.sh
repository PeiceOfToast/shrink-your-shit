#!/bin/sh
# Install shrink-your-shit as a systemd service on Debian/Ubuntu (e.g. a Proxmox LXC).
# Run as root:  sh install.sh [path/to/shrink-your-shit]
# Safe to run again: it updates the program and keeps your settings in /etc/default/shrink-your-shit.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
bin=${1:-$here/../shrink-your-shit}
[ -f "$bin" ] || bin=$here/shrink-your-shit
env_file=/etc/default/shrink-your-shit
data_dir=/var/lib/shrink-your-shit

[ "$(id -u)" -eq 0 ] || { echo "Run this as root." >&2; exit 1; }
command -v apt-get >/dev/null 2>&1 || { echo "This script needs a Debian/Ubuntu system (apt-get)." >&2; exit 1; }
command -v systemctl >/dev/null 2>&1 || { echo "This script needs systemd." >&2; exit 1; }
[ -f "$bin" ] || {
  echo "Can't find the shrink-your-shit program. Build or copy it first, then: sh install.sh /path/to/shrink-your-shit" >&2
  exit 1
}

if ! command -v ffmpeg >/dev/null 2>&1 || ! command -v ffprobe >/dev/null 2>&1; then
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ffmpeg ca-certificates
fi

id syp >/dev/null 2>&1 || useradd --system --user-group --home-dir "$data_dir" --shell /usr/sbin/nologin syp
install -d -o syp -g syp -m 0750 "$data_dir"
install -m 0755 "$bin" /usr/local/bin/shrink-your-shit
install -m 0644 "$here/shrink-your-shit.service" /etc/systemd/system/shrink-your-shit.service

if [ ! -f "$env_file" ]; then
  cat >"$env_file" <<CONF
# Settings for shrink-your-shit. After editing: systemctl restart shrink-your-shit
# Folders to watch, separated by ":". The user "syp" must be able to read AND write them.
# WARNING: originals are REPLACED. Start with a copy of your media.
SYP_LIBRARIES=/media
# 0.0.0.0 lets other machines open the web page. There is no login yet: keep it on a trusted network.
SYP_ADDR=0.0.0.0:8484
SYP_DATA=$data_dir
CONF
  chmod 0644 "$env_file"
fi

systemctl daemon-reload
systemctl enable shrink-your-shit >/dev/null
systemctl restart shrink-your-shit
echo "Installed. Web page: http://<this-machine>:8484   Settings: $env_file   Logs: journalctl -u shrink-your-shit -f"
