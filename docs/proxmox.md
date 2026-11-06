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

### Option 1: pull the published image

> **Not usable yet.** No image has been published. This works only after a version tag
> (`v0.1.0`, ...) has been pushed and CI has published it, **and** the package has been made
> public (see below). Until then use Option 2.

```
docker pull ghcr.io/peiceoftoast/shrink-your-shit:latest        # or a version, e.g. :0.1.0
docker run -d --name syp -p 8484:8484 -v syp-config:/config -v /path/to/COPY-of-videos:/media \
  ghcr.io/peiceoftoast/shrink-your-shit:latest
```

or, with the example `docker-compose.yml`: comment out `build: .`, enable the `image:` line
shown in the file, then `docker compose pull && docker compose up -d`.

The images are built for `linux/amd64` and `linux/arm64`, by the workflow in
`.github/workflows/release-image.yml`, only when a tag starting with `v` is pushed (it can
also be started by hand from the Actions tab: on a branch it only builds and smoke-tests,
on a tag it publishes).

**For the repository owner, one manual step:** the first publish creates the package
**private**. CI cannot change that. In GitHub go to the repository's *Packages* > the
`shrink-your-shit` package > *Package settings* > *Change visibility* > **Public**, or nobody
else can `docker pull` it without logging in.

If the first tag push fails, look first at the image name: GHCR only accepts lowercase, and
the repository name is mixed-case (`PeiceOfToast/...`). The workflow relies on
`docker/metadata-action` lowercasing it (its documented behaviour, never run here). Next,
check that the workflow had `packages: write` permission and the repository allows Actions
to publish packages. Tags like `v0.1.0-rc1` publish a version tag but not `latest`.

### Option 2: build the image from this repository

Build it yourself (works today):

```
docker build -t shrink-your-shit .
docker run -d --name syp -p 8484:8484 \
  -v syp-config:/config \
  -v /path/to/COPY-of-videos:/media \
  shrink-your-shit
```

Or copy `docker-compose.yml`, edit the media path, run `docker compose up -d` (it builds the image from this folder by default).
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

**Docker inside an LXC.** If you run Docker *inside* a Proxmox container (rather than on a
VM or the host), enable *Nesting* and *keyctl* first: in the Proxmox web page under the container's
Options > Features, or on the host: `pct set 101 -features nesting=1,keyctl=1` (then restart it).

**Proxmox VE with OCI images (tech preview in recent versions).** Proxmox can create an LXC
from an OCI image pulled from a registry. Once the image is published (Option 1) Proxmox can pull it from `ghcr.io`; until then you
would have to push an image you built to a registry you control first. We have not
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
   After editing: `systemctl restart shrink-your-shit`. To change the service itself, use
   `systemctl edit shrink-your-shit` (a drop-in); `install.sh` overwrites the unit file each run. Logs: `journalctl -u shrink-your-shit -f`.
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

Checked: the Docker image builds; in it the program runs as uid 1000, ffmpeg 6.1 (with
libx265) and ffprobe are present, the web page answers and the healthcheck goes healthy,
and real files (an mkv with subtitles and an mp4) were converted to HEVC from a mounted
folder, with the database in a named volume. `docker stop` ends it cleanly in under a
second. A host `/config` folder that Docker creates itself (root-owned) does fail with
"unable to open database file", as described above. `docker compose config` accepts the
compose file. `install.sh` passes `shellcheck`; the systemd unit passes
`systemd-analyze verify`.

The Dockerfile cross-compiles the Go program for the target platform; a `linux/arm64` build of
that stage produced an aarch64 binary. **Not checked:** the arm64 image actually running (no
emulation was available when this was written), the release workflow
(`release-image.yml`; it has been through `actionlint` only, and has never run on GitHub),
and pulling the image from `ghcr.io`.

**Not checked** (no Proxmox host or Debian container was available): the install script
actually running, the Proxmox mount and permission steps, running Docker inside an LXC,
and Proxmox's OCI import.
