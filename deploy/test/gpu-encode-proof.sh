#!/bin/sh
# S3-2's real proof: a card's own encoder writes a file on real hardware, run by the exact arguments the golden
# table holds, on a fresh throwaway container that is deleted afterwards.
#
#   deploy/test/gpu-encode-proof.sh <vmid> <debian13|ubuntu24> <intel|nvidia|amd> <hevc_qsv|hevc_vaapi|hevc_nvenc>
#
# vmid 500-519 is Host05 (intel or nvidia passed through) and 200-219 is Host04 (amd); 200 and 201 are kept by the
# team and refused. The installer under test is served from SYP_ARTIFACTS (syp-linux-amd64, install.sh and
# checksums.txt) over the container's loopback, so the local build is what is proven, not a published release.
#
# For each row the golden table writes, the script runs that argv, then checks the file: ffprobe reads the encoder
# tag the muxer wrote, and the decode the result check uses runs end to end with no decode errors. The installer's
# vendor notes are checked too (L-HW-21 and L-HW-22). The container is deleted at the end unless KEEP=1; the log
# goes to slices/15-rebuild/gpu-proof-<vmid>-<UTC stamp>.log (SYP_SLICES overrides), and the outputs are copied
# back beside it so the result check can run on them.
#
# Needs: ssh, curl, tar, python3, the local pve-api5/pve-api wrappers, and the restricted host05-gpu/host04-gpu keys.
set -eu

VMID="${1:-}"
TEMPLATE="${2:-}"
GPU="${3:-}"
ENCODER="${4:-}"
ART="${SYP_ARTIFACTS:-}"
KEEP="${KEEP:-0}"
KEY="${SYP_SSH_KEY:-$HOME/.ssh/openrig_proxmox_test}"
SLICES="${SYP_SLICES:-/home/mb/.openrig/workspace/missions/shrink-your-shit/slices/15-rebuild}"
REPO="$(cd "$(dirname "$0")/../.." && pwd)"
TABLE="$REPO/internal/encode/testdata/args.tsv"

if [ -z "$VMID" ] || [ -z "$TEMPLATE" ] || [ -z "$GPU" ] || [ -z "$ENCODER" ]; then
  echo "usage: $0 <vmid> <debian13|ubuntu24> <intel|nvidia|amd> <hevc_qsv|hevc_vaapi|hevc_nvenc>" >&2
  exit 2
fi
case "$VMID" in *[!0-9]*) echo "gpu-proof: vmid must be a number, got '$VMID'" >&2; exit 2 ;; esac

# Which host, template and driver the four proofs use. Anything else is a mistake worth stopping for.
case "$VMID" in
  50[0-9]|51[0-9]) NODE=Host05
    case "$TEMPLATE" in
      debian13) VOLID=local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst ;;
      ubuntu24) VOLID=local:vztmpl/ubuntu-24.04-standard_24.04-2_amd64.tar.zst ;;
      *) echo "gpu-proof: unknown template '$TEMPLATE' for Host05; use debian13 or ubuntu24" >&2; exit 2 ;;
    esac
    STORAGE=local-lvm
    IP="10.10.1.$((200 + VMID - 500))" ;;
  20[2-9]|21[0-9]) NODE=Host04
    case "$TEMPLATE" in
      debian13) VOLID=nvme512gb:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst ;;
      *) echo "gpu-proof: unknown template '$TEMPLATE' for Host04; only debian13 is there" >&2; exit 2 ;;
    esac
    STORAGE=local-lvm
    # Host04's bridge is 10.10.1.40/20 and its own containers take DHCP there; .240-.249 is free
    # of both that pool (checked with ping) and Host05's 10.10.1.200-219 container block.
    IP="10.10.1.$((230 + VMID - 200))" ;;
  200|201) echo "gpu-proof: refusing vmid $VMID; 200 and 201 are kept containers" >&2; exit 2 ;;
  *) echo "gpu-proof: refusing vmid $VMID; workshop's range is 500-519 on Host05 and 200-219 on Host04" >&2; exit 2 ;;
esac
case "$NODE" in
  Host05) API=pve-api5 ;;
  Host04) API=pve-api ;;
esac
case "$NODE:$GPU:$ENCODER" in
  Host05:intel:hevc_qsv|Host05:intel:hevc_vaapi|Host05:nvidia:hevc_nvenc|Host04:amd:hevc_vaapi) ;;
  *) echo "gpu-proof: $NODE with '$GPU' and '$ENCODER' is not a proof this slice makes" >&2; exit 2 ;;
esac
[ -n "$ART" ] || { echo "gpu-proof: set SYP_ARTIFACTS to the folder with syp-linux-amd64, install.sh and checksums.txt" >&2; exit 2; }
for f in syp-linux-amd64 install.sh checksums.txt; do
  [ -f "$ART/$f" ] || { echo "gpu-proof: $ART is missing $f" >&2; exit 2; }
done
[ -f "$TABLE" ] || { echo "gpu-proof: the golden table is missing: $TABLE" >&2; exit 2; }

STAMP=$(date -u +%Y%m%dT%H%M%SZ)
SAFE_GPU=$(printf '%s-%s' "$GPU" "$ENCODER" | tr -c 'A-Za-z0-9._-' '-')
LOG_TMP=$(mktemp /tmp/syp-gpu-proof.XXXXXX)
LOG="$SLICES/gpu-proof-$SAFE_GPU-$VMID-$STAMP.log"
OUT_HOST="$SLICES/gpu-proof-$SAFE_GPU-$VMID-$STAMP-out"
WORK=$(mktemp -d /tmp/syp-gpu-proof-work.XXXXXX)
BASE=http://$IP:8484
FAILURES=0
CREATED=0
ct_hostname="syp-test-s3-2-gpu-$(printf '%s' "$GPU" | tr -c 'A-Za-z0-9-' '-')"

say() { printf '%s\n' "$*" | tee -a "$LOG_TMP"; }
pass() { say "PASS  $*"; }
fail() { say "FAIL  $*"; FAILURES=$((FAILURES + 1)); }
step() { say ""; say "== $*"; }
SSH_OPTS="-i $KEY -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o LogLevel=ERROR"
# shellcheck disable=SC2086
ct() { ssh $SSH_OPTS "root@$IP" "$@"; }

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

step "The proof"
say "$NODE, $VOLID, ip $IP"
say "passed through: $GPU; encoder: $ENCODER"
say "artifacts: $ART (syp-linux-amd64 sha256 $(sha256sum "$ART/syp-linux-amd64" | cut -d' ' -f1))"
say "golden table: $TABLE"

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
# start=0: the device is attached before the container boots (a running container keeps the device set it started
# with, and NVIDIA's nodes only appear on the next boot).
if ! "$API" POST "/nodes/$NODE/lxc" \
  -d vmid="$VMID" -d ostemplate="$VOLID" -d hostname="$ct_hostname" \
  -d memory=2048 -d cores=2 -d swap=512 -d rootfs="$STORAGE":8 \
  --data-urlencode "net0=name=eth0,bridge=vmbr0,ip=$IP/20,gw=10.10.0.1" \
  -d unprivileged=1 -d start=0 -d tags=syp-test -d pool=syp-test \
  --data-urlencode "description=S3-2 GPU proof ($GPU/$ENCODER); deleted by deploy/test/gpu-encode-proof.sh" \
  --data-urlencode "ssh-public-keys=$(cat "$KEY.pub")" >>"$LOG_TMP" 2>&1; then
  fail "the create call failed (the API's answer is in $LOG_TMP)"
  exit 1
fi
CREATED=1
printf '\n' >>"$LOG_TMP"
# The create is asynchronous: wait for the config and its syp-test tag before the wrapper reads
# them (the wrapper refuses a container it cannot see tagged, and the tag lands after the status).
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
# The wrapper starts the container itself, so "already running" is the normal answer here, not a failure.
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
say "inside: $(ct '. /etc/os-release && echo "$PRETTY_NAME"')"
say "cpu: $(ct "grep -m1 'model name' /proc/cpuinfo" | cut -d: -f2)"

step "The devices the container can see"
devices=$(ct 'ls /dev/dri/renderD* /dev/nvidia* 2>/dev/null | tr "\n" " "')
say "$devices"
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

# The notes the brief asks the installer to print, per machine.
if [ "$GPU" = intel ] && [ "$TEMPLATE" = debian13 ]; then
  if grep -q 'non-free Intel media driver' "$WORK/install.out"; then
    pass "the installer printed the two steps that enable non-free's Intel media driver"
  else
    fail "the installer did not print the non-free Intel driver steps on Debian 13 with Intel"
  fi
  if grep -q 'older than Tiger Lake' "$WORK/install.out"; then
    pass "the installer said this pre-Tiger Lake chip has no Quick Sync here and VA-API is used"
  else
    fail "the installer did not name the pre-Tiger Lake Quick Sync limit"
  fi
fi
if [ "$GPU" = intel ] && [ "$TEMPLATE" = ubuntu24 ]; then
  # Ubuntu offers the non-free iHD driver and libmfx1 in its own sections; a failed install is a note there, and
  # the QSV encode below is what proves the result either way.
  if grep -qE 'intel-media-va-driver[-a-z]* did not install|libmfx1 did not install' "$WORK/install.out"; then
    fail "an Intel driver package did not install on Ubuntu; QSV cannot be proven here"
  else
    pass "the installer's Intel driver packages did not report a failure"
  fi
fi
if [ "$GPU" = nvidia ]; then
  if grep -q 'NVIDIA card found; driver' "$WORK/install.out"; then
    pass "the installer found the loaded NVIDIA driver and named it"
  else
    fail "the installer did not report the loaded NVIDIA driver"
  fi
fi
if [ "$GPU" = amd ]; then
  if grep -q 'AMD card found' "$WORK/install.out"; then
    pass "the installer found the AMD card and named mesa as its driver"
  else
    fail "the installer did not report the AMD card"
  fi
  if grep -q 'intel-media-va-driver' "$WORK/install.out"; then
    fail "the installer asked an AMD machine for the Intel driver (L-HW-22)"
  else
    pass "no Intel driver is asked for on an AMD-only machine (L-HW-22)"
  fi
fi

# Debian 13 shows the non-free steps but does not take them (the script never edits apt sources): this follows
# them by hand, the way the note tells a person to, so the VA-API proof has its driver.
if [ "$GPU" = intel ] && [ "$TEMPLATE" = debian13 ]; then
  step "Following the installer's non-free steps by hand"
  # The template's Components line is "contrib main", not "main", so non-free is appended to
  # whatever is there; an older sources.list gets it appended to each deb line instead.
  ct 'f=/etc/apt/sources.list.d/debian.sources; if [ -f "$f" ]; then sed -i -E "s/^(Components: .*[^ ])$/\1 non-free/" "$f"; grep "^Components:" "$f"; else sed -i -E "s/^(deb .*[^ ])$/\1 non-free/" /etc/apt/sources.list; grep "^deb" /etc/apt/sources.list; fi' \
    || fail "could not add non-free to the apt sources"
  if ct 'export DEBIAN_FRONTEND=noninteractive; (apt-get update && apt-get install -y intel-media-va-driver-non-free) >/root/apt.log 2>&1'; then
    pass "intel-media-va-driver-non-free is installed from non-free"
  else
    say "$(ct 'tail -15 /root/apt.log')"
    fail "the non-free Intel driver did not install; the VA-API proof cannot run"
    exit 1
  fi
fi

step "A source clip whose codec differs from the target"
SOURCE=/root/source.mkv
# The golden rows map 0:0, 0:1 and 0:2 and copy the audio and subtitle, so the source has all three.
ct "printf '1\\n00:00:00,000 --> 00:00:04,000\\nhello\\n' > /root/sub.srt"
if ct "/opt/syp/ffmpeg/ffmpeg -nostdin -hide_banner -loglevel error -y -f lavfi -i testsrc2=size=1920x1080:rate=30 -f lavfi -i sine=frequency=440 -i /root/sub.srt -t 5 -map 0:v:0 -map 1:a:0 -map 2:s:0 -c:v libx264 -preset veryfast -crf 16 -pix_fmt yuv420p -c:a aac -c:s srt -shortest $SOURCE"; then
  src_codec=$(ct "/opt/syp/ffmpeg/ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 $SOURCE")
  if [ "$src_codec" = h264 ]; then
    pass "the source is h264, so the target HEVC cannot be the source's own"
  else
    fail "the source is not h264 (got: ${src_codec:-nothing})"
  fi
else
  fail "the source clip could not be made"
  exit 1
fi

# run_row NAME: run the golden table's argv for that row against the source, then check the file it wrote.
run_row() {
  row=$1
  argv=$(awk -v n="$row" -F '\t' '$1 == n { print $3 }' "$TABLE")
  if [ -z "$argv" ]; then
    fail "the golden table has no row '$row'"
    return
  fi
  OUT="/root/$row.mkv"
  # The table's in.mkv and out.mkv are the placeholders; the row's own name is the output here.
  remote_argv=$(printf '%s' "$argv" | sed -e "s# in.mkv# $SOURCE#g" -e "s# out.mkv# $OUT#g")
  if ct "export LD_LIBRARY_PATH=/opt/syp/ffmpeg/lib; /opt/syp/ffmpeg/ffmpeg $remote_argv" \
      >"$WORK/$row.out" 2>&1; then
    pass "$row: the encode ran"
  else
    fail "$row: the encode failed (last lines in $LOG_TMP)"
    tee -a "$LOG_TMP" <"$WORK/$row.out" | tail -5
    return
  fi
  probe=$(ct "/opt/syp/ffmpeg/ffprobe -v error -select_streams v:0 -show_entries stream=codec_name:stream_tags=ENCODER -of default=nw=1 $OUT" 2>/dev/null)
  say "  $probe"
  if printf '%s\n' "$probe" | grep -q '^codec_name=hevc$'; then
    pass "$row: ffprobe reads hevc"
  else
    fail "$row: ffprobe does not read hevc ($probe)"
  fi
  if printf '%s\n' "$probe" | grep -qi "$ENCODER"; then
    pass "$row: the encoder tag names $ENCODER"
  else
    fail "$row: the encoder tag does not name $ENCODER ($probe)"
  fi
  # The result check's own decode: every picture back with no decoder error.
  decode=$(ct "/opt/syp/ffmpeg/ffmpeg -nostdin -loglevel level+verbose -nostats -copyts -i $OUT -map 0:v:0 -map 0:a? -f null -" 2>&1)
  say "$(printf '%s\n' "$decode" | grep 'frames decoded' | tail -2)"
  if printf '%s\n' "$decode" | grep -qE 'frames decoded; 0 decode errors'; then
    pass "$row: every picture decodes with no decode errors"
  else
    fail "$row: the decode reported an error"
  fi
  frames=$(printf '%s\n' "$decode" | sed -n 's/.* \([0-9][0-9]*\) frames decoded.*/\1/p' | head -n 1)
  if [ "${frames:-0}" -gt 0 ]; then
    pass "$row: $frames pictures came back"
  else
    fail "$row: the decode came back with no pictures"
  fi
  mkdir -p "$OUT_HOST"
  ct "tar -C /root -cf - $(basename "$OUT")" 2>/dev/null | tar -C "$OUT_HOST" -xf - 2>/dev/null \
    || fail "$row: the output could not be copied back"
}

step "The golden table's arguments, on the card ($ENCODER)"
case "$ENCODER" in
  hevc_qsv)
    run_row qsv-hevc-8-none-mkv
    run_row qsv-full-hevc-8-none-mkv ;;
  hevc_nvenc)
    run_row nvenc-hevc-8-none-mkv ;;
  hevc_vaapi)
    run_row vaapi-hevc-8-none-mkv
    run_row vaapi-full-hevc-8-none-mkv ;;
esac

if [ -d "$OUT_HOST" ]; then
  ct "tar -C /root -cf - source.mkv" 2>/dev/null | tar -C "$OUT_HOST" -xf - 2>/dev/null || true
  say "outputs copied back to $OUT_HOST"
fi

step "What the card says about itself"
case "$GPU" in
  intel) say "$(ct 'command -v vainfo >/dev/null && vainfo --display drm --device /dev/dri/renderD128 2>&1 | head -6' || true)" ;;
  amd) say "$(ct 'command -v vainfo >/dev/null && vainfo --display drm --device /dev/dri/renderD128 2>&1 | head -6' || true)" ;;
  nvidia) say "$(ct 'nvidia-smi -L 2>&1 | head -2' || true)" ;;
esac
