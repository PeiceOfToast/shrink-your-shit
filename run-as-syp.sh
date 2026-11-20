#!/bin/sh
# run-as-syp runs a command as syp (uid 1000) with the groups it needs for the graphics card: the group
# of each /dev/dri device, any group Docker added (group_add) and syp's own. setpriv --init-groups keeps
# only syp's own groups, so Quick Sync and VA-API could not open /dev/dri. With this, passing /dev/dri
# (devices: in compose) is enough; there is no group id to look up. SYP_DRI is for tests only.
set -eu
dri=${SYP_DRI:-/dev/dri}
gids=$(
    {
        id -G 1000
        id -G
        for d in "$dri"/renderD* "$dri"/card*; do
            if [ -e "$d" ]; then stat -c %g "$d"; fi
        done
    } | tr ' ' '\n' | grep -vx -e '' -e 0 -e 65534 | sort -un | paste -sd, -
)
# The Intel GPU's busy counters need CAP_PERFMON (see internal/web/gpu_perf_linux.go). Changing user drops every
# capability, so PERFMON is handed on as an ambient one, and only when the container was given it
# (cap_add: PERFMON puts it in the bounding set). Otherwise the app runs with no extra capability, as before.
perf=
bnd=$(awk '/^CapBnd:/ { print $2 }' /proc/self/status)
if [ -n "$bnd" ] && [ $(( 0x$bnd >> 38 & 1 )) = 1 ]; then
    perf="--inh-caps +perfmon --ambient-caps +perfmon"
fi
# shellcheck disable=SC2086 # $perf is two options or nothing
exec setpriv --reuid=1000 --regid=1000 --groups "$gids" $perf "$@"
