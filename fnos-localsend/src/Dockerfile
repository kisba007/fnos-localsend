# syntax=docker/dockerfile:1
# Multi-arch: buildx (CI) sets BUILDPLATFORM/TARGETPLATFORM/TARGETARCH.
# Legacy `docker build` works too — all three default to the host.
ARG BUILDPLATFORM=linux/amd64
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/localsend-nas ./cmd/localsend-nas

FROM alpine:3.21
RUN apk add --no-cache shadow su-exec \
 && adduser -D -H -h /data localsend \
 && mkdir -p /data && chown localsend:localsend /data
COPY --from=build /out/localsend-nas /usr/local/bin/localsend-nas
COPY entrypoint.sh /entrypoint.sh
# Container starts as root; the entrypoint renumbers localsend to
# $PUID/$PGID and drops privileges via su-exec (linuxserver convention).
# Web UI on :8080 (unprivileged); LocalSend protocol on 53317.
ENV LOCALSEND_NAS_LISTEN=:8080 \
    LOCALSEND_NAS_DATA_DIR=/data \
    PUID=1000 \
    PGID=1000
EXPOSE 8080 53317 53317/udp
VOLUME /data
ENTRYPOINT ["/entrypoint.sh", "localsend-nas"]
