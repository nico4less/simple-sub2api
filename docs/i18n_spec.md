# Simple Sub2API Multi-Language i18n Specification

This document defines the multi-language internationalization architecture, key conventions, and rollout plan for the `Simple Sub2API` Vue 3 / Vite admin console. The design aligns with the upstream `Antigravity-Manager` language configuration model and treats English and Chinese as first-class supported languages.

---

## 1. Background and Goals

As `Simple Sub2API` becomes more complete, with account rotation, SOCKS5 outbound proxies, and Cloudflare Tunnel NAT-safe access, its users now include multilingual developers and collaborative teams. Hardcoded console text must be replaced by a controlled i18n-driven model:

1. **Upstream alignment**: Follow `Antigravity-Manager` language matching patterns in `src/i18n.ts`, including browser language detection, primary-language fallback, and smooth downgrades such as `zh-CN` to `zh`.
2. **Lightweight with minimal overhead**: Use the official `vue-i18n` stack for Vue 3 and Composition API to keep runtime footprint small and tree-shaking friendly.
3. **Fully local, no CDN leakage**: Locale dictionaries are bundled into local static assets embedded in the binary. No third-party translation API or external static hosting is required.

---

## 2. Technology Selection

The frontend uses the official Vue 3 i18n stack:

- **Core package**: `vue-i18n@9`, with native Vue 3 Composition API support.
- **Build helper**: `@intlify/unplugin-vue-i18n`, for Vite-time JSON validation and message precompilation.

Development install command:

```bash
npm install vue-i18n@9
```

---

## 3. i18n Engine Initialization

Create `frontend/src/i18n.ts`:

```typescript
import { createI18n } from 'vue-i18n'
import en from './locales/en.json'
import zh from './locales/zh.json'

function getBrowserLanguage(): string {
  const lang = navigator.language || (navigator as any).userLanguage || 'en'
  const normalized = lang.toLowerCase()

  if (normalized.startsWith('zh-tw') || normalized.startsWith('zh-hk')) {
    return 'zh-TW'
  }
  if (normalized.startsWith('zh')) {
    return 'zh'
  }
  if (normalized.startsWith('ja')) {
    return 'ja'
  }
  return 'en'
}

const i18n = createI18n({
  legacy: false,
  locale: localStorage.getItem('s2a_language') || getBrowserLanguage(),
  fallbackLocale: 'en',
  messages: {
    en,
    zh,
    'zh-CN': zh
  }
})

export default i18n
```

Register it in `frontend/src/main.ts`:

```typescript
import { createApp } from 'vue'
import App from './App.vue'
import router from './router'
import i18n from './i18n'

const app = createApp(App)
app.use(router)
app.use(i18n)
app.mount('#app')
```

---

## 4. Locale JSON Structure

Locale dictionaries live in `frontend/src/locales/`.

### 4.1 English Baseline Dictionary (`frontend/src/locales/en.json`)

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

### 4.2 Chinese Dictionary (`frontend/src/locales/zh.json`)

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
  }
}
```

---

## 5. Component Refactoring Standards

When extracting hardcoded text from existing pages such as `AccountsView.vue`, `GroupsView.vue`, and `ProxiesView.vue`, follow these Vue 3 rules.

### 5.1 Template Usage

Use the global `$t` helper directly:

```html
<button class="btn">{{ $t('common.save') }}</button>
```

### 5.2 `<script setup>` Usage

Use the `useI18n` Composition API:

```vue
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

function handleSave() {
  notice.value = t('common.copied')
}
</script>
```

### 5.3 Language Switcher

Add a minimal translucent glass-style language switcher in `frontend/src/components/AppShell.vue`. When the user selects `en` or `zh`:

1. Set `i18n.global.locale.value = selectedLang`.
2. Persist `localStorage.setItem('s2a_language', selectedLang)` so refreshes keep the selection.

---

## 6. Implementation Task List

### 6.1 Frontend Setup

- [ ] Add `vue-i18n@9` to `frontend/package.json`.
- [ ] Create `frontend/src/locales/en.json` and `zh.json` for common and navigation terms.

### 6.2 Engine and Registration

- [ ] Implement `frontend/src/i18n.ts` with browser language fallback.
- [ ] Register the i18n plugin in `frontend/src/main.ts`.

### 6.3 Static Text Extraction

- [ ] Refactor `frontend/src/components/AppShell.vue`.
- [ ] Refactor `frontend/src/views/DashboardView.vue`.
- [ ] Refactor `frontend/src/views/AccountsView.vue` and `GroupsView.vue`.
- [ ] Refactor `frontend/src/views/ProxiesView.vue`.

---

## 7. Reference Source and Alignment Path

Use `reference/Antigravity-Manager/src/i18n.ts` as the upstream reference for browser-language detection and `zh-CN` to `zh` dictionary mapping.
