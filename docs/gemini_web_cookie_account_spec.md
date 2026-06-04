# Simple Sub2API Gemini Web Cookie Account Specification

This document defines the implementation specification for adding a `gemini_web_cookie` account path to `Simple Sub2API`. It translates the Gemini Web cookie reverse-engineering pattern from `tools/gemini-web-to-api/` into the existing `Simple Sub2API` account pool, routing, group rotation, quota, proxy, metrics, and dashboard architecture.

The target reader is an implementation AI agent or engineer. The specification is intentionally detailed enough to drive direct development.

---

## 1. Mission Summary

Add a new account type:

```text
type = gemini_web_cookie
metadata.platform = gemini
metadata.account_category = web-cookie
```

This account type uses browser cookies from `gemini.google.com` instead of a Gemini API key. It must:

1. Accept Gemini Web cookies (`__Secure-1PSID`, `__Secure-1PSIDTS`) in the account credential envelope.
2. Initialize a Gemini Web session by retrieving the `SNlM0e` / `at` token from Gemini Web HTML.
3. Send prompts to Gemini Web internal `StreamGenerate` using `application/x-www-form-urlencoded` with `at` and `f.req`.
4. Parse batchexecute-style Gemini Web responses into OpenAI-compatible output.
5. Reuse existing `Simple Sub2API` routing, account pool, group rotation, sticky session, quota, proxy, metrics, dashboard, and debug infrastructure.
6. Keep raw cookie logging disabled by default, but allow explicit raw-cookie debug only when the operator has enabled both debug switches.

---

## 2. Reference Behavior From `tools/gemini-web-to-api/`

The reference project is a standalone Fiber server. `Simple Sub2API` should not embed that full server. It should extract only the Gemini Web provider behavior into a local adapter package.

### 2.1 Cookie Input

Reference environment variables:

```text
GEMINI_1PSID=...
GEMINI_1PSIDTS=...
GEMINI_REFRESH_INTERVAL=30
GEMINI_MAX_RETRIES=3
```

Runtime meaning:

- `__Secure-1PSID` is the main authenticated Google session cookie.
- `__Secure-1PSIDTS` is a timestamp/session companion cookie.
- `__Secure-1PSIDTS` may be refreshed by Google cookie rotation.
- The reference caches `__Secure-1PSIDTS` by SHA-256 of `__Secure-1PSID`; `Simple Sub2API` should use a per-account runtime cache, not a shared repository-root `.cookies` folder.

### 2.2 Session Initialization

The reference flow:

1. Optionally hit `https://www.google.com/` to collect extra cookies such as `NID`.
2. Build a full `Cookie` header.
3. GET `https://gemini.google.com/?hl=en`.
4. GET `https://gemini.google.com/app?hl=en` with browser-like headers.
5. Read HTML response and extract session token with either pattern:

```text
"SNlM0e":"<token>"
["SNlM0e","<token>"]
```

6. Save the token as `at` and keep the merged cookie header for future `StreamGenerate` requests.

### 2.3 Cookie Rotation

The reference flow calls:

```text
POST https://accounts.google.com/RotateCookies
Content-Type: application/json
Body: [000,"-0000000000000000000"]
Cookie: __Secure-1PSID=...; __Secure-1PSIDTS=...
```

If response headers include a new `__Secure-1PSIDTS`, update runtime state. If status is `200` but no new cookie appears, treat it as non-fatal because Google may keep the current cookie valid.

### 2.4 Generate Request

Build the nested payload:

```json
[
  ["prompt text"],
  null,
  null,
  "gemini-model-id"
]
```

Then JSON-encode it as a string and wrap it:

```json
[null, "<inner-json-string>"]
```

Final form body:

```text
at=<session-token>&f.req=<outer-json-string>
```

Request target:

```text
POST https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?at=<session-token>
Content-Type: application/x-www-form-urlencoded;charset=utf-8
Origin: https://gemini.google.com
Referer: https://gemini.google.com/
X-Same-Domain: 1
Cookie: <merged cookie header>
```

### 2.5 Response Parsing

Parser rules:

1. Split response by newline.
2. Trim each line.
3. Strip leading `)]}'` if present.
4. Try to unmarshal each line as a JSON array.
5. For each root item array, read index `2` as a JSON string payload.
6. Unmarshal that payload.
7. Extract candidate text from nested payload candidates.
8. Keep the last valid candidate text as final answer.
9. On parse failure, return a sanitized parse error plus response shape metadata.

---

## 3. Account Schema

### 3.1 New Type

Extend account type validation to allow:

```text
gemini_web_cookie
```

Valid example:

```json
{
  "id": "acct_gemini_web_main",
  "type": "gemini_web_cookie",
  "label": "Gemini Web Main",
  "base_url": "",
  "model": "gemini-2.5-pro",
  "tier": "google_ai_pro",
  "tags": ["gemini", "web-cookie"],
  "credential": "psid=...;psidts=...",
  "metadata": {
    "platform": "gemini",
    "account_category": "web-cookie",
    "gemini_auth_mode": "web_cookie",
    "gemini_cookie_debug_allowed": false
  },
  "proxy_ref": "",
  "enabled": true
}
```

### 3.2 Credential Envelope Keys

Support these aliases:

| Meaning | Accepted keys |
| --- | --- |
| `__Secure-1PSID` | `psid`, `secure_1psid`, `__Secure-1PSID`, `gemini_1psid` |
| `__Secure-1PSIDTS` | `psidts`, `secure_1psidts`, `__Secure-1PSIDTS`, `gemini_1psidts` |
| full cookie header | `cookie`, `cookies`, `cookie_header` |
| refresh interval | `refresh_interval_minutes`, `gemini_refresh_interval` |
| max retries | `max_retries`, `gemini_max_retries` |

Validation rules:

1. `psid` is required unless a non-empty `cookie_header` contains `__Secure-1PSID=`.
2. `psidts` is recommended but may be absent if adapter rotation can obtain it.
3. If both individual cookie keys and `cookie_header` exist, individual keys win for normalized runtime cookies; `cookie_header` may still provide extra cookies.
4. Empty credential is invalid.
5. Normal API responses, export, logs, and dashboard display must redact cookie material.
6. Raw cookie logging is allowed only under the explicit debug policy in section 9.

### 3.3 Platform Resolution

`metadata.platform=gemini` remains the source of truth. Also add fallback recognition:

```text
gemini_web_cookie -> gemini
gemini_oauth -> gemini
gemini_api_key -> gemini
```

This lets `platform=gemini` groups include `gemini_web_cookie` accounts cleanly.

---

## 4. Backend Package Design

Create package:

```text
tools/simple_sub2api/internal/geminiweb
```

Recommended files:

```text
internal/geminiweb/client.go
internal/geminiweb/credentials.go
internal/geminiweb/request.go
internal/geminiweb/response.go
internal/geminiweb/cache.go
internal/geminiweb/debug.go
internal/geminiweb/client_test.go
internal/geminiweb/response_test.go
```

### 4.1 Public API

Expose a small gateway-facing API:

```go
package geminiweb

type AccountCredentials struct {
    PSID                   string
    PSIDTS                 string
    ExtraCookieHeader      string
    RefreshIntervalMinutes int
    MaxRetries             int
}

type RuntimeState struct {
    SessionToken string
    CookieHeader string
    Healthy      bool
    UpdatedAt    time.Time
}

type GenerateRequest struct {
    Model       string
    Prompt      string
    Stream      bool
    Temperature *float64
    MaxTokens   *int
}

type GenerateResponse struct {
    Text     string
    Metadata map[string]any
}

func ParseCredentials(account config.Account) (AccountCredentials, error)
func IsGeminiWebCookieAccount(account config.Account) bool
func NewClient(account config.Account, httpClient *http.Client, logger *slog.Logger, options Options) (*Client, error)
func (c *Client) Init(ctx context.Context) error
func (c *Client) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
func (c *Client) RotateCookies(ctx context.Context) error
func (c *Client) Snapshot() RuntimeState
```

### 4.2 Client Lifetime

MVP may use an in-memory account cache keyed by account ID plus credential hash. Preferred follow-up is a gateway-level `GeminiWebManager` that reuses clients per account and invalidates on config version or credential hash changes.

Cache key input:

```text
sha256(account.ID + "\n" + normalized_psid + "\n" + normalized_psidts)
```

Never use raw cookies as direct map keys.

### 4.3 Persistent Cache

Do not write `.cookies` into repository root. MVP may keep state in memory only. If persistence is added later, use a configurable runtime cache directory and never commit cache files.

---

## 5. Gateway Integration

### 5.1 Current Flow

Current gateway flow:

```text
ServeHTTP
  -> ParseChatCompletionRequest
  -> routing.Decide
  -> accountpool.SelectWithPolicyOptions
  -> forwardAttempt
  -> upstreamcompat.BuildUpstreamRequest
  -> client.Do
  -> writeStream / writeNonStream
```

### 5.2 New Branch

Add an early branch inside `forwardAttempt`:

```go
func (h Handler) forwardAttempt(r *http.Request, cfg config.Config, account config.Account, body []byte) (*http.Response, error) {
    if geminiweb.IsGeminiWebCookieAccount(account) {
        return h.forwardGeminiWebAttempt(r, cfg, account, body)
    }

    // existing OpenAI / Anthropic / OpenAI-compatible flow
}
```

`forwardGeminiWebAttempt` should:

1. Parse original request body as OpenAI-compatible chat completion.
2. Convert `messages` or `input` to a prompt string.
3. Resolve model from request model, account model, or a Gemini Web default.
4. Create/reuse Gemini Web client with account proxy-aware `http.Client`.
5. Call `Generate`.
6. Wrap output into an `http.Response` consumable by existing gateway writers.

### 5.3 Supported Endpoints

| Endpoint | MVP status | Behavior |
| --- | --- | --- |
| `/v1/chat/completions` | required | Convert messages to prompt and return OpenAI chat completion JSON. |
| `/v1/responses` | phase 2 | Convert `input` to prompt and return responses-compatible JSON. |
| `/v1/messages` | not required | Return structured unsupported account endpoint error. |

For `/v1/messages` selected with a Gemini Web account, return:

```json
{
  "error": {
    "message": "gemini_web_cookie accounts do not support /v1/messages in this release",
    "type": "invalid_request_error",
    "code": "unsupported_account_endpoint"
  }
}
```

### 5.4 Prompt Conversion

Implement:

```go
func PromptFromOpenAIChatCompletion(body []byte) (prompt string, model string, stream bool, err error)
```

Rules:

1. Preserve message order.
2. Render system messages as `System: ...`.
3. Render user messages as `User: ...`.
4. Render assistant messages as `Assistant: ...`.
5. Support string content and array content with text parts.
6. Ignore image/file parts in MVP and record safe metadata, not raw payload.
7. Reject empty final prompt.
8. Do not log raw request JSON or prompt text by default.

Example prompt:

```text
System: You are a helpful assistant.

User: Hello.

Assistant: Hi.

User: Explain GPU mining in one paragraph.
```

### 5.5 OpenAI Response Wrapper

For non-stream `/v1/chat/completions`, return OpenAI-compatible `chat.completion` JSON with one assistant message and zero token usage in MVP.

### 5.6 Streaming

Recommended MVP: simulate OpenAI SSE chunks after receiving the full Gemini Web text. End with:

```text
data: [DONE]
```

If simulation is deferred, return a documented `400` error for `stream=true`.

---

## 6. Account Check / Probe

Extend account health checking:

```go
if geminiweb.IsGeminiWebCookieAccount(account) {
    // validate credentials, create proxy-aware client, Init(ctx), return healthy/error
}
```

Probe behavior:

1. Validate credentials locally.
2. Use existing proxy resolution semantics.
3. Call Gemini Web `Init` with timeout from `ProbeConfig`.
4. Return `healthy` if `SNlM0e` / session token is extracted.
5. Return sanitized error messages otherwise.

Normal probe messages must never include raw cookies. Raw debug follows section 9.

---

## 7. Config / Import / Export Changes

### 7.1 Validation

Account type allowlist becomes:

```text
openai_api_key
oauth
openai_compatible
gemini_web_cookie
```

Additional validation:

- `type=gemini_web_cookie` requires resolved platform `gemini`.
- `type=gemini_web_cookie` requires cookie material.

### 7.2 Redaction

Redact credential keys containing:

```text
psid
psidts
cookie
cookies
cookie_header
secure-1psid
secure_1psid
```

Normal dashboard/export display example:

```text
psid=abcd...wxyz;psidts=redacted
```

### 7.3 Upstream Create Payload

Support dashboard/API payload:

```json
{
  "name": "Gemini Web Main",
  "platform": "gemini",
  "type": "web-cookie",
  "credentials": {
    "psid": "...",
    "psidts": "...",
    "tier": "google_ai_pro"
  },
  "extra": {
    "gemini_auth_mode": "web_cookie"
  }
}
```

Mapping:

```text
upstream type web-cookie -> local type gemini_web_cookie
account_category -> web-cookie
```

Line-token import may remain unchanged for MVP.

---

## 8. Dashboard / Frontend Requirements

The existing dashboard uses card-based admin forms, badges, compact controls, and dark-mode friendly Tailwind classes. New UI must fit this style and must not introduce a separate visual language.

### 8.1 Account Type Card

For platform `gemini`, add a third category card:

```text
Gemini Web Cookie
Use browser cookies from gemini.google.com
```

Suggested category value:

```text
web-cookie
```

Suggested active tone:

```text
border-purple-500 bg-purple-50 dark:bg-purple-900/20
```

### 8.2 Form Fields

When `platform=gemini` and `category=web-cookie`, show:

1. Account label.
2. Tier selector reusing existing Gemini tier options.
3. Default model selector reusing existing Gemini model whitelist.
4. `__Secure-1PSID` input or textarea.
5. `__Secure-1PSIDTS` input or textarea.
6. Optional `Full Cookie Header` textarea.
7. Optional `Refresh Interval Minutes` input.
8. Optional `Max Retries` input.
9. Existing proxy selector.
10. Existing group assignment.
11. Existing advanced raw credential envelope editor.

### 8.3 Guidance Panel

Add a compact warning panel:

```text
Gemini Web Cookie uses browser login cookies from gemini.google.com.
Use only on your own local gateway. Normal logs and exports redact cookies.
Raw cookie output appears only when debug mode explicitly enables it.
```

Suggested class style:

```text
rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-800/40 dark:bg-amber-900/20 dark:text-amber-200
```

### 8.4 Credential Construction

Frontend `buildCredential()` should emit:

```text
psid=<value>;psidts=<value>;cookie_header=<optional>;refresh_interval_minutes=<optional>;max_retries=<optional>
```

Frontend metadata:

```json
{
  "platform": "gemini",
  "account_category": "web-cookie",
  "gemini_auth_mode": "web_cookie",
  "gemini_tier": "google_ai_pro"
}
```

### 8.5 Display Badges

Show badges:

```text
Gemini
Web Cookie
Google AI Pro / Ultra / Free
```

Do not display raw cookie key names in badges except generic `Web Cookie`.

### 8.6 Static Dashboard Asset

If the frontend bundle is manually embedded into `internal/dashboard/static`, update generated static assets in the same implementation PR according to existing project workflow.

---

## 9. Debug Logging and Raw Cookie Policy

Default behavior:

- Never log raw `psid`, `psidts`, `cookie`, `cookie_header`, `Authorization`, API keys, or prompt text.
- Log only safe shape metadata.

Operator-approved raw debug exception:

- Raw cookie values may be logged only when both flags are true:

```text
SIMPLE_SUB2API_DEBUG_API=1
SIMPLE_SUB2API_DEBUG_GEMINI_WEB_RAW_COOKIE=1
```

Suggested debug event names:

```text
gemini_web_debug_init_request
gemini_web_debug_init_response
gemini_web_debug_generate_request
gemini_web_debug_generate_response
gemini_web_debug_cookie_rotation
```

Safe fields always allowed:

```text
account_id
account_label
credential_keys
has_psid
has_psidts
cookie_header_sha256
cookie_header_bytes
session_token_present
session_token_sha256
upstream_host
upstream_path
status_code
response_bytes
parse_candidate_found
duration_ms
```

Raw fields allowed only under both debug flags:

```text
raw_psid
raw_psidts
raw_cookie_header
raw_set_cookie_headers
raw_init_body_preview
raw_generate_body_preview
```

Even in raw debug mode:

- Limit raw body previews, for example 4 KiB.
- Do not write raw cookies to exports or dashboard API responses.
- Mark events with `raw_cookie_debug=true`.

---

## 10. Error Handling / Rotation Behavior

### 10.1 Auth Errors

If init fails because `SNlM0e` is absent and HTML looks like a login page, return a sanitized error:

```text
authentication failed: Gemini Web cookies invalid or expired
```

Account pool behavior:

- For `401` / `403`, mark failure and respect group rotation policy.
- Do not auto-disable on first failure.
- Existing permanent failure / three-strike logic may apply if available.

### 10.2 Rate Limits

If Gemini Web returns `429`:

- Treat as OAuth-like subscription account.
- Respect `Retry-After` if present.
- Avoid aggressive auto-disable.
- Let group failover try another eligible account.

### 10.3 Parse Errors

If response parsing fails:

- Return `502` with sanitized message.
- Log response shape and SHA-256.
- Do not mark credential permanently invalid.

---

## 11. Tests Required

### 11.1 Config Tests

1. `gemini_web_cookie` accepted when `metadata.platform=gemini`.
2. `gemini_web_cookie` rejected when platform is not `gemini`.
3. Missing `psid` rejected.
4. Redaction masks `psid`, `psidts`, `cookie_header`.
5. `platform=gemini` group can include `gemini_web_cookie` account.

### 11.2 Adapter Tests

1. Credential parser aliases.
2. Cookie header merge and normalization.
3. `SNlM0e` extraction from both known patterns.
4. Cookie rotation updates `psidts` from `Set-Cookie`.
5. HTTP 200 rotation without new cookie is non-fatal.
6. Generate request encodes `at` and `f.req` correctly.
7. Response parser extracts final text from fixture.
8. Malformed response returns sanitized parse error.

### 11.3 Gateway Tests

1. `/v1/chat/completions` with Gemini Web account calls adapter path instead of `BuildUpstreamRequest`.
2. Non-stream output is OpenAI-compatible.
3. Stream output emits OpenAI SSE chunks or documented unsupported error.
4. `/v1/messages` returns documented unsupported endpoint error.
5. Prompt conversion preserves message order.
6. Image/file parts are ignored safely in MVP.
7. `429` handling aligns with OAuth-like account cooldown behavior.

### 11.4 Account Check Tests

1. Healthy when init finds `SNlM0e`.
2. Error when login page / missing token.
3. Proxy resolution is used.
4. Normal probe output does not include raw cookie.

### 11.5 Frontend Checks

1. Gemini category cards include `web-cookie`.
2. Build credential includes `psid` and `psidts`.
3. Existing OAuth/API-key flows remain unchanged.
4. UI matches existing Simple Sub2API dashboard card/form/badge style.

---

## 12. Implementation Milestones

### Milestone A — Schema and Redaction

Likely files:

```text
internal/config/config.go
internal/accountcompat/models.go
internal/server/server.go
internal/server/server_test.go
```

Deliverables:

- Account type accepted.
- Upstream create payload maps `web-cookie` to `gemini_web_cookie`.
- Redaction covers cookie fields.
- Config tests pass.

### Milestone B — Gemini Web Adapter

Likely files:

```text
internal/geminiweb/client.go
internal/geminiweb/credentials.go
internal/geminiweb/request.go
internal/geminiweb/response.go
internal/geminiweb/debug.go
```

Deliverables:

- Init extracts session token.
- Generate returns text from fixture-backed local test server.
- Cookie rotation behavior tested.
- No raw cookie logs unless both debug flags are enabled.

### Milestone C — Gateway Path

Likely files:

```text
internal/gateway/gateway.go
internal/gateway/gateway_test.go
internal/upstreamcompat/openai.go
```

Deliverables:

- Gemini Web account selected through existing pool.
- Forwarding branches to adapter.
- Response is OpenAI-compatible.
- Routing/group/cooldown behavior preserved.

### Milestone D — Account Check and Dashboard

Likely files:

```text
internal/accountcheck/accountcheck.go
frontend/src/views/AccountsView.vue
frontend/src/api/client.ts
internal/dashboard/static/index.html
internal/dashboard/static/assets/index.js
```

Deliverables:

- Account check supports Gemini Web init probe.
- Dashboard can create/edit Gemini Web Cookie accounts.
- UI follows existing card/form/badge style.
- Static dashboard assets updated if required.

### Milestone E — Documentation and Validation

Likely files:

```text
README_zh_CN.md
docs/gemini_web_cookie_account_spec.md
```

Deliverables:

- Operator docs explain cookie capture, risks, debug flags, and limitations.
- Targeted Go tests pass.
- Frontend checks pass in the approved environment.

---

## 13. Acceptance Criteria

Implementation is acceptable when:

1. Admin can create a `Gemini Web Cookie` account from dashboard.
2. Account list redacts cookies by default.
3. Account health check initializes Gemini Web and reports healthy when `SNlM0e` is extracted.
4. Client can call `/v1/chat/completions` and receive OpenAI-compatible JSON.
5. Existing OpenAI, Anthropic, Gemini API-key/OAuth, and Antigravity flows still pass tests.
6. Group rotation can include `gemini_web_cookie` accounts in a `platform=gemini` group.
7. Normal logs never include raw cookies.
8. Raw cookies appear only when both debug flags are explicitly enabled.
9. UI matches existing dashboard card/badge/form visual style and dark-mode behavior.
10. MVP limitations are documented: text-only, no true multimodal, tool calling bridged or unsupported, streaming simulated or unsupported.

---

## 14. Explicit Non-Goals For MVP

Do not implement in first pass:

1. Full Gemini Web multimodal image/file upload.
2. True Gemini Web streaming parser if not straightforward.
3. Deep Research.
4. Full Claude `/v1/messages` compatibility on top of Gemini Web.
5. Browser automation or headless browser login.
6. Persistent raw-cookie debug storage.
7. Cross-project changes outside `tools/simple_sub2api`.

---

## 15. Developer Notes

- Keep Gemini Web protocol details isolated in `internal/geminiweb`.
- Prefer small helpers and table-driven tests.
- Do not change existing OpenAI/Anthropic compatibility logic unless required for shared abstractions.
- Do not modify translation files during first implementation unless explicitly approved. Temporary English text is acceptable under the deferred i18n strategy.
- Respect web build isolation rules. Do not run host-side `npm install` or `npm run build`; use the existing container/build workflow if static assets need regeneration.
