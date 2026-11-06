# Running shrink-your-shit on Proxmox (or any Docker/Linux box)

> **Work in progress. Not tested on a real Proxmox host yet.** Everything below was written
> from the code and from Proxmox's documented behaviour. If a step doesn't work for you,
> please tell us exactly where.

## Read this first

- **It REPLACES your original video files** (the default `-output replace`). Start with a
  **copy** of some media, check the results, and only then point it at the real thing.
  `-output beside` keeps the original and writes `name.hevc.mkv` next to it.
- **There is no login yet.** Anyone who can open the web page can see your file names and
  retry jobs. Keep it on your home network. Never forward the port to the internet.
- Transcoding is CPU-heavy. Give the container a few cores; it runs at low priority (systemd
  route) but will still use what it's given. GPU/hardware encoding is not supported yet.

## Route A: Docker / Podman / docker-compose (also the Proxmox OCI route)

Build the image from this repository (nothing is published to a registry yet):

```
docker build -t shrink-your-shit .
docker run -d --name syp -p 8484:8484 \
  -v syp-config:/config \
  -v /path/to/COPY-of-videos:/media \
  shrink-your-shit
```

Or copy `docker-compose.yml`, edit the media path, and run `docker compose up -d`.
Then open `http://<host>:8484`.

- `/config` holds the database. It is a Docker *named volume* here so it is writable by the
  program. If you use a host folder instead, create it first and give it to uid 1000
  (`mkdir -p /srv/syp-config && chown 1000:1000 /srv/syp-config`); Docker would otherwise
  create it owned by root and the program can't start. `/media` is the library. For more folders set
  `SYP_LIBRARIES=/media:/more` and mount each one.
- The program runs as user `1000:1000`. That user must be able to **read and write** the media
  folder. If your files belong to someone else, run with `--user <uid>:<gid>` (compose:
  `user:`) or `chown` the folder.
- Extra settings are the normal program flags, e.g.
  `docker run ... shrink-your-shit -min-bitrate 3000 -crf 24` (see `-h`).

**Proxmox VE with OCI images (tech preview in recent versions).** Proxmox can create an LXC
from an OCI image pulled from a registry. Because this project doesn't publish an image
yet, you would have to push the image you built to a registry you control first. We have not
tried this. Route B is simpler for a Proxmox LXC.

## Route B: a plain Debian/Ubuntu LXC with a systemd service

1. In Proxmox, create a Debian 12 (or Ubuntu) container from a normal template. 2 cores,
   1 GB RAM and 4 GB disk are plenty (the videos live on the mounted folder, not the disk).
2. **On your own computer**, build the Linux program (Go 1.26+):
   ```
   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o shrink-your-shit .
   ```
   (Use `GOARCH=arm64` for an ARM host.)
3. Copy the program and the `deploy/` folder into the container, from the Proxmox host
   (replace `101` with your container id):
   ```
   pct push 101 shrink-your-shit /root/shrink-your-shit
   pct push 101 deploy/install.sh /root/install.sh
   pct push 101 deploy/shrink-your-shit.service /root/shrink-your-shit.service
   ```
4. Mount your media into the container (see the next section), then in the container:
   ```
   sh /root/install.sh /root/shrink-your-shit
   ```
   It installs ffmpeg, creates a `syp` user, installs the program to `/usr/local/bin`,
   and starts the service on boot. Running it again later updates the program and keeps
   your settings.
5. Settings are in `/etc/default/shrink-your-shit` (`SYP_LIBRARIES`, `SYP_ADDR`, `SYP_DATA`).
   After editing: `systemctl restart shrink-your-shit`. Logs: `journalctl -u shrink-your-shit -f`.
   The database is in `/var/lib/shrink-your-shit`.

## Mounting your media into the container

On the Proxmox host, add a bind mount (container id `101`, host folder `/tank/videos-copy`):

```
pct set 101 -mp0 /tank/videos-copy,mp=/media
```

(or add the line `mp0: /tank/videos-copy,mp=/media` to `/etc/pve/lxc/101.conf`). Restart the
container.

**Permissions in an unprivileged container.** Unprivileged containers shift user ids: id `N`
inside is id `100000 + N` on the host. The program must be able to write the folder, so on
the host give the folder to the shifted id of the user that runs it:

- Docker image running as uid 1000 inside an LXC: `chown -R 101000:101000 /tank/videos-copy`
- systemd route: find the id with `id syp` inside the container (say `999`), then on the host
  `chown -R 100999:100999 /tank/videos-copy`

Other containers/VMs that also use the folder may need a shared group or a custom id map;
see the Proxmox documentation on "unprivileged container UID mapping". A *privileged*
container avoids the shift but is less isolated.

## What has and hasn't been checked

Checked: the program runs with the container's environment variables, answers on all
addresses, and converts a real file; `install.sh` passes `shellcheck`; the systemd unit
passes `systemd-analyze verify`.

**Not checked** (no Docker and no Proxmox host were available when this was written): building
and running the image, the install script actually running on Debian, the Proxmox mount and
permission steps, and Proxmox's OCI import.
