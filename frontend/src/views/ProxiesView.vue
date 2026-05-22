<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'

import {
  createProxy,
  deleteProxy,
  loadDashboardState,
  loadProxies,
  updateProxy,
  type AdminConfig,
  type ProxyConfig
} from '@/api/client'
import AppShell from '@/components/AppShell.vue'
import DataTable from '@/components/DataTable.vue'
import Icon from '@/components/Icon.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { copyTextToClipboard } from '@/composables/useClipboard'
import type { Column } from '@/types/ui'

type ProxyProtocol = 'http' | 'https' | 'socks5' | 'socks5h'

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

const columns: Column<ProxyRow>[] = [
  { key: 'name', label: 'Name' },
  { key: 'protocol', label: 'Protocol' },
  { key: 'host', label: 'Address' },
  { key: 'username', label: 'Auth' },
  { key: 'accountCount', label: 'Accounts' },
  { key: 'status', label: 'Status' },
  { key: 'actions', label: 'Actions' }
]

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
    const [state, proxies] = await Promise.all([loadDashboardState(), loadProxies()])
    configVersion.value = state.config.config_version
    rawProxies.value = proxies
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
    afterConfigUpdate(updated, `Created ${proxy.id}.`)
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
    afterConfigUpdate(updated, `Updated ${proxy.id}.`)
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
    notice.value = `Imported ${batchParse.value.proxies.length} proxy record(s).`
    createOpen.value = false
    batchInput.value = ''
    await loadAll()
  } finally {
    saving.value = false
  }
}

async function removeProxy(row: ProxyRow) {
  if (row.accountCount > 0) {
    error.value = `Proxy ${row.id} is referenced by ${row.accountCount} account(s). Remove account references before deleting.`
    return
  }
  const updated = await deleteProxy(configVersion.value, row.id)
  afterConfigUpdate(updated.config, `Deleted ${row.id}.`)
}

function afterConfigUpdate(config: AdminConfig, message: string) {
  configVersion.value = config.config_version
  rawProxies.value = config.proxies || []
  notice.value = message
  error.value = ''
}

async function copyProxy(row: ProxyRow) {
  const copied = await copyTextToClipboard(row.url)
  notice.value = copied ? `Copied ${row.id} URL.` : `Copy failed for ${row.id}. Select the proxy URL and copy it manually.`
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

      <section class="card">
        <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div class="grid flex-1 gap-3 sm:grid-cols-3">
            <input v-model="search" class="input" placeholder="Search proxy name, host, ID, auth" />
            <select v-model="protocolFilter" class="input">
              <option value="">all protocols</option>
              <option value="http">HTTP</option>
              <option value="https">HTTPS</option>
              <option value="socks5h">SOCKS5H</option>
            </select>
            <select v-model="statusFilter" class="input">
              <option value="">all status</option>
              <option value="active">active</option>
              <option value="disabled">disabled</option>
            </select>
          </div>
          <div class="flex flex-wrap gap-2">
            <button class="btn btn-secondary" type="button" :disabled="loading" @click="loadAll">
              <Icon name="refresh" />
              <span class="ml-2">Refresh</span>
            </button>
            <button class="btn btn-secondary" type="button" @click="exportSelected">{{ selected.size > 0 ? 'Export Selected' : 'Export JSON' }}</button>
            <button class="btn btn-primary" type="button" @click="openCreate">Create Proxy</button>
          </div>
        </div>
      </section>

      <p v-if="notice" class="rounded-xl border border-primary-200 bg-primary-50 p-3 text-sm text-primary-700 dark:border-primary-900 dark:bg-primary-950/40 dark:text-primary-200">{{ notice }}</p>
      <p v-if="error" class="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-200">{{ error }}</p>

      <section class="card">
        <div class="mb-3 flex items-center justify-between gap-3">
          <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Proxy Management</h2>
          <span class="badge badge-primary">{{ filteredRows.length }} / {{ rows.length }} shown</span>
        </div>
        <DataTable :columns="columns" :rows="filteredRows" empty-text="No proxies match the current filter.">
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
              <button class="block text-xs text-primary-600 hover:underline dark:text-primary-300" type="button" @click="copyProxy(row as ProxyRow)">Copy proxy URL</button>
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
            <span class="badge bg-gray-100 text-gray-700 dark:bg-dark-800 dark:text-gray-200">{{ value }} account(s)</span>
          </template>
          <template #cell-status="{ value }">
            <StatusBadge :status="String(value)" />
          </template>
          <template #cell-actions="{ row }">
            <div class="flex flex-wrap justify-end gap-2 md:justify-start">
              <button class="btn btn-secondary px-3 py-1.5" type="button" @click="openEdit(row as ProxyRow)">Edit</button>
              <button class="btn btn-danger px-3 py-1.5" type="button" @click="removeProxy(row as ProxyRow)">Delete</button>
            </div>
          </template>
        </DataTable>
      </section>

      <div v-if="createOpen" class="xforce-modal-backdrop fixed inset-0 z-50 grid place-items-center">
        <section class="card xforce-modal-panel max-h-[92vh] w-full max-w-2xl overflow-auto">
          <div class="flex items-center justify-between gap-3">
            <h2 class="text-lg font-black">Create Proxy</h2>
            <button class="btn btn-secondary" type="button" @click="createOpen = false">Close</button>
          </div>
          <div class="mt-4 flex border-b border-gray-200 dark:border-dark-600">
            <button type="button" :class="['-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors', createMode === 'standard' ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300']" @click="createMode = 'standard'">Standard Add</button>
            <button type="button" :class="['-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors', createMode === 'batch' ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300']" @click="createMode = 'batch'">Batch Add</button>
          </div>
          <form v-if="createMode === 'standard'" class="mt-4 space-y-4" @submit.prevent="saveCreate">
            <label class="grid gap-1"><span class="input-label">Name</span><input v-model="form.name" class="input normal-case" placeholder="US residential proxy" /></label>
            <label class="grid gap-1"><span class="input-label">Protocol</span><select v-model="form.protocol" class="input normal-case"><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5h">SOCKS5H</option></select></label>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="grid gap-1"><span class="input-label">Host</span><input v-model="form.host" required class="input normal-case" placeholder="127.0.0.1" /></label>
              <label class="grid gap-1"><span class="input-label">Port</span><input v-model.number="form.port" required type="number" min="1" max="65535" class="input" placeholder="8080" /></label>
            </div>
            <label class="grid gap-1"><span class="input-label">Username</span><input v-model="form.username" class="input normal-case" placeholder="optional auth" /></label>
            <label class="grid gap-1"><span class="input-label">Password</span><input v-model="form.password" :type="createPasswordVisible ? 'text' : 'password'" class="input normal-case" placeholder="optional auth" /></label>
            <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="createPasswordVisible" type="checkbox" /> Show password</label>
            <div class="flex justify-end gap-2"><button class="btn btn-secondary" type="button" @click="createOpen = false">Cancel</button><button class="btn btn-primary" :disabled="saving" type="submit">{{ saving ? 'Creating...' : 'Create' }}</button></div>
          </form>
          <div v-else class="mt-4 space-y-4">
            <label class="grid gap-1"><span class="input-label">Batch Input</span><textarea v-model="batchInput" rows="10" class="input font-mono normal-case" placeholder="http://user:pass@127.0.0.1:8080&#10;socks5h://127.0.0.1:1080"></textarea></label>
            <div class="rounded-lg bg-gray-50 p-4 text-sm dark:bg-dark-700">
              Parsed {{ batchParse.valid }} valid / {{ batchParse.invalid }} invalid / {{ batchParse.duplicate }} duplicate from {{ batchParse.total }} line(s).
            </div>
            <div class="flex justify-end gap-2"><button class="btn btn-secondary" type="button" @click="createOpen = false">Cancel</button><button class="btn btn-primary" :disabled="saving || batchParse.valid === 0" type="button" @click="saveBatch">{{ saving ? 'Importing...' : `Import ${batchParse.valid}` }}</button></div>
          </div>
        </section>
      </div>

      <div v-if="editOpen" class="xforce-modal-backdrop fixed inset-0 z-50 grid place-items-center">
        <section class="card xforce-modal-panel max-h-[92vh] w-full max-w-2xl overflow-auto">
          <div class="flex items-center justify-between gap-3"><h2 class="text-lg font-black">Edit Proxy</h2><button class="btn btn-secondary" type="button" @click="editOpen = false">Close</button></div>
          <form class="mt-4 space-y-4" @submit.prevent="saveEdit">
            <label class="grid gap-1"><span class="input-label">Name</span><input v-model="form.name" class="input normal-case" /></label>
            <label class="grid gap-1"><span class="input-label">Protocol</span><select v-model="form.protocol" class="input normal-case"><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5h">SOCKS5H</option></select></label>
            <div class="grid gap-3 sm:grid-cols-2"><label class="grid gap-1"><span class="input-label">Host</span><input v-model="form.host" required class="input normal-case" /></label><label class="grid gap-1"><span class="input-label">Port</span><input v-model.number="form.port" required type="number" min="1" max="65535" class="input" /></label></div>
            <label class="grid gap-1"><span class="input-label">Username</span><input v-model="form.username" class="input normal-case" /></label>
            <label class="grid gap-1"><span class="input-label">Password</span><input v-model="form.password" :type="editPasswordVisible ? 'text' : 'password'" class="input normal-case" /></label>
            <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="editPasswordVisible" type="checkbox" /> Show password</label>
            <div class="flex justify-end gap-2"><button class="btn btn-secondary" type="button" @click="editOpen = false">Cancel</button><button class="btn btn-primary" :disabled="saving" type="submit">{{ saving ? 'Updating...' : 'Update' }}</button></div>
          </form>
        </section>
      </div>

      <div v-if="showExportDialog" class="xforce-modal-backdrop fixed inset-0 z-50 grid place-items-center">
        <section class="card xforce-modal-panel max-h-[92vh] w-full max-w-3xl overflow-auto">
          <div class="flex items-center justify-between gap-3"><h2 class="text-lg font-black">Proxy Export JSON</h2><button class="btn btn-secondary" type="button" @click="showExportDialog = false">Close</button></div>
          <pre class="mt-4 max-h-96 overflow-auto rounded-xl bg-gray-100 p-3 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300">{{ exportOutput }}</pre>
        </section>
      </div>
    </main>
  </AppShell>
</template>
