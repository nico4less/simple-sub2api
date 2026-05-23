# Queue E / M4 Evidence — Dashboard API and Embedded Dashboard

## Scope

- TASK 14 Dashboard API.
- TASK 15 Web Dashboard.

## Implementation Evidence

- Added `internal/dashboard/assets.go` and `internal/dashboard/static/index.html` for Go-embedded Dashboard assets.
- Added public same-origin Dashboard shell routes: `GET /` and `GET /dashboard`.
- Added admin-only `GET /api/admin/dashboard/state` with redacted config, account pool, quota state, gateway status, and version metadata.
- Added admin-only `POST /api/admin/config/save` for full config snapshot save with `config_version` conflict handling and redacted-secret preservation.
- Added admin-only `DELETE /api/admin/accounts/{id}` for account deletion through the validated config update path.
- Added admin-only `POST /api/admin/accounts/{id}/refresh` for bounded account check and account pool refresh.
- Added admin-only `POST /api/admin/oauth-sources/{id}/refresh` and `POST /api/admin/oauth-sources/{id}/reauth` as bounded local source-status contracts. Provider-specific live OAuth exchange remains deferred to provider-runtime work.

## UI Evidence

- Single-page Dashboard covers status, gateway key reveal/rotate, accounts, account add/save, OAuth source refresh/reauth actions, quota progress bars, routing edit/test, proxy config/probe, import preview/apply, and Queue-F metrics placeholder notes.
- UI uses local inline CSS/JS only; no external CDN, remote script, or remote font is referenced.
- Visual style follows the requested dense admin layout direction: rounded cards, primary gradient buttons, badges, tables, light/dark mode, and mobile-friendly responsive grid.
- Frontend does not store plaintext credentials or gateway keys in persistent browser storage.

## Security / Risk Gate Notes

- All management state/mutation APIs remain Dashboard-admin-session protected.
- Gateway key auth is still separate from Dashboard admin session auth.
- Config mutation still uses `config_version` optimistic conflict handling.
- Redacted config snapshots can be saved without replacing existing secrets with masked placeholders. Regression coverage includes gateway key, admin password, account credentials, OAuth credentials, and inline subscription bundles.
- No external assets, database, billing, user/group, multi-tenant API-key, or commercial quota surface was introduced.

## Validation

- Target validation: `go test ./...` in `tools/simple_sub2api`.
- Test coverage includes Dashboard shell availability, Dashboard action-section markers, external-asset absence smoke check, admin auth protection, state redaction, config conflict handling, redacted-secret preservation for all currently preserved secret classes, OAuth refresh/reauth, account refresh, and account delete.
