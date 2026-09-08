# Security model

MoonDav contains reading-history metadata and credentials capable of updating a library user's reading state. Treat it as a private service.

## Trust boundaries

- Moon+ Reader authenticates to the WebDAV path with dedicated HTTP Basic Auth credentials.
- The dashboard, `/status`, and `/api/*` use separate admin Basic Auth credentials.
- TLS should terminate before MoonDav, normally at Pangolin, Tailscale, or another trusted reverse proxy.
- MoonDav authenticates independently to Calibre-Web or BookLore.
- Backend credentials are never returned by `/status`.
- `/healthz` contains no state and intentionally does not require authentication.
- `/status`, the dashboard, and `/api/*` require admin Basic Auth.
- WebDAV paths require separate device Basic Auth.

## Pangolin

Use an HTTPS public resource targeted through a Newt site. Do not expose MoonDav's port directly on the VPS and do not use a raw TCP resource for this WebDAV service.

Keep both MoonDav authentication layers enabled. Do not reuse the WebDAV password as the admin password. Pangolin should be an additional access layer, not the only credential boundary.

Shareable Links are not a transparent replacement for WebDAV authentication. Pangolin requires the direct access token on every programmatic request. Moon+ cannot normally set Pangolin-specific headers, so verify any `p_token` query-string setup with real `PROPFIND`, `GET`, and `PUT` operations before relying on it.

## Container restrictions

The sample deployment runs as an unprivileged user, drops all capabilities, uses a read-only root filesystem, enables `no-new-privileges`, and mounts only `/data` writable.

Do not add the Docker socket, host networking, privileged mode, or broad host filesystem mounts.

## Secrets

Use long random values. Never commit `.env`. Rotate a credential if it appears in logs, shell history, screenshots, or a public repository.


## Shared Shelf

The Shared Shelf is read-only by design. MoonDav accepts only GET and HEAD on /opds paths.

For OPDS proxy mode, the upstream target is constrained to the configured scheme and host. Rewritten links cannot turn MoonDav into a general-purpose cross-origin proxy.

For filesystem mode, mount the canonical ebook directory read-only. MoonDav rejects absolute paths, parent traversal, symbolic links, directories, and unsupported file types.

MoonDav does not cache ebook payloads in /data. Remote and filesystem acquisitions are streamed directly to the requesting reader. Only OPDS XML metadata is buffered in memory and is bounded by MOONDAV_SHELF_MAX_FEED_BYTES.

Use separate upstream OPDS credentials through environment variables or *_FILE secret paths. These credentials are never exposed in the dashboard or to Moon+.
