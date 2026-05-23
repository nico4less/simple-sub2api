# Antigravity Manager Upstream Reference Summary

This document summarizes upstream implementation patterns from `reference/Antigravity-Manager` that are relevant to `simple_sub2api`. It focuses on six topics: API Proxy, MCP, account rotation, advanced thinking, public access, and multi-protocol support.

## 1. Overall Structure

The upstream core layers are:

- Config layer: `src-tauri/src/proxy/config.rs`
- Routing layer: `src-tauri/src/proxy/server.rs`
- Handler layer: `src-tauri/src/proxy/handlers/claude.rs`, `openai.rs`, and `gemini.rs`
- Account scheduling layer: `src-tauri/src/proxy/token_manager.rs`
- Protocol mapping layer: Claude, OpenAI, and Gemini request/response mappers

The key idea is not a single forwarding proxy. The proxy is decomposed into:

1. Entry authentication and security policy.
2. Protocol dispatch and request shaping.
3. Scheduling by model, account, quota, and session.
4. Side capabilities for MCP, thinking, and cloudflared.

---

## 2. API Proxy: Implementation and Behavior

### 2.1 Proxy Config and Hot Updates

The upstream proxy config includes `enabled`, `allow_lan_access`, `auth_mode`, `port`, `api_key`, `upstream_proxy`, `zai`, `scheduling`, `experimental`, `security_monitor`, `preferred_account_id`, `thinking_budget`, `global_system_prompt`, `image_thinking_mode`, and `proxy_pool`.

Hot updates occur after saving config: security policy and z.ai config are refreshed without restarting the service.

### 2.2 Auth Modes

`ProxyAuthMode` supports:

- `off`
- `strict`
- `all_except_health`
- `auto`

`auto` decides by LAN access:

- LAN enabled -> `all_except_health`
- Otherwise -> `off`

The middleware accepts only `Authorization: Bearer <proxy.api_key>` and does not forward the proxy key upstream.

### 2.3 Protocol Entry Points

Upstream exposes routes such as:

- `/v1/messages`
- `/v1/messages/count_tokens`
- `/mcp/web_search_prime/mcp`
- `/mcp/web_reader/mcp`
- `/mcp/zai-mcp-server/mcp`

This shows the upstream is a multi-protocol proxy with extra capabilities, not just an OpenAI relay.

---

## 3. MCP: Call Chain and Principles

### 3.1 Web Search / Web Reader: Local Reverse Proxy to z.ai

These MCP capabilities are reverse-proxied MCP endpoints:

- Local handlers are in `src-tauri/src/proxy/handlers/mcp.rs`.
- Upstream URLs are z.ai MCP endpoints.

Flow:

1. Check `proxy.zai.enabled` and `proxy.zai.mcp.enabled`.
2. Check `web_search_enabled` or `web_reader_enabled`.
3. Copy a small safe header set.
4. Replace `Authorization` with `Bearer <zai_key>`.
5. Stream the upstream response back unchanged.

This lets clients use the local proxy without configuring z.ai keys directly.

### 3.2 Vision MCP: Embedded MCP Server

Vision MCP is not a reverse proxy. It is an embedded Streamable HTTP MCP server inside the proxy.

Protocol flow:

1. `POST /mcp` + `initialize` creates a session and returns `Mcp-Session-Id`.
2. `POST /mcp` + `tools/list` returns tools.
3. `POST /mcp` + `tools/call` dispatches to tools.
4. `GET /mcp` provides SSE keepalive.
5. `DELETE /mcp` destroys the session.

Tools include UI-to-artifact conversion, screenshot text extraction, error screenshot diagnosis, technical diagram understanding, data visualization analysis, UI diff checking, image analysis, and video analysis.

Implementation principles:

- The client submits only local paths or URLs.
- Local files are encoded as `data:<mime>;base64,...`.
- Images are limited to 5 MB and videos to 8 MB.
- The upstream call uses the z.ai vision API.

### 3.3 MCP Compatibility Enhancements

The upstream also compensates for model-side incompatibilities:

- Claude injects an MCP XML bridge.
- Claude responses parse `<mcp__...>` tool calls.
- Gemini performs fuzzy MCP tool-name matching.
- JSON Schema is cleaned and adapted.
- Tool adapters provide extension points.

The principle is that models do not always strictly follow tool definitions, so the proxy compensates for hallucinated tool names and schema mismatches.

---

## 4. Account Rotation, Sticky Sessions, and Quota Awareness

### 4.1 Rotation Triggers

Rotation rules are centralized in `should_rotate_account(...)`:

- Rotate on `429 / 401 / 403 / 404 / 500`.
- Usually do not rotate on `503 / 529`.
- `GraceRetry` explicitly does not rotate.

The principle is that not every error should switch accounts. Some errors are backend overload, while others are account-level failures.

### 4.2 TokenManager Selection Logic

The scheduling core supports:

- `session_id` sticky sessions.
- `force_rotate`.
- `preferred_account_id` fixed-account mode.
- Quota filtering by target model.
- 60-second global lock avoidance.
- P2C, round-robin, priority, and least-connections policies.

Important fields include `session_accounts`, `preferred_account_id`, `model_quotas`, and `protected_models`.

### 4.3 Quota Protection and Model-Level Isolation

Upstream rotation is model-aware:

- Accounts expose remaining percentages by quota/model.
- Low-quota models are protected or blocked.
- Model-level recovery and disabling are supported.
- `quotaResetDelay` is preferred, followed by live quota refresh, then local cached lock time.

This is an account pool plus rate-limit lock plus model-level health-state scheduler, not pure round-robin.

---

## 5. Advanced Thinking: Global Thinking Control

Advanced Thinking upstream is a global configuration matrix:

- `ThinkingBudgetConfig`
- `GlobalSystemPromptConfig`
- `image_thinking_mode`

### 5.1 Thinking Budget

`ThinkingBudgetMode` supports `auto`, `passthrough`, `custom`, and `adaptive`. The default upper limit is 24576, and Gemini Flash / Thinking routes clamp budget limits.

### 5.2 Global System Prompt

The global system prompt is injected into Claude, OpenAI, and Gemini request paths.

### 5.3 Image Thinking Mode

`image_thinking_mode` explicitly enables or disables thinking on image routes to avoid unwanted thinking blocks in some image generation paths.

Summary:

- Budget control prevents upstream limit rejection.
- Global system prompt injection unifies identity and constraints.
- Image thinking mode balances quality and thinking behavior.

---

## 6. Public Access

Upstream Public Access is implemented through Cloudflared tunnels rather than directly exposing a public port.

### 6.1 Config Entry

The UI maps to Cloudflared / Public Access config, including `CloudflaredConfig` and `TunnelMode`.

### 6.2 Implementation

The Cloudflared manager:

- Checks whether cloudflared is installed.
- Supports downloading and extracting the binary.
- Supports Quick Tunnel with `tunnel --url http://localhost:<port>`.
- Supports authenticated tunnel with `tunnel run --token <token>`.
- Extracts the public URL from logs.
- Monitors process health in the background and updates state.

This means public access is not direct exposure of the local service:

1. The local proxy still serves localhost/LAN.
2. Cloudflare Tunnel exposes it safely.
3. The UI only manages tunnel lifecycle and state.

---

## 7. Multi-Protocol Support

The upstream multi-protocol support is not simple API compatibility. Each protocol has request shaping, response repair, tool compensation, and shared scheduling.

### 7.1 Protocol Entry Points

- Claude: `handle_messages`
- OpenAI: `handle_completions`
- Gemini: `handle_*`

### 7.2 Shared Capabilities

All three protocols share:

- Account rotation and rate-limit recovery.
- Thinking budget control.
- Global system prompt injection.
- Image thinking mode control.
- Upstream proxy forwarding.
- Request-body cleanup and format repair.

### 7.3 Protocol-Specific Handling

#### Claude

- Handles `thinking` and `signature`.
- Supports MCP XML bridge.
- Repairs historical messages and tool streams.

#### OpenAI

- Handles `chat/completions` compatible input.
- Constrains `thinking` and `max_tokens`.
- Also injects global system prompt.

#### Gemini

- Has the strongest schema compatibility handling.
- Applies special clamps to `thinkingBudget`, `thinkingLevel`, and image thinking.
- Supports fuzzy MCP tool-name matching and schema cleaning.

Conceptually, the upstream is a collection of protocol adapters plus a shared scheduling kernel.

---

## 8. Reusable Reference Points for `simple_sub2api`

Use these upstream ideas first:

1. **Proxy entry and security policy layering**: auth mode, LAN access, health bypass, and hot reload.
2. **Account scheduling is not pure round-robin**: session sticky, preferred account, force rotate, quota protection, and precise locking.
3. **MCP splits into reverse proxy and embedded server modes**: search/reader can be reverse-proxied, while vision can be embedded.
4. **Thinking is a global capability**: budget, system prompt, and image mode are separate controls.
5. **Multi-protocol support combines protocol specialization with shared scheduling**.
6. **Public access should use a tunnel layer rather than directly exposing local services**.

## 9. Conclusion

Antigravity Manager is fundamentally a multi-protocol, hot-updatable proxy framework with account-pool scheduling, embeddable MCP capabilities, and extensible public access channels. Its value is not a single API forwarding route, but rather:

- Configuration-driven behavior.
- Middleware that repairs protocol differences.
- A scheduler that controls account and quota risk.
- MCP and Cloudflared capabilities that turn local functionality into externally consumable services.

This structure is suitable as an upstream reference for future `simple_sub2api` feature expansion.
