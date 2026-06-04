# Simple Sub2API 🚀

一款高保真、轻量级、专为极客个人与小团队打造的订阅账号池调度网关。

`Simple Sub2API` 是一款专为**独立开发者、极客个人及小团队协同**设计的轻量级、高保真 API 代理与订阅账户调度网关。不同于市面上臃肿的商业化中转系统，它坚持**“单租户、高保真、绝对隐私”**的核心哲学，为您提供对个人 API 订阅池的绝对主控权。

---

## 🌟 核心亮点与设计哲学

### 1. ⚡ 零商业负担，极简自调整额度均衡
> [!TIP]
> **拒绝额度错配与资源浪费。**
> 在典型的团队或个人多设备场景下，常常出现某些账号额度被迅速用尽，而另一些账号空闲浪费的现象。Simple Sub2API 支持在本地/团队内部实现精细化并发自动分流与自调整路由，最大化释放账号潜力，且无任何三方商业计费损耗。

* **极简管理**：完全移除商业中转系统里繁琐复杂的“多级计费”、“代币积分划转”、“用户组限制”与大型数据库依赖。
* **友好界面**：配备极其清透优雅的 Glassmorphism 拟态玻璃 Dashboard 界面，账号状态、路由配置及实时网关吞吐一目了然。

### 2. 🛡️ 高保真透明代理，杜绝掺水与“降智”混淆
> [!IMPORTANT]
> **绝对诚实，拒绝上下文缩减。**
> 传统商业中转商为了控制成本，常常在后台对上下文进行恶意剪裁、注入隐蔽系统提示词或在不同模型间进行欺骗性切换。Simple Sub2API 承诺高保真透明代理，严禁任何形式的上下文缩减或模型伪装。

* **100% 官方原生响应**：直接对接官方原生端点。客户端发送什么，上游模型即接收什么；官方响应什么，IDE 终端（如 Copilot, Claude, Cursor）就流式接收什么，保障最高精度的推理和编码产出。

### 3. 🔒 独立安全域，严防账号交叉滥用风控
> [!WARNING]
> **保护您珍贵的订阅主钥，隔离风控波及。**
> 多个互不信任的客户端在公共中转出口中混杂使用相同的 API 凭证，极易触发服务商的滥用风险扫描，导致大规模封号或账户锁定。

* **单租户私有设计**：网关完全在本地或您的受控局域网内独立运行。账号校验、代理绑定和本地配置绝不向公网泄露任何元数据，提供最严密的专卡专用隐私防风控保护。

### 4. 🔄 智能路由组管理与秒级无感容灾
* **并发与配额感知**：支持根据标签划分路由组，内置四种高效路由算法：**最少连接（Least Connections）、P2C 算法、轮询（Round-Robin）和手动权重（Priority）**。
* **三振出局熔断器**：当某账号因改密失效、过期或被源头锁定时，网关瞬间拦截异常，动态对其进行冷却锁定，并在后台触发配置持久化切换（`Enabled = false`），同时在秒级内无感重试并切换至组内其他健康账号——对客户端调用全程透明无抖动。

### 5. 🌐 零开孔公网安全访问通道 (Cloudflare Tunnel)
* **安全出站长连接**：原生集成 `cloudflared` 治理引擎。无需路由器端口映射（Port Forwarding）或购买公网静态 IP，即可安全地将本地网关暴露至公网。
* **双模式灵活切换**：支持一键启动的免费匿名快速隧道（**Quick Tunnel**，自动生成 trycloudflare.com 域名）与绑定顶级域名的自建命名隧道（**Named Tunnel**），并在 Proxies 标签页中内建终端级实时 stderr 日志和拟态呼吸状态灯。

---

## 🧭 设计哲学对比矩阵 (Comparison Matrix)

| 核心维度 | 传统商业中转网关 (Commercialized) | Simple Sub2API 极客网关 (Single-Tenant) |
| :--- | :--- | :--- |
| **设计定位** | 追求转售利润、多级代理加盟、复杂计费扣费 | **追求极简、自用/团队额度平滑均衡、小团队协同** |
| **数据保真度** | 掺水削减上下文限制、恶意提示词注入以压低成本 | **100% 原生官方接口高保真透明代理，无任何篡改** |
| **账号安全性** | 万人共用出口 IP 和 API Key，极易被标记滥用封号 | **本地私有网关绝对隔离，专卡专用，防源头风控** |
| **网络暴露方式** | 直接向公网开放服务器 TCP 端口，面临扫描攻击风险 | **基于 Cloudflare Tunnel 零入站开孔安全穿透** |
| **操作复杂度** | 臃肿的用户组、账单划转设置，庞大的数据库依赖 | **轻量化 Go 内存状态机，单 JSON 配置热更新** |

---

## 🏗️ 架构与开发里程碑 (Milestones)

项目按以下迭代队列（Queue）和里程碑稳步推进：

* **Queue A / M0 (基础安全)**：支持标准 health/version 端点、本地环回绑定、启用 LAN 强 admin 密码防护、本地 gateway 单密钥鉴权，以及 CORS 默认跨域隔离。
* **Queue B / M1 (配置与基线)**：基于 `config_version` 乐观锁的单文件 `config.json` 强 Schema 读写校验、OAuth 数据导入及凭证安全脱敏预览。
* **Queue C / M2 (运行时引擎)**：Fail-fast 代理传输支持（Socks5h 远程 DNS 防止泄露）、配额状态机机制（`available`, `exhausted`）、以及池状态热重载保护。
* **Queue D / M3 (OpenAI 网关边界)**：实现 `/v1/chat/completions` 非流式与流式 SSE 协议高保真中转转发，本地异常结构化封装，及上游 429 动态置冷。
* **Queue E / M4 (内嵌 Dashboard 控制台)**：Go 语言内嵌的无外部 CDN 依赖单页 Dashboard 页面，实时状态卡片及 Queue F 运行时指标渲染。
* **Queue F / M5 (可观测性与发布)**：内存态指标记录器（秒级 QPS、命中率、脫敏异常展示）、轻量级多阶段 `Dockerfile` 及全平台 Release Cross-Compile 编译体系。

---

## 🛠️ 快速上手

### 本地开发运行
```bash
go run ./cmd/simple-sub2api --config simple_sub2api.config.json
```
*首次运行将在本地自动生成 `config.json`，并默认生成一个高强度的本地 Bearer 网关密钥 `s2a_...`*。

### 局域网接入运行 (默认 Fail-Closed 隔离)
若需要在安全的局域网内暴露此服务，请执行：
```bash
go run ./cmd/simple-sub2api --bind 0.0.0.0:8080 --allow-lan --admin-password change-me
```
*对于局域网客户端，只需将 OpenAI 兼容的 Base URL 配置为 `http://<lan-host>:8080/v1`，并将生成的 `s2a_...` 网关密钥设置为 Bearer Token 即可。*

### 容器化部署
从本地构建轻量级 Docker 镜像：
```bash
make docker-image
```
通过挂载配置并传递管理密码运行容器：
```bash
docker run --rm -p 8080:8080 -v simple-sub2api-config:/config -e SIMPLE_SUB2API_ADMIN_PASSWORD=change-me simple-sub2api:local
```

### 全平台 Release 交叉编译
```bash
make test
make linux-amd64
make darwin-amd64
make darwin-arm64
make windows-amd64
```
*生成的构件将被写入 `dist/` 目录下。*

---

## 🔌 API 核心接口参考

### 控制台管理接口 (需 Dashboard Admin Cookie 鉴权)
- `GET /api/admin/config` - 获取脱敏后的完整配置快照。
- `GET/POST /api/admin/oauth-sources` - 列表或管理第三方 OAuth 账号来源。
- `GET/POST /api/admin/subscription-sources` - 列表或管理第三方订阅地址来源。
- `GET/POST /api/admin/proxies` - 出站网络代理配置（HTTP, SOCKS5）。
- `GET/POST /api/admin/tunnel/status` - 获取 Cloudflare Tunnel 实时状态及 Stderr 滚动日志。
- `POST /api/admin/tunnel/config` - 热更新隧道配置，自动重启子进程。
- `GET /api/admin/account-pool` - 实时查看内存态路由健康状态池。
- `GET /api/admin/metrics` - 获取 QPS、账号命中率、以及脱敏后的运行时错误日志。

### 聊天转发接口 (需 Bearer `s2a_...` 网关密钥鉴权)
- `POST /v1/chat/completions` - 官方兼容的高保真流式/非流式对话代理入口。
  - *支持的自定义 Header 参数*：
    - `X-Simple-Task-Type`: 设为 `document`, `code`, 或 `default` 智能匹配组路由规则。
    - `X-Simple-Tags`: 英文逗号分隔的 Tag 标签列表，用于显式圈定账号路由边界。

### 本地 API 兼容性调试
当需要对比 Claude CLI、Roo、Cline 等客户端的请求差异时，可在本地启动时显式打开安全调试日志：

```bash
SIMPLE_SUB2API_DEBUG_API=1 go run ./cmd/simple-sub2api --config simple_sub2api.config.json
```

或使用等价参数：

```bash
go run ./cmd/simple-sub2api --config simple_sub2api.config.json --debug-api
```

开启后，每次网关入口请求会输出 `gateway_api_debug_request`，每次最终提交给上游前会输出 `gateway_api_debug_upstream_request`。两类日志会记录路径、Header 键、脱敏 Header、账号路由目标、上游 URL 形状、请求体 SHA-256、JSON 顶层键与结构摘要，用于比较客户端差异；日志不会打印 Authorization、Cookie、API Key、Token、密码或 prompt/message 明文。该开关仅用于本地兼容性诊断，默认关闭。

---

## 🚫 安全非目标与绝对边界
为了保障 `simple_sub2api` 的极简轻量性与绝对合规性，**严禁**加入以下任何带有商业中转倾向的功能：
- 多用户注册、分发管理及自定义分发 Key 体系。
- 商业计费、余额充值接口、积分扣费与财务报表模块。
- 公共 IP 白名单/黑名单鉴权策略。
- 任何形式的公共上下文压缩、重构缓存或 API 二手转售套利优化。
