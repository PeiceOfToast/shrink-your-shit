#!/bin/sh
# Entrypoint for the shrink-your-shit image. The container starts as root so that, when the
# container is allowed to mount, the network-share helper can run. The app itself always runs as
# syp (uid 1000); it is not root.
set -eu

APP=shrink-your-shit
SOCK=/run/shrink-your-shit/helper.sock

if [ "$(id -u)" != 0 ]; then
    # Someone set user: in compose, so there is nothing to drop from.
    exec "$APP" "$@"
fi

# mount_problem prints why this container cannot mount network shares, or nothing if it can.
mount_problem() {
    # A user namespace (for example Docker inside an unprivileged LXC) makes the kernel refuse
    # NFS and SMB mounts, so the map must be the whole host range.
    if ! grep -q '^ *0 *0 *4294967295 *$' /proc/self/uid_map; then
        echo "user namespace"
        return
    fi
    d=$(mktemp -d)
    if mount -t tmpfs tmpfs "$d" 2>/dev/null; then
        umount "$d"
        rmdir "$d"
    else
        rmdir "$d"
        echo "mount not permitted"
    fi
}

reason=$(mount_problem)
if [ -z "$reason" ]; then
    syp-network-helper -user syp -nfs-nolock &
    i=0
    while [ ! -S "$SOCK" ] && [ "$i" -lt 25 ]; do
        sleep 0.2
        i=$((i + 1))
    done
    if [ -S "$SOCK" ]; then
        echo "network shares: mounting inside the container"
    else
        echo "network shares: manual (the helper did not start)"
    fi
else
    echo "network shares: manual (container can't mount: $reason)"
fi

exec run-as-syp "$APP" "$@" # syp, with the /dev/dri groups (see run-as-syp.sh)
