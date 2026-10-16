<p align="center"><img src="web/logo.png" width="200" alt="localsend-nas logo"></p>

# localsend-nas

A [LocalSend](https://localsend.org) node with a web UI, built for
NAS-style deployments: browse host-mounted share directories in a browser,
assemble a basket of files, pick a discovered device, and the server sends the
files directly to the recipient's LocalSend app — the files never
round-trip through your laptop. Receiving works too: a desktop-app-style
dialog lets you accept inbound files straight into a share.

Speaks LocalSend Protocol v2.2 over HTTPS (port 53317 by default) and
announces itself via multicast, so it shows up in other devices' lists.

## Receiving

On by default. When a device sends files to the NAS, every open UI tab pops a
dialog with the sender, file list, a destination picker (any configured
share), and a countdown (default 30 s):

- **Accept / Decline** — your call, per transfer.
- **Countdown expires** — with a *dropbox* configured, files are auto-accepted
  into that share; otherwise the request is politely rejected.
- **`--read-only`** (env `LOCALSEND_NAS_READ_ONLY=1`) — restores the strict
  v1 send-only posture: no receiving at all (senders get a clean 403).

Countdown length and the dropbox share live in the settings menu (gear icon,
top right) and persist server-side in `<data-dir>/settings.json`. The menu
also has a toggle for showing NAS metadata files (`@eaDir`, `.DS_Store`,
`#recycle`, …) — hidden by default.

Received files keep their folder structure, never overwrite existing files
(`name (1).ext` dedupe), and land in shares as normal files — browsable and
re-sendable immediately.

Single static Go binary, no runtime dependencies, embedded web UI.

## Running

Binary (see [releases](https://github.com/alexindigo/localsend-nas/releases)):

```bash
localsend-nas --listen :80 --share movies=/srv/movies --share books=/srv/books
```

Docker (`alexindigo/localsend-nas` on Docker Hub — also on
[GHCR](https://github.com/alexindigo/localsend-nas/pkgs/container/localsend-nas);
Docker Hub is the one Synology Container Manager can browse):

```bash
docker run -d --name localsend-nas --network host \
  -v /volume1/movies:/srv/movies:ro \
  -v /volume1/books:/srv/books:ro \
  -v localsend-nas-data:/data \
  -e PUID=1026 -e PGID=100 \
  -e LOCALSEND_NAS_SHARES="movies=/srv/movies,books=/srv/books" \
  alexindigo/localsend-nas:latest
```

(Volume syntax is `-v host-path:container-path`; shares stay read-only in
the container. On PUID/PGID see the permissions note below.)

`--network host` is recommended: LocalSend discovery rides multicast UDP
53317, which NAT/bridge networking breaks. All flags have
`LOCALSEND_NAS_*` env equivalents (`--listen` → `LOCALSEND_NAS_LISTEN`,
repeatable `--share` → comma-separated `LOCALSEND_NAS_SHARES`, …).

docker-compose equivalent:

```yaml
services:
  localsend-nas:
    # use `alexindigo/localsend-nas` for the Docker Hub based image
    image: ghcr.io/alexindigo/localsend-nas:latest
    container_name: localsend-nas
    network_mode: host          # required for multicast discovery
    restart: unless-stopped
    environment:
      # Your host user's uid/gid (`id` over SSH; Synology default user is
      # typically 1026:100). DSM's permission-manager abstraction doesn't
      # exist inside containers, but the underlying POSIX ACLs are enforced
      # by the kernel — the process's uid/gid must match the grant.
      PUID: "1026"
      PGID: "100"
      LOCALSEND_NAS_SHARES: movies=/srv/movies,books=/srv/books
      # LOCALSEND_NAS_LISTEN: ":8080"   # image default; :80 needs root
      # LOCALSEND_NAS_ALIAS: "Nas living-room"
    volumes:
      # host path : container path  (shares stay read-only inside the container)
      - /volume1/movies:/srv/movies:ro
      - /volume1/books:/srv/books:ro
      - localsend-nas-data:/data      # named volume for the persistent device identity

volumes:
  localsend-nas-data:
```

With `network_mode: host` the web UI lands on the host's port 8080
directly; change `LOCALSEND_NAS_LISTEN` if that clashes.

**Permissions:** the image follows the linuxserver.io `PUID`/`PGID`
convention — the container starts as root, renumbers the built-in
`localsend` user, then drops privileges. Defaults are `1000`/`1000`; set
them to the uid/gid that owns your shares (`id` on the host). The app never
writes to `:ro` mounts; received files land owned by that uid/gid.

## Operability (for scripts and monitoring)

- `GET /api/health` — liveness + identity: `{"version","protocol","alias","fingerprint","lsPort"}`.
  Always 200 when the process serves HTTP.
- `GET /api/selftest` — subsystem diagnostics: share roots re-stat'd,
  LocalSend port self-dial, multicast listener state, known-device count.
  `200 {"ok":true,…}` when healthy, `503` with per-check error strings
  otherwise — so `curl -sf …/api/selftest` works as an install gate.

## Status

Early development. See the commit history and the plan document for scope.

Logo: `web/logo.svg` (source) / `web/logo.png` (rendered) — the LocalSend
"device + broadcast ring" motif, squared: a NAS box at the center of the
local network. Style derived from the LocalSend logo (© Tien Do Nam,
[Apache-2.0](https://github.com/localsend/localsend)) as an ecosystem
homage — this is not an official LocalSend project.

## License

GNU Lesser General Public License v3.0 (LGPL-3.0). See [LICENSE](LICENSE).
