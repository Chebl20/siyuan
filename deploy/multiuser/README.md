# SiYuan multi-user gateway

This deployment keeps SiYuan's single-workspace kernel model intact. The gateway owns users, sessions and workspace memberships in PostgreSQL, and starts one private SiYuan container and one filesystem directory per workspace. A browser can reach a kernel only through the gateway after the membership check succeeds.

## Architecture

`https://app.example.com` is the portal. `https://<workspace-slug>.example.com` is proxied to the corresponding private SiYuan container. Do not publish ports from workspace containers and do not attach them to a public Docker network. The gateway needs access to the Docker socket, so it must be treated as an administrative component and run on a dedicated host.

The gateway uses opaque sessions stored as SHA-256 hashes, Argon2id password hashes, SameSite secure HTTP-only cookies, CSRF tokens bound to each session, origin checks, and an IP/email login throttle. Password-reset tokens are random, single use, short lived and stored only as hashes. Audit events intentionally omit passwords, session values, reset tokens and cookies.

The initial database schema is applied transactionally on gateway startup. It creates `users`, `workspaces`, `workspace_members`, `sessions`, `password_reset_tokens`, and `audit_events`; it does not change a SiYuan workspace or its index database. Set `MULTIUSER_TEST_DATABASE_URL` when running the PostgreSQL membership-isolation integration test.

Each workspace keeps its normal SiYuan data directory. Existing workspaces are not migrated or modified. Importing one is an administrative operation: stop its old kernel, copy its directory below `WORKSPACE_ROOT/<workspace-id>`, then create the matching metadata row and container through the portal or an administrative migration tool.

## Run

1. Copy `.env.example` to `.env` and replace every value marked `change-me`.
2. Create DNS records for `APP_HOST` and `*.BASE_DOMAIN`, and terminate TLS in a reverse proxy in front of port 8080. The proxy must preserve the original `Host` header and set `X-Forwarded-Proto: https`.
3. Start `docker compose up --build` from this directory.
4. Open `https://APP_HOST/register`. Registration creates the user's personal workspace and its private SiYuan container.

The Compose file exposes only the gateway. The `workspace-data` volume is a host bind mount because the gateway and Docker Engine must refer to the same directory.

## Required environment variables

| Variable | Purpose |
| --- | --- |
| `DATABASE_URL` | PostgreSQL connection for gateway metadata |
| `BASE_DOMAIN` | Parent domain used by workspace subdomains |
| `APP_HOST` | Portal host, for example `app.example.com` |
| `PUBLIC_PORT` | Public gateway port; leave empty behind HTTPS on 443, set to `8080` for local development |
| `COOKIE_SECRET` | At least 32 random bytes, base64 encoded or arbitrary high-entropy text |
| `WORKSPACE_ROOT` | Absolute path inside the gateway container where the host directory is mounted |
| `DOCKER_WORKSPACE_ROOT` | Absolute host path for the same directory; passed to the gateway by Compose |
| `SIYUAN_IMAGE` | Immutable SiYuan image reference used for new workspaces |
| `MULTIUSER_NETWORK` | Docker network connecting gateway and workspace containers |

`COOKIE_SECURE` defaults to `true`. Set it to `false` only for an HTTPS-free local development environment. Configure `SMTP_*` to deliver password-reset messages; without SMTP the endpoint still returns a generic response but cannot deliver a reset link.

## Operations and limits

- A workspace deletion is a soft delete after the owner types its name. The container is stopped and removed, while data remains on disk for recovery. Purge the workspace directory only after your retention period using an audited operational procedure.
- The service is designed for one gateway instance. Run it with one replica until durable distributed rate limiting and a Docker orchestration backend are introduced.
- PostgreSQL Row-Level Security is not enabled for the gateway tables. The gateway uses a server-only connection and authorizes every membership lookup explicitly; RLS would require per-request database roles or transaction variables and must not be treated as a replacement for those checks.
- Workspace containers use `SIYUAN_ACCESS_AUTH_CODE_BYPASS=true` only because they have no published ports and are reachable solely from the gateway's internal network.
