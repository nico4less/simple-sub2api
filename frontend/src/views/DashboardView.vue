<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { createKey, deleteKey, loadKeys, loadRecentUsage, rotateKey, updateKey } from '@/api/client'
import AppShell from '@/components/AppShell.vue'
import Icon from '@/components/Icon.vue'
import MetricCard from '@/components/MetricCard.vue'
import RecentUsageCard from '@/components/RecentUsageCard.vue'
import { copyTextToClipboard } from '@/composables/useClipboard'
import { useAdminState } from '@/composables/useAdminState'
import type { AccountConfig, GatewayKey, GroupConfig, KeyRoutingPolicy, UsageAggregate } from '@/api/client'

const { state, metrics, loading, error, refresh } = useAdminState()
const keysConfigVersion = ref(0)
const gatewayKeys = ref<GatewayKey[]>([])
const keyAccounts = ref<AccountConfig[]>([])
const keyGroups = ref<GroupConfig[]>([])
const notice = ref('')
const newKeyValue = ref('')
const keyDraft = ref({ name: 'Personal Gateway Key', customKey: '', mode: 'groups', groupIds: 'default1x0', tiers: '', tags: '', accountIds: '', note: '' })
const recentUsage = ref<UsageAggregate[]>([])
const recentUsageLoading = ref(false)
const recentUsageError = ref('')

const metricCards = computed(() => {
  const snapshot = metrics.value
  return [
    { label: 'QPS', value: Number(snapshot.qps || 0).toFixed(4), hint: 'Current in-memory rate' },
    { label: 'Total', value: snapshot.total_requests || 0, hint: 'Gateway requests' },
    { label: 'Success', value: snapshot.success_requests || 0, hint: 'Forwarded successfully' },
    { label: 'Errors', value: snapshot.error_requests || 0, hint: 'Sanitized failures' },
    { label: 'Hit-rate', value: `${Math.round(Number(snapshot.success_rate || 0) * 100)}%`, hint: 'Success ratio' },
    { label: 'Uptime', value: `${snapshot.uptime_seconds || 0}s`, hint: 'Recorder uptime' }
  ]
})

async function refreshKeys() {
  const result = await loadKeys()
  keysConfigVersion.value = result.config_version
  gatewayKeys.value = result.keys || []
  keyAccounts.value = result.accounts || []
  keyGroups.value = result.groups || []
}

async function refreshRecentUsage() {
  recentUsageLoading.value = true
  recentUsageError.value = ''
  try {
    const result = await loadRecentUsage()
    recentUsage.value = result.top_usage || []
  } catch (err) {
    recentUsageError.value = err instanceof Error ? err.message : String(err)
  } finally {
    recentUsageLoading.value = false
  }
}

async function createGatewayKey() {
  const result = await createKey(keysConfigVersion.value, {
    name: keyDraft.value.name,
    status: 'enabled',
    routing_policy: draftPolicy(),
    note: keyDraft.value.note
  }, keyDraft.value.customKey)
  newKeyValue.value = result.key_value
  notice.value = 'Gateway key created. Copy the key value now; hashed keys cannot be revealed later.'
  keyDraft.value.customKey = ''
  await refresh()
  await refreshKeys()
}

async function rotateGatewayKeyRow(key: GatewayKey) {
  const result = await rotateKey(keysConfigVersion.value, key.id)
  newKeyValue.value = result.key_value
  notice.value = `Key ${key.name} rotated. Copy the new value now.`
  await refreshKeys()
}

async function toggleGatewayKey(key: GatewayKey) {
  await updateKey(keysConfigVersion.value, { ...key, status: key.status === 'enabled' ? 'disabled' : 'enabled' })
  notice.value = `Key ${key.name} ${key.status === 'enabled' ? 'disabled' : 'enabled'}.`
  await refreshKeys()
}

async function removeGatewayKey(key: GatewayKey) {
  await deleteKey(keysConfigVersion.value, key.id)
  notice.value = `Key ${key.name} deleted.`
  await refreshKeys()
}

async function copyNewKey() {
  const copied = await copyTextToClipboard(newKeyValue.value)
  notice.value = copied ? 'Gateway key copied.' : 'Copy failed. Select the key value and copy it manually.'
}

function draftPolicy(): KeyRoutingPolicy {
  return {
    mode: keyDraft.value.mode as KeyRoutingPolicy['mode'],
    group_ids: splitList(keyDraft.value.groupIds),
    tiers: splitList(keyDraft.value.tiers),
    tags: splitList(keyDraft.value.tags),
    account_ids: splitList(keyDraft.value.accountIds)
  }
}

function splitList(value: string) {
  return value.split(',').map((item) => item.trim()).filter(Boolean)
}

function describePolicy(policy: KeyRoutingPolicy) {
  if (policy.mode === 'groups') return `groups: ${(policy.group_ids || []).join(', ') || 'any active group'}`
  if (policy.mode === 'tier_preference') return `tiers: ${(policy.tiers || []).join(', ') || 'routing default'}`
  if (policy.mode === 'tags') return `tags: ${(policy.tags || []).join(', ') || 'any'}`
  if (policy.mode === 'account_ids') return `accounts: ${(policy.account_ids || []).join(', ') || 'any'}`
  return 'all enabled accounts'
}

onMounted(() => {
  refresh().catch(() => undefined)
  refreshKeys().catch(() => undefined)
  refreshRecentUsage().catch(() => undefined)
})
</script>

<template>
  <AppShell>
    <main class="grid gap-4">
      <section class="grid gap-4 lg:grid-cols-3">
        <article class="card lg:col-span-2">
          <div class="flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Gateway Endpoint</h2>
              <p class="mt-2 font-mono text-sm text-gray-700 dark:text-gray-200">Base URL: same-origin /v1</p>
              <p class="mt-1 font-mono text-sm text-gray-700 dark:text-gray-200">Authorization: Bearer &lt;gateway key&gt;</p>
            </div>
            <button class="btn btn-secondary" type="button" :disabled="loading" @click="refresh">
              <Icon name="refresh" />
              <span class="ml-2">Refresh State</span>
            </button>
          </div>
        </article>
        <article class="card">
          <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Gateway Keys</h2>
          <p class="mt-2 text-3xl font-semibold text-gray-900 dark:text-white">{{ gatewayKeys.length }}</p>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">Local hashed bearer keys with per-key routing policy.</p>
        </article>
      </section>

      <p v-if="notice" class="rounded-xl border border-primary-200 bg-primary-50 p-3 text-sm text-primary-700 dark:border-primary-900 dark:bg-primary-950/40 dark:text-primary-200">{{ notice }}</p>
      <p v-if="error" class="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-200">{{ error }}</p>

      <section class="grid gap-4 lg:grid-cols-3">
        <article class="card lg:col-span-2">
          <div class="mb-3 flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
            <div>
              <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">API Keys</h2>
              <p class="text-sm text-gray-500 dark:text-gray-400">Create, rotate, disable, or bind keys to accounts configured in /admin/accounts.</p>
            </div>
            <button class="btn btn-secondary" type="button" @click="refreshKeys">Refresh Keys</button>
          </div>
          <div class="grid gap-3">
            <div v-for="key in gatewayKeys" :key="key.id" class="rounded-2xl border border-gray-200 p-4 dark:border-dark-700">
              <div class="flex flex-col justify-between gap-3 md:flex-row md:items-start">
                <div>
                  <div class="flex flex-wrap items-center gap-2">
                    <h3 class="font-semibold text-gray-900 dark:text-white">{{ key.name }}</h3>
                    <span class="rounded-full px-2 py-1 text-xs font-semibold" :class="key.status === 'enabled' ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-200' : 'bg-gray-100 text-gray-500 dark:bg-dark-800 dark:text-gray-400'">{{ key.status }}</span>
                  </div>
                  <p class="mt-1 font-mono text-xs text-gray-500 dark:text-gray-400">{{ key.preview }}</p>
                  <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ describePolicy(key.routing_policy) }}</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-500">Created {{ key.created_at }} · Last used {{ key.last_used_at || 'never' }}</p>
                  <p v-if="key.note" class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ key.note }}</p>
                </div>
                <div class="flex flex-wrap gap-2">
                  <button class="btn btn-secondary" type="button" @click="toggleGatewayKey(key)">{{ key.status === 'enabled' ? 'Disable' : 'Enable' }}</button>
                  <button class="btn btn-secondary" type="button" @click="rotateGatewayKeyRow(key)">Rotate</button>
                  <button class="btn btn-danger" type="button" @click="removeGatewayKey(key)">Delete</button>
                </div>
              </div>
            </div>
            <p v-if="gatewayKeys.length === 0" class="rounded-2xl border border-dashed border-gray-300 p-4 text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">No keys configured yet.</p>
          </div>
        </article>
        <article class="card">
          <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Create Key</h2>
          <div class="mt-3 grid gap-3">
            <label class="grid gap-1 text-sm">Name<input v-model="keyDraft.name" class="input" /></label>
            <label class="grid gap-1 text-sm">Custom key (optional)<input v-model="keyDraft.customKey" class="input font-mono" placeholder="s2a_..." /></label>
            <label class="grid gap-1 text-sm">Routing mode<select v-model="keyDraft.mode" class="input"><option value="groups">Groups</option><option value="all_enabled">All enabled accounts</option><option value="tier_preference">Tier preference</option><option value="tags">Tags</option><option value="account_ids">Explicit account IDs</option></select></label>
            <label v-if="keyDraft.mode === 'groups'" class="grid gap-1 text-sm">Groups<input v-model="keyDraft.groupIds" class="input" :placeholder="keyGroups.map((g) => g.id).join(', ') || 'default1x0'" /></label>
            <label v-if="keyDraft.mode === 'tier_preference'" class="grid gap-1 text-sm">Tiers<input v-model="keyDraft.tiers" class="input" placeholder="simple, advanced" /></label>
            <label v-if="keyDraft.mode === 'tags'" class="grid gap-1 text-sm">Tags<input v-model="keyDraft.tags" class="input" placeholder="code, document" /></label>
            <label v-if="keyDraft.mode === 'account_ids'" class="grid gap-1 text-sm">Account IDs<input v-model="keyDraft.accountIds" class="input" :placeholder="keyAccounts.map((a) => a.id).join(', ')" /></label>
            <label class="grid gap-1 text-sm">Note<textarea v-model="keyDraft.note" class="input min-h-20" /></label>
            <button class="btn btn-primary" type="button" @click="createGatewayKey">Create Key</button>
          </div>
          <div v-if="newKeyValue" class="mt-4 rounded-xl border border-amber-200 bg-amber-50 p-3 dark:border-amber-900 dark:bg-amber-950/30">
            <p class="text-xs font-semibold uppercase tracking-wide text-amber-700 dark:text-amber-200">Copy now</p>
            <p class="mt-2 break-all font-mono text-xs text-amber-900 dark:text-amber-100">{{ newKeyValue }}</p>
            <button class="btn btn-secondary mt-3" type="button" @click="copyNewKey">Copy</button>
          </div>
        </article>
      </section>

      <section class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <MetricCard v-for="item in metricCards" :key="item.label" :label="item.label" :value="item.value" :hint="item.hint" />
      </section>

      <section class="grid gap-4 lg:grid-cols-3">
        <RecentUsageCard :rows="recentUsage" :loading="recentUsageLoading" :error="recentUsageError" @refresh="refreshRecentUsage" />
        <article class="card">
          <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Runtime Status</h2>
          <pre class="mt-3 overflow-auto rounded-xl bg-gray-100 p-3 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300">{{ JSON.stringify({ version: state?.version, config_version: state?.config.config_version, gateway_key_configured: state?.gateway.key_configured }, null, 2) }}</pre>
        </article>
      </section>
    </main>
  </AppShell>
</template>
