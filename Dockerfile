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

FROM debian:trixie-slim
# Debian 13 (trixie), not Alpine: Quick Sync on Tiger Lake and newer needs the Intel GPU runtime
# (libmfx-gen), which Alpine does not package. The three Intel packages are amd64 only; arm64 gets ffmpeg alone.
RUN set -eux; \
    pkgs="ffmpeg wget"; \
    if [ "$(dpkg --print-architecture)" = "amd64" ]; then \
        sed -i 's/^Components: main$/Components: main non-free/' /etc/apt/sources.list.d/debian.sources; \
        pkgs="$pkgs libvpl2 libmfx-gen1.2 intel-media-va-driver-non-free"; \
    fi; \
    apt-get update; \
    apt-get install -y --no-install-recommends $pkgs; \
    rm -rf /var/lib/apt/lists/*; \
    useradd -u 1000 -m syp; \
    mkdir -p /config /media; \
    chown syp /config /media
COPY --from=build /out/shrink-your-shit /usr/local/bin/shrink-your-shit

# /config holds the database, /media is the library folder. The web UI must listen on
# all addresses inside the container; publish the port only to machines you trust.
ENV SYP_DATA=/config \
    SYP_LIBRARIES=/media \
    SYP_ADDR=0.0.0.0:8484
USER 1000:1000
VOLUME ["/config"]
EXPOSE 8484
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD wget -q -O /dev/null http://127.0.0.1:8484/healthz || exit 1
ENTRYPOINT ["shrink-your-shit"]
