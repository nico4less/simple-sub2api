# Queue D / M3 Evidence — UpstreamCompat and Gateway API

## Scope

- TASK 05 UpstreamCompat.
- TASK 13 Gateway API.

## Implementation Evidence

- Created `internal/upstreamcompat/SYNC_MANIFEST.md` to document allowed sync anchors and forbidden high-coupling upstream surfaces.
- Added `internal/upstreamcompat/openai.go` for strict OpenAI chat-completion request parsing and OpenAI-compatible error envelopes.
- Added `internal/upstreamcompat/sse.go` for OpenAI SSE `data:` extraction, multi-line payload handling, `[DONE]` filtering, and re-emission helpers.
- Added `internal/upstreamcompat/compat_shim.go` as the only mapper from local `config.Account` credentials/base URLs to upstream-style HTTP requests.
- Added `internal/gateway/gateway.go` as the local gateway shell for `/v1/chat/completions` without copying upstream `gateway_service.go` or `openai_gateway_service.go`.

## Gateway Wiring Evidence

- Gateway remains protected by the single local gateway key through existing `/v1/*` middleware.
- Routing uses task type/model/tags through `internal/routing`.
- Account selection uses `internal/accountpool` healthy account selection.
- Proxy transport uses `internal/proxyclient`, preserving configured-proxy fail-closed behavior.
- Non-streaming responses are proxied with safe header copying and quota usage accounting from `usage.total_tokens`.
- Streaming responses are proxied as `text/event-stream` and re-framed with OpenAI `data:` events.
- Upstream `401`, `403`, `404`, `408`, `409`, `429`, and `5xx` statuses trigger account cooldown and are returned as OpenAI-compatible responses when generated locally.

## Risk Gate Notes

- Did not port high-coupling upstream gateway services.
- Did not enable Anthropic Messages, OpenAI WS, billing, channel, group, sticky session, or commercial API-key management.
- Did not add PostgreSQL, Redis, Ent, billing, users, or groups.
- Gateway remains scoped to local single-user account-pool operation.

## Validation

- Target validation: `go test ./...` in `tools/simple_sub2api`.
- Contract coverage includes strict request parsing, OpenAI error envelope helpers, SSE payload extraction, non-streaming gateway proxy, streaming gateway proxy, and upstream-error cooldown behavior.
