# MoonDav

MoonDav is a small WebDAV bridge for Moon+ Reader Pro. It preserves Moon+'s native position files for reliable Moon+-to-Moon+ synchronization and can mirror normalized reading progress into Calibre-Web or a KOReader-compatible backend such as BookLore.

> [!IMPORTANT]
> Moon+ Reader's `.po` file contains a private chapter/offset position. The trailing percentage alone is not enough to reconstruct that location. MoonDav therefore does **not** fabricate a Moon+ position from a Calibre-Web or BookLore percentage. Remote progress that is ahead is detected and reported by `/status`, but is not written into `.po` files until a safe translator exists. Moon+ device-to-device sync remains bidirectional because the original `.po` value is preserved verbatim.

## Architecture

```text
Moon+ phone  ─┐
              ├─ WebDAV ─> MoonDav ─> Calibre-Web Kobo state
Moon+ tablet ─┘                  └──> BookLore / KOReader sync
```

MoonDav is designed to run inside the trusted home network. Use Tailscale or an authenticated Pangolin/Newt route instead of publishing port `8765` directly to the Internet.

## Features

- Moon+ compatible WebDAV using Go's WebDAV implementation.
- Stores Moon+ `.po`, `.an`, shelf, settings, and related files under `/data/webdav`.
- Preserves Moon+ position files verbatim.
- Default `furthest` conflict policy prevents a stale offline device from overwriting newer progress.
- Persistent retry state. Failed backend writes are retried after restart and every minute.
- Calibre-Web Kobo reading-state adapter.
- BookLore / generic KOReader-sync adapter.
- Mandatory HTTP Basic authentication for WebDAV and `/status`.
- Hardened non-root container with a read-only root filesystem and no Linux capabilities.
- GitHub Actions builds multi-architecture images and publishes them to GHCR.

## Quick deploy

Copy this `compose.yml`:

```yaml
services:
  moondav:
    image: ghcr.io/3115a083/moondav:latest
    container_name: moondav
    restart: unless-stopped
    env_file:
      - .env
    ports:
      - "127.0.0.1:8765:8765"
    volumes:
      - ./data:/data
    read_only: true
    tmpfs:
      - /tmp:size=8m,mode=1777
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    user: "65532:65532"
    pids_limit: 128
    mem_limit: 128m
```

Create `.env`:

```dotenv
MOONDAV_DAV_USER=moon
MOONDAV_DAV_PASSWORD=replace-with-a-long-random-password
MOONDAV_BACKEND=none
```

Start the service:

```bash
mkdir -p data
docker compose up -d
```

The default WebDAV endpoint is `http://127.0.0.1:8765/dav/`.

Do not change the sample port binding to `0.0.0.0` unless you have a specific network-isolation reason. With Pangolin/Newt, route to the host or container through the Newt site instead.

## Moon+ Reader setup

Moon+ cloud sync requires Moon+ Reader Pro.

1. Open Moon+ Reader Pro.
2. Choose WebDAV in the cloud/sync settings.
3. Set the URL to the externally reachable MoonDav URL, for example `https://moon.example.net/dav/`.
4. Enter `MOONDAV_DAV_USER` and `MOONDAV_DAV_PASSWORD`.
5. Enable **Sync books across devices / Sync my shelf** if you also want shelf metadata in WebDAV.
6. Run one manual sync.

MoonDav does not need access to the ebook files for Moon+-to-Moon+ position sync.

## Conflict handling

The default policy is:

```dotenv
MOONDAV_CONFLICT_POLICY=furthest
```

If the server has 61% and an offline device later uploads 35%, MoonDav keeps the 61% position. The device receives the server copy on its next read/sync cycle.

This protects against the most common stale-device failure mode. It also means an intentional backward move does not globally rewind the book. Set `MOONDAV_CONFLICT_POLICY=latest` only if you explicitly prefer last-write-wins behavior.

## Backend book mapping

Moon+ identifies cache entries by filename. Calibre-Web identifies books by Calibre UUID. KOReader-compatible backends use a document identifier. MoonDav therefore uses an explicit mapping file.

Create `data/book-map.json`:

```json
{
  "entries": {
    "the necromancers house - buehlman christopher.epub": "BACKEND_BOOK_ID"
  }
}
```

Keys are Moon+ `.po` filenames without the final `.po`, lower-cased by MoonDav.

Values are:

- **Calibre-Web:** the Calibre book UUID used by the Kobo API.
- **BookLore / KOReader sync:** the document identifier used by the KOReader sync endpoint.

Restart MoonDav after editing the mapping file.

## Calibre-Web

Calibre-Web exposes a Kobo-compatible reading-state API with progress, status, timestamps, and bookmark state. MoonDav uses that HTTP API and never writes Calibre-Web's SQLite database directly.

### Setup

1. Enable Kobo synchronization in Calibre-Web.
2. Open the Calibre-Web profile for the user MoonDav should update.
3. Create or view the **Kobo Sync Token**.
4. Copy the token from a URL shaped like `https://calibre.example/kobo/TOKEN`.
5. Make sure that user can access the mapped books.
6. Add Calibre UUIDs to `data/book-map.json`.

Configure MoonDav:

```dotenv
MOONDAV_BACKEND=calibre-web
MOONDAV_BACKEND_URL=http://calibre-web:8083
MOONDAV_BACKEND_TOKEN=your-kobo-sync-token
```

MoonDav mirrors Moon+ progress to:

```text
PUT /kobo/<token>/v1/library/<book-uuid>/state
```

It sets Kobo status to `Reading`, or `Finished` at 99.5% and above.

### Reverse direction

MoonDav polls the mapped Calibre-Web state. If Calibre-Web is ahead, `/status` reports `remote_ahead: true`.

MoonDav intentionally does not rewrite the Moon+ `.po` file from a percentage. Changing only the percentage is known not to move Moon+ to the corresponding location.

## BookLore

BookLore exposes a KOReader-compatible sync endpoint and supports progress synchronization between KOReader and BookLore.

1. Go to **Settings > Devices > KOReader Sync**.
2. Enable KOReader Sync.
3. Create the KOReader username and password.
4. Copy the KOReader API path, normally similar to `https://booklore.example/api/koreader`.
5. Add the corresponding KOReader document IDs to `data/book-map.json`.

Configure MoonDav:

```dotenv
MOONDAV_BACKEND=booklore
MOONDAV_BACKEND_URL=https://booklore.example/api/koreader
MOONDAV_BACKEND_USER=your-koreader-user
MOONDAV_BACKEND_PASSWORD=your-koreader-password
```

MoonDav prepares the KOReader `x-auth-key` locally from the password as required by that protocol. You may instead provide an already prepared value with `MOONDAV_BACKEND_KEY`.

BookLore itself describes cross-reader position conversion as best-effort. MoonDav therefore keeps Moon+'s original `.po` as the authoritative exact Moon+ resume position.

## Calibre Content Server

Calibre's Content Server synchronizes its own browser viewer's last-read position. That state is not exposed as a stable public progress API intended for arbitrary third-party readers.

For plain Calibre, use one of these patterns:

- Use Calibre OPDS/Content Server for browsing and downloading, and run MoonDav with `MOONDAV_BACKEND=none` for Moon+ device-to-device position sync.
- Put Calibre-Web in front of the same Calibre library and use the Calibre-Web adapter.
- Use BookLore as the library/progress service and use its KOReader-compatible adapter.

MoonDav deliberately does not write Calibre internal databases or private viewer state.

## Pangolin + Newt

Recommended topology:

```text
Moon+ Reader
    |
  HTTPS
    |
Pangolin on VPS
    |
 Newt tunnel
    |
MoonDav:8765 in home network
```

Create an HTTPS public resource in Pangolin:

- Target the Newt site in the home network.
- Target MoonDav's host/LAN address and port `8765`.
- Use HTTP inside the Newt tunnel and HTTPS publicly.
- Do not use a raw TCP public resource for WebDAV.
- Keep MoonDav Basic Auth enabled.
- Add Pangolin identity, IP, CIDR, or other rules where they fit your clients.

Pangolin documents Newt sites as the preferred option for public resources.

### Shareable Links

Pangolin Shareable Links are credentials. For direct programmatic access Pangolin requires the access token on **every** request, through either:

```text
?p_token=<token-id>.<access-token>
```

or Pangolin-specific headers.

This is important for WebDAV. Moon+ Reader does not expose arbitrary `P-Access-Token-Id` and `P-Access-Token` headers. A normal browser Share Link also uses a browser-oriented validation/redirect flow.

Therefore:

- Do not assume a browser Share Link alone protects a Moon+ WebDAV configuration.
- If your Moon+ version preserves `p_token` on every generated WebDAV request, test the direct token URL with real `PROPFIND`, `GET`, and `PUT`, not only the connection test.
- The supported baseline is Pangolin HTTPS transport plus MoonDav Basic Auth, with MoonDav itself not reachable directly from the Internet.
- For machine-only access, prefer a Pangolin private resource or Tailscale when the reading device can run the corresponding client.

Treat Pangolin access tokens, Newt secrets, MoonDav passwords, Calibre-Web Kobo tokens, and BookLore credentials as secrets.

Pangolin references:

- https://docs.pangolin.net/manage/access-control/links
- https://docs.pangolin.net/manage/resources/understanding-resources
- https://docs.pangolin.net/manage/sites/credentials

## Tailscale alternative

If every Moon+ device can run Tailscale, this is simpler than a public reverse proxy. Keep MoonDav on a private address, retain Basic Auth, and restrict access to port `8765` with tailnet policy.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `MOONDAV_LISTEN` | `:8765` | HTTP listener inside the container |
| `MOONDAV_DATA_DIR` | `/data` | Persistent state root |
| `MOONDAV_BASE_PATH` | `/dav/` | WebDAV URL prefix |
| `MOONDAV_DAV_USER` | required | Basic Auth username |
| `MOONDAV_DAV_PASSWORD` | required | Basic Auth password |
| `MOONDAV_MAX_UPLOAD_BYTES` | `8388608` | PUT size limit |
| `MOONDAV_CONFLICT_POLICY` | `furthest` | `furthest` or `latest` |
| `MOONDAV_BACKEND` | `none` | `none`, `calibre-web`, `booklore`, `kosync` |
| `MOONDAV_BACKEND_URL` | empty | Backend base URL |
| `MOONDAV_BACKEND_TOKEN` | empty | Calibre-Web Kobo token |
| `MOONDAV_BACKEND_USER` | empty | BookLore/KOReader username |
| `MOONDAV_BACKEND_PASSWORD` | empty | BookLore/KOReader password |
| `MOONDAV_BACKEND_KEY` | empty | Precomputed KOReader key |
| `MOONDAV_BOOK_MAP_FILE` | `/data/book-map.json` | Mapping file |

## Health and status

Health endpoint:

```text
GET /healthz
```

Authenticated state:

```bash
curl -u moon:password https://moon.example.net/status
```

`remote_ahead: true` means the backend reported a higher percentage than the exact Moon+ position currently stored.

## Data and backup

Back up the full `data/` directory:

```text
data/
├── webdav/
├── state.json
└── book-map.json
```

`state.json` is updated through a temporary file and atomic rename. The original `.po` remains the exact Moon+ resume-position source of truth.

## Container security

The provided image and Compose use:

- non-root UID/GID `65532`
- read-only container root filesystem
- writable `/data` only
- all Linux capabilities dropped
- `no-new-privileges`
- no Docker socket
- no host networking
- no privileged mode
- loopback-only published port in the sample Compose
- upload-size and HTTP timeout limits

TLS is expected to terminate at Pangolin, Tailscale HTTPS, Caddy, Traefik, or another trusted ingress. Do not expose MoonDav's plain HTTP listener directly to the Internet.

## Build and release

Local:

```bash
go test ./...
go build ./cmd/moondav
docker build -t moondav .
```

GitHub Actions tests the Go service and builds the Dockerfile for `linux/amd64` and `linux/arm64`. Pushes to `main` and version tags publish to:

```text
ghcr.io/3115a083/moondav
```

A GitHub Pages Compose builder is intentionally not included in the first version. The deployment currently has one service and a small environment file, so a builder would add maintenance and secret-handling risk without removing meaningful complexity.

## Roadmap

True backend-to-Moon resume synchronization requires translating a backend locator or percentage into Moon+'s private chapter/character-offset format. The safe implementation path is:

1. Collect repeatable `.po` samples for EPUB and PDF.
2. Define a tested Moon+ position codec per format.
3. For EPUB, optionally mount the source book read-only and translate a normalized locator to Moon+'s spine/character offset.
4. Enable backend-to-Moon writes only after round-trip tests prove that Moon+ opens at the expected location.

Until then MoonDav provides exact Moon+-to-Moon+ sync, safe Moon+ progress mirroring to supported backends, persistent retry, and detection of reverse-direction conflicts without corrupting resume positions.

## Upstream documentation

- Calibre-Web Kobo state: https://github.com/janeczku/calibre-web/blob/master/cps/kobo.py
- BookLore device sync: https://booklore.org/docs/tools/devices
- BookLore OPDS: https://booklore.org/docs/integration/opds
- Calibre Content Server: https://manual.calibre-ebook.com/server.html
- Pangolin Shareable Links: https://docs.pangolin.net/manage/access-control/links
