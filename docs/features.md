# Simple Sub2API Features and Design Philosophy

`Simple Sub2API` is a lightweight, high-fidelity API proxy and subscription-account scheduling gateway designed for independent developers, power users, and small collaborative teams. Unlike common multi-hop commercial relays and billing systems, this project follows a single-tenant philosophy of **de-commercialization, absolute transparency, and extreme availability**.

---

## 🌟 Core Feature Highlights

### 1. ⚡ Zero Commercial Burden and Minimal Self-Adjusting Quota Balance

- **Highlight**: The gateway completely removes the cumbersome multi-level billing, token transfer, deduction statistics, and third-party database dependencies found in commercial relay systems. Configuration stays pure and friendly, while accounts, routes, and connection state are visible in real time.
- **Scenario**: It fits personal multi-account use and small-team collaboration where some users run out of quota while others underuse theirs. The local or internal gateway performs fine-grained self-adjusting routing and maximizes the potential of idle account quota.

### 2. 🛡️ High-Fidelity Transparent Proxying Without Dilution or Degradation

- **Highlight**: The gateway keeps a strict high-fidelity proxy boundary. It does not tamper with upstream responses, reduce context, obfuscate content, inject extra system prompts, or rewrite model names in the background.
- **Scenario**: Developers doing complex IDE-assisted programming, long-context reasoning, or precision research receive 100% official native reasoning results. This preserves development logic accuracy and avoids the "dumbed-down response" problems common in commercial resale relays.

### 3. 🔒 Isolated Security Domain Against Cross-Abuse Risk Controls

- **Highlight**: The gateway uses a strict single-tenant control-plane structure and does not provide public large-scale API-key distribution across clients. All request authentication is verified inside the private local gateway.
- **Scenario**: This avoids the high-frequency IP abuse risks caused by commercial shared exits used by many users. It protects valuable subscription accounts from being labeled as abnormal traffic or high-risk proxy usage and safeguards long-term availability and data privacy for individuals and teams.

### 4. 🔄 Intelligent Routing Group Management and Smooth Rotation Scheduling

- **Highlight**: Subscription accounts can be grouped by multiple labels. Four scheduling policies are supported: Least Connections, P2C, Round-Robin, and Priority.
- **Scenario**: Built-in concurrency awareness and real-time quota-limit detection provide weekly quota protection. When an account suddenly fails because the upstream password changed, authorization expired, or the source account was locked, the gateway intercepts immediately and starts second-level failover to another healthy account in the group.

### 5. 🌐 Zero-Inbound Public Access Tunnel (Cloudflare Tunnel)

- **Highlight**: A native Cloudflare Tunnel (`cloudflared`) process supervisor and auto-governance engine are built in. It supports one-click anonymous Quick Tunnel links and Named Tunnel mode for user-owned domains.
- **Scenario**: In NAT, multi-layer LAN, home NAS, or environments with no public IP and no router port forwarding, the tunnel only establishes encrypted outbound long-lived connections. It avoids exposing inbound TCP ports, while the Proxies page embeds terminal-style real-time stderr logs and breathing status indicators for remote access and cross-location collaboration.

---

## 🧭 Design Philosophy Comparison Matrix

| Dimension | Traditional Commercial Relay Gateway | Simple Sub2API Single-Tenant Gateway |
| :--- | :--- | :--- |
| **Design Positioning** | Resale profit, multi-level proxy affiliates, complex billing and deductions | **Minimal personal/team quota balancing and small-team collaboration** |
| **Data Fidelity** | Context dilution, malicious prompt injection, cost-reduction rewrites | **100% native official high-fidelity transparent proxying with no tampering** |
| **Account Safety** | Shared exit IPs and API keys across many users, easily flagged for abuse | **Private local gateway isolation, dedicated-account use, upstream risk-control reduction** |
| **Network Exposure** | Publicly exposes server TCP ports and faces scanning attacks | **Zero-inbound secure access through Cloudflare Tunnel** |
| **Operational Complexity** | Bloated user groups, billing transfers, large database dependencies | **Lightweight Go in-memory state machine with single-JSON hot updates** |

---

> [!NOTE]
> **Compliance-aligned design**: This gateway strictly aligns with high-availability scheduling for personal subscription account pools. It does not involve commercial resale, billing splits, or multi-tenant commercial operations. Tunnel traffic depends entirely on the user's local open-source `cloudflared` binary and Cloudflare's official Zero Trust edge nodes, matching the pure personal-use high-availability boundary.
