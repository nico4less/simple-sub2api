# Queue F / M5 Evidence — Metrics, Docker, Release Docs

## Scope

Queue F closes TASK 16 Metrics / Observability and TASK 17 Docker / Docs for `tools/simple_sub2api`.

## TASK 16 — Metrics and Observability

- Added an in-memory metrics recorder at `internal/metrics`.
- The recorder tracks QPS, uptime, total requests, success/error counts, per-account hits/errors, cooldown counts, quota switch transition counts, recent routing decisions, and a bounded recent-error ring buffer.
- Added admin-only `GET /api/admin/metrics`.
- `GET /api/admin/dashboard/state` now embeds the current metrics snapshot for single-call Dashboard refresh.
- Gateway request flow now records successful upstream calls, upstream HTTP errors, local parse/routing/proxy/upstream failures, routing decisions, cooldown events, and quota switch transitions.
- Quota switch counting is deterministic: `quota_switch_counts[accountID]` increments once when a successful response's `usage.total_tokens` moves that account from non-switch-blocked quota state into `switch_blocked` / `exhausted`; later requests while already blocked do not increment the transition counter again.
- Metrics remain memory-only and do not require PostgreSQL, Redis, Ent, or any other database.
- Recent-error messages are bounded and sanitized for common secret markers before storage.

## TASK 15 Queue-F Dashboard completion

- Replaced the prior Queue-F placeholder in the embedded Dashboard with live metrics tiles.
- Dashboard now renders QPS, total requests, success/error counts, hit-rate, uptime, per-account hit/error counters, cooldown/quota-switch counters, recent routing decisions, and recent sanitized errors.
- No external Dashboard assets were introduced.

## TASK 17 — Docker and Release Docs

- Added `Dockerfile` with a multi-stage static Go build and non-root runtime user.
- Added `Makefile` targets for local test/build, Linux/macOS/Windows cross-builds, Docker image build, and cleanup.
- README now documents local run, LAN run, Docker run, client configuration, proxy configuration, security boundaries, release build targets, and Metrics API.

## Verification

- Passed: `go test ./...` in `tools/simple_sub2api`, executed inside `golang:1.22` Docker container per workspace build-isolation rules.
- Revision validation passed after quota-switch runtime wiring with the same Docker `go test ./...` workflow.
- Docker image build is documented through `make docker-image`; host-side Docker execution was not required for the code-level Queue F changes.
