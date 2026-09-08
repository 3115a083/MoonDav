# Security model

MoonDav contains reading-history metadata and credentials capable of updating a library user's reading state. Treat it as a private service.

## Trust boundaries

- Moon+ Reader authenticates to MoonDav with HTTP Basic Auth.
- TLS should terminate before MoonDav, normally at Pangolin, Tailscale, or another trusted reverse proxy.
- MoonDav authenticates independently to Calibre-Web or BookLore.
- Backend credentials are never returned by `/status`.
- `/healthz` contains no state and intentionally does not require authentication.
- `/status` and all WebDAV paths require Basic Auth.

## Pangolin

Use an HTTPS public resource targeted through a Newt site. Do not expose MoonDav's port directly on the VPS and do not use a raw TCP resource for this WebDAV service.

Keep MoonDav Basic Auth enabled. Pangolin should be an additional access layer, not the only credential boundary.

Shareable Links are not a transparent replacement for WebDAV authentication. Pangolin requires the direct access token on every programmatic request. Moon+ cannot normally set Pangolin-specific headers, so verify any `p_token` query-string setup with real `PROPFIND`, `GET`, and `PUT` operations before relying on it.

## Container restrictions

The sample deployment runs as an unprivileged user, drops all capabilities, uses a read-only root filesystem, enables `no-new-privileges`, and mounts only `/data` writable.

Do not add the Docker socket, host networking, privileged mode, or broad host filesystem mounts.

## Secrets

Use long random values. Never commit `.env`. Rotate a credential if it appears in logs, shell history, screenshots, or a public repository.
