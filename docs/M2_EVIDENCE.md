# Queue C / M2 Evidence — Proxy, Quota, Routing, AccountCheck, AccountPool

## Scope

- TASK 06 Proxy runtime boundary.
- TASK 09 Quota monitor.
- TASK 10 Routing rules.
- TASK 11 AccountCheck save-time probe path.
- TASK 12 AccountPool runtime state and hot reload.

## Implementation Evidence

- Proxy runtime is isolated in `internal/proxyclient` with strict proxy URL parsing, `socks5` → `socks5h` compatibility inherited from config normalization, HTTP/HTTPS proxy support, and an explicit SOCKS5H dialer that keeps hostname resolution remote.
- Quota runtime is isolated in `internal/quota` and returns `available`, `near_limit`, `exhausted`, `unknown`, and `error` states. Exhausted quota marks an account as switch-blocked.
- Routing runtime is isolated in `internal/routing` and supports explicit `default`, `document`, and `code` task types plus model-pattern and tag matching. It intentionally avoids semantic classification or a custom DSL.
- AccountCheck runtime is isolated in `internal/accountcheck`; static credentials are validated without leaking secrets, and accounts with `base_url` use a bounded `/v1/models` probe through the configured proxy client.
- AccountPool runtime is isolated in `internal/accountpool`; it tracks `healthy`, `error`, `disabled`, `cooldown`, and `quota` states, supports tier-aware round-robin selection, and validates candidate config before replacing the current pool.

## Admin API Evidence

- `GET/POST /api/admin/proxies`, `DELETE /api/admin/proxies/{id}`.
- `GET/POST /api/admin/quota`, `GET /api/admin/quota/state`.
- `GET/POST /api/admin/routing`, `POST /api/admin/routing/decide`.
- `GET/POST /api/admin/account-check`.
- `GET /api/admin/account-pool`.

All APIs remain Dashboard-admin-session protected and reuse `config_version` conflict handling for mutations.

## Risk Gate Notes

- Configured proxy failure does not silently fall back to direct: invalid proxy references reject candidate config or mark account check error.
- Exhausted accounts are represented as `quota` in AccountPool and excluded from healthy selection.
- Hot reload preserves the previous pool if candidate validation fails.
- No database, billing, multi-tenant user, group, or commercial API-key-management surface was added.

## Validation

- Target validation: `go test ./...` in `tools/simple_sub2api`.
