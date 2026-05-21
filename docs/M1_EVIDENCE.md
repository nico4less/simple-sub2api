# Queue B / M1 Evidence — Config, Sources, Import Preview

## Scope

- TASK 02 Config schema and optimistic persistence.
- TASK 07 OAuth / subscription source management API baseline.
- TASK 08 subscription import preview and apply baseline.
- Revised M1 Account Management compatibility pivot: original `tools/sub2api` Account Management / Add Account is now the sync target.

## Delivered

- Expanded JSON config schema in `internal/config` covering `server`, `gateway_auth`, `dashboard`, `oauth_sources`, `subscription_sources`, `accounts`, `proxies`, `quota`, `routing`, `probe`, `metrics`, and `upstreamcompat`.
- Strict JSON decode with unknown-field rejection.
- Default filling for routing, quota thresholds, probe, metrics, upstreamcompat manifest path, and generated `s2a_` gateway key.
- Atomic config save still writes to a temporary file and renames only after validation.
- `config_version` optimistic update path returns conflict errors instead of overwriting concurrent changes.
- Proxy URL validation is fail-closed and normalizes `socks5://` to `socks5h://`.
- Admin-only source APIs:
  - `GET /api/admin/config`
  - `GET/POST /api/admin/oauth-sources`
  - `GET/DELETE /api/admin/oauth-sources/{id}`
  - `GET/POST /api/admin/subscription-sources`
  - `GET/DELETE /api/admin/subscription-sources/{id}`
- Import APIs:
  - `POST /api/admin/import/preview` returns redacted candidate sources/accounts/proxies and diff metadata.
  - `POST /api/admin/import/apply` validates `config_version`, re-parses raw import content when provided, and saves unredacted credentials only after explicit apply.
- Source deletion impact API lists derived accounts before deletion; deletion disables derived accounts instead of silently deleting credential history.
- Added `internal/accountcompat` as the Account Management / Add Account compatibility boundary.
- Added `internal/accountcompat/ADD_ACCOUNT_SYNC_MANIFEST.md` tracking upstream anchors, local shim strategy, skipped platform dependencies, and evidence requirements.
- Ported upstream-style `DataPayload`, `DataAccount`, `DataProxy`, proxy key, header validation, proxy validation, account validation, and batch import feedback counters.
- Routed `subscription_import` preview/apply through `accountcompat` adapters so the M1 import path is no longer an incompatible local-only format.

## Security Notes

- OAuth sources are account/source management only; they do not create a user-login OAuth system.
- API responses redact gateway key, dashboard password state, OAuth credentials, subscription inline bundles, and account credentials by default.
- Preview does not mutate config and does not expose full tokens.
- `config_version` conflict is explicit HTTP `409`.
- Runtime OAuth exchange/refresh, provider probes, and privacy mutation are explicitly deferred; M1 preserves static credential/extra envelopes and import semantics without adding DB/user/group/billing dependencies.

## Tests Added

- Config defaults cover Queue B modules.
- Strict decoder rejects unknown fields.
- Invalid account schema fails closed.
- SOCKS5 proxy is normalized to SOCKS5H and validated.
- Store update rejects stale `config_version`.
- Import preview redacts credentials and does not mutate config.
- Apply path preserves real credentials only after explicit apply.
- Source impact lists derived accounts.
- Server API tests cover import preview/apply and config version conflict.
- Accountcompat tests cover upstream header validation, DataAccount static rules, proxy key / SOCKS5H adaptation, credential envelope preservation, and line-token-to-refresh-token envelope generation.
- Subscription import tests cover `sub2api-data` JSON payload preview via accountcompat and credential redaction.
