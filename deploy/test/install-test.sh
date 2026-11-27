#!/bin/sh
# Stop 1's proof: the one-line install on a brand-new machine, then a clip converted through the real UI.
#
#   deploy/test/install-test.sh <vmid> <template>
#
# <vmid> is 500-519 (workshop's range on Host05); anything else is refused, so the user's containers (100, 101)
# can never be touched. <template> is debian12, debian13, ubuntu24 — or a full local:vztmpl/... volume id.
#
# By default it runs the EXACT published one-liner, with no SYP_BASE_URL:
#
#   wget -qO- https://github.com/PeiceOfToast/shrink-your-shit/releases/download/rebuild/install.sh | sh
#
# on a fresh unprivileged container on Host05 tagged syp-test, then checks the whole stop-1 path. With
# SYP_ARTIFACTS holding syp-linux-amd64, install.sh and checksums.txt, the same one-liner shape runs against
# that folder served over the container's loopback instead (for a build not published yet, and for the
# wrong-checksum check).
# Every check prints PASS or FAIL; the container is destroyed at the end unless KEEP=1. The run's log is written,
# with tokens redacted, to slices/15-rebuild/install-test-<template>-<vmid>-<UTC stamp>.log (SYP_SLICES
# overrides). The vmid keeps two runs started in the same second from writing over each other's log.
#
# Needs: ssh, curl, tar, sha256sum, python3 and the pve-api5 wrapper that holds the Host05 token. The published
# path also needs the container to reach github.com. The token itself is never printed.
set -eu

NODE=Host05
VMID="${1:-}"
TEMPLATE="${2:-}"
PASSWORD="${SYP_TEST_PASSWORD:-install-test-password-1}"
LIBRARY=/srv/syp-clips
KEEP="${KEEP:-0}"
KEY="${SYP_SSH_KEY:-$HOME/.ssh/openrig_proxmox_test}"
SLICES="${SYP_SLICES:-/home/mb/.openrig/workspace/missions/shrink-your-shit/slices/15-rebuild}"
# The installer's own documented command, word for word.
ONE_LINER='wget -qO- https://github.com/PeiceOfToast/shrink-your-shit/releases/download/rebuild/install.sh | sh'

if [ -z "$VMID" ] || [ -z "$TEMPLATE" ]; then
  echo "usage: $0 <vmid 500-519> <debian12|debian13|ubuntu24|local:vztmpl/...>" >&2
  exit 2
fi
case "$VMID" in
  *[!0-9]*) echo "install-test: vmid must be a number, got '$VMID'" >&2; exit 2 ;;
esac
if [ "$VMID" -lt 500 ] || [ "$VMID" -gt 519 ]; then
  echo "install-test: refusing vmid $VMID; workshop's Host05 range is 500-519 (100 and 101 are the user's)" >&2
  exit 2
fi
case "$TEMPLATE" in
  debian12|debian-12) VOLID=local:vztmpl/debian-12-standard_12.12-1_amd64.tar.zst ;;
  debian13|debian-13) VOLID=local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst ;;
  ubuntu24|ubuntu-24.04) VOLID=local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst ;;
  local:vztmpl/*) VOLID=$TEMPLATE ;;
  *) echo "install-test: unknown template '$TEMPLATE'; use debian12, debian13, ubuntu24, or local:vztmpl/..." >&2; exit 2 ;;
esac

STAMP=$(date -u +%Y%m%dT%H%M%SZ)
SAFE_TPL=$(printf '%s' "$TEMPLATE" | tr -c 'A-Za-z0-9._-' '-')
LOG_TMP=$(mktemp /tmp/syp-install-test.XXXXXX)
LOG="$SLICES/install-test-$SAFE_TPL-$VMID-$STAMP.log"
WORK=$(mktemp -d /tmp/syp-install-test-work.XXXXXX)
IP="10.10.1.$((200 + VMID - 500))"
BASE=http://$IP:8484
JAR=$WORK/cookies.txt
FAILURES=0
CREATED=0
CT_HOSTNAME="syp-test-r1-11-$SAFE_TPL"

say() { printf '%s\n' "$*" | tee -a "$LOG_TMP"; }
pass() { say "PASS  $*"; }
fail() { say "FAIL  $*"; FAILURES=$((FAILURES + 1)); }
step() { say ""; say "== $*"; }
SSH_OPTS="-i $KEY -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o LogLevel=ERROR"
# The options are separate words on purpose.
# shellcheck disable=SC2029,SC2086
ct() { ssh $SSH_OPTS "root@$IP" "$@"; }
# -f: the local ssh returns once the remote process is started, so a server left running in the container does
# not hold this script; without it sshd keeps the session open until that process exits.
# shellcheck disable=SC2086
ctf() { ssh -f $SSH_OPTS "root@$IP" "$@"; }

finish() {
  rc=$?
  if [ "$CREATED" = 1 ]; then
    if [ "$KEEP" = 1 ]; then
      say "KEEP=1: container $VMID is still there; delete it with: pve-api5 DELETE '/nodes/$NODE/lxc/$VMID?purge=1'"
    else
      say "Deleting container $VMID"
      pve-api5 POST "/nodes/$NODE/lxc/$VMID/status/stop" >/dev/null 2>&1 || true
      i=0
      while [ "$i" -lt 120 ]; do
        pve-api5 GET "/nodes/$NODE/lxc/$VMID/status/current" 2>/dev/null | grep -q '"status":"running"' || break
        i=$((i + 1))
        sleep 1
      done
      # The DELETE can still lose a race with the shutdown: Ubuntu 24.04 was once still stopping when it was
      # tried, the call errored, and the container was left behind. Retry it, and make a surviving container a
      # failure of this run rather than a note, so a green run never hides a container someone has to delete.
      i=0
      while [ "$i" -lt 3 ]; do
        pve-api5 DELETE "/nodes/$NODE/lxc/$VMID?purge=1" >/dev/null 2>&1 && break
        i=$((i + 1))
        sleep 5
      done
      if [ "$i" -ge 3 ]; then
        fail "container $VMID could not be deleted; delete it with: pve-api5 DELETE '/nodes/$NODE/lxc/$VMID?purge=1'"
      fi
    fi
  fi
  if [ -d "$SLICES" ]; then LOGDEST=$LOG; else LOGDEST=$LOG_TMP; fi
  # A nonzero exit with no FAIL line is an abort (set -e) the checks never named; count it. When a FAIL was
  # already recorded, the exit is that failure leaving, not a second one.
  if [ "$rc" -ne 0 ] && [ "$FAILURES" -eq 0 ]; then FAILURES=1; fi
  say "log: $LOGDEST"
  say "$FAILURES check(s) failed"
  # The log must never carry a sign-in token or a session cookie. It is written last, so the saved copy
  # carries the verdict printed just above.
  if [ "$LOGDEST" = "$LOG_TMP" ]; then
    echo "note: $SLICES is not a folder; the log is $LOG_TMP (not redacted, so do not share it)" >&2
  else
    sed -E -e 's/([?&]token=)[^"& ]+/\1REDACTED/g' -e 's/(syp_session=)[^; ]+/\1REDACTED/g' "$LOG_TMP" >"$LOGDEST"
  fi
  [ "$FAILURES" -eq 0 ]
}
trap finish EXIT
trap 'exit 130' INT TERM

# --- what the container installs ------------------------------------------------------------------
step "The installer to test"
ART="${SYP_ARTIFACTS:-}"
if [ -n "$ART" ]; then
  say "using the prepared folder $ART"
  missing=""
  for f in syp-linux-amd64 install.sh checksums.txt; do
    [ -f "$ART/$f" ] || missing="$missing $f"
  done
  if [ -n "$missing" ]; then
    fail "the artifacts folder is missing:$missing"
    exit 1
  fi
  pass "artifacts have syp-linux-amd64, install.sh and checksums.txt"
  say "syp-linux-amd64 sha256: $(sha256sum "$ART/syp-linux-amd64" | cut -d' ' -f1)"
else
  say "the published release, the exact one-liner with no SYP_BASE_URL:"
  say "  $ONE_LINER"
fi

# --- the container ---------------------------------------------------------------------------------
step "Container $VMID from $VOLID"
if pve-api5 GET "/nodes/$NODE/lxc/$VMID" >/dev/null 2>&1; then
  fail "container $VMID already exists on $NODE; refusing to reuse it"
  exit 1
fi
CREATED=1
# The values with "=" or spaces go as --data-urlencode: a bare -d for net0 makes Proxmox read only the first
# key=value pair ("invalid format - value without key").
pve-api5 POST "/nodes/$NODE/lxc" \
  -d vmid="$VMID" -d ostemplate="$VOLID" -d hostname="$CT_HOSTNAME" \
  -d memory=2048 -d cores=2 -d swap=512 -d rootfs=local-lvm:8 \
  --data-urlencode "net0=name=eth0,bridge=vmbr0,ip=$IP/20,gw=10.10.0.1" \
  -d unprivileged=1 -d start=1 -d tags=syp-test -d pool=syp-test \
  --data-urlencode "description=R1-11 install test; deleted by deploy/test/install-test.sh" \
  --data-urlencode "ssh-public-keys=$(cat "$KEY.pub")" >>"$LOG_TMP" 2>&1 \
  || { fail "the create call failed (the API's answer is in $LOG_TMP)"; exit 1; }
# The API's answer has no trailing newline; without one the first PASS lands on the same line in the log.
printf '\n' >>"$LOG_TMP"
i=0
while [ "$i" -lt 60 ]; do
  if ct true >/dev/null 2>&1; then break; fi
  i=$((i + 1))
  sleep 2
done
if ct true >/dev/null 2>&1; then
  pass "container answers ssh at $IP"
else
  fail "container did not answer ssh at $IP within 2 minutes"
  exit 1
fi
if ct 'command -v wget >/dev/null'; then
  pass "wget is on the fresh template (the one-liner's tool)"
else
  fail "wget is not on the fresh template; the documented one-liner cannot run"
fi
# The expansion happens in the container, not here.
# shellcheck disable=SC2016
say "inside: $(ct '. /etc/os-release && echo "$PRETTY_NAME"')"

if [ -n "$ART" ]; then
  step "Copying install.sh, checksums.txt and syp-linux-amd64 in"
  tar -C "$ART" -cf - install.sh checksums.txt syp-linux-amd64 \
    | ct 'mkdir -p /srv/syp-test && tar -C /srv/syp-test -xf -' 2>>"$LOG_TMP" \
    || { fail "copying the artifacts in failed"; exit 1; }
  remote_sha=$(ct 'sha256sum /srv/syp-test/syp-linux-amd64' | cut -d' ' -f1)
  local_sha=$(sha256sum "$ART/syp-linux-amd64" | cut -d' ' -f1)
  if [ "$remote_sha" = "$local_sha" ]; then
    pass "the artifact arrived byte for byte"
  else
    fail "the artifact did not arrive byte for byte ($local_sha vs $remote_sha)"
  fi
fi

if [ -n "$ART" ]; then
  # wget does not read file://, so the folder goes over HTTP on the container's own loopback: the one-liner is
  # then the documented `wget -qO- <url> | sh` shape.
  step "Serving the artifacts inside the container"
  ctf 'cd /srv/syp-test && exec python3 -m http.server 18080 --bind 127.0.0.1 >/dev/null 2>&1' >/dev/null 2>&1 || true
  i=0
  while [ "$i" -lt 15 ]; do
    if ct 'wget -qO- http://127.0.0.1:18080/install.sh' 2>/dev/null | head -n 1 | grep -q '^#!/bin/sh'; then break; fi
    i=$((i + 1))
    sleep 1
  done
  if ct 'wget -qO- http://127.0.0.1:18080/install.sh' 2>/dev/null | head -n 1 | grep -q '^#!/bin/sh'; then
    pass "the container serves install.sh on 127.0.0.1:18080"
  else
    fail "the artifact server did not answer inside the container"
    exit 1
  fi
fi

# install_once runs the one-liner itself: the published URL, or the prepared folder on loopback.
install_once() {
  if [ -n "$ART" ]; then
    # The variable inside the remote command is the container's, not this shell's.
    # shellcheck disable=SC2016
    ct 'SYP_BASE_URL=http://127.0.0.1:18080; export SYP_BASE_URL; wget -qO- "$SYP_BASE_URL/install.sh" | sh'
  else
    ct "$ONE_LINER"
  fi
}

# run_installer prints the install's output and sets INSTALL_RC to its real status. A plain
# `install_once | tee` would report tee's status (dash has no pipefail), so a failing installer would look like
# a success and the checks after it would pass on whatever the previous install left behind.
run_installer() {
  INSTALL_RC=0
  install_once >"$WORK/install.out" 2>&1 || INSTALL_RC=$?
  tee -a "$LOG_TMP" <"$WORK/install.out"
}

# --- the one-line install ---------------------------------------------------------------------------
step "The one-line install"
run_installer
if [ "$INSTALL_RC" -eq 0 ] && ct 'test -x /usr/local/bin/syp && test -f /etc/systemd/system/syp.service'; then
  pass "the one-line install exited 0 and put syp in place"
else
  fail "the one-line install exited $INSTALL_RC without putting syp in place (its output above is the record)"
  exit 1
fi
if curl -fsS -o /dev/null "$BASE/api/v2/health"; then
  pass "health answers from outside the container"
else
  fail "health does not answer from outside the container"
  exit 1
fi
if ct 'systemctl is-active --quiet syp' >/dev/null 2>&1 && ct 'systemctl is-enabled --quiet syp' >/dev/null 2>&1; then
  pass "the syp service is active and enabled"
else
  fail "the syp service is not active and enabled"
fi
ff=$(ct '/opt/syp/ffmpeg/ffmpeg -version' 2>/dev/null | head -n 1)
case "$ff" in
  "ffmpeg version n8.0.1"*) pass "the installed ffmpeg is the pinned 8 build ($ff)" ;;
  *) fail "the installed ffmpeg is not the pinned 8 build (got: ${ff:-nothing})" ;;
esac

# --- the setup page, before there is an account -----------------------------------------------------
step "The setup page before an account exists"
page=$WORK/page.html
code=$(curl -sS -o "$page" -w '%{http_code}' "$BASE/")
if [ "$code" = 200 ] && grep -q 'id="root"' "$page"; then
  pass "GET / answers 200 with the web interface before setup"
else
  fail "GET / before setup answered $code without the interface (first 200 bytes: $(head -c 200 "$page" | tr '\n' ' '))"
fi

# --- a library of three clips, and setup ------------------------------------------------------------
step "A library of three clips and POST /setup"
ct "install -d -o syp -g syp -m 0750 $LIBRARY" || fail "the library folder could not be made"
i=1
while [ "$i" -le 3 ]; do
  # crf 6: a high-bitrate source so the HEVC result is really smaller and the job converts instead of keeping it.
  ct "/opt/syp/ffmpeg/ffmpeg -hide_banner -loglevel error -y -f lavfi -i testsrc2=size=1280x720:rate=30 -t 3 -c:v libx264 -preset veryfast -crf 6 -pix_fmt yuv420p $LIBRARY/clip$i.mp4" \
    >>"$LOG_TMP" 2>&1 || fail "generating clip$i failed"
  i=$((i + 1))
done
ct "sha256sum $LIBRARY/clip*.mp4 > /root/originals.sha256 && chown -R syp:syp $LIBRARY"
PROBED=$(ct "/opt/syp/ffmpeg/ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 $LIBRARY/clip1.mp4")
if [ "$PROBED" = h264 ]; then
  pass "three clips exist in $LIBRARY, generated by the installed ffmpeg (clip1 is $PROBED)"
else
  fail "clip1 does not probe as h264 (got: ${PROBED:-nothing})"
fi

setup_code=$(curl -sS -o "$WORK/setup.json" -w '%{http_code}' -c "$JAR" -b "$JAR" \
  -H 'X-SYP: 1' -H 'Content-Type: application/json' \
  -d "{\"account\":{\"password\":\"$PASSWORD\"},\"library\":{\"path\":\"$LIBRARY\"},\"starter\":{\"id\":\"shrink-videos\",\"answers\":{}}}" \
  "$BASE/api/v2/setup")
if [ "$setup_code" = 200 ]; then
  pass "POST /setup answered 200"
else
  fail "POST /setup answered $setup_code: $(head -c 300 "$WORK/setup.json" | tr '\n' ' ')"
  exit 1
fi
LIBID=$(curl -sS -b "$JAR" -c "$JAR" "$BASE/api/v2/libraries" | LIBRARY="$LIBRARY" python3 -c '
import json, os, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
if isinstance(d, dict):
    d = d.get("items") or []
for lib in d:
    if lib.get("path") == os.environ["LIBRARY"] and lib.get("id"):
        print(lib["id"])
        break
' || true)
if [ -n "$LIBID" ]; then
  pass "the library is saved (id $LIBID)"
else
  fail "the library is not in GET /libraries"
fi

# --- one job, done, with its output ----------------------------------------------------------------
step "One job reaches done (up to 3 minutes)"
DEADLINE=$(( $(date +%s) + 180 ))
JOB=""
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  JOB=$(curl -sS -b "$JAR" -c "$JAR" "$BASE/api/v2/jobs?state=done&limit=50" | python3 -c '
import json, sys
clips = {"clip1.mp4", "clip2.mp4", "clip3.mp4"}
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for it in d.get("items") or []:
    if it.get("state") == "done" and it.get("file") in clips:
        print(it["id"])
        break
' || true)
  [ -n "$JOB" ] && break
  if [ -n "$LIBID" ]; then
    # A first scan can pass over a file whose size was still settling; ask again.
    curl -sS -b "$JAR" -c "$JAR" -H 'X-SYP: 1' -H 'Content-Type: application/json' -d '{}' \
      "$BASE/api/v2/libraries/$LIBID/scan" >/dev/null 2>&1 || true
  fi
  sleep 10
done
if [ -n "$JOB" ]; then
  pass "job $JOB is done"
else
  fail "no clip job reached done within 3 minutes"
  say "the job list: $(curl -sS -b "$JAR" "$BASE/api/v2/jobs?limit=20" | head -c 600)"
  exit 1
fi

# The detail wraps the job: outputPath is where its result went, file is the source's name.
JOBFILE=$(curl -sS -b "$JAR" -c "$JAR" "$BASE/api/v2/jobs/$JOB" | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
job = d.get("job") or d
print(d.get("outputPath") or "")
print(job.get("file") or "")
')
OUT=$(printf '%s\n' "$JOBFILE" | head -n 1)
NAME=$(printf '%s\n' "$JOBFILE" | sed -n 2p)
[ -n "$OUT" ] || OUT="$LIBRARY/${NAME%.*}-shrunk.mkv"
CODEC=$(ct "/opt/syp/ffmpeg/ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 '$OUT'" 2>/dev/null || true)
if [ -n "$NAME" ] && [ "$CODEC" = hevc ]; then
  pass "$OUT is next to its clip and probes as hevc"
else
  fail "$OUT does not exist next to ${NAME:-its clip} or is not hevc (got: ${CODEC:-missing})"
fi
if ct 'sha256sum -c /root/originals.sha256' >/dev/null 2>&1; then
  pass "the originals' sha256 is unchanged"
else
  fail "an original's sha256 changed"
fi
KINDS=$(curl -sS -b "$JAR" -c "$JAR" "$BASE/api/v2/jobs/$JOB/events" | python3 -c '
import json, sys
try:
    evs = json.load(sys.stdin)
except Exception:
    sys.exit(0)
print(" ".join(e.get("kind", "") for e in evs))
')
case "$KINDS" in
  *run_start*run_end*) pass "the Job log has run_start and run_end" ;;
  *) fail "the Job log has no run_start and run_end (kinds: ${KINDS:-none})" ;;
esac

# --- a re-run keeps the account and the library ------------------------------------------------------
step "Running the one-liner again"
run_installer
if [ "$INSTALL_RC" -eq 0 ] && ct 'test -x /usr/local/bin/syp && test -d /var/lib/syp'; then
  pass "the re-run exited 0 and kept the data folder"
else
  fail "the re-run exited $INSTALL_RC (its output above is the record)"
fi
signin_code=$(curl -sS -o "$WORK/signin.json" -w '%{http_code}' -c "$JAR" -b "$JAR" \
  -H 'X-SYP: 1' -H 'Content-Type: application/json' -d "{\"password\":\"$PASSWORD\"}" "$BASE/api/v2/signin")
if [ "$signin_code" = 204 ] || [ "$signin_code" = 200 ]; then
  pass "the account still signs in after the re-run"
else
  fail "signing in after the re-run answered $signin_code"
fi
if curl -sS -b "$JAR" -c "$JAR" "$BASE/api/v2/libraries" | grep -q "$LIBRARY"; then
  pass "the library is still there after the re-run"
else
  fail "the library is gone after the re-run"
fi
