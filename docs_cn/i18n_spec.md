# Simple Sub2API 多语言国际化设计规范 (Multi-Language i18n Spec)

本文档定义了 `Simple Sub2API`（极简个人订阅网关）中管理控制台（Vue 3 / Vite）的多语言国际化（i18n）架构设计、键值规范及落地路线。设计完全对齐上游 `Antigravity-Manager` 的多语言配置精髓，提供中英双语首等公民级支持。

---

## 1. 业务背景与设计目标

随着 `Simple Sub2API` 功能渐趋完备（支持账户轮换、SOCKS5 出站代理、Cloudflare Tunnel NAT 安全通道），其使用受众也扩展到了多语言开发者和协同团队。管理控制台的硬编码文本必须被替换为高度可控的国际化（i18n）驱动模型：
1. **对齐上游参考**：参照 `Antigravity-Manager` 在 `src/i18n.ts` 中的多语言匹配范式，支持浏览器语言（Navigator Language）自动识别、主语言回档（Fallback）、以及简繁体/常见小语种的平滑降级（如 `zh-CN` -> `zh`）。
2. **轻量与零开销**：在 Vue 3 (Vite + TypeScript) 环境下采用官方标准的 `vue-i18n` 库，利用 Composition API 取得最低的运行时足迹与最佳的代码树摇（Tree-Shaking）性能。
3. **完全本地化，无 CDN 泄露**：国际化词条字典完全打包在本地二进制的静态资源段中，不依赖任何第三方三方翻译 API 或外部静态托管，保障 100% 局域网离线高可用。

---

## 2. 技术选型与依赖包

在前端 `frontend` 目录中，我们将引入官方标准的 Vue 3 i18n 栈：
- **核心包**：`vue-i18n@9`（原生支持 Vue 3 组合式 API）。
- **编译时辅助**：`@intlify/unplugin-vue-i18n`（支持 Vite 编译时 JSON 校验与词条预编译，提升性能）。

安装指令（由编码智能体在开发时执行）：
```bash
npm install vue-i18n@9
```

---

## 3. 核心国际化初始化配置 (i18n Engine)

新建国际化初始化中心：`frontend/src/i18n.ts`。代码逻辑实现如下：

```typescript
import { createI18n } from 'vue-i18n'
import en from './locales/en.json'
import zh from './locales/zh.json'

// 提取并标准化用户的浏览器首选语言
function getBrowserLanguage(): string {
  const lang = navigator.language || (navigator as any).userLanguage || 'en'
  const normalized = lang.toLowerCase()

  if (normalized.startsWith('zh-tw') || normalized.startsWith('zh-hk')) {
    return 'zh-TW' // 繁体中文分流 (可选扩展，基线降级至 zh)
  }
  if (normalized.startsWith('zh')) {
    return 'zh' // 简体中文
  }
  if (normalized.startsWith('ja')) {
    return 'ja' // 日语 (可选扩展，基线降级至 en)
  }
  return 'en' // 默认回档英文
}

const i18n = createI18n({
  legacy: false, // 强制启用 Composition API 模式
  locale: localStorage.getItem('s2a_language') || getBrowserLanguage(), // 记忆化用户手动选择，若无则自动侦测
  fallbackLocale: 'en', // 默认回退语言
  messages: {
    en,
    zh,
    // 对齐上游重叠路由映射关系
    'zh-CN': zh
  }
})

export default i18n
```

在 `frontend/src/main.ts` 中注册应用：
```typescript
import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import i18n from './i18n' // 导入 i18n 配置

const app = createApp(App)
app.use(router)
app.use(i18n) // 挂载国际化组件
app.mount('#app')
```

---

## 4. 字典资源 Schema (Locale JSON Structure)

翻译字典物理放置于 `frontend/src/locales/` 目录下。

### 4.1 英文基准字典 (`frontend/src/locales/en.json`)
```json
{
  "common": {
    "save": "Save",
    "cancel": "Cancel",
    "delete": "Delete",
    "edit": "Edit",
    "create": "Create",
    "refresh": "Refresh",
    "status": "Status",
    "actions": "Actions",
    "loading": "Loading...",
    "copy": "Copy",
    "copied": "Copied to clipboard!"
  },
  "nav": {
    "dashboard": "Dashboard",
    "accounts": "Accounts",
    "groups": "Groups",
    "proxies": "Proxies & Tunnels",
    "logout": "Logout"
  },
  "proxies": {
    "tab_outbound": "Outbound Proxies",
    "tab_tunnel": "Public Access Tunnel",
    "tunnel_title": "Secure Zero-Inbound Public Access",
    "tunnel_subtitle": "Expose local account pools to the web securely via Cloudflare Tunnel.",
    "tunnel_mode": "Tunnel Mode",
    "quick_mode": "Quick Tunnel (Free Anonymous Link)",
    "named_mode": "Named Tunnel (Zero Trust Domain Token)",
    "binary_path": "Binary Path (cloudflared)",
    "token_placeholder": "Enter your Zero Trust Tunnel UUID Token",
    "connected_alert": "Public Access Established Successfully",
    "copy_link": "Copy Public Link",
    "logs_title": "Tunnel Runtime Log"
  }
}
```

### 4.2 中文翻译字典 (`frontend/src/locales/zh.json`)
```json
{
  "common": {
    "save": "保存",
    "cancel": "取消",
    "delete": "删除",
    "edit": "编辑",
    "create": "创建",
    "refresh": "刷新",
    "status": "状态",
    "actions": "操作",
    "loading": "加载中...",
    "copy": "复制",
    "copied": "已复制到剪贴板！"
  },
  "nav": {
    "dashboard": "控制面板",
    "accounts": "账号管理",
    "groups": "路由分组",
    "proxies": "代理与通道",
    "logout": "登出"
  },
  "proxies": {
    "tab_outbound": "出口代理配置 (Outbound Proxies)",
    "tab_tunnel": "零开孔安全通道 (Public Access Tunnel)",
    "tunnel_title": "零开孔公网安全访问 (Cloudflare Tunnel)",
    "tunnel_subtitle": "在 NAT 下安全暴露个人账号池服务，无公网入站安全隐患。",
    "tunnel_mode": "隧道接入模式",
    "quick_mode": "Quick Tunnel (免注册快速匿名域名)",
    "named_mode": "Named Tunnel (绑定 Zero Trust 凭证 Token)",
    "binary_path": "Binary Path (cloudflared 物理路径)",
    "token_placeholder": "输入从 Cloudflare Dashboard 复制的 UUID Token",
    "connected_alert": "公开访问连接已建立",
    "copy_link": "复制链接",
    "logs_title": "Tunnel Runtime Log (运行日志)"
  }
}
```

---

## 5. 组件代码提取重构规约 (Refactoring Standards)

当 Codex/Cline 智能体对现有页面（`AccountsView.vue`, `GroupsView.vue`, `ProxiesView.vue` 等）进行硬编码提取时，必须严格遵守以下 Vue 3 双轨规约：

### 5.1 在 `<template>` 模板中直接调用
直接使用全局自带的 `$t` 辅助函数：
```html
<!-- 重构前 -->
<button class="btn">Save</button>
<!-- 重构后 -->
<button class="btn">{{ $t('common.save') }}</button>
```

### 5.2 在 `<script setup>` 逻辑中调用
必须通过导入 `useI18n` 组合式 API 获取 `t` 转换句柄：
```vue
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

function handleSave() {
  // 在 JS/TS 代码中使用国际化词条
  notice.value = t('common.copied')
}
</script>
```

### 5.3 导航栏语种切换器设计
在 `frontend/src/components/AppShell.vue` 中内嵌一个极简、半透明的玻璃拟态**语种切换按钮**。
* 用户手动选择语种（`en` / `zh`）后：
  1. 调用 `i18n.global.locale.value = selectedLang`。
  2. 触发持久化：`localStorage.setItem('s2a_language', selectedLang)`，确保后续刷新或二次打开时状态记忆不丢失。

---

## 6. 开发任务清单与落地路线

### 6.1 前端环境准备
- [ ] 执行 `npm install vue-i18n@9`，将核心包引入 `frontend/package.json` 的 dependencies。
- [ ] 新建 `frontend/src/locales/en.json` 与 `zh.json`，完成底层通用词条（`common`）、导航词条（`nav`）的配置。

### 6.2 引擎编写与挂载
- [ ] 编写 `frontend/src/i18n.ts` 侦测器与配置初始化中心，对齐浏览器语言标准化降级规则。
- [ ] 在 `frontend/src/main.ts` 中注册 `i18n` 插件。

### 6.3 静态文本全量重构提取
- [ ] **重构一**：重构 `frontend/src/components/AppShell.vue`（导航与语种记忆切换器）。
- [ ] **重构二**：重构 `frontend/src/views/DashboardView.vue`（核心大屏仪表盘翻译）。
- [ ] **重构三**：重构 `frontend/src/views/AccountsView.vue` 与 `GroupsView.vue`（表单、状态、列表项提取）。
- [ ] **重构四**：重构 `frontend/src/views/ProxiesView.vue`（出口代理与公网穿透 Tab 的双语对齐）。

---

## 7. 参考来源与实现对齐 (Reference Source & Alignment Path)

为了协助 Codex/Cline 编码智能体快速查阅语言映射关系与降级规则，请优先参考：
* **上游多语言规则初始化器**：[i18n.ts](file:///home/hare/dev/workspace_web/reference/Antigravity-Manager/src/i18n.ts) (物理源码地址)。重点参考其对于浏览器语言的捕获规则，以及对 `zh-CN` 平滑归属于 `zh` 字典的映射写法，确保多语言逻辑的高保真承袭。
