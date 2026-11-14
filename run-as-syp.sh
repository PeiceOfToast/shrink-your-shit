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
exec setpriv --reuid=1000 --regid=1000 --groups "$gids" "$@"
