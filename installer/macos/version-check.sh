#!/bin/sh
# Checks which version the running program reports. Reads GET /api/v2/update (its JSON) on stdin, and exits 1
# unless its "current" field is the expected version. The macOS smoke test uses it to prove an upgrade really
# took: the version has to change from A to B, not just the install script run again.
#
# Usage: printf '%s' "$json" | sh installer/macos/version-check.sh <expected-version>
set -eu

want=${1:?usage: version-check.sh <expected-version>}
got=$(python3 -c '
import json, sys
print(json.load(sys.stdin).get("current", ""))
') || {
  echo "FAIL: could not read the program's version from the API" >&2
  exit 1
}
if [ "$got" != "$want" ]; then
  echo "FAIL: the program reports version '$got', expected '$want'" >&2
  exit 1
fi
echo "version $got"
