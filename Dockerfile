# shrink-your-shit container image: static Go binary + ffmpeg, runs as a normal user.
# The Go build always runs natively on the build machine and cross-compiles for the target
# platform (TARGETOS/TARGETARCH are set by buildx), so multi-arch builds need no emulation here.
# The UI (web/, served at /) is built once on the build machine (Node LTS, as web/.nvmrc); its files are the
# same for every platform. Vite writes them to /internal/web/next/dist (outDir ../internal/web/next/dist).
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /internal/web/next/dist ./internal/web/next/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w -X main.version=${VERSION:-dev}" -o /out/shrink-your-shit .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/syp-network-helper ./cmd/syp-network-helper

FROM debian:trixie-slim
# Debian 13 (trixie), not Alpine: Quick Sync on Tiger Lake and newer needs the Intel GPU runtime
# (libmfx-gen), which Alpine does not package. The three Intel packages are amd64 only; arm64 gets ffmpeg alone.
RUN set -eux; \
    pkgs="ffmpeg wget nfs-common cifs-utils"; \
    if [ "$(dpkg --print-architecture)" = "amd64" ]; then \
        sed -i 's/^Components: main$/Components: main non-free/' /etc/apt/sources.list.d/debian.sources; \
        pkgs="$pkgs libvpl2 libmfx-gen1.2 intel-media-va-driver-non-free"; \
    fi; \
    apt-get update; \
    apt-get install -y --no-install-recommends $pkgs; \
    rm -rf /var/lib/apt/lists/*; \
    useradd -u 1000 -m syp; \
    mkdir -p /config /media /var/lib/syp/shares; \
    chown syp /config /media
COPY --from=build /out/shrink-your-shit /usr/local/lib/shrink-your-shit/shrink-your-shit
# docker exec runs as root; this wrapper runs the app as syp so token/doctor/restore see what the app sees
# and never leave root-owned files in /config.
RUN printf '%s\n' '#!/bin/sh' \
    'APP=/usr/local/lib/shrink-your-shit/shrink-your-shit' \
    'if [ "$(id -u)" = 0 ]; then exec setpriv --reuid=1000 --regid=1000 --init-groups "$APP" "$@"; fi' \
    'exec "$APP" "$@"' > /usr/local/bin/shrink-your-shit && chmod 0755 /usr/local/bin/shrink-your-shit
COPY --from=build /out/syp-network-helper /usr/local/bin/syp-network-helper
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 0755 /usr/local/bin/docker-entrypoint.sh

# /config holds the database, /media is the library folder. The web UI must listen on
# all addresses inside the container; publish the port only to machines you trust.
# The container starts as root only so the entrypoint can run the network-share helper; the app
# then runs as syp (uid 1000). Do not set USER here.
# HOME=/config: the app keeps the key that encrypts share passwords in $HOME/.config, which must be
# writable by uid 1000 and survive restarts.
ENV SYP_DATA=/config \
    HOME=/config \
    SYP_LIBRARIES=/media \
    SYP_ADDR=0.0.0.0:8484
VOLUME ["/config"]
EXPOSE 8484
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD wget -q -O /dev/null http://127.0.0.1:8484/healthz || exit 1
ENTRYPOINT ["docker-entrypoint.sh"]
