# Simple Sub2API Queue A / M0 Evidence Package

## Scope

- Owner: Backend Core Engineer
- Covered tasks: TASK 01, TASK 03, TASK 04
- Project root: `tools/simple_sub2api`

## Implemented M0 entry points

- CLI entry: `cmd/simple-sub2api/main.go`
- Config path flag: `--config`
- Version output flag: `--version`
- Bind flags: `--bind`, `--allow-lan`
- Dashboard password flag/env: `--admin-password`, `SIMPLE_SUB2API_ADMIN_PASSWORD`
- Explicit CORS allow-list flag: `--cors-origins`
- Public health endpoint: `GET /healthz`
- Public version endpoint: `GET /version`
- Gateway-protected placeholder endpoint: `GET /v1/models`
- Dashboard admin endpoints:
  - `POST /api/admin/login`
  - `POST /api/admin/logout`
  - `GET /api/admin/me`
  - `GET /api/admin/gateway-key`
  - `POST /api/admin/gateway-key/rotate`

## Security boundary evidence encoded as tests

- Default bind is loopback.
- Non-loopback bind fails unless `server.allow_lan=true`.
- LAN bind fails unless Dashboard admin password is set.
- Gateway key uses project-owned `s2a_` style.
- Dashboard admin password must not equal gateway key.
- Wildcard CORS origin is rejected.
- `/healthz` is public.
- `/v1/*` requires Bearer gateway key.
- Gateway key cannot authenticate Dashboard admin APIs.
- Admin login issues a Dashboard session cookie.
- Gateway key rotation immediately invalidates the old gateway key.
- CORS is closed by default and only opens for explicit allowed origins.

## Validation commands

Executed from `tools/simple_sub2api`:

```bash
go test ./...
```

Result:

- `github.com/0xForce-Network/simple-sub2api/internal/config`: passed
- `github.com/0xForce-Network/simple-sub2api/internal/server`: passed
- other M0 packages: no test files

