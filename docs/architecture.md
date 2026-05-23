# Simple Sub2API Architecture Design and Protocol Compatibility Specification

This document records the core design decisions, de-commercialized positioning, protocol compatibility, and data-leak-prevention architecture rules for `Simple Sub2API`, the minimal personal subscription gateway. It is the highest-level technical standard for future maintenance, feature alignment, and auditable compliance-oriented development.

---

## 1. Project Positioning: Personal Use and De-Commercialization

Traditional API proxy gateways integrate complex billing, online payment, multi-tenant balance transfer, and other commercial functions for reseller scenarios. That adds significant system complexity and increases the risk of unintended data leakage.

`Simple Sub2API` follows a fully de-commercialized, single-user-isolated personal-tool model:

1. **De-commercialized design**: Multi-tenant registration, online payment, multi-level revenue sharing, and commercial detailed billing systems are completely removed.
2. **Minimal data storage**: Heavy relational databases are discarded. All configuration is stored in a single JSON file (`config.json`). Runtime counters, cooldowns, and recent request state remain in memory, enabling lightweight cold starts and high portability.
3. **Resale loophole elimination**: The product boundary is limited to personal or internal test usage, removing distribution and commercial resale loopholes by design while greatly reducing the security attack surface and compliance review boundary.

---

## 2. High-Fidelity Data and No Emulation Adulteration

Commercial relay systems often use "model dilution" or "forged emulation" to bypass limits, such as modifying prompts, compressing context, or pretending to be a private client request. These behaviors seriously damage data integrity.

`Simple Sub2API` follows a compliance principle of data transparency and protocol fidelity:

1. **Transparent proxying, lossless forwarding**: The gateway only performs transparent routing for standard protocols and strictly forbids any form of context reduction or silent model substitution.
2. **No simulated or forged behavior**: Any obfuscation code used to emulate specific clients, such as Claude Code or proprietary IDE plugin interfaces, is removed and must not be ported. All interfaces and responses follow native API specifications and remain highly auditable.
3. **Physical restriction through allowed models**: The `allowed_models` mechanism lets users explicitly constrain which models are available on a channel, ensuring exact model invocation from the foundation.

---

## 3. Protocol Compatibility and Privacy Leak Prevention

In distributed API proxy scenarios, preserving the original request protocol and preventing gateway management metadata from being sent upstream are fundamental to privacy and compliance.

`Simple Sub2API` implements the following proxy-transparency and network-boundary protections:

```
[Local Client] --(Native Request)--> [Simple Sub2API] --(Protocol-Aligned/SOCKS5h)--> [Upstream API Node]
```

### 3.1 Standards-Compliant User-Agent Compatibility and Header Sanitization

- **Standards-compliant User-Agent passthrough**: The gateway does not rely on Go's default `Go-http-client` User-Agent, which can fail to express the actual client identity and may be rejected by some protection layers. It transparently forwards the real UA fingerprint sent by the local client, or uses an explicit, recognizable generic client identifier when missing.
- **Native header protocol integrity**: All protocol-specific headers sent by standard clients are forwarded with high fidelity, such as Anthropic's required `anthropic-version` and OpenAI's `OpenAI-Beta`, preventing protocol-level validation failures caused by missing headers.
- **No management metadata leakage**: Requests sent to upstream APIs must never contain local gateway debug or management headers such as `X-Simple-Sub2API-*`. These gateway-specific fields may only appear in responses returned to the local client and are automatically stripped from upstream requests.

### 3.2 Raw Body Pass-Through

- **No re-serialization loss**: Standard JSON parsing and repackaging can reorder keys, for example Go's `json.Marshal` sorts keys alphabetically. This causes unnecessary CPU serialization overhead and breaks original request hash integrity.
- **High-fidelity pass-through**: `Simple Sub2API` directly forwards the raw `body []byte` sent by the client. This improves high-concurrency efficiency and preserves the native JSON structure emitted by the local caller, allowing upstream systems to perform the most precise signature validation.

### 3.3 SOCKS5h Domain-Resolution Privacy Boundary

- **Remote DNS resolution**: For channels configured with SOCKS5 proxies, the gateway requires the `socks5h` protocol behavior.
- **Local DNS leak prevention**: All domain resolution, such as `api.openai.com`, must happen on the remote proxy node. This prevents unencrypted local DNS leakage from the gateway host and protects enterprise or personal network access privacy.

---

## 4. Architecture Maintenance and Audit Rules

To ensure that protocol compatibility and privacy boundaries are not broken during long-term iteration, all developers and AI assistants must follow these rules:

1. **Leak-prevention audit**: Periodically verify outbound HTTP headers sent to upstream API nodes through debug logs and ensure all unnecessary local gateway identifiers are removed.
2. **Lightweight compatibility boundary**: Keep `compat_shim.go` as the only upstream-channel data compatibility layer. It should only perform standard field alignment and passthrough, and must not add aggressive, evasive, or obfuscating field rewrite logic.
3. **State persistence isolation**: Any new persistent configuration attribute must be representable in the single-file JSON config and must strictly follow the `config_version` optimistic-lock conflict handling contract.
