# Account Management / Add Account Sync Manifest

## Purpose

`internal/accountcompat` is the compatibility boundary for the original `tools/sub2api` Account Management / Add Account behavior. Simple Sub2API must not invent incompatible account credential formats or import paths; local code should adapt upstream account payloads into the lightweight config model while cutting only platform dependencies such as Ent, PostgreSQL, Redis, users, groups, billing, channels, and commercial API-key management.

## Upstream Anchors

| Area | Upstream file / symbol | Local strategy | Status |
|---|---|---|---|
| Add Account UI behavior | `tools/sub2api/frontend/src/components/account/CreateAccountModal.vue` | Track source choices and credential/extra shape in tests/manifest; Dashboard implementation will call local accountcompat APIs instead of inventing a new schema. | Static parity baseline |
| OAuth UI state | `tools/sub2api/frontend/src/composables/useAccountOAuth.ts` | Keep OAuth as account-source authorization only, not Dashboard login. Runtime exchange deferred until provider services land. | Deferred runtime |
| OpenAI OAuth UI | `tools/sub2api/frontend/src/composables/useOpenAIOAuth.ts` | Preserve `openai` + `oauth` payload shape, refresh-token import shape, and Codex session category. | Static parity baseline |
| Admin routes | `tools/sub2api/backend/internal/server/routes/admin.go` account/OAuth routes | Local admin APIs stay cookie-admin-only and route through accountcompat adapters. DB/user/group/billing routes skipped. | Shimmed |
| Data import/export | `tools/sub2api/backend/internal/handler/admin/account_data.go` `DataPayload`, `DataAccount`, `DataProxy`, `validateDataAccount` | Port static schema, header validation, proxy key semantics, proxy/account validation, and import result feedback. | Ported |
| Batch add feedback | `tools/sub2api/backend/internal/handler/admin/account_handler.go` `BatchCreate` | Preserve per-account success/failure aggregation in local preview/apply result model; async privacy side effects skipped as platform runtime. | Partial static parity |
| Provider credential paths | OpenAI / Gemini / Antigravity / Anthropic OAuth services and handlers | Preserve platform/type/credential/extra compatibility envelope; live token exchange, refresh, privacy mutation, and provider probes deferred to AccountCheck / provider runtime slices. | Deferred runtime |

## Supported Static Compatibility in M1

- `sub2api-data` and legacy `sub2api-bundle` headers.
- `DataProxy` fields: `proxy_key`, `name`, `protocol`, `host`, `port`, `username`, `password`, `status`.
- `DataAccount` fields: `name`, `notes`, `platform`, `type`, `credentials`, `extra`, `proxy_key`, `concurrency`, `priority`, `rate_multiplier`, `expires_at`, `auto_pause_on_expired`.
- Account types kept from upstream: `oauth`, `setup-token`, `apikey`, `upstream`.
- Platforms tracked for Add Account parity: `openai`, `gemini`, `antigravity`, `anthropic`.
- Batch feedback keeps created/reused/failed counters and per-item errors.

## Explicitly Skipped in M1

- Ent / PostgreSQL persistence.
- Redis/idempotency locks.
- User, group, billing, channel, API-key resale surfaces.
- Live OAuth code exchange and token refresh.
- Antigravity/OpenAI async privacy mutation.
- Provider-specific account probes; these belong to Queue C AccountCheck.

## Required Evidence

- Unit tests for header validation, proxy validation, account validation, DataPayload adapter behavior, redaction, duplicate/conflict feedback, and provider credential envelopes.
- Server tests proving import preview/apply route through accountcompat rather than an incompatible local-only schema.
