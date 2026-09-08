# MoonDav

> **Beta:** MoonDav is under active development. Back up `/data` before upgrades and review release notes before deploying a new beta.

MoonDav is a small WebDAV bridge for Moon+ Reader Pro. It preserves Moon+'s native position files for reliable Moon+-to-Moon+ synchronization and can mirror normalized reading progress into Calibre-Web or a KOReader-compatible backend such as BookLore.

> [!IMPORTANT]
> Moon+ Reader's trailing percentage is not an exact locator. MoonDav can now translate the full EPUB `.po` coordinate into Calibre-Web's KoboSpan coordinate space when exact sync is explicitly enabled and the same source EPUB is mounted read-only. If any exact prerequisite is missing, MoonDav falls back to percentage sync and never invents a locator.

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
- Persistent offline queue. Backend outages never fail Moon+ WebDAV writes; the newest progress per book is stored on disk and retried with bounded exponential backoff.
- Calibre-Web Kobo reading-state adapter.
- Opt-in EPUB position translator between Moon+ chapter/offset coordinates and Calibre-Web/Kobo `KoboSpan` locations.
- Shared zero-copy OPDS shelf backed by exactly one canonical source: Calibre-Web, BookLore, or a read-only filesystem.
- BookLore / generic KOReader-sync adapter.
- Separate HTTP Basic credentials for WebDAV devices and the admin dashboard/API.
- Optional outage/recovery notifications through SMTP, Telegram, or a generic HTTPS webhook.
- Hardened non-root container with a read-only root filesystem and no Linux capabilities.
- GitHub Actions builds multi-architecture images and publishes them to GHCR.

## Quick deploy

Copy this `compose.yml`:

```yaml
services:
  moondav:
    image: ghcr.io/3115a083/moondav:beta
    container_name: moondav
    restart: unless-stopped
    env_file:
      - .env
    ports:
      - "127.0.0.1:8765:8765"
    volumes:
      - moondav-data:/data
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

volumes:
  moondav-data:
```

Create `.env`:

```dotenv
MOONDAV_DAV_USER=moon
MOONDAV_DAV_PASSWORD=replace-with-a-long-random-password
MOONDAV_ADMIN_USER=admin
MOONDAV_ADMIN_PASSWORD=replace-with-a-different-long-random-password
MOONDAV_BACKEND=none
```

Start the service:

```bash
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

For the common book catalog, add a separate OPDS catalog in Moon+ under **Net Library → OPDS catalogs**:

- URL: `https://moon.example.net/opds/`
- Username: `MOONDAV_DAV_USER`
- Password: `MOONDAV_DAV_PASSWORD`

MoonDav does not need access to the ebook files for Moon+-to-Moon+ position sync.

## Web dashboard

Open the MoonDav base URL in a browser, for example:

```text
https://moon.example.net/
```

The dashboard and JSON API use separate admin Basic Auth credentials. Moon+ only receives the WebDAV credentials. It shows:

- Moon+ and backend progress side by side.
- conflicts, unmapped books, and backend errors.
- editable backend book mappings.
- the latest Moon+ update time.
- conflict actions.

For a backend-ahead conflict MoonDav offers two safe actions:

- **Keep Moon+ position** pushes the exact current Moon+ progress back to the backend.
- **Ignore until caught up** suppresses the warning until Moon+ reaches the backend percentage.

MoonDav does not offer an unsafe "use backend position in Moon+" action because a percentage cannot safely reconstruct Moon+'s private chapter/offset position.

The UI is embedded in the Go binary. It has no CDN, external JavaScript, analytics, or third-party fonts. API writes are same-origin checked and all UI/API routes require authentication.

## Conflict handling

The default policy is:

```dotenv
MOONDAV_CONFLICT_POLICY=furthest
```

If the server has 61% and an offline device later uploads 35%, MoonDav keeps the 61% position. The device receives the server copy on its next read/sync cycle.

This protects against the most common stale-device failure mode. It also means an intentional backward move does not globally rewind the book. Set `MOONDAV_CONFLICT_POLICY=latest` only if you explicitly prefer last-write-wins behavior.

## Backend book mapping

Moon+ identifies cache entries by filename. Calibre-Web identifies books by Calibre UUID. KOReader-compatible backends use a document identifier. MoonDav therefore uses an explicit mapping file.

Create `data/book-map.json`, or edit the same fields in the dashboard:

```json
{
  "entries": {
    "the necromancers house - buehlman christopher.epub": {
      "backend_id": "CALIBRE_BOOK_UUID",
      "epub_path": "Christopher Buehlman/The Necromancer's House (123)/The Necromancer's House - Christopher Buehlman.epub"
    }
  }
}
```

Keys are Moon+ `.po` filenames without the final `.po`, lower-cased by MoonDav.

`backend_id` is the Calibre-Web UUID or KOReader-compatible document identifier. `epub_path` is optional and must be relative to `MOONDAV_LIBRARY_ROOT`. Legacy string-only mapping values remain accepted and are treated as `backend_id` only.

The EPUB path is used only for exact Calibre-Web position translation. It is never written to and path traversal outside the configured library root is rejected.

## Exact EPUB position translation

MoonDav reverse-engineers the Moon+ EPUB position string as:

```text
{timestamp_ms}*{zero_based_spine_chapter}@{section}#{character_offset}:{percent}%
```

For example:

```text
1703297605115*21@0#4826:11.1%
```

The interpretation is supported by independent Moon+ WebDAV implementations and historical position samples. MoonDav treats the chapter as a zero-based EPUB spine index and the offset as a character coordinate in that content document.

Calibre-Web's Kobo API stores an exact KEPUB location as:

```json
{
  "Source": "text/chapter.xhtml",
  "Type": "KoboSpan",
  "Value": "kobo.52.4"
}
```

MoonDav uses the upstream `kepubify` Go library on the mounted source XHTML to generate Kobo's span markers, then maps the Moon+ character coordinate to the corresponding span. This avoids maintaining a second, potentially divergent KoboSpan algorithm.

Enable it only for Calibre-Web:

```dotenv
MOONDAV_BACKEND=calibre-web
MOONDAV_EXACT_POSITIONS=true
MOONDAV_LIBRARY_ROOT=/books
```

Mount the Calibre library read-only:

```yaml
volumes:
  - moondav-data:/data
  - /srv/calibre-library:/books:ro
```

Then set each book's `epub_path` relative to `/books`.

### Direction: Moon+ to Calibre-Web

MoonDav decodes the Moon+ spine chapter and character offset, resolves the corresponding EPUB content document, transforms that document with `kepubify`, selects the matching KoboSpan, and sends both progress percentage and `Location` to Calibre-Web.

If the EPUB cannot be opened, the chapter is outside the spine, or no matching span can be generated, MoonDav sends percentage only. It never sends a guessed location.

### Direction: Calibre-Web to Moon+

When Calibre-Web reports a newer KoboSpan, MoonDav resolves its source document and span back to a Moon+ spine chapter and character offset. It rewrites the stored `.po` only when an original valid Moon+ position already exists, because the original Moon+ timestamp is preserved rather than fabricated.

Kobo reading-state locations identify a KoboSpan, not an arbitrary character inside that span. Reverse translation therefore resolves to the beginning of the corresponding span. In normal KEPUBs this is usually sentence or text-segment granularity.

### Scope and validation

This exact translator currently targets EPUB only. PDF, CBZ and other formats remain percentage-only.

The implementation is opt-in during beta because Moon+ does not publish the format. Unit tests cover full codec round-trips, EPUB spine resolution, kepubify span generation, reverse mapping and path traversal. Real-device calibration with multiple EPUB structures and non-ASCII books is still valuable before making exact mode the default.

## Shared Shelf without duplicate ebook storage

MoonDav exposes a single reader-facing catalog at:

```text
https://moon.example.net/opds/
```

Use the MoonDav WebDAV username and password when Moon+ asks for OPDS credentials.

The Shared Shelf is deliberately **single-source**. Choose exactly one canonical ebook source:

- `opds`: proxy Calibre-Web or BookLore OPDS.
- `filesystem`: index and stream a read-only directory.
- `off`: no MoonDav catalog.

MoonDav never imports, copies, renames, converts, or modifies ebook files for the Shared Shelf. Remote acquisitions are streamed directly from the configured OPDS server to the reader. Filesystem acquisitions are opened read-only and streamed with HTTP range support.

Moon+ still downloads a book locally onto each Android device when you open or acquire it. That client-side copy is required by Moon+ and is outside MoonDav. The server-side library remains single-copy.

### Calibre-Web as the canonical shelf

Calibre-Web exposes an OPDS catalog and acquisition links. Configure a Calibre-Web user with download permission, then:

```dotenv
MOONDAV_SHELF_MODE=opds
MOONDAV_SHELF_URL=http://calibre-web:8083/opds
MOONDAV_SHELF_USER=reader
MOONDAV_SHELF_PASSWORD_FILE=/run/secrets/shelf_password
```

MoonDav rewrites navigation, cover and acquisition links so Moon+ only talks to MoonDav. The upstream Calibre-Web credentials are not given to the Android device.

### BookLore as the canonical shelf

Enable BookLore OPDS and create an OPDS user. BookLore documents its catalog at `/api/v1/opds`. Configure:

```dotenv
MOONDAV_SHELF_MODE=opds
MOONDAV_SHELF_URL=http://booklore:6060/api/v1/opds
MOONDAV_SHELF_USER=reader
MOONDAV_SHELF_PASSWORD_FILE=/run/secrets/shelf_password
```

This uses BookLore's supported OPDS interface instead of its undocumented internal application API.

### Filesystem as the canonical shelf

Mount the source read-only:

```yaml
volumes:
  - moondav-data:/data
  - /srv/ebooks:/shelf:ro
```

Configure:

```dotenv
MOONDAV_SHELF_MODE=filesystem
MOONDAV_SHELF_ROOT=/shelf
```

MoonDav recursively indexes supported regular files and serves EPUB, PDF, MOBI, AZW/AZW3, FB2, CBZ and CBR. Symbolic links are not indexed or served.

### Integrity and duplicate rules

The design avoids ambiguous merging completely:

1. Only one canonical source can be active at a time.
2. MoonDav does not maintain a second ebook cache.
3. The Shelf API accepts only `GET` and `HEAD`; writes return HTTP 405.
4. Filesystem sources should be mounted `:ro`.
5. Symlinks and path traversal are rejected.
6. Remote proxy targets are restricted to the configured OPDS origin, preventing the proxy from becoming an arbitrary SSRF endpoint.
7. `Range`, `ETag`, `Last-Modified`, `Content-Range` and `Accept-Ranges` are preserved for remote downloads.
8. MoonDav applies no whole-download timeout after response headers, so large books are not truncated simply because they take longer than a fixed request deadline.
9. OPDS XML is size-limited in memory. Ebook payloads are streamed and are never written into `/data`.

Because there is only one active source, MoonDav never tries to guess whether two differently named files from Calibre-Web, BookLore and a filesystem are "the same book." That avoids false deduplication and accidental cross-source replacement.

Moon+'s own shelf and reading-state metadata continue to synchronize through WebDAV. OPDS is the common distribution shelf for the actual book files.

## Backend configuration and secrets

MoonDav deliberately does **not** allow backend credentials to be edited in the web dashboard.

Backend type, URL, and credentials are startup configuration. This keeps deployments reproducible and prevents a compromised dashboard session from replacing or reading Calibre-Web, BookLore, SMTP, Telegram, or webhook credentials.

Use environment variables for non-secret configuration:

```dotenv
MOONDAV_BACKEND=calibre-web
MOONDAV_BACKEND_URL=http://calibre-web:8083
```

For credentials, MoonDav supports both the normal variable and a matching `_FILE` variable. The file value takes precedence:

```dotenv
MOONDAV_BACKEND_TOKEN_FILE=/run/secrets/backend_token
MOONDAV_DAV_PASSWORD_FILE=/run/secrets/dav_password
MOONDAV_ADMIN_PASSWORD_FILE=/run/secrets/admin_password
```

This works with Docker Compose secrets or read-only mounted secret files. See `compose.secrets.example.yml`.

The dashboard shows the selected backend and its health state, but never returns credentials.

## Offline backend behavior

Calibre-Web and BookLore are treated as eventually available services.

When a backend cannot be reached because of DNS failure, connection refusal, timeout, HTTP 408, HTTP 429, or a 5xx response:

1. Moon+'s WebDAV request still succeeds after the Moon+ state has been saved locally.
2. The newest normalized reading percentage is persisted in `/data/state.json`.
3. The book is shown as **Queued**, not **Error**.
4. Retry uses bounded exponential backoff: 15s, 30s, 1m, 2m, 5m, then every 10m.
5. A newer Moon+ position replaces the older pending percentage. Intermediate stale writes do not accumulate.
6. Once the backend is reachable, the queued state is delivered automatically and the queue entry clears.

Authentication failures, invalid mappings, malformed backend responses, and other non-temporary failures remain visible as errors because retrying them without a configuration change is unlikely to help.

Backend health is persisted too, so restart does not lose outage context.

## Notifications

Notifications are optional and disabled unless a channel is configured. MoonDav waits before alerting, so short network interruptions do not create noise.

Defaults:

```dotenv
MOONDAV_NOTIFY_AFTER=10m
MOONDAV_NOTIFY_REPEAT=6h
```

A recovery notification is sent only when an outage notification was previously emitted.

### Telegram Bot

```dotenv
MOONDAV_TELEGRAM_BOT_TOKEN_FILE=/run/secrets/telegram_bot_token
MOONDAV_TELEGRAM_CHAT_ID=123456789
```

### SMTP

STARTTLS example:

```dotenv
MOONDAV_SMTP_HOST=smtp.example.net
MOONDAV_SMTP_PORT=587
MOONDAV_SMTP_TLS=starttls
MOONDAV_SMTP_USER=moondav@example.net
MOONDAV_SMTP_PASSWORD_FILE=/run/secrets/smtp_password
MOONDAV_SMTP_FROM=moondav@example.net
MOONDAV_SMTP_TO=you@example.net
```

Implicit TLS on port 465 is supported with `MOONDAV_SMTP_TLS=tls`. TLS 1.2 or newer is required.

### Generic HTTPS webhook

```dotenv
MOONDAV_WEBHOOK_URL=https://notify.example.net/moondav
MOONDAV_WEBHOOK_BEARER_FILE=/run/secrets/webhook_bearer
```

MoonDav sends JSON shaped like:

```json
{
  "kind": "backend_offline",
  "title": "MoonDav backend unavailable",
  "message": "calibre-web push: ...",
  "time": "2026-09-08T12:00:00Z"
}
```

The generic webhook can be used with notification gateways or automation platforms without adding provider-specific code to MoonDav.

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

Without exact mode, MoonDav polls the mapped Calibre-Web state and reports `remote_ahead: true` when the backend is ahead.

With `MOONDAV_EXACT_POSITIONS=true`, a valid KoboSpan is translated back into the mounted EPUB's spine chapter and character coordinate. MoonDav then updates the Moon+ `.po` while preserving its original timestamp. A percentage without a KoboSpan is never used to fabricate an exact Moon+ position.

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
| `MOONDAV_DAV_PASSWORD` | required | WebDAV Basic Auth password |
| `MOONDAV_ADMIN_USER` | required | Dashboard/API Basic Auth username |
| `MOONDAV_ADMIN_PASSWORD` | required | Dashboard/API Basic Auth password |
| `MOONDAV_MAX_UPLOAD_BYTES` | `8388608` | PUT size limit |
| `MOONDAV_CONFLICT_POLICY` | `furthest` | `furthest` or `latest` |
| `MOONDAV_BACKEND` | `none` | `none`, `calibre-web`, `booklore`, `kosync` |
| `MOONDAV_BACKEND_URL` | empty | Backend base URL |
| `MOONDAV_BACKEND_TOKEN` | empty | Calibre-Web Kobo token |
| `MOONDAV_BACKEND_USER` | empty | BookLore/KOReader username |
| `MOONDAV_BACKEND_PASSWORD` | empty | BookLore/KOReader password |
| `MOONDAV_BACKEND_KEY` | empty | Precomputed KOReader key |
| `MOONDAV_BOOK_MAP_FILE` | `/data/book-map.json` | Mapping file |
| `MOONDAV_EXACT_POSITIONS` | `false` | Enable opt-in EPUB Moon+ ↔ KoboSpan translation |
| `MOONDAV_LIBRARY_ROOT` | empty | Read-only root containing mapped EPUB files |
| `MOONDAV_SHELF_MODE` | `off` | Shared Shelf source: `off`, `opds`, or `filesystem` |
| `MOONDAV_SHELF_URL` | empty | Canonical Calibre-Web/BookLore OPDS URL |
| `MOONDAV_SHELF_USER` | empty | Upstream OPDS Basic Auth username |
| `MOONDAV_SHELF_PASSWORD` | empty | Upstream OPDS Basic Auth password |
| `MOONDAV_SHELF_ROOT` | empty | Read-only filesystem shelf root |
| `MOONDAV_SHELF_MAX_FEED_BYTES` | `8388608` | Maximum proxied OPDS XML size |
| `MOONDAV_NOTIFY_AFTER` | `10m` | Delay before outage/error notification |
| `MOONDAV_NOTIFY_REPEAT` | `6h` | Minimum repeat interval for persistent outage |
| `MOONDAV_TELEGRAM_BOT_TOKEN` | empty | Telegram Bot token |
| `MOONDAV_TELEGRAM_CHAT_ID` | empty | Telegram destination chat |
| `MOONDAV_WEBHOOK_URL` | empty | Generic HTTPS webhook |
| `MOONDAV_WEBHOOK_BEARER` | empty | Optional webhook bearer secret |
| `MOONDAV_SMTP_HOST` | empty | SMTP server |
| `MOONDAV_SMTP_PORT` | `587` | SMTP port |
| `MOONDAV_SMTP_TLS` | `starttls` | `starttls` or implicit `tls` |
| `MOONDAV_SMTP_USER` | empty | SMTP username |
| `MOONDAV_SMTP_PASSWORD` | empty | SMTP password |
| `MOONDAV_SMTP_FROM` | empty | Notification sender |
| `MOONDAV_SMTP_TO` | empty | Comma-separated recipients |

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

Back up the Docker volume `moondav-data`. It contains WebDAV data, queued progress, backend health, sync state, and `book-map.json`.

`state.json` is updated through a temporary file and atomic rename. The original `.po` remains the exact Moon+ resume-position source of truth.

## Container security

The provided image and Compose use:

- non-root UID/GID `65532`
- read-only container root filesystem
- writable `/data` named volume only
- optional ebook library and shelf mounts documented as read-only
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

GitHub Actions tests the Go service and builds the Dockerfile for `linux/amd64` and `linux/arm64`. Pushes to `main` publish the beta image. Version tags also publish versioned images to:

```text
ghcr.io/3115a083/moondav
```

A GitHub Pages Compose builder is intentionally not included in the first version. The deployment currently has one service and a small environment file, so a builder would add maintenance and secret-handling risk without removing meaningful complexity.

## Roadmap

The EPUB Moon+ ↔ KoboSpan translator is now implemented behind `MOONDAV_EXACT_POSITIONS`.

Remaining position work:

1. Collect real-device calibration samples across EPUB2, EPUB3, non-ASCII text, unusual whitespace and books with non-linear spine items.
2. Add an optional diagnostics page that compares Moon+ chapter/offset, generated KoboSpan and surrounding text.
3. Investigate PDF coordinates separately. PDF must not reuse the EPUB codec.
4. Consider automatic discovery of Calibre library EPUB paths from `metadata.opf` so `epub_path` does not need to be entered manually.

Percentage-only fallback remains the safe behavior whenever exact translation cannot be proven for a specific book.

## Upstream documentation

- Calibre-Web Kobo state: https://github.com/janeczku/calibre-web/blob/master/cps/kobo.py
- BookLore device sync: https://booklore.org/docs/tools/devices
- BookLore OPDS: https://booklore.org/docs/integration/opds
- Calibre Content Server: https://manual.calibre-ebook.com/server.html
- Pangolin Shareable Links: https://docs.pangolin.net/manage/access-control/links


## Repository security

Repository-enforced safeguards include CodeQL, Dependabot, CODEOWNERS, a pull-request security checklist, and least-privilege GitHub Actions permissions.

GitHub-hosted settings such as branch rulesets, Actions defaults, Dependabot alerts, secret scanning, and merge policy must also be enabled in the repository settings. See `docs/GITHUB_SECURITY.md`.
