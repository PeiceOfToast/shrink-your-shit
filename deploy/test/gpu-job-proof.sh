#!/bin/sh
# S3-3's real proof: one whole job through the worker converts a file on a card, and a forced
# failure of the card rung shows the L-HW-7 line and a processor result.
#
#   deploy/test/gpu-job-proof.sh <vmid> <debian13|ubuntu24> <intel|nvidia|amd> <hevc_qsv|hevc_nvenc|hevc_vaapi>
#
# vmid 500-519 is Host05 (intel or nvidia passed through) and 202-219 is Host04 (amd); 200 and 201 are
# kept by the team and refused. The installer under test is served from SYP_ARTIFACTS (syp-linux-amd64,
# install.sh and checksums.txt), so the local build is what is proven, not a published release.
#
# FALLBACK=1 also breaks the card rung for a second job: an ffmpeg wrapper fails an encode of a real file
# on the card's encoder while the detector's synthetic (lavfi) probes still pass, so the picker offers the
# card and the ladder has to step down. The job log must carry L-HW-7's sentence and the file must be the
# processor's.
#
# The checks: the job log's card line (L-HW-9), the command the worker ran, the card's encoder tag on the
# file the job placed, the same decode the result check runs (every picture back, no decode errors), and
# the original unchanged. The container is deleted at the end unless KEEP=1; the log goes to
# slices/15-rebuild/gpu-job-<gpu>-<encoder>-<vmid>-<UTC stamp>.log (SYP_SLICES overrides).
#
# Needs: ssh, curl, tar, python3, the local pve-api5/pve-api wrappers, and the restricted
# host05-gpu/host04-gpu keys.
set -eu

VMID="${1:-}"
TEMPLATE="${2:-}"
GPU="${3:-}"
ENCODER="${4:-}"
ART="${SYP_ARTIFACTS:-}"
KEEP="${KEEP:-0}"
FALLBACK="${FALLBACK:-0}"
KEY="${SYP_SSH_KEY:-$HOME/.ssh/openrig_proxmox_test}"
SLICES="${SYP_SLICES:-/home/mb/.openrig/workspace/missions/shrink-your-shit/slices/15-rebuild}"

if [ -z "$VMID" ] || [ -z "$TEMPLATE" ] || [ -z "$GPU" ] || [ -z "$ENCODER" ]; then
  echo "usage: $0 <vmid> <debian13|ubuntu24> <intel|nvidia|amd> <hevc_qsv|hevc_nvenc|hevc_vaapi>" >&2
  exit 2
fi
case "$VMID" in *[!0-9]*) echo "gpu-job: vmid must be a number, got '$VMID'" >&2; exit 2 ;; esac

# Which host, template and address the proofs use. Anything else is a mistake worth stopping for.
case "$VMID" in
  50[0-9]|51[0-9]) NODE=Host05
    case "$TEMPLATE" in
      debian13) VOLID=local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst ;;
      ubuntu24) VOLID=local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst ;;
      *) echo "gpu-job: unknown template '$TEMPLATE' for Host05; use debian13 or ubuntu24" >&2; exit 2 ;;
    esac
    STORAGE=local-lvm
    IP="10.10.1.$((200 + VMID - 500))" ;;
  20[2-9]|21[0-9]) NODE=Host04
    case "$TEMPLATE" in
      debian13) VOLID=nvme512gb:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst ;;
      *) echo "gpu-job: unknown template '$TEMPLATE' for Host04; only debian13 is there" >&2; exit 2 ;;
    esac
    STORAGE=local-lvm
    IP="10.10.1.$((230 + VMID - 200))" ;;
  200|201) echo "gpu-job: refusing vmid $VMID; 200 and 201 are kept containers" >&2; exit 2 ;;
  *) echo "gpu-job: refusing vmid $VMID; workshop's range is 500-519 on Host05 and 202-219 on Host04" >&2; exit 2 ;;
esac
case "$NODE" in
  Host05) API=pve-api5 ;;
  Host04) API=pve-api ;;
esac
case "$NODE:$GPU:$ENCODER" in
  Host05:intel:hevc_qsv|Host05:nvidia:hevc_nvenc|Host04:amd:hevc_vaapi) ;;
  *) echo "gpu-job: $NODE with '$GPU' and '$ENCODER' is not a proof this slice makes" >&2; exit 2 ;;
esac
case "$GPU" in
  intel) FRIENDLY="Intel Quick Sync" ;;
  nvidia) FRIENDLY="NVIDIA" ;;
  amd) FRIENDLY="graphics card" ;;
esac
[ -n "$ART" ] || { echo "gpu-job: set SYP_ARTIFACTS to the folder with syp-linux-amd64, install.sh and checksums.txt" >&2; exit 2; }
for f in syp-linux-amd64 install.sh checksums.txt; do
  [ -f "$ART/$f" ] || { echo "gpu-job: $ART is missing $f" >&2; exit 2; }
done

STAMP=$(date -u +%Y%m%dT%H%M%SZ)
SAFE=$(printf '%s-%s' "$GPU" "$ENCODER" | tr -c 'A-Za-z0-9._-' '-')
LOG_TMP=$(mktemp /tmp/syp-gpu-job.XXXXXX)
LOG="$SLICES/gpu-job-$SAFE-$VMID-$STAMP.log"
WORK=$(mktemp -d /tmp/syp-gpu-job-work.XXXXXX)
FAILURES=0
CREATED=0
LIBRARY=/srv/syp-clips
BASE=http://$IP:8484
JAR=$WORK/cookies.txt
PASSWORD="${SYP_TEST_PASSWORD:-gpu-job-proof-password-1}"
CT_HOSTNAME="syp-test-s3-3-job-$(printf '%s' "$GPU" | tr -c 'A-Za-z0-9-' '-')"

say() { printf '%s\n' "$*" | tee -a "$LOG_TMP"; }
pass() { say "PASS  $*"; }
fail() { say "FAIL  $*"; FAILURES=$((FAILURES + 1)); }
step() { say ""; say "== $*"; }
SSH_OPTS="-i $KEY -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o LogLevel=ERROR"
# SSH_OPTS is a list of options and must split into words; the command is expanded here on purpose and run
# remotely as one string.
# shellcheck disable=SC2086,SC2029
ct() { ssh $SSH_OPTS "root@$IP" "$@"; }
# api METHOD path [curl args...] — the session cookie is the sign-in; every POST carries the CSRF header.
api() {
  method=$1
  path=$2
  shift 2
  curl -sS -b "$JAR" -c "$JAR" -X "$method" -H 'X-SYP: 1' -H 'Content-Type: application/json' "$@" "$BASE$path"
}

finish() {
  rc=$?
  if [ "$CREATED" = 1 ]; then
    if [ "$KEEP" = 1 ]; then
      say "KEEP=1: container $VMID is still there; delete it with: $API DELETE '/nodes/$NODE/lxc/$VMID?purge=1'"
    else
      say "Deleting container $VMID"
      "$API" POST "/nodes/$NODE/lxc/$VMID/status/stop" >/dev/null 2>&1 || true
      i=0
      while [ "$i" -lt 120 ]; do
        "$API" GET "/nodes/$NODE/lxc/$VMID/status/current" 2>/dev/null | grep -q '"status":"running"' || break
        i=$((i + 1))
        sleep 1
      done
      i=0
      while [ "$i" -lt 3 ]; do
        "$API" DELETE "/nodes/$NODE/lxc/$VMID?purge=1" >/dev/null 2>&1 && break
        i=$((i + 1))
        sleep 5
      done
      if [ "$i" -ge 3 ]; then
        fail "container $VMID could not be deleted; delete it with: $API DELETE '/nodes/$NODE/lxc/$VMID?purge=1'"
      fi
    fi
  fi
  if [ -d "$SLICES" ]; then LOGDEST=$LOG; else LOGDEST=$LOG_TMP; fi
  if [ "$rc" -ne 0 ] && [ "$FAILURES" -eq 0 ]; then FAILURES=1; fi
  say "log: $LOGDEST"
  say "$FAILURES check(s) failed"
  if [ "$LOGDEST" = "$LOG_TMP" ]; then
    echo "note: $SLICES is not a folder; the log is $LOG_TMP" >&2
  else
    sed -E -e 's/([?&]token=)[^"& ]+/\1REDACTED/g' -e 's/(syp_session=)[^; ]+/\1REDACTED/g' "$LOG_TMP" >"$LOGDEST"
  fi
  [ "$FAILURES" -eq 0 ]
}
trap finish EXIT
trap 'exit 130' INT TERM

# job_for FILE: the id of a done job for that file name, or nothing.
job_for() {
  curl -sS -b "$JAR" "$BASE/api/v2/jobs?state=done&limit=50" | python3 -c '
import json, sys
name = sys.argv[1]
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
items = d.get("items") if isinstance(d, dict) else d
for it in items or []:
    if it.get("file") == name and it.get("state") == "done":
        print(it["id"])
        break
' "$1"
}

# wait_job FILE: wait for that file's job to be done, poking a scan every few seconds (a new file is
# skipped as still_changing on the first scan; the next one queues it). Prints the job id.
wait_job() {
  file=$1
  deadline=$(( $(date +%s) + 300 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    id=$(job_for "$file")
    if [ -n "$id" ]; then echo "$id"; return 0; fi
    api POST "/api/v2/libraries/$LIBID/scan" -d '{}' >/dev/null 2>&1 || true
    sleep 8
  done
  echo ""
}

# job_events JOB FILE: download the job's log to $WORK and print its lines.
job_events() {
  job=$1
  file=$2
  api GET "/api/v2/jobs/$job/events" -o "$WORK/$file.events.json" || return 1
  python3 - "$WORK/$file.events.json" <<'PY' | tee -a "$LOG_TMP"
import json, sys
try:
    evs = json.load(open(sys.argv[1]))
except Exception as e:
    print("  (the job log could not be read: %s)" % e)
    raise SystemExit(0)
for e in evs:
    print("  %-9s %-5s %-8s %s" % (e.get("kind") or "", e.get("level") or "", e.get("source") or "", e.get("message") or ""))
PY
}

# check_events FILE MODE: the job log's claims. MODE is card or fallback.
check_events() {
  if python3 - "$WORK/$1.events.json" "$2" "$FRIENDLY" "$ENCODER" >"$WORK/$1.check" 2>&1 <<'PY'
import json, sys
evfile, mode, friendly, enc = sys.argv[1:5]
try:
    evs = json.load(open(evfile))
except Exception as e:
    print("FAIL  the job log could not be read: %s" % e)
    sys.exit(1)
msgs = [(e.get("kind") or "", e.get("message") or "") for e in evs]
text = "\n".join(m for _, m in msgs)
card = "Encoding on %s (%s)" % (friendly, enc)
fallback = "Converted on the processor instead."
bad = 0
def want(ok, line):
    global bad
    if ok:
        print("PASS  %s" % line)
    else:
        print("FAIL  %s" % line)
        bad += 1
if mode == "card":
    want(any(m.startswith(card) for _, m in msgs), "the job log names the card rung: %r" % card)
    want(any(k == "command" and enc in m for k, m in msgs), "the command the worker ran uses %s" % enc)
    want(fallback not in text, "the file never reached the processor")
elif mode == "fallback":
    want(("(%s) failed:" % enc) in text and fallback in text, "the job log has L-HW-7's line for %s" % enc)
    want(any(k == "command" and "libx265" in m for k, m in msgs), "a processor command ran (libx265)")
sys.exit(1 if bad else 0)
PY
  then rc=0; else rc=1; fi
  tee -a "$LOG_TMP" <"$WORK/$1.check"
  return $rc
}

step "The proof"
say "$NODE, $VOLID, ip $IP"
say "passed through: $GPU; encoder: $ENCODER; fallback: $FALLBACK"
say "artifacts: $ART (syp-linux-amd64 sha256 $(sha256sum "$ART/syp-linux-amd64" | cut -d' ' -f1))"

step "Container $VMID"
probe_rc=0
probe=$("$API" GET "/nodes/$NODE/lxc/$VMID" 2>&1) || probe_rc=$?
if [ "$probe_rc" -eq 0 ]; then
  fail "container $VMID already exists on $NODE; refusing to reuse it"
  exit 1
fi
case "$probe" in
  *"does not exist"*) [ "$probe_rc" -eq 22 ] || { fail "the API's not-found answer came with status $probe_rc, not 22"; exit 1; } ;;
  *) fail "could not ask the API whether $VMID exists (its answer: $probe); nothing was touched"; exit 1 ;;
esac
# start=0: the device is attached before the container boots (a running container keeps the device set it
# started with, and NVIDIA's nodes only appear on the next boot).
if ! "$API" POST "/nodes/$NODE/lxc" \
  -d vmid="$VMID" -d ostemplate="$VOLID" -d hostname="$CT_HOSTNAME" \
  -d memory=3072 -d cores=2 -d swap=512 -d rootfs="$STORAGE":8 \
  --data-urlencode "net0=name=eth0,bridge=vmbr0,ip=$IP/20,gw=10.10.0.1" \
  -d unprivileged=1 -d start=0 -d tags=syp-test -d pool=syp-test \
  --data-urlencode "description=S3-3 GPU job proof ($GPU/$ENCODER); deleted by deploy/test/gpu-job-proof.sh" \
  --data-urlencode "ssh-public-keys=$(cat "$KEY.pub")" >>"$LOG_TMP" 2>&1; then
  fail "the create call failed (the API's answer is in $LOG_TMP)"
  exit 1
fi
CREATED=1
printf '\n' >>"$LOG_TMP"
# The create is asynchronous: wait for the config and its syp-test tag before the wrapper reads them.
i=0
while [ "$i" -lt 30 ]; do
  "$API" GET "/nodes/$NODE/lxc/$VMID/config" 2>/dev/null | grep -q '"tags":"[^"]*syp-test' && break
  i=$((i + 1))
  sleep 2
done

step "Passing $GPU in"
if [ "$NODE" = Host05 ]; then
  out=$(ssh -o BatchMode=yes host05-gpu gpu "$VMID" "$GPU" 2>&1) || { fail "the Host05 GPU wrapper refused: $out"; exit 1; }
else
  out=$(ssh -o BatchMode=yes host04-gpu gpu "$VMID" 2>&1) || { fail "the Host04 GPU wrapper refused: $out"; exit 1; }
fi
say "$out" | tail -3
pass "$GPU is attached to $VMID"

step "Starting the container"
running=no
i=0
while [ "$i" -lt 30 ]; do
  if "$API" GET "/nodes/$NODE/lxc/$VMID/status/current" 2>/dev/null | grep -q '"status":"running"'; then
    running=yes
    break
  fi
  i=$((i + 1))
  sleep 2
done
if [ "$running" = no ]; then
  "$API" POST "/nodes/$NODE/lxc/$VMID/status/start" >>"$LOG_TMP" 2>&1 || true
fi
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
# shellcheck disable=SC2016
say "inside: $(ct '. /etc/os-release && echo "$PRETTY_NAME"')"
devices=$(ct 'ls /dev/dri/renderD* /dev/nvidia* 2>/dev/null | tr "\n" " "')
say "devices: $devices"
case "$GPU:$devices" in
  intel:*renderD*) pass "a render node is there for Intel" ;;
  amd:*renderD*) pass "a render node is there for AMD" ;;
  nvidia:*nvidia0*) pass "an NVIDIA device node is there" ;;
  *) fail "the passed-through $GPU device is not there (saw: ${devices:-nothing})" ;;
esac

step "The one-line install from the built artifacts"
tar -C "$ART" -cf - install.sh checksums.txt syp-linux-amd64 \
  | ct 'mkdir -p /srv/syp-test && tar -C /srv/syp-test -xf -' 2>>"$LOG_TMP" \
  || { fail "copying the artifacts in failed"; exit 1; }
# shellcheck disable=SC2086
ssh -f $SSH_OPTS "root@$IP" 'cd /srv/syp-test && exec python3 -m http.server 18080 --bind 127.0.0.1 >/dev/null 2>&1' >/dev/null 2>&1 || true
i=0
while [ "$i" -lt 15 ]; do
  if ct 'wget -qO- http://127.0.0.1:18080/install.sh' 2>/dev/null | head -n 1 | grep -q '^#!/bin/sh'; then break; fi
  i=$((i + 1))
  sleep 1
done
INSTALL_RC=0
# shellcheck disable=SC2016
ct 'SYP_BASE_URL=http://127.0.0.1:18080; export SYP_BASE_URL; wget -qO- "$SYP_BASE_URL/install.sh" | sh' \
  >"$WORK/install.out" 2>&1 || INSTALL_RC=$?
tee -a "$LOG_TMP" <"$WORK/install.out"
if [ "$INSTALL_RC" -eq 0 ] && ct 'test -x /usr/local/bin/syp && test -f /etc/systemd/system/syp.service'; then
  pass "the installer exited 0 and put syp in place"
else
  fail "the installer exited $INSTALL_RC without putting syp in place"
  exit 1
fi
if curl -fsS -o /dev/null "$BASE/api/v2/health"; then
  pass "health answers"
else
  fail "health does not answer"
  exit 1
fi

step "A library with a high-bitrate clip"
ct "install -d -o syp -g syp -m 0750 $LIBRARY" || fail "the library folder could not be made"
# crf 6: a high-bitrate source, so the HEVC result is really smaller and the job converts instead of
# keeping the original.
ct "/opt/syp/ffmpeg/ffmpeg -nostdin -hide_banner -loglevel error -y -f lavfi -i testsrc2=size=1920x1080:rate=30 -t 4 -c:v libx264 -preset veryfast -crf 6 -pix_fmt yuv420p $LIBRARY/clip1.mp4" \
  || fail "generating clip1 failed"
ct "sha256sum $LIBRARY/clip1.mp4 > /root/originals.sha256 && chown -R syp:syp $LIBRARY"
say "clip1: $(ct "du -h $LIBRARY/clip1.mp4" | cut -f1)"

step "Setup, with the shrink-videos starter (graphics card on)"
setup_code=$(api POST /api/v2/setup -o "$WORK/setup.json" -w '%{http_code}' \
  -d "{\"account\":{\"password\":\"$PASSWORD\"},\"library\":{\"path\":\"$LIBRARY\"},\"starter\":{\"id\":\"shrink-videos\"}}")
if [ "$setup_code" = 200 ]; then
  pass "POST /setup answered 200"
else
  fail "POST /setup answered $setup_code: $(head -c 300 "$WORK/setup.json" | tr '\n' ' ')"
  exit 1
fi
LIBID=$(api GET /api/v2/libraries | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(1)
items = d.get("items") if isinstance(d, dict) else d
for lib in items or []:
    if lib.get("path") == "/srv/syp-clips" and lib.get("id"):
        print(lib["id"])
        break
')
if [ -n "$LIBID" ]; then
  pass "the library is saved (id $LIBID)"
else
  fail "the library is not in GET /libraries"
  exit 1
fi

step "Waiting for the hardware pass"
# The System log's line is in the events table; the detector writes it when its pass ends. The global event
# stream drops note lines (they are not in its contract), so the table is read directly.
pass_line=""
i=0
while [ "$i" -lt 40 ]; do
  pass_line=$(ct "python3 -c \"import sqlite3;c=sqlite3.connect('/var/lib/syp/syp.db');r=c.execute('select message from events where code=?',('encoders.tested',)).fetchone();print(r[0] if r else '')\"" 2>/dev/null || true)
  if [ -n "$pass_line" ]; then break; fi
  i=$((i + 1))
  sleep 3
done
if [ -n "$pass_line" ]; then
  pass "the detector's pass finished"
else
  fail "no encoders.tested line within two minutes"
fi
say "the pass: $pass_line"

step "A whole job: clip1 on the card"
JOB=$(wait_job clip1.mp4)
if [ -n "$JOB" ]; then
  pass "job $JOB is done"
else
  fail "no done job for clip1.mp4 within five minutes"
  say "the job list: $(api GET '/api/v2/jobs?limit=20' | head -c 600)"
  exit 1
fi
job_events "$JOB" clip1 || fail "the job log could not be read"
if check_events clip1 card; then
  pass "the job ran on $ENCODER and the log says so"
else
  fail "the job log does not show the card rung"
fi
OUT="$LIBRARY/clip1-shrunk.mkv"
probe=$(ct "/opt/syp/ffmpeg/ffprobe -v error -select_streams v:0 -show_entries stream=codec_name:stream_tags=ENCODER -of default=nw=1 '$OUT'" 2>/dev/null || true)
say "  $probe"
case "$probe" in
  *codec_name=hevc*) pass "the job placed $OUT and it is hevc" ;;
  *) fail "the job's output is not hevc at $OUT (got: ${probe:-missing})" ;;
esac
case "$probe" in
  *"$ENCODER"*) pass "the file's encoder tag names $ENCODER" ;;
  *) fail "the file's encoder tag does not name $ENCODER ($probe)" ;;
esac
if ct 'sha256sum -c /root/originals.sha256' >/dev/null 2>&1; then
  pass "the original's sha256 is unchanged"
else
  fail "the original's sha256 changed"
fi
decode=$(ct "/opt/syp/ffmpeg/ffmpeg -nostdin -loglevel level+verbose -nostats -copyts -i '$OUT' -map 0:v:0 -map 0:a? -f null -" 2>&1 || true)
say "$(printf '%s\n' "$decode" | grep 'frames decoded' | tail -1)"
case "$decode" in
  *"frames decoded; 0 decode errors"*) pass "the result check's decode returns every picture with no decode errors" ;;
  *) fail "the result check's decode reported an error" ;;
esac

if [ "$FALLBACK" = 1 ]; then
  step "Forced fallback: the card rung fails on a real file"
  # The detector's probes are synthetic (lavfi) and still pass, so the picker keeps offering the card; only
  # an encode of a real file on the card's encoder fails, which is what a job does.
  cat >"$WORK/card-fail-ffmpeg" <<'WRAP'
#!/bin/sh
# S3-3's forced-failure wrapper: fail an encode of a real file on a card encoder, pass everything else
# (the detector's lavfi probes, the processor rung) to the real ffmpeg.
enc=""
lav=""
for a in "$@"; do
  case "$a" in
    hevc_qsv|h264_qsv|av1_qsv|hevc_nvenc|h264_nvenc|av1_nvenc|hevc_vaapi|h264_vaapi|av1_vaapi) enc=$a ;;
    lavfi) lav=1 ;;
  esac
done
if [ -n "$enc" ] && [ -z "$lav" ]; then
  echo "[$enc @ 0x55] Error while opening encoder: forced by the S3-3 proof." >&2
  exit 1
fi
exec /opt/syp/ffmpeg/ffmpeg.real "$@"
WRAP
  ct 'test -e /opt/syp/ffmpeg/ffmpeg.real || mv /opt/syp/ffmpeg/ffmpeg /opt/syp/ffmpeg/ffmpeg.real'
  # shellcheck disable=SC2086
  ssh $SSH_OPTS "root@$IP" 'cat > /opt/syp/ffmpeg/ffmpeg && chmod 0755 /opt/syp/ffmpeg/ffmpeg' <"$WORK/card-fail-ffmpeg"
  pass "the card encoder now fails on a real file (probes still pass)"
  ct "/opt/syp/ffmpeg/ffmpeg -nostdin -hide_banner -loglevel error -y -f lavfi -i testsrc2=size=1920x1080:rate=30 -t 4 -c:v libx264 -preset veryfast -crf 6 -pix_fmt yuv420p $LIBRARY/clip2.mp4" \
    || fail "generating clip2 failed"
  ct 'chown -R syp:syp /srv/syp-clips'
  JOB=$(wait_job clip2.mp4)
  if [ -n "$JOB" ]; then
    pass "fallback job $JOB is done"
  else
    fail "no done job for clip2.mp4 within five minutes"
    say "the job list: $(api GET '/api/v2/jobs?limit=20' | head -c 600)"
    exit 1
  fi
  job_events "$JOB" clip2 || fail "the fallback job log could not be read"
  if check_events clip2 fallback; then
    pass "the log has L-HW-7's line and a processor command ran"
  else
    fail "the fallback log is missing L-HW-7's line or the processor command"
  fi
  OUT="$LIBRARY/clip2-shrunk.mkv"
  probe=$(ct "/opt/syp/ffmpeg/ffprobe -v error -select_streams v:0 -show_entries stream=codec_name:stream_tags=ENCODER -of default=nw=1 '$OUT'" 2>/dev/null || true)
  say "  $probe"
  case "$probe" in
    *codec_name=hevc*) pass "the fallback job placed $OUT and it is hevc" ;;
    *) fail "the fallback job's output is not hevc at $OUT (got: ${probe:-missing})" ;;
  esac
  case "$probe" in
    *libx265*) pass "the fallback file is the processor's (libx265)" ;;
    *) fail "the fallback file is not the processor's ($probe)" ;;
  esac
  ct 'mv /opt/syp/ffmpeg/ffmpeg.real /opt/syp/ffmpeg/ffmpeg' || fail "the real ffmpeg could not be put back"
fi

step "What the card says about itself"
case "$GPU" in
  intel|amd) say "$(ct 'command -v vainfo >/dev/null && vainfo --display drm --device /dev/dri/renderD128 2>&1 | head -6' || true)" ;;
  nvidia) say "$(ct 'nvidia-smi -L 2>&1 | head -2' || true)" ;;
esac
