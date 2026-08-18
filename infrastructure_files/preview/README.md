# Preview environment

Everything in this directory exists to give Autonoma a disposable, self-contained
NetBird control plane to run end-to-end tests against. It is **not** a
recommended production deployment — see [`../getting-started.sh`](../getting-started.sh)
and the compose templates in the parent directory for that.

## What gets deployed

A single container running the **combined** server (`../../combined`), which
multiplexes Management, Signal, Relay and the embedded Dex identity provider onto
one HTTP port, plus a Postgres service for the management store. TLS is
terminated by the preview ingress, so the container speaks plain HTTP (h2c) and
the public `https://` origin is passed in as `NB_PREVIEW_PUBLIC_URL`.

| Path | Served by |
| --- | --- |
| `/oauth2/…` | embedded Dex IdP — login, token, `keys` (JWKS), discovery |
| `/api/…` | management REST API |
| everything else on the port | management gRPC / websocket-proxy |

The health check is `GET /oauth2/keys`, which serves the JWKS unauthenticated
once the IdP has finished booting.

There is no dashboard container: the NetBird dashboard lives in a separate
repository, so a preview built from this repo exposes the login UI and the API,
not the full web console.

## Files

- `Dockerfile` — multi-stage build of `netbird-server` from the repository root.
- `entrypoint.sh` — renders `/etc/netbird/config.yaml` from environment
  variables, then execs the server. A preview's public URL is only known at
  deploy time, so the issuer, redirect URIs and relay/signal addresses cannot be
  baked into a static config file.

## Environment variables

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `NB_PREVIEW_PUBLIC_URL` | yes | — | The preview's public origin, e.g. `https://abc.previews.example.com`. Drives the OIDC issuer, redirect URIs, relay/signal addresses and the management DNS domain. |
| `NB_PREVIEW_DATABASE_URL` | no | unset | Postgres connection URL for the management store. Falls back to SQLite in the data dir when unset. |
| `NB_PREVIEW_PORT` | no | `8080` | Listen port inside the container. |
| `NB_PREVIEW_DATA_DIR` | no | `/var/lib/netbird` | Data dir for SQLite stores and generated state. |
| `NB_PREVIEW_LOG_LEVEL` | no | `info` | Log level for all embedded services. |
| `NB_PREVIEW_OWNER_EMAIL` | no | `admin@preview.autonoma.app` | Seeded owner account. |
| `NB_PREVIEW_OWNER_PASSWORD_HASH` | no | bcrypt of `Preview!2345` | bcrypt hash of the owner password. Override together with the email. |
| `NB_PREVIEW_AUTH_SECRET` | no | fixed preview value | Shared secret for relay authentication. |
| `NB_PREVIEW_STORE_ENCRYPTION_KEY` | no | fixed preview value | Base64 32-byte key. Fixed so a redeploy can still read what the previous boot encrypted. |
| `NB_PREVIEW_COOKIE_ENCRYPTION_KEY` | no | fixed preview value | Base64 32-byte key for embedded IdP session cookies. |

## Preview credentials

`admin@preview.autonoma.app` / `Preview!2345`.

These are deliberately fixed and committed so automated tests can log in. They
are only ever wired into throwaway preview environments — never reuse them
anywhere a real user or real data can reach.

## Running it locally

```bash
docker build -f infrastructure_files/preview/Dockerfile -t netbird-preview .
docker run --rm -p 8080:8080 -e NB_PREVIEW_PUBLIC_URL=http://localhost:8080 netbird-preview
curl -s http://localhost:8080/oauth2/keys | head -c 200
```
