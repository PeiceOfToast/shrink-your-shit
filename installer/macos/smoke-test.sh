#!/bin/bash
# The macOS installer test, run by the "macos installer" job in .github/workflows/ci.yml (like
# installer/windows/smoke-test.ps1 for Windows). It runs the repo's install.sh against a release-shaped
# folder (SYP_BASE_URL=file://...): release A is installed, then the agent and the page are checked, one clip is
# converted, and an upgrade to release B (a different version) is checked by the version the program reports.
# Then the Update screen is checked (on a Mac it says to run the install line; the button is refused), and the
# install is uninstalled. Runs on macos-latest (bash 3.2: no bash 4 features).
#
# Usage: smoke-test.sh <release-a-dir> <repo-root> <diag-dir> <release-b-dir>
# The versions are the ones ci.yml stamps into the two releases.
# The token is never printed: it is masked with ::add-mask:: and install output is redacted before it is shown.
set -euo pipefail

# It deletes the install's data folder, so it runs only in CI (GitHub Actions sets CI=true), never on a Mac in use.
[ -n "${CI:-}" ] || { echo "refusing to run outside CI: it deletes the shrink-your-shit data folder" >&2; exit 1; }

usage="usage: smoke-test.sh <release-a-dir> <repo-root> <diag-dir> <release-b-dir>"
rel=${1:?$usage}
root=${2:?$usage}
diag=${3:?$usage}
relb=${4:?$usage}
version_a=0.0.0-ci-a
version_b=0.0.0-ci-b
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

# Prints the install-owned paths that exist now: the launchd plist, the binary, the log, the data folder, and any
# other entry named like them in the folders install.sh writes to. Uninstall must leave this list as it was.
owned_paths() {
  local dir
  for dir in "$HOME/.local/bin" "$HOME/Library/LaunchAgents" "$HOME/Library/Logs" "$HOME/Library/Application Support"; do
    [ -d "$dir" ] && find "$dir" -maxdepth 1 \( -name 'shrink-your-shit*' -o -name 'dev.shrink-your-shit*' \)
  done | sort || true
  return 0
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
  local name=$1 release=$2
  shift 2
  local out="$work/install-$name.log"
  local rc=0
  SYP_BASE_URL="file://$release" sh "$root/install.sh" "$@" >"$out" 2>&1 || rc=$?
  # The saved copy is written redacted: no unredacted copy ever sits in the diagnostics folder.
  sed -E 's/token=[A-Za-z0-9_-]+/token=REDACTED/g' "$out" | tee "$diag/install-$name.log"
  echo "install ($name) exit $rc"
  [ "$rc" -eq 0 ] || fail "install.sh ($name) exited $rc"
}

# Prints how many jobs are in the Completed tab (the bare /api/v2/jobs is the Waiting tab only).
completed_count() {
  local body
  body=$(api "/api/v2/jobs?tab=completed&limit=200") || return 1
  printf '%s' "$body" | python3 -c '
import json, sys
print(len(json.load(sys.stdin)["items"]))
'
}

# Prints the state of the job for movie.mp4 ("completed", "failed", "running", "waiting", ...), or "none".
# The bare /api/v2/jobs is the Waiting tab only: a job leaves it as soon as a worker takes it. So every tab is
# asked. A failed job's reason goes to stderr. Exit 2 means the API did not answer (or sent bad JSON): that is not
# "none", and the caller must fail on it.
movie_status() {
  local tab body rc
  for tab in running waiting completed failed; do
    body=$(api "/api/v2/jobs?tab=$tab&limit=200") || return 2
    rc=0
    printf '%s' "$body" | python3 -c '
import json, sys
tab = sys.argv[1]
try:
    items = json.load(sys.stdin)["items"]
except Exception as e:
    print("bad job list for tab " + tab + ": " + str(e), file=sys.stderr)
    sys.exit(3)
hit = [j for j in items if str(j.get("path", "")).endswith("movie.mp4")]
if hit:
    j = hit[0]
    print(j.get("state", tab))
    if j.get("state") == "failed":
        print("ERROR: " + json.dumps(j.get("reason")), file=sys.stderr)
    sys.exit(0)
sys.exit(1)
' "$tab" || rc=$?
    case $rc in
      0) return 0 ;;
      1) ;;
      *) return 2 ;;
    esac
  done
  echo none
}

# Copies what a failure needs into $diag: the app log and the library and job lists. Best effort.
diag_dump() {
  # The app log holds the sign-in link: the copy is redacted as it is written.
  if [ -f "$applog" ]; then
    sed -E 's/token=[A-Za-z0-9_-]+/token=REDACTED/g' "$applog" >"$diag/shrink-your-shit.log" || true
  fi
  if [ -n "$TOKEN" ]; then
    api /api/libraries >"$diag/api-libraries.json" 2>&1 || true
    for tab in running waiting completed failed; do
      api "/api/v2/jobs?tab=$tab&limit=200" >"$diag/api-v2-jobs-$tab.json" 2>&1 || true
    done
  fi
}

step "1. install with the README's line (the repo's install.sh; the release comes from the local folder)"
mkdir -p "$lib"
owned_before=$(owned_paths)
run_install first "$rel" --library "$lib"

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
api /api/v2/update | sh "$root/installer/macos/version-check.sh" "$version_a" || fail "release A is not the version running"

step "3. convert one non-HEVC clip (ffmpeg came from Homebrew in step 1)"
command -v ffmpeg >/dev/null || fail "ffmpeg not on PATH after install"
ffmpeg -v error -f lavfi -i 'testsrc2=duration=12:size=1280x720:rate=30' \
  -f lavfi -i 'sine=duration=12' -c:v libx264 -b:v 8M -c:a aac "$lib/movie.mp4" \
  || fail "could not make the test clip"
# Backdated like installer/windows/smoke-test.ps1: a file written seconds ago is still "being copied" to the scanner.
touch -t "$(date -v-2M +%Y%m%d%H%M.%S)" "$lib/movie.mp4"
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
  rc=0
  st=$(movie_status 2>"$work/movie-err.txt") || rc=$?
  if [ "$rc" -ne 0 ]; then diag_dump; cat "$work/movie-err.txt" >&2; fail "the job list did not answer during the conversion"; fi
  if [ "$st" = completed ]; then ok=1; break; fi
  if [ "$st" = failed ]; then diag_dump; cat "$work/movie-err.txt" >&2; fail "conversion job failed"; fi
  sleep 10
done
if [ "$ok" != 1 ]; then
  diag_dump
  fail "conversion did not finish in 10 minutes (last status: ${st:-none})"
fi
[ -f "$lib/movie.mkv" ] || fail "no movie.mkv after conversion: $(ls -A "$lib" | tr '\n' ' ')"
[ ! -f "$lib/movie.mp4" ] || fail "movie.mp4 still there after it became movie.mkv"
codec=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 "$lib/movie.mkv")
after=$(stat -f %z "$lib/movie.mkv")
echo "codec=$codec size $before -> $after"
[ "$codec" = hevc ] || fail "expected hevc, got $codec"
[ "$after" -lt "$before" ] || fail "converted file is not smaller"
ls "$lib/.syp-trash"/movie*.mp4 >/dev/null 2>&1 || fail "the original movie.mp4 is not kept in .syp-trash"
completed_before=$(completed_count)
libs_before=$(printf '%s' "$(api /api/libraries)" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["items"]))')
echo "completed jobs before upgrade: $completed_before, libraries: $libs_before"

step "4. upgrade A -> B (run install.sh again with release B, no flags): the version changes, service back, data kept"
run_install upgrade "$relb"
code=$(curl -s -o /dev/null -w '%{http_code}' "$base/" || true)
[ "$code" = 200 ] || fail "page did not answer 200 after upgrade (got $code)"
launchctl print "$label" >"$diag/launchctl-after-upgrade.txt" 2>&1 || fail "agent not loaded after upgrade"
grep -q 'state = running' "$diag/launchctl-after-upgrade.txt" || fail "agent not running after upgrade"
load_token
api /api/v2/update | sh "$root/installer/macos/version-check.sh" "$version_b" || fail "upgrade did not change the running version to B"
completed_after=$(completed_count)
libs_after=$(printf '%s' "$(api /api/libraries)" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["items"]))')
echo "completed jobs after upgrade: $completed_after, libraries: $libs_after"
[ "$completed_after" -ge "$completed_before" ] || fail "completed jobs went down on upgrade ($completed_before -> $completed_after)"
[ "$libs_after" = "$libs_before" ] || fail "library count changed on upgrade ($libs_before -> $libs_after)"
rc=0
st=$(movie_status 2>"$work/movie-err.txt") || rc=$?
[ "$rc" -eq 0 ] || fail "the job list did not answer after upgrade"
[ "$st" = completed ] || fail "the converted movie is not completed after upgrade (state: $st)"

step "4b. the Update screen on a Mac: it shows the install line to run, and the install button is refused"
api /api/settings -X PATCH -H 'Content-Type: application/json' -d '{"checkForUpdates":true}' >/dev/null
upd=$(api /api/v2/update)
printf '%s' "$upd" | python3 -c '
import json, sys
u = json.load(sys.stdin)
want = {"os": "darwin", "installType": "script", "method": "commands"}
for k, v in want.items():
    if u.get(k) != v:
        sys.exit("update screen: " + k + " is " + repr(u.get(k)) + ", expected " + repr(v))
if not any("install.sh | sh" in c for c in (u.get("commands") or [])):
    sys.exit("update screen: no install line in commands: " + repr(u.get("commands")))
if (u.get("install") or {}).get("state") != "idle":
    sys.exit("update screen: install state is not idle: " + repr(u.get("install")))
print("update screen: os darwin, method commands, install line shown, install idle")
' || fail "the Update screen does not show the Mac install line"
inst_body="$work/install-refusal.json"
inst_code=$(curl -s -o "$inst_body" -w '%{http_code}' -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -X POST -d '{"when":"now"}' "$base/api/v2/update/install" || true)
echo "POST /api/v2/update/install http $inst_code"
[ "$inst_code" = 409 ] || fail "install on a Mac should be refused with 409 (got $inst_code)"
python3 -c '
import json, sys
code = json.load(open(sys.argv[1]))["error"]["code"]
if code != "install_unsupported":
    sys.exit("refused with " + code + ", expected install_unsupported")
' "$inst_body" || fail "install refusal is not install_unsupported"
echo "install refused: install_unsupported"

step "5. uninstall: remove what install.sh created, then check nothing of it is left"
owned_installed=$(owned_paths)
echo "installed: $(printf '%s' "$owned_installed" | tr '\n' ' ')"
for p in "$plist" "$bin" "$data"; do
  printf '%s\n' "$owned_installed" | grep -qxF "$p" || fail "install did not create $p"
done
# An entry install.sh created that this script does not remove fails here, so the removal below stays complete.
unknown=$(printf '%s\n' "$owned_installed" | grep -vxF -e "$plist" -e "$bin" -e "$data" -e "$applog" | grep -v '^$' || true)
[ -z "$unknown" ] || fail "install created entries uninstall does not remove: $unknown"
launchctl bootout "$label" || fail "launchctl bootout failed"
# bootout returns before launchd has unloaded the agent (CI saw it still loaded right after): wait up to 10 s.
unloaded=
for _ in $(seq 1 20); do
  if ! launchctl print "$label" >/dev/null 2>&1; then unloaded=1; break; fi
  sleep 0.5
done
[ -n "$unloaded" ] || fail "agent still loaded after bootout"
rm -f "$plist" "$bin" "$applog"
rm -rf "$data"
owned_after=$(owned_paths)
[ "$owned_after" = "$owned_before" ] || fail "uninstall left shrink-your-shit entries: $(printf '%s' "$owned_after" | tr '\n' ' ')"
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
