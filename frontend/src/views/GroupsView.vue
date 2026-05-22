<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'

import { createGroup, deleteGroup, loadGroups, updateGroup, type AccountConfig, type GroupConfig, type GroupSummary, type GroupsResponse } from '@/api/client'
import AppShell from '@/components/AppShell.vue'
import DataTable from '@/components/DataTable.vue'
import Icon from '@/components/Icon.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { useAdminState } from '@/composables/useAdminState'
import type { Column } from '@/types/ui'

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
  platform: 'mixed' as GroupConfig['platform'],
  description: '',
  status: 'active' as GroupConfig['status'],
  account_ids: [] as string[],
  tags: ''
})

const columns: Column<GroupRow>[] = [
  { key: 'name', label: 'Name' },
  { key: 'platform', label: 'Platform' },
  { key: 'accounts', label: 'Accounts' },
  { key: 'status', label: 'Status' },
  { key: 'actions', label: 'Actions' }
]

const accounts = computed(() => groupsState.value?.accounts || [])

const rows = computed<GroupRow[]>(() =>
  (groupsState.value?.groups || []).map((summary) => {
    const group = summary.config
    return {
      id: group.id,
      name: group.name || group.id,
      platform: group.platform || 'mixed',
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
  form.platform = 'mixed'
  form.description = ''
  form.status = 'active'
  form.account_ids = []
  form.tags = ''
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

function splitList(value: string) {
  return value.split(/[,\n]+/).map((item) => item.trim()).filter(Boolean)
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
    created_at: '',
    updated_at: ''
  }
}

async function saveGroup() {
  if (!groupsState.value) return
  const group = buildGroup()
  if (editorMode.value === 'create') {
    await createGroup(groupsState.value.config_version, group)
    notice.value = `Created group ${group.name}.`
  } else {
    await updateGroup(groupsState.value.config_version, group)
    notice.value = `Updated group ${group.name}.`
  }
  editorOpen.value = false
  await loadAll()
}

async function toggleGroup(row: GroupRow) {
  if (!groupsState.value) return
  const group = structuredClone(row.summary.config)
  group.status = group.status === 'active' ? 'disabled' : 'active'
  await updateGroup(groupsState.value.config_version, group)
  notice.value = `${group.name} ${group.status}.`
  await loadAll()
}

async function removeGroup(row: GroupRow) {
  if (!groupsState.value) return
  await deleteGroup(groupsState.value.config_version, row.id)
  notice.value = `Deleted group ${row.name}.`
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
              <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Group Management</h2>
              <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">Manage API key groups. Groups bind gateway keys to OpenAI, Claude, Gemini, or Antigravity account pools without billing rate logic.</p>
            </div>
            <button class="btn btn-primary" type="button" @click="openCreate">Create Group</button>
          </div>
        </article>
        <article class="card">
          <p class="text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Active groups</p>
          <p class="mt-2 text-3xl font-black text-gray-950 dark:text-white">{{ rows.filter((row) => row.status === 'active').length }}</p>
        </article>
      </section>

      <section class="card">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <input v-model="search" class="input sm:max-w-md" placeholder="Search groups, platform, status, or account ID" />
          <button class="btn btn-secondary" type="button" :disabled="loading" @click="loadAll"><Icon name="refresh" /><span class="ml-2">Refresh</span></button>
        </div>
      </section>

      <p v-if="notice" class="rounded-xl border border-primary-200 bg-primary-50 p-3 text-sm text-primary-700 dark:border-primary-900 dark:bg-primary-950/40 dark:text-primary-200">{{ notice }}</p>
      <p v-if="error" class="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-200">{{ error }}</p>

      <section class="card">
        <DataTable :columns="columns" :rows="filteredRows" empty-text="No groups match the current filter.">
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
              <p>{{ row.accountCount }} linked accounts</p>
              <p class="break-all text-gray-500 dark:text-gray-400">{{ row.accounts.join(', ') || 'none' }}</p>
            </div>
          </template>
          <template #cell-status="{ row }"><StatusBadge :status="String(row.status)" /></template>
          <template #cell-actions="{ row }">
            <div class="flex flex-wrap gap-2">
              <button class="btn btn-secondary px-3 py-1.5" type="button" @click="openEdit(row)">Edit</button>
              <button class="btn btn-secondary px-3 py-1.5" type="button" @click="toggleGroup(row)">{{ row.status === 'active' ? 'Disable' : 'Enable' }}</button>
              <button class="btn btn-danger px-3 py-1.5" type="button" @click="removeGroup(row)">Delete</button>
            </div>
          </template>
        </DataTable>
      </section>

      <div v-if="editorOpen" class="xforce-modal-backdrop fixed inset-0 z-50 grid place-items-center">
        <section class="card xforce-modal-panel max-h-[92vh] w-full max-w-4xl overflow-auto">
          <div class="flex items-center justify-between gap-3">
            <div>
              <h2 class="text-lg font-black">{{ editorMode === 'create' ? 'Create Group' : 'Edit Group' }}</h2>
              <p class="text-xs text-gray-500 dark:text-gray-400">Groups define which upstream account pool an API key can use. Rate multipliers are intentionally omitted.</p>
            </div>
            <button class="btn btn-secondary" type="button" @click="editorOpen = false">Close</button>
          </div>
          <div class="mt-4 grid gap-4">
            <input v-model="form.id" type="hidden" />
            <label class="grid gap-1 text-sm font-semibold">Name<input v-model="form.name" class="input" placeholder="OpenAI primary group" /></label>
            <label class="grid gap-1 text-sm font-semibold">Description<textarea v-model="form.description" class="input min-h-20" placeholder="Optional local routing note" /></label>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="grid gap-1 text-sm font-semibold">Platform
                <select v-model="form.platform" class="input">
                  <option value="mixed">Mixed</option>
                  <option value="openai">OpenAI</option>
                  <option value="anthropic">Claude</option>
                  <option value="gemini">Gemini</option>
                  <option value="antigravity">Antigravity</option>
                </select>
              </label>
              <label class="grid gap-1 text-sm font-semibold">Status
                <select v-model="form.status" class="input">
                  <option value="active">active</option>
                  <option value="disabled">disabled</option>
                </select>
              </label>
            </div>
            <label class="grid gap-1 text-sm font-semibold">Tags<input v-model="form.tags" class="input" placeholder="optional matching aliases" /></label>

            <section class="rounded-2xl border border-gray-200 p-4 dark:border-dark-700">
              <div class="mb-3 flex items-center justify-between gap-3">
                <h3 class="text-sm font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">Accounts in this group</h3>
                <span class="badge badge-primary">{{ form.account_ids.length }} selected</span>
              </div>
              <div class="grid gap-2 sm:grid-cols-2">
                <label v-for="account in accounts" :key="account.id" :class="['rounded-xl border-2 p-3 transition', form.account_ids.includes(account.id) ? 'border-primary-500 bg-primary-50 dark:bg-primary-950/30' : 'border-gray-200 dark:border-dark-700']">
                  <input :checked="form.account_ids.includes(account.id)" type="checkbox" class="mr-2" @change="toggleAccount(account.id)" />
                  <span class="font-semibold">{{ accountLabel(account) }}</span>
                  <span class="ml-2 text-xs text-gray-500 dark:text-gray-400">{{ accountPlatform(account) }} · {{ account.id }}</span>
                </label>
              </div>
              <p v-if="accounts.length === 0" class="text-sm text-gray-500 dark:text-gray-400">Create accounts first, then return here to assign them into routing groups.</p>
            </section>
          </div>
          <div class="mt-4 flex flex-wrap justify-end gap-2">
            <button class="btn btn-secondary" type="button" @click="editorOpen = false">Cancel</button>
            <button class="btn btn-primary" type="button" :disabled="!form.name.trim()" @click="saveGroup">Save Group</button>
          </div>
        </section>
      </div>
    </main>
  </AppShell>
</template>
