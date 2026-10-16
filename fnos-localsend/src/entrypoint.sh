#!/bin/sh
# localsend-nas entrypoint: PUID/PGID convention (linuxserver.io-style).
# Starts as root, renumbers the built-in localsend user to the requested
# ids, fixes /data ownership, then drops privileges via su-exec.
# If the container was started non-root (compose `user:`), just exec.
set -e

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

if [ "$(id -u)" = "0" ]; then
    if [ "$(id -g localsend)" != "$PGID" ]; then
        groupmod -o -g "$PGID" localsend
    fi
    if [ "$(id -u localsend)" != "$PUID" ]; then
        usermod -o -u "$PUID" localsend
    fi
    # Only the data dir (identity/settings) is re-owned — shares stay
    # mounted read-only with their host ownership.
    chown -R localsend:localsend /data
    exec su-exec localsend "$@"
fi

exec "$@"
