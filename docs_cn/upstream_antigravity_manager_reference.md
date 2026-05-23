# Antigravity Manager 上游参考总结

本文档汇总 [`reference/Antigravity-Manager`](../../../reference/Antigravity-Manager) 中与 `simple_sub2api` 相关的上游实现方式，重点覆盖 API Proxy、MCP、account rotate、advanced thinking、public access、Multi-Protocol Support 六个主题，作为后续对齐与扩展的参考源。

## 1. 总体结构

上游的核心分层是：

- 配置层：[`src-tauri/src/proxy/config.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/config.rs:457)
- 路由层：[`src-tauri/src/proxy/server.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/server.rs:407)
- 处理层：[`src-tauri/src/proxy/handlers/claude.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/handlers/claude.rs:559)、[`src-tauri/src/proxy/handlers/openai.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/handlers/openai.rs:433)、[`src-tauri/src/proxy/handlers/gemini.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/handlers/gemini.rs:615)
- 账号调度层：[`src-tauri/src/proxy/token_manager.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/token_manager.rs:1070)
- 协议映射层：[`src-tauri/src/proxy/mappers/claude/request.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/mappers/claude/request.rs:402)、[`src-tauri/src/proxy/mappers/openai/request.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/mappers/gemini/wrapper.rs:154)

核心思路不是“单一代理转发”，而是把代理拆成：

1. **入口认证与安全策略**
2. **按协议分流与请求整形**
3. **按模型 / 账号 / 配额 / 会话做调度**
4. **对 MCP、thinking、cloudflared 做旁路能力扩展**

---

## 2. API Proxy：如何实现与如何工作

### 2.1 代理配置与热更新

代理主配置集中在 [`ProxyConfig`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/config.rs:457)，包括：

- `enabled`
- `allow_lan_access`
- `auth_mode`
- `port`
- `api_key`
- `upstream_proxy`
- `zai`
- `scheduling`
- `experimental`
- `security_monitor`
- `preferred_account_id`
- `thinking_budget`
- `global_system_prompt`
- `image_thinking_mode`
- `proxy_pool`

热更新由保存配置后触发，见 [`docs/proxy/auth.md`](../../../reference/Antigravity-Manager/docs/proxy/auth.md:26) 与 [`src-tauri/src/commands/mod.rs`](../../../reference/Antigravity-Manager/src-tauri/src/commands/mod.rs:27) 的说明：保存配置后会刷新安全策略和 z.ai 配置，无需重启服务。

### 2.2 认证模式

认证策略定义在 [`ProxyAuthMode`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/config.rs:144) 中：

- `off`
- `strict`
- `all_except_health`
- `auto`

`auto` 会根据是否允许 LAN 访问决定最终策略：

- LAN 开启 → `all_except_health`
- 否则 → `off`

中间件仅接受 `Authorization: Bearer <proxy.api_key>` 这一客户端契约，且不把代理 key 转发给上游，详见 [`docs/proxy/auth.md`](../../../reference/Antigravity-Manager/docs/proxy/auth.md:30)。

### 2.3 协议入口

路由在 [`src-tauri/src/proxy/server.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/server.rs:407) 中注册。可见它同时暴露了：

- `/v1/messages`
- `/v1/messages/count_tokens`
- `/mcp/web_search_prime/mcp`
- `/mcp/web_reader/mcp`
- `/mcp/zai-mcp-server/mcp`

说明上游不是单纯的 OpenAI 代理，而是一个“多协议 + 附加能力”的统一代理层。

---

## 3. MCP：实现方法、调用链、原理

### 3.1 Web Search / Web Reader：本地反代到 z.ai

这两条 MCP 能力是**反向代理式 MCP**：

- 本地地址：[`src-tauri/src/proxy/handlers/mcp.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/handlers/mcp.rs:116)
- 上游地址：
  - `https://api.z.ai/api/mcp/web_search_prime/mcp`
  - `https://api.z.ai/api/mcp/web_reader/mcp`

实现方式：

1. 检查 `proxy.zai.enabled` 和 `proxy.zai.mcp.enabled`
2. 检查对应子开关 `web_search_enabled` / `web_reader_enabled`
3. 复制少量安全请求头
4. 将 `Authorization` 替换成 `Bearer <zai_key>`
5. 以流式方式把上游响应原样回传

关键代码见 [`forward_mcp(...)`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/handlers/mcp.rs:46)。

原理上，它解决的是：**客户端无需直连 z.ai，也不需要在 MCP 客户端里配置 z.ai key**，只要连本地代理即可。

### 3.2 Vision MCP：内置 MCP Server

Vision MCP 不是反代，而是**代理内嵌的 Streamable HTTP MCP Server**，核心文件：

- 路由处理：[`handle_zai_mcp_server`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/handlers/mcp.rs:377)
- 会话状态：[`ZaiVisionMcpState`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/zai_vision_mcp.rs:5)
- 工具与调用：[`src-tauri/src/proxy/zai_vision_tools.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/zai_vision_tools.rs:167)

协议流程：

1. `POST /mcp` + `initialize` → 创建 session，返回 `Mcp-Session-Id`
2. `POST /mcp` + `tools/list` → 返回工具清单
3. `POST /mcp` + `tools/call` → 路由到具体工具
4. `GET /mcp` → SSE keepalive
5. `DELETE /mcp` → 销毁 session

它支持的工具包括：

- `ui_to_artifact`
- `extract_text_from_screenshot`
- `diagnose_error_screenshot`
- `understand_technical_diagram`
- `analyze_data_visualization`
- `ui_diff_check`
- `analyze_image`
- `analyze_video`

实现原则：

- 客户端只提交本地路径或 URL
- 本地文件会被编码为 `data:<mime>;base64,...`
- 图片限 5MB，视频限 8MB
- 上游统一调用 z.ai vision API：[`https://api.z.ai/api/paas/v4/chat/completions`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/zai_vision_tools.rs:8)

### 3.3 MCP 兼容性增强：Gemini 工具名与 schema 适配

上游不仅做转发，还做了模型侧兼容增强：

- Claude 路径里注入 MCP XML bridge：[`src-tauri/src/proxy/mappers/claude/request.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/mappers/claude/request.rs:874)
- Claude 响应里解析 `<mcp__...>`：[`src-tauri/src/proxy/mappers/claude/response.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/mappers/claude/response.rs:435)
- Gemini 侧做 MCP 工具名模糊匹配：[`src-tauri/src/proxy/mappers/claude/streaming.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/mappers/claude/streaming.rs:991)
- JSON Schema 清理和适配：[`src-tauri/src/proxy/common/json_schema.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/common/json_schema.rs:100)
- Tool adapter 扩展点：[`src-tauri/src/proxy/common/tool_adapter.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/common/tool_adapter.rs:3)

原理可以概括为：**模型不总能严格遵循工具定义，所以代理要主动补偿模型幻觉与 schema 不兼容问题**。

---

## 4. account rotate：轮换、粘性会话、配额感知

### 4.1 轮换触发条件

轮换策略集中在 [`should_rotate_account(...)`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/handlers/common.rs:155)：

- 需要轮换：`429 / 401 / 403 / 404 / 500`
- 通常不轮换：`503 / 529`
- `GraceRetry` 明确不轮换

这体现的原则是：**不是所有错误都值得换号**，有些是后端过载，有些是账号级问题。

### 4.2 TokenManager 的选择逻辑

调度核心在 [`TokenManager::get_token_internal`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/token_manager.rs:1122) 一带：

- 支持 `session_id` 粘性会话
- 支持 `force_rotate`
- 支持 `preferred_account_id` 固定账号模式
- 支持按目标模型做配额过滤
- 支持 60s 全局锁定回避
- 支持 P2C / 轮询 / 优先级 / 最少连接等策略

关键字段：

- [`session_accounts`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/token_manager.rs:48)
- [`preferred_account_id`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/token_manager.rs:49)
- [`model_quotas`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/token_manager.rs:37)
- [`protected_models`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/token_manager.rs:29)

### 4.3 配额保护与模型级隔离

上游的轮换不是“简单的下一个账号”，而是带模型感知的：

- 账号会根据 quota/models 读出各模型剩余百分比
- 低配额模型会被保护或屏蔽
- 支持按模型恢复/禁用
- `quotaResetDelay` 优先，其次实时刷新配额，再次用本地缓存锁定时间

原理上，这是一个**账号池 + 限流锁 + 模型级健康状态**的组合调度器，而不是纯 round-robin。

---

## 5. Advanced Thinking：全局思维链控制

“Advanced Thinking” 在上游不是单点开关，而是一套全局配置矩阵：

- [`ThinkingBudgetConfig`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/config.rs:337)
- [`GlobalSystemPromptConfig`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/config.rs:124)
- [`image_thinking_mode`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/config.rs:549)

### 5.1 Thinking Budget

[`ThinkingBudgetMode`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/config.rs:316) 支持：

- `auto`
- `passthrough`
- `custom`
- `adaptive`

默认上限是 24576，且 Gemini Flash / Thinking 相关路径会做预算上限裁剪。

### 5.2 全局系统提示词

全局系统提示词会注入到请求里，见：

- Claude：[`src-tauri/src/proxy/mappers/claude/request.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/mappers/claude/request.rs:849)
- OpenAI：[`src-tauri/src/proxy/mappers/openai/request.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/mappers/openai/request.rs:691)
- Gemini：[`src-tauri/src/proxy/mappers/gemini/wrapper.rs`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/mappers/gemini/wrapper.rs:495)

### 5.3 Image Thinking Mode

`image_thinking_mode` 允许对图像链路显式启用/禁用 thinking，避免某些图像生成路径带入不希望的思维块。

原理总结：

- **预算控制**：防止上游模型拒绝超限请求
- **全局提示词注入**：统一身份/约束
- **图像思维模式切换**：在画质和思维链之间做全局权衡

---

## 6. Public Access：公开访问的实现方式

Public Access 在上游主要通过 Cloudflared 隧道完成，而不是直接暴露公网端口。

### 6.1 配置入口

UI 上对应 Cloudflared / Public Access 模块，核心配置在：

- [`CloudflaredConfig`](../../../reference/Antigravity-Manager/src-tauri/src/modules/cloudflared.rs:36)
- [`TunnelMode`](../../../reference/Antigravity-Manager/src-tauri/src/modules/cloudflared.rs:20)

### 6.2 实现方式

Cloudflared 管理器见 [`CloudflaredManager`](../../../reference/Antigravity-Manager/src-tauri/src/modules/cloudflared.rs:88)：

- 检查是否已安装 cloudflared
- 支持下载并解压二进制
- 支持快速隧道 `tunnel --url http://localhost:<port>`
- 支持认证隧道 `tunnel run --token <token>`
- 从日志里提取公开 URL
- 后台监控进程存活并更新状态

这意味着“公开访问”不是服务端直接监听公网，而是：

1. 本地 proxy 仍然只服务本机/局域网
2. Cloudflare Tunnel 负责安全地对外暴露
3. UI 中只管理 tunnel 生命周期与状态

---

## 7. Multi-Protocol Support：为何能同时支持 OpenAI / Claude / Gemini

上游的多协议支持不是简单的接口兼容，而是**按协议分别进行请求整形、响应修复、工具补偿和调度复用**。

### 7.1 协议入口

- Claude：[`handle_messages`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/handlers/claude.rs:559)
- OpenAI：[`handle_completions`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/handlers/openai.rs:433)
- Gemini：[`handle_*`](../../../reference/Antigravity-Manager/src-tauri/src/proxy/handlers/gemini.rs:615)

### 7.2 共同能力

三条协议共享的核心能力包括：

- 账号轮换 / 限流恢复
- 思维链预算控制
- 全局系统提示词注入
- 图像思维模式控制
- 上游代理转发
- 请求体清理与格式修正

### 7.3 协议差异化处理

#### Claude

- 处理 `thinking` / `signature`
- 支持 MCP XML bridge
- 做历史消息修复与工具流兼容

#### OpenAI

- 处理 `chat/completions` 兼容输入
- 做 `thinking` / `max_tokens` 约束
- 同样注入全局提示词

#### Gemini

- 最强调 schema 兼容
- 对 `thinkingBudget`、`thinkingLevel`、图像思维做特殊裁剪
- 兼容 MCP 工具名模糊匹配与 schema 清洗

原理上，它是一个**协议适配器集合 + 共享调度内核**。

---

## 8. 对 `simple_sub2api` 的可复用参考点

如果要对齐上游，可优先复用这些设计思想：

1. **代理入口与安全策略分层**
   - auth mode / LAN access / health bypass / hot reload
2. **账号调度不是纯轮询**
   - session sticky、preferred account、force rotate、配额保护、精确锁定
3. **MCP 要分成“反代”与“内嵌 server”两类**
   - 搜索/阅读走反代，Vision 可走内嵌 server
4. **thinking 是全局能力，不是单模型字段**
   - budget、system prompt、image mode 分离控制
5. **多协议支持要靠“协议特化 + 公共调度”组合实现**
6. **公开访问建议走隧道层，不直接暴露本地服务**

## 9. 结论

Antigravity Manager 的上游实现本质上是一个**多协议、可热更新、带账号池调度、可嵌入 MCP 能力、可扩展公开访问通道**的代理框架。其关键价值不在于单一 API 转发，而在于：

- 通过配置驱动行为
- 通过中间层修复协议差异
- 通过调度器控制账号和配额风险
- 通过 MCP / Cloudflared 把本地能力扩展成可外部消费的服务

这套结构适合作为 `simple_sub2api` 在后续功能扩展时的上游参考。
