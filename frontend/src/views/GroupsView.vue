<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { createGroup, deleteGroup, loadGroups, updateGroup, type AccountConfig, type GroupConfig, type GroupSummary, type GroupsResponse } from '@/api/client'
import AppShell from '@/components/AppShell.vue'
import DataTable from '@/components/DataTable.vue'
import Icon from '@/components/Icon.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { useAdminState } from '@/composables/useAdminState'
import type { Column } from '@/types/ui'

const { t } = useI18n()

interface GroupRow extends Record<string, unknown> {
  id: string
  name: string
  platform: string
  status: string
  accountCount: number
  accounts: string[]
  tags: string[]
  summary: GroupSummary
}

const { refresh } = useAdminState()
const loading = ref(false)
const error = ref('')
const notice = ref('')
const groupsState = ref<GroupsResponse | null>(null)
const search = ref('')
const editorOpen = ref(false)
const editorMode = ref<'create' | 'edit'>('create')

const form = reactive({
  id: '',
  name: '',
  platform: 'openai' as GroupConfig['platform'],
  description: '',
  status: 'active' as GroupConfig['status'],
  account_ids: [] as string[],
  tags: '',
  strategy: 'polling' as NonNullable<GroupConfig['rotation_policy']>['strategy'],
  sticky_sessions_enabled: false,
  sticky_header: 'X-Session-ID',
  retry_on_errors: true,
  rotate_error_codes: '429, 401, 403, 404, 500',
  cooldown_duration_seconds: 60,
  enable_quota_protection: false,
  min_quota_threshold_percent: 10
})

const columns = computed<Column<GroupRow>[]>(() => [
  { key: 'name', label: t('groups.columns.name') },
  { key: 'platform', label: t('groups.columns.platform') },
  { key: 'accounts', label: t('groups.columns.accounts') },
  { key: 'status', label: t('common.status') },
  { key: 'actions', label: t('common.actions') }
])

const accounts = computed(() => groupsState.value?.accounts || [])

const rows = computed<GroupRow[]>(() =>
  (groupsState.value?.groups || []).map((summary) => {
    const group = summary.config
    return {
      id: group.id,
      name: group.name || group.id,
      platform: group.platform || 'openai',
      status: group.status || 'active',
      accountCount: summary.account_count || group.account_ids?.length || 0,
      accounts: group.account_ids || [],
      tags: group.tags || [],
      summary
    }
  })
)

const filteredRows = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return rows.value
  return rows.value.filter((row) => [row.id, row.name, row.platform, row.status, ...row.accounts, ...row.tags].join(' ').toLowerCase().includes(q))
})

async function loadAll() {
  loading.value = true
  error.value = ''
  try {
    await refresh()
    groupsState.value = await loadGroups()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

function resetForm() {
  form.id = ''
  form.name = ''
  form.platform = 'openai'
  form.description = ''
  form.status = 'active'
  form.account_ids = []
  form.tags = ''
  form.strategy = 'polling'
  form.sticky_sessions_enabled = false
  form.sticky_header = 'X-Session-ID'
  form.retry_on_errors = true
  form.rotate_error_codes = '429, 401, 403, 404, 500'
  form.cooldown_duration_seconds = 60
  form.enable_quota_protection = false
  form.min_quota_threshold_percent = 10
}

function openCreate() {
  editorMode.value = 'create'
  resetForm()
  editorOpen.value = true
}

function openEdit(row: GroupRow) {
  const group = row.summary.config
  editorMode.value = 'edit'
  form.id = group.id
  form.name = group.name
  form.platform = group.platform
  form.description = group.description || ''
  form.status = group.status
  form.account_ids = [...(group.account_ids || [])]
  form.tags = (group.tags || []).filter((tag) => tag !== group.id).join(', ')
  form.strategy = group.rotation_policy?.strategy || 'polling'
  form.sticky_sessions_enabled = group.rotation_policy?.sticky_sessions_enabled || false
  form.sticky_header = group.rotation_policy?.sticky_header || 'X-Session-ID'
  form.retry_on_errors = group.rotation_policy?.retry_on_errors ?? true
  form.rotate_error_codes = (group.rotation_policy?.rotate_error_codes || [429, 401, 403, 404, 500]).join(', ')
  form.cooldown_duration_seconds = group.rotation_policy?.cooldown_duration_seconds || 60
  form.enable_quota_protection = group.rotation_policy?.enable_quota_protection || false
  form.min_quota_threshold_percent = Math.round((group.rotation_policy?.min_quota_threshold_percent || 0.1) * 100)
  editorOpen.value = true
}

function toggleAccount(accountID: string) {
  if (form.account_ids.includes(accountID)) {
    form.account_ids = form.account_ids.filter((id) => id !== accountID)
    return
  }
  form.account_ids.push(accountID)
}

function accountLabel(account: AccountConfig) {
  return account.label || account.id
}

function accountPlatform(account: AccountConfig) {
  const platform = String(account.metadata?.platform || '')
  if (platform === 'anthropic') return 'Claude'
  if (platform) return platform
  return account.type || 'unknown'
}

function accountPlatformValue(account: AccountConfig) {
  const platform = String(account.metadata?.platform || '')
  if (platform) return platform
  if ((account.type || '').startsWith('openai')) return 'openai'
  if ((account.type || '').startsWith('anthropic')) return 'anthropic'
  if ((account.type || '').startsWith('gemini')) return 'gemini'
  return ''
}

const accountsForSelectedPlatform = computed(() => accounts.value.filter((account) => accountPlatformValue(account) === form.platform))

function groupAccentClass(platform: string) {
  if (platform === 'anthropic') return 'ring-orange-400/50 dark:ring-orange-500/40 border-orange-300 dark:border-orange-700'
  if (platform === 'gemini' || platform === 'antigravity') return 'ring-purple-400/50 dark:ring-purple-500/40 border-purple-300 dark:border-purple-700'
  return 'ring-emerald-400/50 dark:ring-emerald-500/40 border-emerald-300 dark:border-emerald-700'
}

function groupRowClass(row: GroupRow) {
  const accent = groupAccentClass(row.platform)
  return [
    'border-l-4 bg-white/70 dark:bg-dark-800/70',
    accent,
    'md:shadow-[0_0_0_1px_rgba(255,255,255,0.03)_inset]'
  ].join(' ')
}

function splitList(value: string) {
  return value.split(/[,\n]+/).map((item) => item.trim()).filter(Boolean)
}

function parseStatusCodes(value: string) {
  const codes = value.split(/[,\n]+/).map((item) => Number(item.trim())).filter((code) => Number.isInteger(code) && code >= 100 && code <= 599)
  return Array.from(new Set(codes))
}

function buildGroup(): GroupConfig {
  const tags = Array.from(new Set([form.id, ...splitList(form.tags)].filter(Boolean)))
  return {
    id: form.id.trim(),
    name: form.name.trim(),
    platform: form.platform,
    description: form.description.trim() || undefined,
    status: form.status,
    account_ids: [...form.account_ids],
    tags,
    rotation_policy: {
      strategy: form.strategy,
      sticky_sessions_enabled: form.sticky_sessions_enabled,
      sticky_header: form.sticky_header.trim() || 'X-Session-ID',
      retry_on_errors: form.retry_on_errors,
      rotate_error_codes: parseStatusCodes(form.rotate_error_codes),
      cooldown_duration_seconds: Number(form.cooldown_duration_seconds) || 60,
      enable_quota_protection: form.enable_quota_protection,
      min_quota_threshold_percent: Math.max(0, Math.min(100, Number(form.min_quota_threshold_percent) || 10)) / 100
    },
    created_at: '',
    updated_at: ''
  }
}

async function saveGroup() {
  if (!groupsState.value) return
  const group = buildGroup()
  if (editorMode.value === 'create') {
    await createGroup(groupsState.value.config_version, group)
    notice.value = t('groups.created', { name: group.name })
  } else {
    await updateGroup(groupsState.value.config_version, group)
    notice.value = t('groups.updated', { name: group.name })
  }
  editorOpen.value = false
  await loadAll()
}

async function toggleGroup(row: GroupRow) {
  if (!groupsState.value) return
  const group = structuredClone(row.summary.config)
  group.status = group.status === 'active' ? 'disabled' : 'active'
  await updateGroup(groupsState.value.config_version, group)
  notice.value = t('groups.statusChanged', { name: group.name, status: group.status })
  await loadAll()
}

async function removeGroup(row: GroupRow) {
  if (!groupsState.value) return
  await deleteGroup(groupsState.value.config_version, row.id)
  notice.value = t('groups.deleted', { name: row.name })
  await loadAll()
}

onMounted(() => {
  loadAll().catch(() => undefined)
})
</script>

<template>
  <AppShell>
    <main class="grid gap-4">
      <section class="grid gap-4 lg:grid-cols-3">
        <article class="card lg:col-span-2">
          <div class="flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ $t('groups.title') }}</h2>
              <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ $t('groups.subtitle') }}</p>
              <p class="mt-1 text-xs text-amber-600 dark:text-amber-300">{{ $t('groups.warning') }}</p>
            </div>
            <button class="btn btn-primary" type="button" @click="openCreate">{{ $t('groups.createGroup') }}</button>
          </div>
        </article>
        <article class="card">
          <p class="text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ $t('groups.activeGroups') }}</p>
          <p class="mt-2 text-3xl font-black text-gray-950 dark:text-white">{{ rows.filter((row) => row.status === 'active').length }}</p>
        </article>
      </section>

      <section class="card">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <input v-model="search" class="input sm:max-w-md" :placeholder="$t('groups.searchPlaceholder')" />
          <button class="btn btn-secondary" type="button" :disabled="loading" @click="loadAll"><Icon name="refresh" /><span class="ml-2">{{ $t('common.refresh') }}</span></button>
        </div>
      </section>

      <p v-if="notice" class="rounded-xl border border-primary-200 bg-primary-50 p-3 text-sm text-primary-700 dark:border-primary-900 dark:bg-primary-950/40 dark:text-primary-200">{{ notice }}</p>
      <p v-if="error" class="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-200">{{ error }}</p>

      <section class="card">
        <DataTable :columns="columns" :rows="filteredRows" :empty-text="$t('groups.noMatch')" :row-class="groupRowClass">
          <template #cell-name="{ row }">
            <div class="space-y-1">
              <p class="font-semibold text-gray-950 dark:text-white">{{ row.name }}</p>
              <p class="font-mono text-xs text-gray-500 dark:text-gray-400">{{ row.id }}</p>
              <p v-if="row.summary.config.description" class="text-xs text-gray-500 dark:text-gray-400">{{ row.summary.config.description }}</p>
            </div>
          </template>
          <template #cell-platform="{ row }"><span class="badge badge-primary">{{ row.platform }}</span></template>
          <template #cell-accounts="{ row }">
            <div class="max-w-md space-y-1 text-xs">
              <p>{{ $t('groups.linkedAccounts', { count: row.accountCount }) }}</p>
              <p class="break-all text-gray-500 dark:text-gray-400">{{ row.accounts.join(', ') || $t('common.none') }}</p>
            </div>
          </template>
          <template #cell-status="{ row }"><StatusBadge :status="String(row.status)" /></template>
          <template #cell-actions="{ row }">
            <div class="flex flex-wrap gap-2">
              <button class="btn btn-secondary px-3 py-1.5" type="button" @click="openEdit(row)">{{ $t('common.edit') }}</button>
              <button class="btn btn-secondary px-3 py-1.5" type="button" @click="toggleGroup(row)">{{ row.status === 'active' ? $t('common.disable') : $t('common.enable') }}</button>
              <button class="btn btn-danger px-3 py-1.5" type="button" @click="removeGroup(row)">{{ $t('common.delete') }}</button>
            </div>
          </template>
        </DataTable>
      </section>

      <div v-if="editorOpen" class="xforce-modal-backdrop fixed inset-0 z-50 grid place-items-center">
        <section class="card xforce-modal-panel max-h-[92vh] w-full max-w-4xl overflow-auto">
          <div class="flex items-center justify-between gap-3">
            <div>
              <h2 class="text-lg font-black">{{ editorMode === 'create' ? $t('groups.createGroup') : $t('groups.editGroup') }}</h2>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ $t('groups.editorSubtitle') }}</p>
            </div>
            <button class="btn btn-secondary" type="button" @click="editorOpen = false">{{ $t('common.close') }}</button>
          </div>
          <div class="mt-4 grid gap-4">
            <input v-model="form.id" type="hidden" />
            <label class="grid gap-1 text-sm font-semibold">{{ $t('groups.columns.name') }}<input v-model="form.name" class="input" placeholder="OpenAI primary group" /></label>
            <label class="grid gap-1 text-sm font-semibold">{{ $t('groups.description') }}<textarea v-model="form.description" class="input min-h-20" placeholder="Optional local routing note" /></label>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="grid gap-1 text-sm font-semibold">{{ $t('groups.platform') }}
                <select v-model="form.platform" class="input">
                  <option value="openai">OpenAI</option>
                  <option value="anthropic">Claude</option>
                  <option value="gemini">Gemini</option>
                  <option value="antigravity">Antigravity</option>
                </select>
              </label>
              <label class="grid gap-1 text-sm font-semibold">{{ $t('common.status') }}
                <select v-model="form.status" class="input">
                  <option value="active">active</option>
                  <option value="disabled">disabled</option>
                </select>
              </label>
            </div>
            <label class="grid gap-1 text-sm font-semibold">{{ $t('groups.tags') }}<input v-model="form.tags" class="input" placeholder="optional matching aliases" /></label>

            <section class="rounded-2xl border border-primary-200/70 bg-primary-50/40 p-4 dark:border-primary-900/60 dark:bg-primary-950/20">
              <div class="mb-3 flex items-center justify-between gap-3 border-b border-primary-200/50 pb-2 dark:border-primary-900/40">
                <div class="flex items-center gap-2">
                  <Icon name="refresh" />
                  <h3 class="text-sm font-bold uppercase tracking-wider text-gray-700 dark:text-gray-300">{{ $t('groups.rotationTitle') }}</h3>
                </div>
                <span class="badge badge-primary">{{ $t('groups.groupPolicy') }}</span>
              </div>
              <div class="grid gap-4 sm:grid-cols-2">
                <label class="grid gap-1 text-sm font-semibold">{{ $t('groups.rotationStrategy') }}
                  <select v-model="form.strategy" class="input">
                    <option value="polling">{{ $t('groups.roundRobin') }}</option>
                    <option value="least_connections">{{ $t('groups.leastConnections') }}</option>
                    <option value="p2c">P2C</option>
                    <option value="priority">{{ $t('groups.priorityOrder') }}</option>
                  </select>
                  <span class="text-[10px] font-normal text-gray-500">{{ $t('groups.rotationHint') }}</span>
                </label>
                <label class="grid gap-1 text-sm font-semibold">{{ $t('groups.cooldownDuration') }}
                  <input v-model="form.cooldown_duration_seconds" class="input" min="0" max="86400" type="number" />
                  <span class="text-[10px] font-normal text-gray-500">{{ $t('groups.cooldownHint') }}</span>
                </label>
              </div>
              <div class="mt-4 grid gap-4 border-t border-primary-200/50 pt-3 dark:border-primary-900/40">
                <label class="flex items-center justify-between gap-4 text-sm font-semibold">
                  <span><span class="block">{{ $t('groups.stickySession') }}</span><span class="block text-xs font-normal text-gray-500">{{ $t('groups.stickyHint') }}</span></span>
                  <input v-model="form.sticky_sessions_enabled" type="checkbox" />
                </label>
                <label v-if="form.sticky_sessions_enabled" class="grid gap-1 border-l-2 border-primary-500/50 pl-4 text-sm font-semibold">{{ $t('groups.stickyHeader') }}
                  <input v-model="form.sticky_header" class="input" placeholder="X-Session-ID" />
                </label>
              </div>
              <div class="mt-4 grid gap-4 border-t border-primary-200/50 pt-3 dark:border-primary-900/40">
                <label class="flex items-center justify-between gap-4 text-sm font-semibold">
                  <span><span class="block">{{ $t('groups.failover') }}</span><span class="block text-xs font-normal text-gray-500">{{ $t('groups.failoverHint') }}</span></span>
                  <input v-model="form.retry_on_errors" type="checkbox" />
                </label>
                <label v-if="form.retry_on_errors" class="grid gap-1 border-l-2 border-primary-500/50 pl-4 text-sm font-semibold">{{ $t('groups.rotateCodes') }}
                  <input v-model="form.rotate_error_codes" class="input" placeholder="429, 401, 403, 404, 500" />
                </label>
              </div>
              <div class="mt-4 grid gap-4 border-t border-primary-200/50 pt-3 dark:border-primary-900/40">
                <label class="flex items-center justify-between gap-4 text-sm font-semibold">
                  <span><span class="block">{{ $t('groups.quotaGuard') }}</span><span class="block text-xs font-normal text-gray-500">{{ $t('groups.quotaGuardHint') }}</span></span>
                  <input v-model="form.enable_quota_protection" type="checkbox" />
                </label>
                <label v-if="form.enable_quota_protection" class="grid gap-1 border-l-2 border-primary-500/50 pl-4 text-sm font-semibold">{{ $t('groups.minQuota') }}
                  <input v-model="form.min_quota_threshold_percent" class="input" min="0" max="100" type="number" />
                </label>
              </div>
            </section>

            <section class="rounded-2xl border border-gray-200 p-4 dark:border-dark-700">
              <div class="mb-3 flex items-center justify-between gap-3">
                <h3 class="text-sm font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ $t('groups.accountsInGroup') }}</h3>
                <span class="badge badge-primary">{{ $t('groups.selected', { count: form.account_ids.length }) }}</span>
              </div>
              <div class="grid gap-2 sm:grid-cols-2">
                <label v-for="account in accountsForSelectedPlatform" :key="account.id" :class="['rounded-xl border-2 p-3 transition', form.account_ids.includes(account.id) ? 'border-primary-500 bg-primary-50 dark:bg-primary-950/30' : 'border-gray-200 dark:border-dark-700']">
                  <input :checked="form.account_ids.includes(account.id)" type="checkbox" class="mr-2" @change="toggleAccount(account.id)" />
                  <span class="font-semibold">{{ accountLabel(account) }}</span>
                  <span class="ml-2 text-xs text-gray-500 dark:text-gray-400">{{ accountPlatform(account) }} · {{ account.id }}</span>
                </label>
              </div>
              <p v-if="accounts.length === 0" class="text-sm text-gray-500 dark:text-gray-400">{{ $t('groups.createAccountsFirst') }}</p>
              <p v-else-if="accountsForSelectedPlatform.length === 0" class="text-sm text-gray-500 dark:text-gray-400">{{ $t('groups.noPlatformAccounts', { platform: form.platform }) }}</p>
            </section>
          </div>
          <div class="mt-4 flex flex-wrap justify-end gap-2">
            <button class="btn btn-secondary" type="button" @click="editorOpen = false">{{ $t('common.cancel') }}</button>
            <button class="btn btn-primary" type="button" :disabled="!form.name.trim()" @click="saveGroup">{{ $t('groups.saveGroup') }}</button>
          </div>
        </section>
      </div>
    </main>
  </AppShell>
</template>
