# Simple Sub2API Upstream Sync Boundary Manifest

## Scope

This manifest is the long-term sync boundary between `tools/sub2api` and `tools/simple_sub2api`.

`tools/sub2api` is reference material only. `tools/simple_sub2api` may adapt account-management UX, personal gateway-key UX, OpenAI-compatible request behavior, and import/export concepts, but it must stay a JSON-backed personal gateway and must not drift into the full Sub2API SaaS platform.

## Product boundary

| Area | Local target | Sync stance |
|---|---|---|
| Account management | `frontend/src/views/AccountsView.vue`, `internal/accountcompat`, account CRUD/import/export APIs | Core replica target. Keep upstream-compatible account payload/import concepts while persisting through local JSON config only. |
| Personal gateway keys | `frontend/src/views/DashboardView.vue`, `frontend/src/api/client.ts`, `/api/keys` | Simplified personal gateway-key surface. Keep create/copy/disable/delete/rotate UX patterns, but no user API-key resale or group assignment model. |
| OpenAI-compatible gateway | `internal/upstreamcompat/*.go`, gateway handlers | Low-coupling compatible request/error/SSE behavior only. No upstream SaaS gateway orchestration. |
| Dashboard metrics | `internal/metrics`, dashboard state | Memory-only personal observability. No persisted usage ledger, billing usage ledger, or multi-user analytics. |
| Storage | `internal/config` JSON file store | Hard boundary. Durable state stays in JSON with optimistic `config_version`; runtime counters/cooldowns/recent usage remain memory-only. |

## Frontend upstream anchors

| Upstream source | Local file / area | Allowed adaptation | Explicit omissions |
|---|---|---|---|
| `tools/sub2api/frontend/src/views/admin/AccountsView.vue` | `frontend/src/views/AccountsView.vue` | Table layout, search/filter shape, account action grouping, import/export/test flows, status badges, dense admin-table visual language. | Auto-refresh menus may stay simplified; no CRS sync, TLS fingerprint profile management, error passthrough rule UI, column personalization backed by user preferences, or platform admin tooling. |
| `tools/sub2api/frontend/src/components/admin/account/AccountTableFilters.vue` | `frontend/src/views/AccountsView.vue` inline filters | Search/status/type filters and responsive filter-row layout. | No group filters, subscription filters, bulk commercial assignment filters, or saved server-side table preferences. |
| `tools/sub2api/frontend/src/components/admin/account/AccountTableActions.vue` | `frontend/src/views/AccountsView.vue` inline actions | Refresh, create, import, export, and test controls. | No upstream tools dropdown beyond local JSON/account operations; no SaaS admin extensions. |
| `tools/sub2api/frontend/src/components/admin/account/ImportDataModal.vue` | `frontend/src/views/AccountsView.vue` import panel and `internal/accountcompat` | Preview/apply import workflow, redacted preview behavior, per-item result feedback. | No database batch jobs, Redis idempotency locks, or background platform sync. |
| `tools/sub2api/frontend/src/components/account/CreateAccountModal.vue` | `frontend/src/views/AccountsView.vue`, `internal/accountcompat` | Provider/type/credential envelope compatibility and manual Add Account form concepts. | Live OAuth exchange/refresh, provider privacy mutation, and provider-specific SaaS probes remain outside this boundary unless separately scoped. |
| `tools/sub2api/frontend/src/components/account/EditAccountModal.vue` | `frontend/src/views/AccountsView.vue` editor | Edit credential/base URL/model/proxy/quota-policy fields with local redaction rules. | No tenant ownership, billing-plan, group, or subscription-assignment edits. |
| `tools/sub2api/frontend/src/components/account/AccountTestModal.vue` and `tools/sub2api/frontend/src/components/admin/account/AccountTestModal.vue` | `frontend/src/views/AccountsView.vue`, `internal/accountcheck` | Account health/test workflow and status result presentation. | No scheduled SaaS probe orchestration, upstream privacy mutation, or remote worker probe queue. |
| `tools/sub2api/frontend/src/components/common/DataTable.vue` | `frontend/src/components/DataTable.vue` | Reusable table slots, compact rows, loading/empty states, responsive overflow. | No server-side pagination/sort contract unless backed by JSON-local API. |
| `tools/sub2api/frontend/src/components/common/StatusBadge.vue` | `frontend/src/components/StatusBadge.vue` | Status color vocabulary and compact badge style. | No tenant/payment/subscription-specific states. |
| `tools/sub2api/frontend/src/components/common/StatCard.vue` | `frontend/src/components/MetricCard.vue` | Rounded metric-card visual pattern. | No billing revenue, order, or multi-user analytics cards. |
| `tools/sub2api/frontend/src/components/common/BaseDialog.vue`, `ConfirmDialog.vue`, form controls | Local inline dialogs/forms or future local components | Modal/form UX patterns may be mirrored if componentization is needed. | No external assets, remote fonts, or global UI plugin dependencies. |
| `tools/sub2api/frontend/src/views/user/KeysView.vue` | `frontend/src/views/DashboardView.vue`, `/api/keys` | Key table, create/copy/disable/delete/rotate concepts, masked key display, endpoint hinting where local. | No user account system, groups, subscription binding, IP whitelist/blacklist, reseller quota plans, or commercial API-key management. |
| `tools/sub2api/frontend/src/components/keys/EndpointPopover.vue` and `UseKeyModal.vue` | Future local help panel only | Endpoint usage instructions may be adapted for the personal gateway URL. | No multi-endpoint SaaS routing catalog or user onboarding tour dependencies. |

## Backend / API upstream anchors

| Upstream Source | Local File | Sync Mode | Notes |
|---|---|---|---|
| `tools/sub2api/backend/internal/service/openai_sse_data.go` | `sse.go` | clean-room compatible behavior | Preserve OpenAI SSE `data:` payload extraction, multi-line `data:` join, `[DONE]` handling, and deterministic emit behavior without importing `gjson`. |
| `tools/sub2api/backend/internal/service/gateway_request.go` request parse concepts | `openai.go` | lightweight shim only | Preserve strict JSON request parsing for `model`, `messages`, and `stream`; do not port sticky sessions, billing, channels, Anthropic/Gemini bridging, OpenAI WS, or platform-specific high-coupling transforms. |
| OpenAI-compatible error response conventions from gateway handlers | `openai.go` | local contract | Emit `{ "error": { "message", "type", "code" } }` with appropriate HTTP status. |
| `tools/sub2api/backend/internal/pkg/openai/request.go` Codex official-client detection | `compat_shim.go` | adapted header detection | Preserve Codex UA/originator family matching for OpenAI OAuth `/v1/responses` routing decisions without importing SaaS restriction/logging layers. |
| `tools/sub2api/backend/internal/service/openai_codex_transform.go` unsupported-field removal and `store=false` / `stream=true` OAuth normalization | `compat_shim.go` | minimal compatible subset | Strip ChatGPT internal Codex unsupported top-level fields and force non-compact Codex internal requests to `store=false` and `stream=true`; do not port tool transforms, forced instruction templates, image bridge, billing, or ops logging. |
| `tools/sub2api/backend/internal/handler/admin/account_data.go` import/export concepts | `internal/accountcompat` and account admin handlers | adapted schema compatibility | Preserve static `DataPayload` / account / proxy / header validation compatibility and redaction, but write only local JSON config. |
| `tools/sub2api/backend/internal/server/routes/admin.go` account/admin route concepts | local admin server routes | cookie-admin-only local API | Preserve account CRUD/test/import/export intent; do not port tenant, user, billing, group, channel, announcement, or risk-control routes. |
| Upstream key authorization concepts | local `/api/keys`, gateway key validation, config store | simplified personal key model | Persist hashed gateway keys in JSON, authorize local proxy traffic, and expose one personal admin key surface. |

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
- Do not add registration, email login, OAuth login, captcha, 2FA, invite codes, announcements, affiliates, promo/redeem flows, admin-user management, risk-control dashboards, orders, payments, subscriptions, or SaaS subscription assignment pages.
- Do not copy external CDN assets, `fonts.googleapis.com`, remote scripts, or upstream dependencies that require SaaS global app plugins.
- Do not persist counters, cooldowns, recent errors, account hit distribution, or recent usage Top 12 into JSON; those stay memory-only.
- Do not modify `tools/sub2api` during `tools/simple_sub2api` sync work. Record required upstream changes separately instead of patching the reference project.

## Local contract

- `compat_shim.go` is the only mapper between local `config.Account` records and upstream-style HTTP requests.
- `openai.go` owns OpenAI-compatible request/error envelopes.
- `sse.go` owns OpenAI SSE framing helpers and contract tests.
- `internal/accountcompat` owns static account data/import/export compatibility with upstream Account Management / Add Account behavior.
- `internal/config` is the only durable store boundary. Any new durable field must be expressible in the local JSON config and protected by `config_version` conflict handling.
- `frontend/src/views/AccountsView.vue` remains the account-management replica surface.
- `frontend/src/views/DashboardView.vue` remains both personal dashboard and simplified key-management surface; `/keys` may alias to this page rather than becoming a SaaS-style key center.

## Queue D evidence requirements

- Contract tests must cover strict request parsing, OpenAI error envelopes, SSE payload extraction, non-streaming gateway proxying, streaming gateway proxying, accountpool/routing selection, and upstream-error cooldown mapping.

## Future upstream comparison checklist

Before syncing any upstream change from `tools/sub2api`, answer every item below in the implementation or review notes:

- [ ] Does the upstream change touch one of the approved anchors listed in this manifest?
- [ ] Is the change about account CRUD/testing/import/export, table/filter/dialog UX, badge/status visuals, API module organization, simplified key create/copy/disable/delete/rotate, or OpenAI-compatible request behavior?
- [ ] Can the durable part fit in the existing JSON config model with `config_version` conflict handling?
- [ ] Are all counters, cooldowns, recent errors, and usage aggregates still memory-only?
- [ ] Does the change avoid PostgreSQL, Redis, Ent, queues, background jobs, and database migrations?
- [ ] Does the change avoid users, groups, tenant ownership, billing, payments, orders, subscriptions, affiliates, announcements, risk-control, promo/redeem, and registration/login systems?
- [ ] Does the change avoid external assets, remote fonts, CDNs, and global SaaS plugin dependencies?
- [ ] Does the change preserve cookie-admin-only local management and avoid adding a SaaS user/admin role split?
- [ ] Are credentials, gateway keys, prompts, responses, and upstream secrets redacted from logs, preview APIs, metrics, and tests?
- [ ] Are upstream source files and intentionally omitted features recorded in this manifest or a task-specific evidence file?
- [ ] Are local regression tests updated for the adapted behavior?

If any answer is "no", do not sync the upstream change into `tools/simple_sub2api` without a new task explicitly expanding the boundary.
