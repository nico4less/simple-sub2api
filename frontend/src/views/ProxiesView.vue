<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import {
  createProxy,
  deleteProxy,
  loadTunnelStatus,
  loadDashboardState,
  loadProxies,
  saveTunnelConfig,
  updateProxy,
  type AdminConfig,
  type ProxyConfig,
  type TunnelConfig,
  type TunnelRuntimeStatus
} from '@/api/client'
import AppShell from '@/components/AppShell.vue'
import DataTable from '@/components/DataTable.vue'
import Icon from '@/components/Icon.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { copyTextToClipboard } from '@/composables/useClipboard'
import type { Column } from '@/types/ui'

const { t } = useI18n()

type ProxyProtocol = 'http' | 'https' | 'socks5' | 'socks5h'
type ActiveTab = 'proxies' | 'tunnel'

interface ProxyRow extends Record<string, unknown> {
  id: string
  name: string
  protocol: ProxyProtocol
  host: string
  port: number
  username: string
  password: string
  status: 'active' | 'disabled'
  url: string
  accountCount: number
}

interface ParsedProxy {
  protocol: ProxyProtocol
  host: string
  port: number
  username: string
  password: string
}

const loading = ref(false)
const saving = ref(false)
const error = ref('')
const notice = ref('')
const configVersion = ref(0)
const rawProxies = ref<ProxyConfig[]>([])
const accountProxyRefs = ref<Record<string, number>>({})
const search = ref('')
const protocolFilter = ref('')
const statusFilter = ref('')
const createOpen = ref(false)
const editOpen = ref(false)
const createMode = ref<'standard' | 'batch'>('standard')
const createPasswordVisible = ref(false)
const editPasswordVisible = ref(false)
const editingID = ref('')
const batchInput = ref('')
const selected = reactive(new Set<string>())
const showExportDialog = ref(false)
const exportOutput = ref('')
const activeTab = ref<ActiveTab>('proxies')
const tunnelSaving = ref(false)
const tunnelLoading = ref(false)
const tunnelForm = reactive<TunnelConfig>({
  enabled: false,
  mode: 'quick',
  binary_path: '',
  token: '',
  log_limit_lines: 100
})
const tunnelStatus = ref<TunnelRuntimeStatus>({
  status: 'stopped',
  public_url: '',
  recent_logs: []
})

const form = reactive({
  id: '',
  name: '',
  protocol: 'http' as ProxyProtocol,
  host: '',
  port: 8080,
  username: '',
  password: '',
  status: 'active' as 'active' | 'disabled'
})

const batchParse = computed(() => {
  const lines = batchInput.value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean)
  const seen = new Set<string>()
  const proxies: ParsedProxy[] = []
  let invalid = 0
  let duplicate = 0
  lines.forEach((line) => {
    const parsed = parseProxyURL(line)
    if (!parsed) {
      invalid += 1
      return
    }
    const key = `${parsed.protocol}|${parsed.host}|${parsed.port}|${parsed.username}|${parsed.password}`
    if (seen.has(key)) {
      duplicate += 1
      return
    }
    seen.add(key)
    proxies.push(parsed)
  })
  return { total: lines.length, valid: proxies.length, invalid, duplicate, proxies }
})

const columns = computed<Column<ProxyRow>[]>(() => [
  { key: 'name', label: t('proxies.columns.name') },
  { key: 'protocol', label: t('proxies.columns.protocol') },
  { key: 'host', label: t('proxies.columns.address') },
  { key: 'username', label: t('proxies.columns.auth') },
  { key: 'accountCount', label: t('proxies.columns.accounts') },
  { key: 'status', label: t('common.status') },
  { key: 'actions', label: t('common.actions') }
])

const rows = computed<ProxyRow[]>(() => rawProxies.value.map(toProxyRow))
const filteredRows = computed(() => {
  const q = search.value.trim().toLowerCase()
  return rows.value.filter((row) => {
    const matchesSearch = !q || [row.id, row.name, row.protocol, row.host, row.username, row.url].join(' ').toLowerCase().includes(q)
    const matchesProtocol = !protocolFilter.value || row.protocol === protocolFilter.value
    const matchesStatus = !statusFilter.value || row.status === statusFilter.value
    return matchesSearch && matchesProtocol && matchesStatus
  })
})

const counts = computed(() => {
  const total = rows.value.length
  const active = rows.value.filter((row) => row.status === 'active').length
  const withAuth = rows.value.filter((row) => row.username || row.password).length
  const linked = rows.value.filter((row) => row.accountCount > 0).length
  return { total, active, withAuth, linked }
})

const tunnelStatusLabel = computed(() => {
  switch (tunnelStatus.value.status) {
    case 'connected':
      return t('proxies.tunnel.status.connected')
    case 'starting':
      return t('proxies.tunnel.status.starting')
    case 'error':
      return t('proxies.tunnel.status.error')
    default:
      return t('proxies.tunnel.status.stopped')
  }
})

const tunnelStatusTone = computed(() => {
  switch (tunnelStatus.value.status) {
    case 'connected':
      return 'emerald'
    case 'starting':
      return 'amber'
    case 'error':
      return 'rose'
    default:
      return 'slate'
  }
})

const tunnelPublicURL = computed(() => tunnelStatus.value.public_url || '')

function parseProxyURL(raw: string): ParsedProxy | null {
  try {
    const parsed = new URL(raw.trim())
    const protocol = parsed.protocol.replace(':', '').toLowerCase() as ProxyProtocol
    if (!['http', 'https', 'socks5', 'socks5h'].includes(protocol)) return null
    const port = Number(parsed.port)
    if (!parsed.hostname || !Number.isFinite(port) || port < 1 || port > 65535) return null
    return {
      protocol,
      host: parsed.hostname,
      port,
      username: decodeURIComponent(parsed.username || ''),
      password: decodeURIComponent(parsed.password || '')
    }
  } catch {
    return null
  }
}

function buildProxyURL(value: { protocol: string; host: string; port: number; username?: string; password?: string }) {
  const auth = value.username || value.password ? `${encodeURIComponent(value.username || '')}:${encodeURIComponent(value.password || '')}@` : ''
  const scheme = value.protocol === 'socks5' ? 'socks5h' : value.protocol
  return `${scheme}://${auth}${value.host.trim()}:${value.port}`
}

function stableProxyID(parsed: Pick<ParsedProxy, 'protocol' | 'host' | 'port'>, name = '') {
  const base = name || `${parsed.protocol}_${parsed.host}_${parsed.port}`
  const slug = base.toLowerCase().replace(/[^a-z0-9_.-]+/g, '_').replace(/^[_\-.]+|[_\-.]+$/g, '').slice(0, 48)
  return `proxy_${slug || 'local'}`
}

function toProxyRow(proxy: ProxyConfig): ProxyRow {
  const parsed = parseProxyURL(proxy.url)
  const metadata = proxy as ProxyConfig & { name?: string; status?: 'active' | 'disabled' }
  const name = metadata.name || proxy.id
  return {
    id: proxy.id,
    name,
    protocol: parsed?.protocol || 'http',
    host: parsed?.host || proxy.url,
    port: parsed?.port || 0,
    username: parsed?.username || '',
    password: parsed?.password || '',
    status: metadata.status || 'active',
    url: proxy.url,
    accountCount: accountProxyRefs.value[proxy.id] || 0
  }
}

function proxyFromForm(): ProxyConfig {
  const id = form.id.trim() || stableProxyID(form, form.name)
  return {
    id,
    url: buildProxyURL(form)
  }
}

function resetForm() {
  form.id = ''
  form.name = ''
  form.protocol = 'http'
  form.host = ''
  form.port = 8080
  form.username = ''
  form.password = ''
  form.status = 'active'
  createPasswordVisible.value = false
  editPasswordVisible.value = false
}

async function loadAll() {
  loading.value = true
  error.value = ''
  try {
    const [state, proxies, status] = await Promise.all([loadDashboardState(), loadProxies(), loadTunnelStatus()])
    configVersion.value = state.config.config_version
    rawProxies.value = proxies
    syncTunnelForm(state.config.tunnel)
    tunnelStatus.value = status
    const refs: Record<string, number> = {}
    ;(state.config.accounts || []).forEach((account) => {
      if (account.proxy_ref) refs[account.proxy_ref] = (refs[account.proxy_ref] || 0) + 1
    })
    accountProxyRefs.value = refs
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

async function refreshTunnelStatus() {
  tunnelLoading.value = true
  try {
    tunnelStatus.value = await loadTunnelStatus()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    tunnelLoading.value = false
  }
}

function syncTunnelForm(config?: TunnelConfig) {
  tunnelForm.enabled = Boolean(config?.enabled)
  tunnelForm.mode = config?.mode || 'quick'
  tunnelForm.binary_path = config?.binary_path || ''
  tunnelForm.token = config?.token || ''
  tunnelForm.log_limit_lines = config?.log_limit_lines || 100
}

function tunnelPayload(): TunnelConfig {
  return {
    enabled: tunnelForm.enabled,
    mode: tunnelForm.mode,
    binary_path: tunnelForm.binary_path?.trim() || '',
    token: tunnelForm.token?.trim() || '',
    log_limit_lines: Number(tunnelForm.log_limit_lines || 100)
  }
}

async function saveTunnelSettings() {
  tunnelSaving.value = true
  error.value = ''
  try {
    const latest = await loadDashboardState()
    configVersion.value = latest.config.config_version
    const result = await saveTunnelConfig(configVersion.value, tunnelPayload())
    configVersion.value = result.config_version
    syncTunnelForm(result.tunnel)
    tunnelStatus.value = result.status
    notice.value = tunnelForm.enabled ? t('proxies.tunnel.settingsApplied') : t('proxies.tunnel.disabled')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    tunnelSaving.value = false
  }
}

function openCreate() {
  resetForm()
  createMode.value = 'standard'
  createOpen.value = true
}

function openEdit(row: ProxyRow) {
  editingID.value = row.id
  form.id = row.id
  form.name = row.name
  form.protocol = row.protocol
  form.host = row.host
  form.port = row.port || 8080
  form.username = row.username
  form.password = row.password
  form.status = row.status
  editOpen.value = true
}

async function saveCreate() {
  saving.value = true
  try {
    const proxy = proxyFromForm()
    const updated = await createProxy(configVersion.value, proxy)
    afterConfigUpdate(updated, t('proxies.created', { id: proxy.id }))
    createOpen.value = false
  } finally {
    saving.value = false
  }
}

async function saveEdit() {
  saving.value = true
  try {
    const proxy = proxyFromForm()
    proxy.id = editingID.value
    const updated = await updateProxy(configVersion.value, proxy)
    afterConfigUpdate(updated, t('proxies.updated', { id: proxy.id }))
    editOpen.value = false
  } finally {
    saving.value = false
  }
}

async function saveBatch() {
  saving.value = true
  try {
    let version = configVersion.value
    for (const parsed of batchParse.value.proxies) {
      const proxy: ProxyConfig = { id: stableProxyID(parsed), url: buildProxyURL(parsed) }
      const updated = await createProxy(version, proxy)
      version = updated.config_version
    }
    notice.value = t('proxies.imported', { count: batchParse.value.proxies.length })
    createOpen.value = false
    batchInput.value = ''
    await loadAll()
  } finally {
    saving.value = false
  }
}

async function removeProxy(row: ProxyRow) {
  if (row.accountCount > 0) {
    error.value = t('proxies.deleteBlocked', { id: row.id, count: row.accountCount })
    return
  }
  const updated = await deleteProxy(configVersion.value, row.id)
  afterConfigUpdate(updated.config, t('proxies.deleted', { id: row.id }))
}

function afterConfigUpdate(config: AdminConfig, message: string) {
  configVersion.value = config.config_version
  rawProxies.value = config.proxies || []
  notice.value = message
  error.value = ''
}

async function copyProxy(row: ProxyRow) {
  const copied = await copyTextToClipboard(row.url)
  notice.value = copied ? t('proxies.copiedUrl', { id: row.id }) : t('proxies.copyFailedFor', { id: row.id })
}

async function copyTunnelURL() {
  if (!tunnelPublicURL.value) return
  const copied = await copyTextToClipboard(tunnelPublicURL.value)
  notice.value = copied ? t('proxies.tunnel.copied') : t('proxies.tunnel.copyFailed')
}

function toggleSelected(id: string) {
  if (selected.has(id)) selected.delete(id)
  else selected.add(id)
}

function exportSelected() {
  const ids = selected.size > 0 ? selected : new Set(rows.value.map((row) => row.id))
  const proxies = rawProxies.value.filter((proxy) => ids.has(proxy.id))
  exportOutput.value = JSON.stringify({ type: 'simple_sub2api_proxy_export', version: 1, proxies }, null, 2)
  showExportDialog.value = true
}

onMounted(() => {
  loadAll().catch(() => undefined)
})
</script>

<template>
  <AppShell>
    <main class="grid gap-4">
      <section class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <article v-for="(value, key) in counts" :key="key" class="card">
          <p class="text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ key }}</p>
          <p class="mt-2 text-3xl font-black text-gray-950 dark:text-white">{{ value }}</p>
        </article>
      </section>

      <section class="card p-0">
        <div class="flex flex-col border-b border-gray-200 dark:border-dark-700 sm:flex-row">
          <button type="button" :class="['flex flex-1 items-center justify-center gap-2 border-b-2 px-5 py-4 text-sm font-black uppercase tracking-wide transition-all', activeTab === 'proxies' ? 'border-primary-500 bg-primary-50/70 text-primary-700 dark:bg-primary-950/20 dark:text-primary-300' : 'border-transparent text-gray-500 hover:bg-gray-50 hover:text-gray-800 dark:text-gray-400 dark:hover:bg-dark-800 dark:hover:text-gray-200']" @click="activeTab = 'proxies'">
            <Icon name="key" />
            <span>{{ $t('proxies.tab_outbound') }}</span>
          </button>
          <button type="button" :class="['flex flex-1 items-center justify-center gap-2 border-b-2 px-5 py-4 text-sm font-black uppercase tracking-wide transition-all', activeTab === 'tunnel' ? 'border-primary-500 bg-primary-50/70 text-primary-700 dark:bg-primary-950/20 dark:text-primary-300' : 'border-transparent text-gray-500 hover:bg-gray-50 hover:text-gray-800 dark:text-gray-400 dark:hover:bg-dark-800 dark:hover:text-gray-200']" @click="activeTab = 'tunnel'">
            <Icon name="refresh" />
            <span>{{ $t('proxies.tab_tunnel') }}</span>
          </button>
        </div>
      </section>

      <section v-if="activeTab === 'proxies'" class="card">
        <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div class="grid flex-1 gap-3 sm:grid-cols-3">
            <input v-model="search" class="input" :placeholder="$t('proxies.searchPlaceholder')" />
            <select v-model="protocolFilter" class="input">
              <option value="">{{ $t('proxies.allProtocols') }}</option>
              <option value="http">HTTP</option>
              <option value="https">HTTPS</option>
              <option value="socks5h">SOCKS5H</option>
            </select>
            <select v-model="statusFilter" class="input">
              <option value="">{{ $t('proxies.allStatus') }}</option>
              <option value="active">active</option>
              <option value="disabled">disabled</option>
            </select>
          </div>
          <div class="flex flex-wrap gap-2">
            <button class="btn btn-secondary" type="button" :disabled="loading" @click="loadAll">
              <Icon name="refresh" />
              <span class="ml-2">{{ $t('common.refresh') }}</span>
            </button>
            <button class="btn btn-secondary" type="button" @click="exportSelected">{{ selected.size > 0 ? $t('proxies.exportSelected') : $t('proxies.exportJson') }}</button>
            <button class="btn btn-primary" type="button" @click="openCreate">{{ $t('proxies.createProxy') }}</button>
          </div>
        </div>
      </section>

      <p v-if="notice" class="rounded-xl border border-primary-200 bg-primary-50 p-3 text-sm text-primary-700 dark:border-primary-900 dark:bg-primary-950/40 dark:text-primary-200">{{ notice }}</p>
      <p v-if="error" class="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-200">{{ error }}</p>

      <section v-if="activeTab === 'proxies'" class="card">
        <div class="mb-3 flex items-center justify-between gap-3">
          <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ $t('proxies.proxyManagement') }}</h2>
          <span class="badge badge-primary">{{ filteredRows.length }} / {{ rows.length }} {{ $t('common.shown') }}</span>
        </div>
        <DataTable :columns="columns" :rows="filteredRows" :empty-text="$t('proxies.noMatch')">
          <template #cell-name="{ row }">
            <div class="flex items-start gap-2">
              <input type="checkbox" class="mt-1 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :checked="selected.has(String(row.id))" @change="toggleSelected(String(row.id))" />
              <div class="space-y-1">
                <p class="font-semibold text-gray-950 dark:text-white">{{ row.name }}</p>
                <p class="font-mono text-xs text-gray-500 dark:text-gray-400">{{ row.id }}</p>
              </div>
            </div>
          </template>
          <template #cell-protocol="{ value }">
            <span :class="['badge', String(value).startsWith('socks5') ? 'badge-primary' : 'bg-gray-100 text-gray-700 dark:bg-dark-800 dark:text-gray-200']">{{ String(value).toUpperCase() }}</span>
          </template>
          <template #cell-host="{ row }">
            <div class="space-y-1">
              <code class="rounded bg-gray-100 px-2 py-1 text-xs dark:bg-dark-800">{{ row.host }}:{{ row.port }}</code>
              <button class="block text-xs text-primary-600 hover:underline dark:text-primary-300" type="button" @click="copyProxy(row as ProxyRow)">{{ $t('proxies.copyProxyUrl') }}</button>
            </div>
          </template>
          <template #cell-username="{ row }">
            <div v-if="row.username || row.password" class="space-y-1 text-xs">
              <p v-if="row.username">{{ row.username }}</p>
              <p v-if="row.password" class="font-mono text-gray-500">••••••</p>
            </div>
            <span v-else class="text-sm text-gray-400">-</span>
          </template>
          <template #cell-accountCount="{ value }">
            <span class="badge bg-gray-100 text-gray-700 dark:bg-dark-800 dark:text-gray-200">{{ $t('proxies.accountCount', { count: value }) }}</span>
          </template>
          <template #cell-status="{ value }">
            <StatusBadge :status="String(value)" />
          </template>
          <template #cell-actions="{ row }">
            <div class="flex flex-wrap justify-end gap-2 md:justify-start">
              <button class="btn btn-secondary px-3 py-1.5" type="button" @click="openEdit(row as ProxyRow)">{{ $t('common.edit') }}</button>
              <button class="btn btn-danger px-3 py-1.5" type="button" @click="removeProxy(row as ProxyRow)">{{ $t('common.delete') }}</button>
            </div>
          </template>
        </DataTable>
      </section>

      <section v-else class="card overflow-hidden">
        <div class="flex flex-col gap-4 border-b border-gray-200 pb-4 dark:border-dark-700 lg:flex-row lg:items-center lg:justify-between">
          <div class="flex items-center gap-3">
            <div class="relative flex h-4 w-4">
              <span :class="['absolute inline-flex h-full w-full rounded-full opacity-75', tunnelStatus.status === 'connected' ? 'animate-ping bg-emerald-400' : tunnelStatus.status === 'starting' ? 'animate-pulse bg-amber-400' : tunnelStatus.status === 'error' ? 'bg-rose-400' : 'bg-slate-400']"></span>
              <span :class="['relative inline-flex h-4 w-4 rounded-full', tunnelStatus.status === 'connected' ? 'bg-emerald-500' : tunnelStatus.status === 'starting' ? 'bg-amber-500' : tunnelStatus.status === 'error' ? 'bg-rose-500' : 'bg-slate-500']"></span>
            </div>
            <div>
              <h2 class="text-base font-black text-gray-950 dark:text-white">{{ $t('proxies.tunnel.title') }}</h2>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ $t('proxies.tunnel.subtitle') }}</p>
            </div>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <span :class="['badge', tunnelStatusTone === 'emerald' ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300' : tunnelStatusTone === 'amber' ? 'bg-amber-100 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300' : tunnelStatusTone === 'rose' ? 'bg-rose-100 text-rose-700 dark:bg-rose-950/40 dark:text-rose-300' : 'bg-slate-100 text-slate-700 dark:bg-dark-800 dark:text-slate-300']">{{ tunnelStatusLabel }}</span>
            <button class="btn btn-secondary" type="button" :disabled="tunnelLoading" @click="refreshTunnelStatus">
              <Icon name="refresh" />
              <span class="ml-2">{{ tunnelLoading ? $t('common.refreshing') : $t('proxies.tunnel.refreshStatus') }}</span>
            </button>
          </div>
        </div>

        <div class="mt-5 grid gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(320px,420px)]">
          <div class="space-y-4">
            <div class="rounded-2xl border border-gray-200 bg-gray-50/70 p-4 dark:border-dark-700 dark:bg-dark-800/50">
              <label class="flex items-center justify-between gap-4">
                <span>
                  <span class="block text-sm font-black text-gray-950 dark:text-white">{{ $t('proxies.tunnel.enable') }}</span>
                  <span class="text-xs text-gray-500 dark:text-gray-400">{{ $t('proxies.tunnel.enableHint') }}</span>
                </span>
                <input v-model="tunnelForm.enabled" type="checkbox" class="h-5 w-5 rounded border-gray-300 text-primary-600 focus:ring-primary-500" @change="saveTunnelSettings" />
              </label>
            </div>

            <div class="grid gap-4 sm:grid-cols-2">
              <label class="grid gap-1">
                <span class="input-label">{{ $t('proxies.tunnel.mode') }}</span>
                <select v-model="tunnelForm.mode" class="input normal-case" :disabled="tunnelSaving" @change="saveTunnelSettings">
                  <option value="quick">{{ $t('proxies.tunnel.quickMode') }}</option>
                  <option value="named">{{ $t('proxies.tunnel.namedMode') }}</option>
                </select>
              </label>
              <label class="grid gap-1">
                <span class="input-label">{{ $t('proxies.tunnel.logLimit') }}</span>
                <input v-model.number="tunnelForm.log_limit_lines" min="10" max="1000" type="number" class="input" :disabled="tunnelSaving" @change="saveTunnelSettings" />
              </label>
              <label class="grid gap-1 sm:col-span-2">
                <span class="input-label">{{ $t('proxies.tunnel.binaryPath') }}</span>
                <input v-model="tunnelForm.binary_path" class="input normal-case" :placeholder="$t('proxies.tunnel.binaryPlaceholder')" :disabled="tunnelSaving" @change="saveTunnelSettings" />
              </label>
              <label v-if="tunnelForm.mode === 'named'" class="grid gap-1 sm:col-span-2">
                <span class="input-label">{{ $t('proxies.tunnel.token') }}</span>
                <input v-model="tunnelForm.token" type="password" class="input normal-case" :placeholder="$t('proxies.tunnel.tokenPlaceholder')" :disabled="tunnelSaving" @change="saveTunnelSettings" />
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ $t('proxies.tunnel.tokenHint') }}</span>
              </label>
            </div>

            <div v-if="tunnelStatus.status === 'connected' && tunnelForm.enabled" class="rounded-2xl border border-emerald-300/60 bg-emerald-50/80 p-4 shadow-inner dark:border-emerald-900/70 dark:bg-emerald-950/20">
              <p class="text-xs font-black uppercase tracking-wider text-emerald-700 dark:text-emerald-300">{{ $t('proxies.tunnel.connectedAlert') }}</p>
              <div class="mt-2 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
                <a v-if="tunnelPublicURL" :href="tunnelPublicURL" target="_blank" rel="noreferrer" class="break-all font-mono text-sm font-bold text-emerald-900 underline decoration-emerald-400 underline-offset-4 dark:text-emerald-100">{{ tunnelPublicURL }}</a>
                <span v-else class="text-sm text-emerald-800 dark:text-emerald-200">{{ $t('proxies.tunnel.namedConnected') }}</span>
                <button v-if="tunnelPublicURL" class="btn btn-secondary shrink-0 border-emerald-300 text-emerald-800 dark:border-emerald-800 dark:text-emerald-200" type="button" @click="copyTunnelURL">
                  <Icon name="copy" />
                  <span class="ml-2">{{ $t('proxies.tunnel.copyUrl') }}</span>
                </button>
              </div>
            </div>

            <p v-if="tunnelStatus.error_message" class="rounded-xl border border-rose-200 bg-rose-50 p-3 text-sm text-rose-700 dark:border-rose-900 dark:bg-rose-950/40 dark:text-rose-200">{{ tunnelStatus.error_message }}</p>
          </div>

          <aside class="rounded-2xl border border-dark-800 bg-dark-950 p-4 text-dark-200 shadow-2xl">
            <div class="mb-3 flex items-center justify-between gap-3">
              <div>
                <p class="text-xs font-black uppercase tracking-[0.2em] text-primary-300">{{ $t('proxies.tunnel.logsTitle') }}</p>
                <p class="text-[11px] text-dark-400">{{ $t('proxies.tunnel.logsHint') }}</p>
              </div>
              <span class="rounded-full bg-dark-800 px-2 py-1 font-mono text-[10px] text-dark-300">{{ $t('proxies.tunnel.lines', { count: tunnelStatus.recent_logs?.length || 0 }) }}</span>
            </div>
            <div class="max-h-72 overflow-y-auto rounded-xl border border-dark-800 bg-black/40 p-3 font-mono text-[11px] leading-relaxed text-emerald-200 shadow-inner">
              <div v-for="(log, idx) in tunnelStatus.recent_logs" :key="`${idx}-${log}`" class="whitespace-pre-wrap break-words"><span class="text-dark-500">{{ String(idx + 1).padStart(3, '0') }} │ </span>{{ log }}</div>
              <div v-if="!tunnelStatus.recent_logs || tunnelStatus.recent_logs.length === 0" class="text-dark-500">{{ $t('proxies.tunnel.waiting') }}</div>
            </div>
          </aside>
        </div>
      </section>

      <div v-if="createOpen" class="xforce-modal-backdrop fixed inset-0 z-50 grid place-items-center">
        <section class="card xforce-modal-panel max-h-[92vh] w-full max-w-2xl overflow-auto">
          <div class="flex items-center justify-between gap-3">
            <h2 class="text-lg font-black">{{ $t('proxies.createProxy') }}</h2>
            <button class="btn btn-secondary" type="button" @click="createOpen = false">{{ $t('common.close') }}</button>
          </div>
          <div class="mt-4 flex border-b border-gray-200 dark:border-dark-600">
            <button type="button" :class="['-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors', createMode === 'standard' ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300']" @click="createMode = 'standard'">{{ $t('proxies.standardAdd') }}</button>
            <button type="button" :class="['-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors', createMode === 'batch' ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300']" @click="createMode = 'batch'">{{ $t('proxies.batchAdd') }}</button>
          </div>
          <form v-if="createMode === 'standard'" class="mt-4 space-y-4" @submit.prevent="saveCreate">
            <label class="grid gap-1"><span class="input-label">{{ $t('proxies.name') }}</span><input v-model="form.name" class="input normal-case" placeholder="US residential proxy" /></label>
            <label class="grid gap-1"><span class="input-label">{{ $t('proxies.protocol') }}</span><select v-model="form.protocol" class="input normal-case"><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5h">SOCKS5H</option></select></label>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="grid gap-1"><span class="input-label">{{ $t('proxies.host') }}</span><input v-model="form.host" required class="input normal-case" placeholder="127.0.0.1" /></label>
              <label class="grid gap-1"><span class="input-label">{{ $t('proxies.port') }}</span><input v-model.number="form.port" required type="number" min="1" max="65535" class="input" placeholder="8080" /></label>
            </div>
            <label class="grid gap-1"><span class="input-label">{{ $t('proxies.username') }}</span><input v-model="form.username" class="input normal-case" :placeholder="$t('proxies.optionalAuth')" /></label>
            <label class="grid gap-1"><span class="input-label">{{ $t('proxies.password') }}</span><input v-model="form.password" :type="createPasswordVisible ? 'text' : 'password'" class="input normal-case" :placeholder="$t('proxies.optionalAuth')" /></label>
            <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="createPasswordVisible" type="checkbox" /> {{ $t('proxies.showPassword') }}</label>
            <div class="flex justify-end gap-2"><button class="btn btn-secondary" type="button" @click="createOpen = false">{{ $t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving" type="submit">{{ saving ? $t('common.createInProgress') : $t('common.create') }}</button></div>
          </form>
          <div v-else class="mt-4 space-y-4">
            <label class="grid gap-1"><span class="input-label">{{ $t('proxies.batchInput') }}</span><textarea v-model="batchInput" rows="10" class="input font-mono normal-case" placeholder="http://user:pass@127.0.0.1:8080&#10;socks5h://127.0.0.1:1080"></textarea></label>
            <div class="rounded-lg bg-gray-50 p-4 text-sm dark:bg-dark-700">
              {{ $t('proxies.batchParsed', { valid: batchParse.valid, invalid: batchParse.invalid, duplicate: batchParse.duplicate, total: batchParse.total }) }}
            </div>
            <div class="flex justify-end gap-2"><button class="btn btn-secondary" type="button" @click="createOpen = false">{{ $t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving || batchParse.valid === 0" type="button" @click="saveBatch">{{ saving ? $t('common.importInProgress') : $t('proxies.importCount', { count: batchParse.valid }) }}</button></div>
          </div>
        </section>
      </div>

      <div v-if="editOpen" class="xforce-modal-backdrop fixed inset-0 z-50 grid place-items-center">
        <section class="card xforce-modal-panel max-h-[92vh] w-full max-w-2xl overflow-auto">
          <div class="flex items-center justify-between gap-3"><h2 class="text-lg font-black">{{ $t('proxies.editProxy') }}</h2><button class="btn btn-secondary" type="button" @click="editOpen = false">{{ $t('common.close') }}</button></div>
          <form class="mt-4 space-y-4" @submit.prevent="saveEdit">
            <label class="grid gap-1"><span class="input-label">{{ $t('proxies.name') }}</span><input v-model="form.name" class="input normal-case" /></label>
            <label class="grid gap-1"><span class="input-label">{{ $t('proxies.protocol') }}</span><select v-model="form.protocol" class="input normal-case"><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5h">SOCKS5H</option></select></label>
            <div class="grid gap-3 sm:grid-cols-2"><label class="grid gap-1"><span class="input-label">{{ $t('proxies.host') }}</span><input v-model="form.host" required class="input normal-case" /></label><label class="grid gap-1"><span class="input-label">{{ $t('proxies.port') }}</span><input v-model.number="form.port" required type="number" min="1" max="65535" class="input" /></label></div>
            <label class="grid gap-1"><span class="input-label">{{ $t('proxies.username') }}</span><input v-model="form.username" class="input normal-case" /></label>
            <label class="grid gap-1"><span class="input-label">{{ $t('proxies.password') }}</span><input v-model="form.password" :type="editPasswordVisible ? 'text' : 'password'" class="input normal-case" /></label>
            <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="editPasswordVisible" type="checkbox" /> {{ $t('proxies.showPassword') }}</label>
            <div class="flex justify-end gap-2"><button class="btn btn-secondary" type="button" @click="editOpen = false">{{ $t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving" type="submit">{{ saving ? $t('common.updateInProgress') : $t('common.update') }}</button></div>
          </form>
        </section>
      </div>

      <div v-if="showExportDialog" class="xforce-modal-backdrop fixed inset-0 z-50 grid place-items-center">
        <section class="card xforce-modal-panel max-h-[92vh] w-full max-w-3xl overflow-auto">
          <div class="flex items-center justify-between gap-3"><h2 class="text-lg font-black">{{ $t('proxies.proxyExportJson') }}</h2><button class="btn btn-secondary" type="button" @click="showExportDialog = false">{{ $t('common.close') }}</button></div>
          <pre class="mt-4 max-h-96 overflow-auto rounded-xl bg-gray-100 p-3 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300">{{ exportOutput }}</pre>
        </section>
      </div>
    </main>
  </AppShell>
</template>
