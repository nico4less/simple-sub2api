# Simple Sub2API UpstreamCompat Sync Manifest

## Scope

This directory is the only Queue D boundary for OpenAI-compatible upstream behavior copied or adapted from `tools/sub2api`.

## Approved low-coupling anchors

| Upstream Source | Local File | Sync Mode | Notes |
|---|---|---|---|
| `tools/sub2api/backend/internal/service/openai_sse_data.go` | `sse.go` | clean-room compatible behavior | Preserve OpenAI SSE `data:` payload extraction, multi-line `data:` join, `[DONE]` handling, and deterministic emit behavior without importing `gjson`. |
| `tools/sub2api/backend/internal/service/gateway_request.go` request parse concepts | `openai.go` | lightweight shim only | Preserve strict JSON request parsing for `model`, `messages`, and `stream`; do not port sticky sessions, billing, channels, Anthropic/Gemini bridging, OpenAI WS, or platform-specific high-coupling transforms. |
| OpenAI-compatible error response conventions from gateway handlers | `openai.go` | local contract | Emit `{ "error": { "message", "type", "code" } }` with appropriate HTTP status. |

## Explicit non-sync surfaces

- Do not copy `gateway_service.go`, `openai_gateway_service.go`, or high-coupling account/channel/billing gateway files.
- Do not enable Anthropic Messages, OpenAI WebSocket, channel management, billing, group isolation, sticky sessions, or commercial API-key management.
- Do not introduce PostgreSQL, Redis, Ent, billing, user, group, or multi-tenant API-key dependencies.

## Local contract

- `compat_shim.go` is the only mapper between local `config.Account` records and upstream-style HTTP requests.
- `openai.go` owns OpenAI-compatible request/error envelopes.
- `sse.go` owns OpenAI SSE framing helpers and contract tests.

## Queue D evidence requirements

- Contract tests must cover strict request parsing, OpenAI error envelopes, SSE payload extraction, non-streaming gateway proxying, streaming gateway proxying, accountpool/routing selection, and upstream-error cooldown mapping.

