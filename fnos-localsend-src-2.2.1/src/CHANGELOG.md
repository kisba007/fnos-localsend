# Changelog

## 2026-08-16 (post-v0.2.1)

### Fix

Web UI navigation no longer wedges on an unreadable share: a mount that
fails to list (ACL-denied subdir, stale NFS) now produces a toast and an
in-table error row with the server's message, the share selector reverts to
the working share, and boot falls through to the first share that actually
lists. The same cascade had silently emptied the settings menu's dropbox
picker — fixed by decoupling settings load from share listing. Listing
fetches carry a 15 s timeout and stale out-of-order responses are dropped.

- fix: navigation surfaces errors instead of silently wedging (`390a356`)

### Feature

The Docker image follows the linuxserver.io PUID/PGID convention: it starts
as root, renumbers the built-in `localsend` user to `PUID`/`PGID` (default
1000/1000), fixes `/data` ownership, and drops privileges via `su-exec`.
This is the standard answer for Synology/TrueNAS mounts, whose ACLs
containers otherwise ignore (only uid/gid matching applies). The settings
menu gained a "show NAS metadata files" toggle; `@eaDir`, `.DS_Store`,
`#recycle`, and friends are hidden by default, as are in-flight receive
partials.

- feat: NAS metadata filter (settings toggle) + hide receive partials (`eb4abe7`)
- feat: PUID/PGID support in Docker image (linuxserver convention) (`be7d439`)

### Docs

Compose example clarifies mount direction with distinct host paths and adds
PUID/PGID guidance.

- docs: distinct host paths in compose example (mount direction clarity) (`e6c8449`)

## 2026-08-16 (post-v0.2.0)

### CI

Docker images now dual-publish to Docker Hub (`alexindigo/localsend-nas`)
alongside GHCR in a single tag-triggered build — Synology Container Manager
and TrueNAS can browse/pull them natively (their registry clients don't
support GHCR's token-auth flow). README and the compose example lead with
the Docker Hub name. Packaging-only change; no code touched.

## 2026-08-16

### Feature

Receiving: localsend-nas is now a full LocalSend node. When a device sends
files to the NAS, every open browser tab pops a dialog (sender, file list,
destination picker over the existing shares) with a countdown — default 30 s,
adjustable in the new settings menu (gear icon, top right) alongside the
optional dropbox share that auto-accepts on timeout ("saved to X" vs
"request will be rejected" captions). Uploads are token-guarded, streamed to
`.lnas-part` partials and atomically renamed, dedupe as `name (1).ext`,
keep sender folder structure, and stay confined to share roots with
per-element sanitization; the sender's claimed fingerprint is verified
against its client certificate per the v2 security context. Receive sessions
ride the Transfers panel (direction `←`, live progress, cancel, × cleanup),
and a global SSE channel drives the popup in every open tab. The escape
hatch `--read-only` / `LOCALSEND_NAS_READ_ONLY=1` restores the strict v1
send-only posture. Verified against the official CLI as sender (manual
accept and dropbox-timeout paths, byte-identical delivery).

- feat: settings store + GET/PUT /api/settings (`c677612`)
- refactor: export confined write-path resolution from shares (`27eca65`)
- refactor: rejectserver → lsserver rename (`e0f27a9`)
- feat: receive pipeline (sessions, upload, cancel) + --read-only flag (`92e8ba3`)
- feat: receive jobs in transfers + decision endpoint + events stream (`543396d`)
- feat: web UI receive modal, countdown, settings menu (`b0793f8`)

### Docs

README gains a Receiving section (dialog flow, dropbox, `--read-only`) and
drops the "send-only" intro phrasing.

- docs: README receiving section (`558ae7e`)

## 2026-08-15 (post-v0.1.2)

### Fix

Theme-toggle icons no longer depend on emoji fonts: bare codepoints without
VS16 fell back to monochrome text glyphs on Linux (U+1F5A5 rendered as an
ambiguous slab that read as a phone) — the toggle now uses inline SVGs that
render identically everywhere and follow `currentColor`. Cancelling a
waiting transfer now flips its state to `cancelled` immediately instead of
appearing stuck until the receiver timed out.

- fix: theme toggle icons as inline SVG (emoji glyphs unreliable on Linux) (`ea29410`)
- fix: cancel transitions to cancelled state immediately (`4024ed4`)

### Feature

UI polish pass, all visible in the header and lists: the v3 logo sits next
to the title (and centered atop the README), the palette is adopted from
localsend.org (deep teal on mint in light, mint on teal-black in dark),
Poppins is self-hosted and embedded, a GitHub repo link lives beside the
theme toggle, device list entries and finished transfers can be cleaned up
with an × button (devices DELETE endpoint now accepts discovered entries;
a live one simply re-announces), and the theme toggle's system icon
reflects the device class (phone on touch, monitor otherwise).

- feat: logo in page header; centered in README (`a8a15e8`)
- feat: self-hosted Poppins webfont; fix header alignment (`5746585`)
- feat: theme toggle system icon reflects device class (`b124c25`)
- feat: adopt localsend.org color palette (`86f5ed8`)
- feat: GitHub repo link in header (`fae0131`)
- feat: × remove for devices and terminal transfers (`35c51f1`)

## 2026-08-15 (post-v0.1.0)

### Fix

The Transfers panel stayed empty while a job was active and only appeared
once it finished: the cancel-button render chained off `Node.append()`,
which returns `undefined`, throwing mid-render for every non-terminal job.
Split into separate statements; verified in headless Chrome with a live
job in both states.

- fix: render cancel button without chaining Node.append (`20e9589`)

### Feature

Operability endpoints for automation: the LXC install script, Docker
operators, and monitoring can now verify the service over plain HTTP
without touching the mTLS LocalSend port. `GET /api/health` reports
liveness plus version/identity; `GET /api/selftest` runs per-subsystem
checks (share roots, LocalSend port self-dial, multicast listener) and
returns 503 with per-check error strings on failure, so
`curl -sf …/api/selftest` works as an install gate.

- feat: /api/health + /api/selftest operability endpoints (`ab3bce6`)

### Docs

New logo: the LocalSend broadcast-ring motif squared — a NAS box at the
center of the local network — white on their teal, with README
attribution to the LocalSend project (© Tien Do Nam, Apache-2.0).
Favicon re-rendered from it, and a new apple-touch-icon plus its
`<link>` makes the UI presentable as a mobile home-screen icon. README
also gained a docker-compose example for Synology/TrueNAS-style hosts.

- docs: LocalSend-style squared logo + apple-touch-icon (`b8f914e`)
- docs: docker-compose example (`ef7f2c4`)

## 2026-08-15

### Feature

Initial build of localsend-nas: a send-only LocalSend Protocol v2.2 node with
an embedded web UI, aimed at NAS-style deployments where files should travel
share → server → recipient device without round-tripping through the
browser's machine. The node announces itself over multicast (224.0.0.167:53317),
keeps a persistent ECDSA identity whose SHA-256 fingerprint is its device ID,
and speaks mutual TLS the way v2.1+ receivers require (client certificate
presented unconditionally, uppercase-hex fingerprint). Incoming transfers are
politely declined with 403 while /info and /register keep the node visible in
other devices' lists. The web UI offers share browsing with a basket flow,
device management (discovered + manual IP targets), and live transfer progress
over SSE; it ships dark mode (system-aware with a persisted toggle) and a
project logo/favicon. File access is read-only and path-confined per share
root, with symlink-escape and traversal guards covered by unit tests.
Verified end-to-end against the official LocalSend CLI 1.18.0 (byte-identical
delivery). Docker image published to GHCR (multi-arch amd64/arm64) alongside
the release tarballs.

- feat: identity + config (persistent cert, share parsing) (`6980f2c`)
- feat: localsend protocol client (info/prepare/upload/cancel) (`66078aa`)
- feat: discovery — multicast announce/listen, registry, manual probe (`a1c1649`)
- feat: send-only reject server on :53317 (`e977ee5`)
- feat: share store with confined read-only access (`de4c8c8`)
- feat: transfer manager (basket sessions, progress, cancel) (`afd2ff8`)
- feat: http API + embedded SPA (`d1d5d61`)
- feat: web UI dark mode (system-aware, persisted toggle) (`eb35469`)
- feat: favicon from project logo (`6cae9cc`)
- feat: Docker image (multi-arch GHCR publish on tag) (`2ab9df6`)

### Docs

Original project logo (teal gradient + send stripes), SVG source plus PNG
renders; referenced from the README and reused as the favicon.

- docs: project logo (`753a255`)

### CI

Tag-driven release pipeline: cross-compiled static binaries packed as
`localsend-nas_<ver>_linux_{amd64,arm64}.tar.gz` with checksums and a
GitHub Release, plus the Docker→GHCR publish workflow.

- ci: tag-triggered release builds (tar.gz, amd64/arm64) (`8f22682`)

### Chore

Repo scaffolding (go.mod pinned Go, LGPL-3.0, README, .gitignore) and module
path settlement to the personal account.

- chore: repo init — go.mod, LICENSE, README skeleton (`8de3754`)
- chore: module path → github.com/alexindigo/localsend-nas (`8bbdf5f`)
