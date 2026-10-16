#!/bin/bash
# dev-docker.sh — fast local container loop.
#
#   ./dev-docker.sh          # build binary on host, (re)start the container
#   ./dev-docker.sh logs     # follow logs
#   ./dev-docker.sh stop     # stop and remove
#
# The container runs the *host-built* binary bind-mounted over the image's
# one, so a test cycle is just: edit → ./dev-docker.sh → reload page.
# Web UI: http://127.0.0.1:18090  (LocalSend port 53319, host networking).
set -euo pipefail
cd "$(dirname "$0")"

NAME=lnas-dev
DEV=.dev
IMG=localsend-nas:dev

case "${1:-}" in
  logs) exec docker logs -f $NAME ;;
  stop) docker rm -f $NAME >/dev/null 2>&1 && echo stopped; exit 0 ;;
esac

mkdir -p $DEV/data
CGO_ENABLED=0 go build -o $DEV/localsend-nas ./cmd/localsend-nas

if ! docker image inspect $IMG >/dev/null 2>&1; then
  docker build -t $IMG .
fi

# Test fixture share.
mkdir -p /tmp/lnas-share/movies
[ -f /tmp/lnas-share/movies/a.txt ] || echo hello > /tmp/lnas-share/movies/a.txt

docker rm -f $NAME >/dev/null 2>&1 || true
docker run -d --name $NAME --network host \
  -v "$PWD/$DEV/localsend-nas":/usr/local/bin/localsend-nas:ro \
  -v "$PWD/$DEV/data":/data \
  -v /tmp/lnas-share:/srv/movies \
  -e PUID="$(id -u)" \
  -e PGID="$(id -g)" \
  -e LOCALSEND_NAS_SHARES="movies=/srv/movies" \
  -e LOCALSEND_NAS_LISTEN=:18090 \
  -e LOCALSEND_NAS_LS_PORT=53319 \
  $IMG >/dev/null

echo "up: http://127.0.0.1:18090 (share 'movies' rw, PUID/PGID=$(id -u):$(id -g))"
