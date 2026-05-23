# Simple Sub2API 零开孔公网安全访问设计规范 (Public Access Tunnel Spec)

本文档定义了 `Simple Sub2API`（极简个人订阅网关）中基于 **Cloudflare Tunnel (Cloudflared)** 的 NAT 穿透公网安全访问的设计规范与实现细节。该功能专为个人多设备协同和小团队协作开发而设计，能够在零路由器端口开孔（Port Forwarding）的环境下安全地将本地网关暴露到公网。

---

## 1. 业务背景与设计目标

`Simple Sub2API` 的主力运行环境通常为本地物理机、家用 NAS 或内网 Docker 容器，面临典型的 NAT（对称 NAT 或多层局域网）环境限制。为了在办公室、移动端或外部 IDE Copilot 中无缝调用部署在家中或内网的网关账号池，需要一种高可用的公网接入方案：
1. **安全零开孔 (Outbound-Only Tunnel)**：拒绝在本地路由器暴露任何 TCP/UDP 入站端口，完全利用 Cloudflare 的出站隧道（Outbound Tunnels）与边缘节点进行长连接握手，彻底隔离本地网络直接暴露在公网的安全隐患。
2. **双模式自调整接入 (Dual Tunnel Modes)**：
   * **快速隧道模式 (Quick Tunnel / 匿名)**：免去 Cloudflare 账号注册和复杂的域名解析配对，一键启动，由 Cloudflare 自动生成并颁发临时的公网二级域名（如 `https://*.trycloudflare.com`）。非常适合临时测试或个人快速开发使用。
   * **命名认证隧道模式 (Named Tunnel / 认证)**：面向长期使用的团队或高级个人用户，支持粘贴 Cloudflare Zero Trust 平台生成的 Named Tunnel Token，绑定用户自己的顶级域名并提供稳定、不变的公网访问端点。
3. **全自动证书管理与 HTTPS 接入**：公网访问地址强制采用 HTTPS，由 Cloudflare Edge 边缘节点自动完成 SSL/TLS 证书的分发与更新，客户端到隧道出口全程加密。
4. **网关状态可视与日志审计**：提供控制台内的“物理进程保活 (Self-Healing)”监控，并实时将 `cloudflared` 的运行日志汇总呈现至 Dashboard 界面，便于故障排错与审计。

---

## 2. 核心架构设计

NAT 穿透公开访问的整体运行逻辑如下图所示：

```mermaid
graph TD
    Client[外部客户端 / 移动端] -->|HTTPS 请求| CF_Edge[Cloudflare 边缘节点]
    CF_Edge -->|通过隧道反向转发| CF_Conn[Cloudflare Outbound Connection]

    subgraph 本地宿主机/容器 (NAT 内部)
        CF_Conn -->|子进程流量输入| CF_Daemon[cloudflared 守护进程]
        CF_Daemon -->|HTTP 转发| GW[Go Gateway 拦截器 (localhost:port)]

        Manager[Tunnel Manager 后台引擎] -->|os/exec 进程拉起| CF_Daemon
        Manager -->|管道实时扫描 Stderr| Parser[Log Regex 扫描器]
        Parser -->|提取临时域名| State[内存态 Tunnel Status]
    end

    GW -->|账号轮询与负载均衡| Pool[Account Pool 账号池]
    Dashboard[Vue 3 管理后台] -->|API 查询状态与日志| Manager
```

---

## 3. 配置模式定义 (Config Schema)

我们需要在 `internal/config/config.go` 中扩展专门的公网隧道配置结构体。

### 3.1 数据结构定义

```go
type Config struct {
	Server    ServerConfig    `json:"server"`
	Dashboard DashboardConfig `json:"dashboard"`
	Gateway   GatewayConfig   `json:"gateway"`
	GatewayAuth GatewayAuth   `json:"gateway_auth"`
	GatewayKeys []GatewayKey  `json:"gateway_keys"`
	Accounts  []Account       `json:"accounts"`
	Groups    []Group         `json:"groups"`
	Proxies   []ProxyConfig   `json:"proxies"`
	Quota     QuotaConfig     `json:"quota"`
	Probe     ProbeConfig     `json:"probe"`
	Metrics   MetricsConfig   `json:"metrics"`

	// [NEW] 新增公网隧道访问配置项
	Tunnel    TunnelConfig    `json:"tunnel,omitempty"`
}

type TunnelConfig struct {
	Enabled       bool   `json:"enabled"`                  // 是否开启公网穿透
	Mode          string `json:"mode"`                     // 运行模式: "quick" (trycloudflare 匿名快速隧道) 或 "named" (自建命名隧道)
	BinaryPath    string `json:"binary_path,omitempty"`    // cloudflared 可执行二进制文件的自定义绝对路径，若为空则在系统 $PATH 中搜索
	Token         string `json:"token,omitempty"`          // named 模式下所需的 Cloudflare Tunnel Token
	LogLimitLines int    `json:"log_limit_lines,omitempty"`  // 前端控制台日志输出最大行数限制，默认 100 行
}
```

### 3.2 默认配置与静态验证规约

在 `internal/config/config.go` 中进行相关默认值的填充和约束校验：
- **默认值填充**：
  - 若 `Mode` 为空，默认设为 `"quick"`。
  - 若 `LogLimitLines` 等于 0，默认设为 `100`。
- **校验约束 (Validate)**：
  - `Mode` 必须在列表 `["quick", "named"]` 之内。
  - 若 `Enabled` 为 `true` 且 `Mode` 为 `"named"`，则 `Token` 不能为空，且字符长度必须符合标准的 Cloudflare UUID/Token 规范。
  - `LogLimitLines` 必须大于等于 10 且小于等于 1000，防止日志过多造成内存膨胀。

---

## 4. 子进程隧道管理引擎 (Tunnel Process Engine)

隧道生命周期的全程控制位于新建的后端包 `internal/tunnel/tunnel.go` 中。

### 4.1 核心数据结构定义

```go
package tunnel

import (
	"sync"
	"time"
	"os/exec"
)

type Status string

const (
	StatusStopped  Status = "stopped"
	StatusStarting Status = "starting"
	StatusConnected Status = "connected"
	StatusError    Status = "error"
)

type RuntimeStatus struct {
	Status       Status    `json:"status"`
	PublicURL    string    `json:"public_url"`
	ErrorMessage string    `json:"error_message,omitempty"`
	ActiveSince  string    `json:"active_since,omitempty"`
	RecentLogs   []string  `json:"recent_logs"`
}

type Manager struct {
	mu           sync.RWMutex
	cmd          *exec.Cmd
	status       RuntimeStatus
	stopChan     chan struct{}
	logLines     []string
	localPort    int
}
```

### 4.2 核心进程治理流程

#### A. 进程拉起与参数拼装
当 `Enabled` 被设为 `true` 时，`Manager` 调用子进程拉起逻辑：
1. **端口获取**：读取当前 `Config.Server.Bind` 中配置的本地端口（例如 `1455`），解析出绑定的本地 HTTP 目标 `http://127.0.0.1:<port>`。
2. **命令行组装**：
   * **`quick` 模式**：
     `cloudflared tunnel --url http://127.0.0.1:<port>`
   * **`named` 模式**：
     `cloudflared tunnel run --token <token>`
3. **后台启动**：使用 Go 的 `os/exec` 创建子进程，并将标准错误重定向至管道：
   ```go
   cmd := exec.Command(binaryPath, args...)
   stderr, err := cmd.StderrPipe()
   ```

#### B. 临时公网 URL 正则匹配与状态流转
针对 `quick` 模式，在启动后实时解析 Stderr 管道内容，捕获 trycloudflare 生成的域名：
- **正则表达式**：`https:\/\/[a-zA-Z0-9-]+\.trycloudflare\.com`
- **解析状态演进**：
  1. 进程刚启动时，设置 `Status` 为 `StatusStarting`。
  2. 管道扫描器扫描到匹配的临时域名时，设置 `PublicURL` 为捕获的域名，设置 `Status` 为 `StatusConnected`，并记录 `ActiveSince`。
  3. 针对 `named` 模式，无法直接从日志提取 URL，可以通过扫描类似 `Registered tunnel connection` 或 `Route registered` 的日志行，判定建立连接成功并设置 `StatusConnected`。

#### C. 保活自愈 (Self-Healing Loop)
```go
func (m *Manager) startSupervisor() {
    go func() {
        backoff := time.Second
        for {
            select {
            case <-m.stopChan:
                return
            default:
                m.runTunnelProcess()
                // 运行结束说明进程异常退出，执行指数退避重试，最大 60 秒
                select {
                case <-m.stopChan:
                    return
                case <-time.After(backoff):
                    backoff = backoff * 2
                    if backoff > 60*time.Second {
                        backoff = 60 * time.Second
                    }
                }
            }
        }
    }()
}
```

---

## 5. API 接口定义 (HTTP Endpoints)

网关管理端在 `internal/server/server.go` 中新增两个控制台专享路由（受 `adminOnly` 拦截器保护）：

### 5.1 获取隧道当前运行状态与日志
* **接口**：`GET /api/admin/tunnel/status`
* **鉴权**：Admin 身份 Cookie/Token。
* **响应值 (JSON)**：
  ```json
  {
    "status": "connected",
    "public_url": "https://random-link-example.trycloudflare.com",
    "active_since": "2026-05-23T09:25:00Z",
    "recent_logs": [
      "2026-05-23T09:25:00Z INF Starting tunnel...",
      "2026-05-23T09:25:01Z INF Registered tunnel connection..."
    ]
  }
  ```

### 5.2 热更新配置应用接口
* **接口**：`POST /api/admin/tunnel/config`
* **参数 (JSON)**：
  ```json
  {
    "config_version": 4,
    "tunnel": {
      "enabled": true,
      "mode": "quick",
      "binary_path": "",
      "token": "",
      "log_limit_lines": 100
    }
  }
  ```
* **工作机制**：后端更新本地 `config.json` 的配置版本后，调用 `s.updateConfig`，并在后台自动销毁当前的 `cloudflared` 进程实例，拉起全新配置的子进程实例。

---

## 6. 管理后台设置项设计 (UI Dashboard Design)

根据系统模块的高内聚原则，网络出口代理（Outbound Proxies）与公开访问隧道（Inbound Tunnels）在概念上同属于**网络连通性模块**。因此，我们将该功能放进现有的 `ProxiesView.vue` 页面中，通过**左右切分 Tab 标签页**的形式呈现：
* **Tab 1: 出口代理配置 (Outbound Proxies)**：保留原有的 HTTP/SOCKS5 代理链管理和数据列表。
* **Tab 2: 零开孔公网安全访问 (Public Access Tunnel)**：展示 Cloudflare Tunnel 控制面板、状态呼吸灯、配置参数与实时子进程日志控制台。

### 6.1 前端状态与 Tab 变量定义
在 `frontend/src/views/ProxiesView.vue` 中新增 Tab 状态及隧道表单定义：
```typescript
const activeTab = ref<'proxies' | 'tunnel'>('proxies')

const tunnelForm = reactive({
  enabled: false,
  mode: 'quick',
  binary_path: '',
  token: '',
  log_limit_lines: 100
})
```

### 6.2 双 Tab 导航及隧道面板 UI 结构
在 `ProxiesView.vue` 根节点内，注入如下优雅的 Tab 切换横条，并在 `activeTab === 'tunnel'` 时展现穿透控制面板：

```html
<!-- Tab 切换标签条 -->
<div class="mb-4 flex border-b border-gray-200 dark:border-dark-800">
  <button type="button"
          :class="['-mb-px border-b-2 px-6 py-3 text-sm font-bold tracking-wide transition-all duration-300', activeTab === 'proxies' ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300']"
          @click="activeTab = 'proxies'">
    <Icon name="link" class="mr-2 inline" />
    出口代理配置 (Outbound Proxies)
  </button>
  <button type="button"
          :class="['-mb-px border-b-2 px-6 py-3 text-sm font-bold tracking-wide transition-all duration-300', activeTab === 'tunnel' ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300']"
          @click="activeTab = 'tunnel'">
    <Icon name="refresh" class="mr-2 inline" />
    零开孔安全通道 (Public Access Tunnel)
  </button>
</div>

<!-- 当切换到安全通道 Tab 时展示的面板 -->
<section v-if="activeTab === 'tunnel'" class="mt-4 card">
  <div class="mb-4 flex items-center justify-between border-b border-gray-150 dark:border-dark-800 pb-3">
    <div class="flex items-center gap-3">
      <div class="relative flex h-3 w-3">
        <!-- 动态呼吸灯状态指示器 -->
        <span :class="[
          'absolute inline-flex h-full w-full rounded-full opacity-75',
          tunnelStatus.status === 'connected' ? 'animate-ping bg-emerald-400' :
          tunnelStatus.status === 'starting' ? 'animate-pulse bg-amber-400' : 'bg-rose-400'
        ]"></span>
        <span :class="[
          'relative inline-flex rounded-full h-3 w-3',
          tunnelStatus.status === 'connected' ? 'bg-emerald-500' :
          tunnelStatus.status === 'starting' ? 'bg-amber-500' : 'bg-rose-500'

        ]"></span>
      </div>
      <div>
        <h3 class="text-base font-bold text-gray-900 dark:text-gray-100">零开孔公网安全访问 (Cloudflare Tunnel)</h3>
        <p class="text-xs text-gray-500">在 NAT 下安全暴露个人账号池服务，无公网入站安全隐患。</p>
      </div>
    </div>
    <input type="checkbox" v-model="tunnelForm.enabled" class="xforce-switch" @change="saveTunnelSettings" />
  </div>

  <div v-if="tunnelForm.enabled" class="grid gap-4 sm:grid-cols-2 mt-4 transition-all duration-300">
    <!-- 隧道模式选择 -->
    <label class="grid gap-1 text-sm font-semibold">
      隧道接入模式
      <select v-model="tunnelForm.mode" class="input">
        <option value="quick">Quick Tunnel (免注册快速匿名域名)</option>
        <option value="named">Named Tunnel (绑定 Zero Trust 凭证 Token)</option>
      </select>
    </label>

    <!-- 可执行文件路径 -->
    <label class="grid gap-1 text-sm font-semibold">
      Binary Path (cloudflared 物理路径)
      <input v-model="tunnelForm.binary_path" class="input" placeholder="默认使用系统环境变量 $PATH 搜索" />
    </label>

    <!-- Token 填写，仅 named 模式可见 -->
    <div v-if="tunnelForm.mode === 'named'" class="sm:col-span-2 grid gap-1 text-sm font-semibold">
      Cloudflare Zero Trust Tunnel Token
      <input v-model="tunnelForm.token" class="input" placeholder="输入从 Cloudflare Dashboard 复制的 UUID Token" />
    </div>
  </div>

  <!-- 连接成功后的高亮状态展示面板 -->
  <div v-if="tunnelStatus.status === 'connected' && tunnelForm.enabled" class="mt-4 rounded-2xl bg-emerald-50/60 dark:bg-emerald-950/20 border border-emerald-200/50 p-4 flex items-center justify-between">
    <div class="grid gap-1">
      <span class="text-xs font-semibold text-emerald-800 dark:text-emerald-400">公开访问连接已建立</span>
      <a :href="tunnelStatus.public_url" target="_blank" class="text-sm font-bold text-emerald-900 dark:text-emerald-100 hover:underline flex items-center gap-1">
        {{ tunnelStatus.public_url }}
        <Icon name="external-link" class="h-3 w-3" />
      </a>
    </div>
    <button @click="copyToClipboard(tunnelStatus.public_url)" class="btn btn-secondary border-emerald-300/30 text-emerald-800 dark:text-emerald-300 bg-white/70 hover:bg-white dark:bg-dark-900/60">
      复制链接
    </button>
  </div>

  <!-- 终端风格实时运行日志面板 -->
  <div v-if="tunnelForm.enabled" class="mt-6">
    <p class="text-xs font-bold text-gray-700 dark:text-gray-300 mb-2 uppercase tracking-widest">Tunnel Runtime Log (运行日志)</p>
    <div class="terminal-body font-mono text-[10px] leading-relaxed p-4 rounded-2xl bg-dark-950 border border-dark-900 text-dark-300 max-h-48 overflow-y-auto shadow-inner select-text">
      <div v-for="(log, idx) in tunnelStatus.recent_logs" :key="idx" class="whitespace-pre-wrap select-text">
        {{ log }}
      </div>
      <div v-if="!tunnelStatus.recent_logs || tunnelStatus.recent_logs.length === 0" class="text-dark-500 italic">
        等待日志流量输出...
      </div>
    </div>
  </div>
</section>

```

---

## 7. 评估与可行性最终校验

1. **热加载的隔离安全性**：`s.updateConfig` 方法会在底层安全地上锁。旧的 `cloudflared` 进程被完全杀掉并等待其退出通道释放后，新配置的进程实例才会被安全拉起。这完全避免了进程多重绑定造成的 NAT 连接串扰。
2. **零依赖自动退避**：对于不需要自建域名的用户，`quick` 模式的加入将使得此项目能够提供开箱即用的公网分享能力，对开发者和个人协同提供极佳的自由度。
3. **完全去商业化合规**：不涉及任何三方公共穿透商业服务器，完全依赖用户自己部署在宿主机本地的开源官方 `cloudflared` 隧道进程，没有任何数据流隐私泄露或商业倒卖行为，100% 符合个人网关池去商业化原则。

---

## 8. 验证与审计测试矩阵 (Verification Plan)

### 8.1 自动化集成测试设计
开发人员应编写 `internal/tunnel/tunnel_test.go` 对后台管理引擎进行模拟测试：
- **测试一：二进制存在性静默校验**：Mock 执行 `cloudflared --version`，测试环境在没有 `cloudflared` 二进制时，抛出规范的找不到二进制的报错消息，验证 `StatusError` 是否正常返回。
- **测试二：Stderr 临时 URL 正则匹配测试**：给日志缓冲区灌入包含 `trycloudflare` 二级域名的 Mock 日志流，校验正则解析器能否精准捕获 `https://xxx.trycloudflare.com` 并瞬间将 `Manager.status.Status` 从 `starting` 演进为 `connected`。
- **测试三：进程自恢复保活测试**：启动 Mock 子进程后主动向其发送 `SIGKILL` 信号迫使其强退，监测保活 supervisor 线程是否在预设的指数延迟后将其重新拉起，测试自愈计数与日志追加是否正常。

### 8.2 手动仪表盘与端对端验证
- **浏览器连通性验证**：在 Dashboard 中一键启动 `Quick Tunnel`，等待呼吸灯转绿，复制生成的临时公网 URL，在移动端（非本地 WiFi）上在浏览器中访问管理端后台以验证页面功能是否能完全加载。
- **API 通道压力审计**：通过新生成的公网域名，使用 `curl` 携带有效的 `GatewayKey` 向上游并发查询 `/v1/models`，审计连接数和请求成功率，证实网关代理的性能没有任何明显耗损。

---

## 9. 参考来源与实现对齐 (Reference Source & Alignment Path)

为了协助 Codex/Cline 等具体编码智能体精确对齐上游参考架构，请优先参阅：
- **上游参考说明文档**：[upstream_antigravity_manager_reference.md](file:///home/hare/dev/workspace_web/tools/simple_sub2api/docs/upstream_antigravity_manager_reference.md)（第 6 节：Public Access 章节说明）。
- **上游核心 Rust 进程管理器**：[cloudflared.rs](file:///home/hare/dev/workspace_web/reference/Antigravity-Manager/src-tauri/src/modules/cloudflared.rs) (物理源码地址)。Codex 可以重点参考其如何检测并下载二进制、构造快速及验证隧道命令行、以及后台捕获 Stderr 日志来更新内存状态的逻辑机制。
