# Simple Sub2API 🚀

A premium, high-fidelity, personal & small-team account-pool subscription gateway.

`Simple Sub2API` is a lightweight, zero-inbound-port API proxy and account rotation gateway designed specifically for **solo developers, power users, and collaborative groups**. Rejecting bloated commercial architectures, it operates on a strict **"Single-Tenant, High-Fidelity, Absolute Privacy"** philosophy, providing the ultimate control over your API subscription pools.

---

## 🌟 Key Highlights & Design Philosophy

### 1. ⚡ Zero Billing Bloat & Dynamic Quota Self-Adjustment
> [!TIP]
> **No more over-utilized or wasted accounts.**
> In typical group/individual settings, some accounts end up exhausted while others sit idle. Simple Sub2API balances dynamic concurrency across your account pools in real-time, matching team workloads without commercial overhead.

* **No Tedious Administration**: Avoids complex user levels, billing systems, credits management, or database dependencies.
* **Friendly Interface**: A gorgeous, reactive glassmorphic UI displays your active accounts, routing configurations, and live gateway throughput.

### 2. 🛡️ High-Fidelity Transparent Proxying (Direct Official API Connection)
> [!IMPORTANT]
> **Zero dilution, zero context manipulation.**
> Commercial proxy resellers frequently trim contexts, inject stealthy system prompts, or switch models behind the scenes to cut costs. Simple Sub2API strictly forbids context clipping, system-prompt interception, or model emulations.

* **100% Genuine Responses**: Connects directly to native official endpoints. What you send is exactly what the model receives; what the model responds is streamed back to your IDE (Copilot, Claude, cursor) in absolute fidelity.

### 3. 🔒 Isolated Private Security Domains (Anti-Abuse Safeguards)
> [!WARNING]
> **Protect your valuable keys from ban-waves.**
> Sharing credentials globally on shared networks exposes accounts to strict risk scanning, leading to account blocks or suspension.

* **Single-Tenant Core**: Keeps your private gateway strictly controlled under same-origin admin keys. Account validation, proxy refs, and local configs never leak external metadata, protecting your subscription accounts from risk tagging and abuse tracking.

### 4. 🔄 Smart Group Routing & Zero-Latency Failover
* **Concurrence & Quota-Awareness**: Groups accounts dynamically with scheduling algorithms: **Least Connections, P2C (Power of Two Choices), Round-Robin, or Manual Priority**.
* **3-Strike Circuit Breaker**: If an account changes password, expires, or is locked at source, the gateway intercepts the error, dynamically registers a cooldown lock, triggers a background config toggle (`Enabled = false`), and transparently re-runs the request on a healthy account in seconds—fully invisible to the client.

### 5. 🌐 Zero-Inbound-Open-Port NAT Traversal (Cloudflare Tunnel)
* **Outbound-Only long connection**: Utilizing `cloudflared`, it securely exposes your local host to the public web without requiring router port-forwarding or public static IPs.
* **Dual Operation Modes**: One-click **Quick Tunnel** (instant free anonymous URLs) or **Named Tunnel** (custom domains using Zero Trust Tokens) with live terminal logs in the Proxies Tab.

---

## 🧭 Design Philosophy Matrix

| Core Vector | Commercial API Middleware | Simple Sub2API (Geek & Solo-Team) |
| :--- | :--- | :--- |
| **Primary Goal** | Profit margins, reselling, credits accounting | **Maximized resource utilization, solo-team balance** |
| **High Fidelity** | Trims contexts, injects prompts to lower costs | **100% transparent high-fidelity native relaying** |
| **Account Safety** | High-frequency shared IP egress, causing bans | **Isolated local private domain, dedicated proxy ref** |
| **Exposure Risks** | Exposed TCP listening ports, open to scanners | **NAT-traversable secure Outbound Cloudflare Tunnel** |
| **Operational Weight** | Heavy databases (Redis/Postgres), complex setups | **Go memory state-machine, single config.json hot updates** |

---

## 🏗️ Architecture & Development Milestones

The project is structured under sequential development milestones:

* **Queue A / M0 (Base Security)**: Standard health/version endpoints, loopback bind, explicit LAN guard with admin password, gateway bearer key auth, and CORS isolation.
* **Queue B / M1 (Config & Baseline)**: Config schema validation with `config_version` optimistic locking, OAuth source records, and secure redacted previews.
* **Queue C / M2 (Runtime Engine)**: Fail-fast proxy transport (SOCKS5h remote DNS), Quota state machine (`available`, `exhausted`), and tier-aware hot reloading.
* **Queue D / M3 (OpenAI Gateway)**: `/v1/chat/completions` non-streaming and streaming SSE proxy support, error envelopes, and automated upstream-error cooldown.
* **Queue E / M4 (Dashboard Shell)**: Go-embedded single-page Dashboard shell (no external CDNs/scripts), real-time status tiles, and active metrics.
* **Queue F / M5 (Observability & Packaging)**: In-memory metrics recorders, Dockerfile multi-stage builds, and cross-platform Release Makefile targets.

---

## 🛠️ Getting Started

### Local Development Run
```bash
go run ./cmd/simple-sub2api --config simple_sub2api.config.json
```
*The initial run will generate a local `config.json` containing a secure default `s2a_` gateway key.*

### LAN Ingress (Fail-Closed Default)
To expose the server on a local network safely:
```bash
go run ./cmd/simple-sub2api --bind 0.0.0.0:8080 --allow-lan --admin-password change-me
```
For LAN clients, configure the OpenAI client base URL to `http://<lan-host>:8080/v1` and use the generated `s2a_` gateway key as the Bearer token.

### Docker Container Deploy
Build the local lightweight image:
```bash
make docker-image
```
Run with config persistence and admin password:
```bash
docker run --rm -p 8080:8080 -v simple-sub2api-config:/config -e SIMPLE_SUB2API_ADMIN_PASSWORD=change-me simple-sub2api:local
```

### Cross-Compilation Targets
```bash
make test
make linux-amd64
make darwin-amd64
make darwin-arm64
make windows-amd64
```
*Artifacts are written under `dist/`.*

---

## 🔌 API Summary Reference

### Admin Portal Endpoints (Dashboard Admin Cookie Required)
- `GET /api/admin/config` - Get redacted config.
- `GET/POST /api/admin/oauth-sources` - Manage account source configurations.
- `GET/POST /api/admin/subscription-sources` - Manage subscription bundles.
- `GET/POST /api/admin/proxies` - Outbound proxy lists (HTTP, SOCKS5).
- `GET/POST /api/admin/tunnel/status` - Live Cloudflare Tunnel status and terminal stderr logs.
- `POST /api/admin/tunnel/config` - Dynamic Cloudflare Tunnel hot reload.
- `GET /api/admin/account-pool` - View real-time active routing pool.
- `GET /api/admin/metrics` - Runtime observability QPS, hit rate, and sanitized logs.

### Chat Gateway Endpoint (Bearer `s2a_...` Auth Required)
- `POST /v1/chat/completions` - High-fidelity streaming/non-streaming chat completions.
  - *Optional Headers*:
    - `X-Simple-Task-Type`: set to `document`, `code`, or `default` to drive routing rules.
    - `X-Simple-Tags`: comma-separated tags for explicit account selection constraints.

---

## 🚫 Security Non-Goals & Absolute Bounds
To preserve the simplicity and compliance of `simple_sub2api`, the following commercial features **MUST NOT** be added:
- Multi-user authentication & custom API-key distribution models.
- Commercial billing, credits ledger, stripe integrations, or paywalls.
- IP whitelist/blacklist access lists.
- Public context compression, emulations, or API reselling optimizations.
