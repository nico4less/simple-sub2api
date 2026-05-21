# Simple Sub2API

Personal-only account-pool gateway skeleton for the Simple Sub2API plan.

## Current status

Queue A / M0 provides the local server skeleton and security boundary:

- health and version endpoints
- default loopback bind
- explicit LAN guard requiring an admin password
- single local gateway key for `/v1/*`
- Dashboard admin session separated from gateway key
- CORS closed by default

Queue B / M1 adds the local configuration and source-management baseline:

- strict JSON config schema with `config_version`
- OAuth/account source records for personal account management only
- subscription source records
- redacted import preview for pasted line tokens, JSON bundles, inline bundles, and subscription URL content
- explicit import apply with optimistic `config_version` conflict handling
- source deletion impact preview and derived-account disable behavior
- proxy schema validation with `socks5://` normalized to `socks5h://`

Queue C / M2 adds the local runtime layer needed before gateway compatibility:

- fail-fast proxy transport with `socks5h` remote DNS behavior
- quota states: `available`, `near_limit`, `exhausted`, `unknown`, and `error`
- explicit routing rules for `document`, `code`, and `default` tasks plus model patterns and tags
- save-time account checks with bounded probes and credential redaction
- account pool states: `healthy`, `error`, `disabled`, `cooldown`, and `quota`
- tier-aware round-robin selection and hot reload that preserves the old pool when candidate validation fails

Queue D / M3 adds the first OpenAI-compatible gateway closure:

- upstream compatibility boundary at `internal/upstreamcompat` with a tracked `SYNC_MANIFEST.md`
- `/v1/chat/completions` non-streaming proxy support
- `/v1/chat/completions` streaming SSE proxy support
- gateway key auth, routing decision, accountpool selection, proxy client, quota usage accounting, and upstream-error cooldown wiring
- OpenAI-compatible error envelopes for local validation and routing failures

Queue E / M4 adds the embedded Dashboard surface:

- `GET /` and `GET /dashboard` serve a Go-embedded single-page Dashboard shell with no external CDN, fonts, or scripts
- `GET /api/admin/dashboard/state` returns redacted config, account pool, quota, version, and gateway status
- `POST /api/admin/config/save` saves a full config snapshot with `config_version` conflict handling and redacted-secret preservation
- `DELETE /api/admin/accounts/{id}` deletes an account through the same validated config update path
- `POST /api/admin/accounts/{id}/refresh` re-runs account check and refreshes account pool state
- `POST /api/admin/oauth-sources/{id}/refresh` updates local OAuth source refresh status when credentials exist
- `POST /api/admin/oauth-sources/{id}/reauth` marks a source as needing external provider authorization

Queue F / M5 adds local observability and release packaging:

- in-memory metrics recorder for QPS, total requests, success/error counts, per-account hit/error counts, cooldown counts, routing decisions, quota switch counts, and recent sanitized errors
- admin-only `GET /api/admin/metrics`
- Dashboard metrics tiles and tables backed by the metrics snapshot
- multi-stage `Dockerfile` with a non-root runtime user
- `Makefile` targets for tests, local builds, cross-platform release binaries, and Docker image builds

## Local run

```bash
go run ./cmd/simple-sub2api --config simple_sub2api.config.json
```

The first run creates a local JSON config with a generated `s2a_` gateway key.

## Local release builds

The Go binary has no database dependency. Release builds can be produced from this directory:

```bash
make test
make linux-amd64
make darwin-amd64
make darwin-arm64
make windows-amd64
```

Artifacts are written under `dist/`. Version metadata may be injected with `VERSION`, `COMMIT`, and `DATE` make variables.

## Queue B admin APIs

All source and import APIs require the Dashboard admin session cookie, not the gateway bearer key.

- `GET /api/admin/config` returns a redacted config snapshot.
- `GET/POST /api/admin/oauth-sources` lists or upserts OAuth/token-bundle sources.
- `GET/DELETE /api/admin/oauth-sources/{id}` previews source impact or disables derived accounts after delete.
- `GET/POST /api/admin/subscription-sources` lists or upserts subscription sources.
- `GET/DELETE /api/admin/subscription-sources/{id}` previews source impact or disables derived accounts after delete.
- `POST /api/admin/import/preview` parses import candidates without mutating the config and redacts credentials.
- `POST /api/admin/import/apply` validates `config_version`, re-parses raw import content when provided, and then saves candidates.

## Queue C admin APIs

All runtime APIs require the Dashboard admin session cookie. Mutating APIs use the same optimistic `config_version` rule as Queue B.

- `GET/POST /api/admin/proxies` lists or upserts proxy records.
- `DELETE /api/admin/proxies/{id}` deletes a proxy and disables accounts that referenced it.
- `GET/POST /api/admin/quota` reads or replaces quota policy config.
- `GET /api/admin/quota/state` returns per-account quota runtime state.
- `GET/POST /api/admin/routing` reads or replaces routing config.
- `POST /api/admin/routing/decide` previews the routing decision for a task/model/tag request.
- `GET /api/admin/account-check` probes all configured accounts.
- `POST /api/admin/account-check` probes one configured account by `account_id`.
- `GET /api/admin/account-pool` returns current pool state after validation and checks.

## Queue D gateway API

Clients use the generated local gateway key as a Bearer token:

- `POST /v1/chat/completions` forwards OpenAI-compatible chat completion requests to the selected upstream account.
- `stream: true` responses are proxied as `text/event-stream` and re-emitted as OpenAI `data:` events.
- `X-Simple-Task-Type` may be set to `document`, `code`, or `default` to drive simple routing.
- `X-Simple-Tags` may be a comma-separated tag list for explicit routing rules.
- Upstream HTTP `401`, `403`, `404`, `429`, and `5xx` responses trigger short account cooldown.

## Queue E Dashboard

Open `http://127.0.0.1:8080/dashboard` after starting the service. The Dashboard is a local embedded page that talks only to same-origin admin APIs and does not persist plaintext credentials in frontend storage. The UI includes account add/save, import preview/apply, OAuth refresh/reauth status actions, routing edit/test, proxy config/probe, quota progress bars, gateway reveal/rotate, and live Queue-F metrics for QPS, hit-rate, counters, routing decisions, cooldowns and recent sanitized errors.

## Queue F metrics API

All metrics APIs require the Dashboard admin session cookie and never use the gateway bearer key.

- `GET /api/admin/metrics` returns the in-memory metrics snapshot.
- `GET /api/admin/dashboard/state` also embeds the same snapshot as `metrics` for Dashboard refresh.
- Metrics are runtime-only: restarting the binary resets counters.
- Recent errors are bounded by `metrics.recent_errors_limit` and are sanitized for common secret markers before storage.

## LAN run

LAN bind is fail-closed unless explicitly enabled and protected by an admin password:

```bash
go run ./cmd/simple-sub2api --bind 0.0.0.0:8080 --allow-lan --admin-password change-me
```

For LAN clients, configure the OpenAI-compatible client base URL as `http://<lan-host>:8080/v1` and use the generated `s2a_` gateway key as the Bearer token. Dashboard admin login remains separate from the gateway key.

## Docker run

Build the local image from this directory:

```bash
make docker-image
```

Run with an explicit admin password and a mounted config directory:

```bash
docker run --rm -p 8080:8080 -v simple-sub2api-config:/config -e SIMPLE_SUB2API_ADMIN_PASSWORD=change-me simple-sub2api:local
```

The container uses the same JSON config schema as local runs and stores it at `/config/simple_sub2api.config.json` by default. The image listens on `0.0.0.0:8080` inside the container, so host exposure is controlled by Docker port publishing.

## Proxy configuration

Proxy records live in `proxies` and can be managed from the Dashboard or config file. `socks5://` is normalized to `socks5h://` to avoid local DNS leakage. If an account references a configured proxy and that proxy cannot be used, gateway traffic fails closed instead of silently falling back to direct connections.

## Security non-goals

This project must not add multi-key management, API-key resale, users, groups, billing, IP allow/deny lists, quota plans, or rate-limit systems.
