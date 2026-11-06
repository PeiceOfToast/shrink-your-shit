# shrink-your-shit container image: static Go binary + ffmpeg, runs as a normal user.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/shrink-your-shit .

FROM alpine:3.21
RUN apk add --no-cache ffmpeg \
 && adduser -D -u 1000 syp \
 && mkdir -p /config /media \
 && chown syp /config /media
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
  CMD wget -q -O /dev/null http://127.0.0.1:8484/api/status || exit 1
ENTRYPOINT ["shrink-your-shit"]
