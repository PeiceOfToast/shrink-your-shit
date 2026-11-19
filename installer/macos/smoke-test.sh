#!/bin/bash
# The macOS installer test, run by the "macos installer" job in .github/workflows/ci.yml (like
# installer/windows/smoke-test.ps1 for Windows). It runs the repo's install.sh against a release-shaped
# folder (SYP_BASE_URL=file://...), then: checks the launchd agent and the page, converts one clip,
# upgrades in place, and uninstalls. Runs on macos-latest (bash 3.2: no bash 4 features).
#
# Usage: smoke-test.sh <release-dir> <repo-root> <diag-dir>
# The token is never printed: it is masked with ::add-mask:: and install output is redacted before it is shown.
set -euo pipefail

rel=${1:?usage: smoke-test.sh <release-dir> <repo-root> <diag-dir>}
root=${2:?usage: smoke-test.sh <release-dir> <repo-root> <diag-dir>}
diag=${3:?usage: smoke-test.sh <release-dir> <repo-root> <diag-dir>}
work=$(mktemp -d)
lib="$work/library"
bin="$HOME/.local/bin/shrink-your-shit"
plist="$HOME/Library/LaunchAgents/dev.shrink-your-shit.plist"
data="$HOME/Library/Application Support/shrink-your-shit"
applog="$HOME/Library/Logs/shrink-your-shit.log"
label="gui/$(id -u)/dev.shrink-your-shit"
base=http://127.0.0.1:8484
TOKEN=""
mkdir -p "$diag"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}
step() {
  echo
  echo "== $*"
}

# Reads the token, masks it, and keeps it in TOKEN. The token command prints it twice; take one copy.
load_token() {
  TOKEN=$("$bin" token 2>/dev/null | grep -Eo '[A-Za-z0-9_-]{20,}' | tail -1 || true)
  [ "${#TOKEN}" -ge 20 ] || fail "shrink-your-shit token printed no token"
  echo "::add-mask::$TOKEN"
  echo "token length ${#TOKEN}"
}

# api PATH [curl args...]: an authenticated call to the loopback API; prints the body.
api() {
  local path=$1
  shift
  curl -fsS -H "Authorization: Bearer $TOKEN" "$@" "$base$path"
}

# Runs install.sh with the given flags. Its output holds the sign-in link, so the saved copy is redacted
# before it is shown.
run_install() {
  local name=$1
  shift
  local out="$work/install-$name.log"
  local rc=0
  SYP_BASE_URL="file://$rel" sh "$root/install.sh" "$@" >"$out" 2>&1 || rc=$?
  sed -E 's/token=[A-Za-z0-9_-]+/token=REDACTED/g' "$out"
  cp "$out" "$diag/install-$name.log"
  sed -E -i '' 's/token=[A-Za-z0-9_-]+/token=REDACTED/g' "$diag/install-$name.log"
  echo "install ($name) exit $rc"
  [ "$rc" -eq 0 ] || fail "install.sh ($name) exited $rc"
}

# Prints the job count from /api/v2/jobs ({"items": [...], "total": N}).
job_count() {
  api /api/v2/jobs | python3 -c '
import json, sys
print(len(json.load(sys.stdin)["items"]))
'
}

# Prints the state of the job for movie.mp4 ("completed", "failed", "queued", ...), or "none".
# A failed job's reason goes to stderr.
movie_status() {
  api /api/v2/jobs | python3 -c '
import json, sys
items = json.load(sys.stdin)["items"]
hit = [j for j in items if str(j.get("path", "")).endswith("movie.mp4")]
if not hit:
    print("none")
else:
    j = hit[0]
    print(j.get("state", "?"))
    if j.get("state") == "failed":
        print("ERROR: " + json.dumps(j.get("reason")), file=sys.stderr)
'
}

step "1. install with the README's line (the repo's install.sh; the release comes from the local folder)"
mkdir -p "$lib"
run_install first --library "$lib"

step "2. the launchd agent and the page"
if ! launchctl print "$label" >"$diag/launchctl.txt" 2>&1; then
  fail "launchd agent $label is not loaded (see the launchctl output in the diagnostics)"
fi
grep -E 'state = ' "$diag/launchctl.txt" || true
grep -q 'state = running' "$diag/launchctl.txt" || fail "launchd agent is not running"
code=$(curl -s -o /dev/null -w '%{http_code}' "$base/" || true)
echo "page http $code"
[ "$code" = 200 ] || fail "page did not answer 200 (got $code)"
load_token
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$base/api/v2/jobs" || true)
echo "api /api/v2/jobs with token http $code"
[ "$code" = 200 ] || fail "API did not answer 200 with the token (got $code)"

step "3. convert one non-HEVC clip (ffmpeg came from Homebrew in step 1)"
command -v ffmpeg >/dev/null || fail "ffmpeg not on PATH after install"
ffmpeg -v error -f lavfi -i 'testsrc2=duration=12:size=1280x720:rate=30' \
  -f lavfi -i 'sine=duration=12' -c:v libx264 -b:v 8M -c:a aac "$lib/movie.mp4" \
  || fail "could not make the test clip"
before=$(stat -f %z "$lib/movie.mp4")
echo "clip made: $before bytes"
# The clip is made after install because it needs ffmpeg, which install.sh installs; the library folder
# must exist before install.sh runs. A fresh install's library waits for Start, so start it through the API.
libs=$(api /api/libraries)
lib_id=$(printf '%s' "$libs" | python3 -c 'import json,sys; print(json.load(sys.stdin)["items"][0]["id"])')
started=$(printf '%s' "$libs" | python3 -c 'import json,sys; print(json.load(sys.stdin)["items"][0].get("started"))')
echo "library started at install: $started"
[ "$started" = "False" ] || fail "a fresh install's library should wait for Start, but it is started"
api "/api/libraries/$lib_id/scan" -X POST -H 'Content-Type: application/json' -d '{}' >/dev/null
wstate=$(api /api/workers | python3 -c 'import json,sys; print(json.load(sys.stdin)["workers"][0]["state"])')
echo "worker state at install: $wstate"
[ "$wstate" = stopped ] || fail "a fresh install's worker should be stopped, but it is $wstate"
api /api/workers/this/start -X POST -H 'Content-Type: application/json' -d '{}' >/dev/null
ok=0
for _ in $(seq 1 60); do
  st=$(movie_status 2>"$work/movie-err.txt" || true)
  if [ "$st" = completed ]; then ok=1; break; fi
  if [ "$st" = failed ]; then cat "$work/movie-err.txt" >&2; fail "conversion job failed"; fi
  sleep 10
done
[ "$ok" = 1 ] || fail "conversion did not finish in 10 minutes (last status: ${st:-none})"
[ -f "$lib/movie.mkv" ] || fail "no movie.mkv after conversion: $(ls -A "$lib" | tr '\n' ' ')"
[ ! -f "$lib/movie.mp4" ] || fail "movie.mp4 still there after it became movie.mkv"
codec=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 "$lib/movie.mkv")
after=$(stat -f %z "$lib/movie.mkv")
echo "codec=$codec size $before -> $after"
[ "$codec" = hevc ] || fail "expected hevc, got $codec"
[ "$after" -lt "$before" ] || fail "converted file is not smaller"
ls "$lib/.syp-trash"/movie*.mp4 >/dev/null 2>&1 || fail "the original movie.mp4 is not kept in .syp-trash"
jobs_before=$(job_count)
libs_before=$(printf '%s' "$(api /api/libraries)" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["items"]))')
echo "jobs before upgrade: $jobs_before, libraries: $libs_before"

step "4. upgrade in place (run install.sh again, no flags): service back, data kept"
run_install upgrade
code=$(curl -s -o /dev/null -w '%{http_code}' "$base/" || true)
[ "$code" = 200 ] || fail "page did not answer 200 after upgrade (got $code)"
launchctl print "$label" >"$diag/launchctl-after-upgrade.txt" 2>&1 || fail "agent not loaded after upgrade"
grep -q 'state = running' "$diag/launchctl-after-upgrade.txt" || fail "agent not running after upgrade"
load_token
jobs_after=$(job_count)
libs_after=$(printf '%s' "$(api /api/libraries)" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["items"]))')
echo "jobs after upgrade: $jobs_after, libraries: $libs_after"
[ "$jobs_after" = "$jobs_before" ] || fail "job count changed on upgrade ($jobs_before -> $jobs_after)"
[ "$libs_after" = "$libs_before" ] || fail "library count changed on upgrade ($libs_before -> $libs_after)"

step "5. uninstall: the files install.sh creates (the README has no macOS uninstall section yet)"
launchctl bootout "$label" || fail "launchctl bootout failed"
if launchctl print "$label" >/dev/null 2>&1; then fail "agent still loaded after bootout"; fi
rm -f "$plist" "$bin" "$applog"
rm -rf "$data"
for p in "$plist" "$bin" "$applog" "$data"; do
  [ ! -e "$p" ] || fail "still there after uninstall: $p"
done
code=$(curl -s -o /dev/null -w '%{http_code}' "$base/" || true)
[ "$code" = 000 ] || fail "page still answers after uninstall (http $code)"
# -x matches the process name only: -f would match this script, whose arguments contain the repo path.
if pgrep -x shrink-your-shit >/dev/null 2>&1; then
  pgrep -lx shrink-your-shit >&2 || true
  fail "a shrink-your-shit process is still running after uninstall"
fi
echo "uninstall: agent unloaded, files gone, page closed, no process"

echo
echo "macOS installer test: PASS"
