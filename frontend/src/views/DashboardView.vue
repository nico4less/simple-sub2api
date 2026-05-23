<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { createKey, deleteKey, loadKeys, loadRecentUsage } from '@/api/client'
import AppShell from '@/components/AppShell.vue'
import Icon from '@/components/Icon.vue'
import UseKeyModal from '@/components/keys/UseKeyModal.vue'
import MetricCard from '@/components/MetricCard.vue'
import RecentUsageCard from '@/components/RecentUsageCard.vue'
import { copyTextToClipboard } from '@/composables/useClipboard'
import { useAdminState } from '@/composables/useAdminState'
import type { AccountConfig, GatewayKey, GroupConfig, KeyRoutingPolicy, UsageAggregate } from '@/api/client'

const { t } = useI18n()
const { state, metrics, loading, error, refresh } = useAdminState()
const keysConfigVersion = ref(0)
const gatewayKeys = ref<GatewayKey[]>([])
const keyAccounts = ref<AccountConfig[]>([])
const keyGroups = ref<GroupConfig[]>([])
const notice = ref('')
const newKeyValue = ref('')
const keyDraft = ref({ name: 'Personal Gateway Key', customKey: '', mode: 'groups', groupIds: '', tiers: '', tags: '', accountIds: '', note: '' })
const plaintextKeysByID = ref<Record<string, string>>({})
const recentUsage = ref<UsageAggregate[]>([])
const recentUsageLoading = ref(false)
const recentUsageError = ref('')
const usageModalKey = ref<GatewayKey | null>(null)
const copiedKeyID = ref('')

const metricCards = computed(() => {
  const snapshot = metrics.value
  return [
    { label: t('dashboard.metrics.qps'), value: Number(snapshot.qps || 0).toFixed(4), hint: t('dashboard.metrics.currentRate') },
    { label: t('dashboard.metrics.total'), value: snapshot.total_requests || 0, hint: t('dashboard.metrics.gatewayRequests') },
    { label: t('dashboard.metrics.success'), value: snapshot.success_requests || 0, hint: t('dashboard.metrics.forwarded') },
    { label: t('dashboard.metrics.errors'), value: snapshot.error_requests || 0, hint: t('dashboard.metrics.failures') },
    { label: t('dashboard.metrics.hitRate'), value: `${Math.round(Number(snapshot.success_rate || 0) * 100)}%`, hint: t('dashboard.metrics.successRatio') },
    { label: t('dashboard.metrics.uptime'), value: `${snapshot.uptime_seconds || 0}s`, hint: t('dashboard.metrics.recorderUptime') }
  ]
})

async function refreshKeys() {
  const result = await loadKeys()
  keysConfigVersion.value = result.config_version
  gatewayKeys.value = (result.keys || []).map((key) => ({ ...key, key_value: key.key_value || plaintextKeysByID.value[key.id] }))
  keyAccounts.value = result.accounts || []
  keyGroups.value = result.groups || []
  syncSelectedGroup()
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
  syncSelectedGroup()
  const result = await createKey(keysConfigVersion.value, {
    name: keyDraft.value.name,
    status: 'enabled',
    routing_policy: draftPolicy(),
    note: keyDraft.value.note
  }, keyDraft.value.customKey)
  newKeyValue.value = result.key_value
  plaintextKeysByID.value = { ...plaintextKeysByID.value, [result.key.id]: result.key_value }
  notice.value = t('dashboard.keyCreated')
  keyDraft.value.customKey = ''
  await refresh()
  await refreshKeys()
}

async function removeGatewayKey(key: GatewayKey) {
  await deleteKey(keysConfigVersion.value, key.id)
  notice.value = t('dashboard.keyDeleted', { name: key.name })
  await refreshKeys()
}

async function copyNewKey() {
  const copied = await copyTextToClipboard(newKeyValue.value)
  notice.value = copied ? t('dashboard.gatewayKeyCopied') : t('dashboard.copyFailed')
}

async function copyKeyValue(key: GatewayKey) {
  const value = key.key_value || key.preview
  const copied = await copyTextToClipboard(value)
  copiedKeyID.value = copied ? key.id : ''
  notice.value = copied
    ? key.key_value
      ? t('dashboard.apiKeyCopied')
      : t('dashboard.previewCopied')
    : t('dashboard.copyFailed')
  if (copied) {
    window.setTimeout(() => {
      if (copiedKeyID.value === key.id) copiedKeyID.value = ''
    }, 2000)
  }
}

function openUsageModal(key: GatewayKey) {
  usageModalKey.value = key
}

function closeUsageModal() {
  usageModalKey.value = null
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

const groupOptions = computed(() => [...keyGroups.value]
  .filter((group) => group.status !== 'disabled')
  .sort((left, right) => {
    if (left.id === 'openai') return -1
    if (right.id === 'openai') return 1
    return left.id.localeCompare(right.id)
  }))
const selectedGroup = computed(() => groupOptions.value.find((group) => group.id === keyDraft.value.groupIds) || groupOptions.value[0])
const usageModalPlatform = computed(() => {
  const key = usageModalKey.value
  if (!key) return null
  return keyPlatform(key)
})
const usageModalBaseUrl = computed(() => `${window.location.origin.replace(/\/+$/, '')}/v1`)

function keyPlatform(key: GatewayKey) {
  const groupID = (key.routing_policy.group_ids || [])[0] || ''
  const group = keyGroups.value.find((item) => item.id === groupID)
  return group?.platform || 'openai'
}

function keyStripeClass(key: GatewayKey) {
  const platform = keyPlatform(key)
  if (platform === 'anthropic') return 'bg-orange-500 dark:bg-orange-500'
  if (platform === 'gemini' || platform === 'antigravity') return 'bg-purple-500 dark:bg-purple-500'
  return 'bg-green-500 dark:bg-green-500'
}


function syncSelectedGroup() {
  if (keyDraft.value.mode !== 'groups') return
  if (groupOptions.value.some((group) => group.id === keyDraft.value.groupIds)) return
  keyDraft.value.groupIds = groupOptions.value[0]?.id || ''
}

function describePolicy(policy: KeyRoutingPolicy) {
  if (policy.mode === 'groups') return t('dashboard.policy.groups', { value: (policy.group_ids || []).join(', ') || t('dashboard.policy.anyActiveGroup') })
  if (policy.mode === 'tier_preference') return t('dashboard.policy.tiers', { value: (policy.tiers || []).join(', ') || t('dashboard.policy.routingDefault') })
  if (policy.mode === 'tags') return t('dashboard.policy.tags', { value: (policy.tags || []).join(', ') || t('dashboard.policy.any') })
  if (policy.mode === 'account_ids') return t('dashboard.policy.accounts', { value: (policy.account_ids || []).join(', ') || t('dashboard.policy.any') })
  return t('dashboard.policy.allEnabled')
}

watch(() => keyDraft.value.mode, syncSelectedGroup)
watch(groupOptions, syncSelectedGroup)

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
              <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ $t('dashboard.gatewayEndpoint') }}</h2>
              <p class="mt-2 font-mono text-sm text-gray-700 dark:text-gray-200">{{ $t('dashboard.baseUrlSameOrigin') }}</p>
              <p class="mt-1 font-mono text-sm text-gray-700 dark:text-gray-200">{{ $t('dashboard.authorizationBearer') }}</p>
            </div>
            <button class="btn btn-secondary" type="button" :disabled="loading" @click="refresh">
              <Icon name="refresh" />
              <span class="ml-2">{{ $t('dashboard.refreshState') }}</span>
            </button>
          </div>
        </article>
        <article class="card">
          <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ $t('dashboard.gatewayKeys') }}</h2>
          <p class="mt-2 text-3xl font-semibold text-gray-900 dark:text-white">{{ gatewayKeys.length }}</p>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ $t('dashboard.gatewayKeysHint') }}</p>
        </article>
      </section>

      <p v-if="notice" class="rounded-xl border border-primary-200 bg-primary-50 p-3 text-sm text-primary-700 dark:border-primary-900 dark:bg-primary-950/40 dark:text-primary-200">{{ notice }}</p>
      <p v-if="error" class="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-200">{{ error }}</p>

      <section class="grid gap-4 lg:grid-cols-3">
        <article class="card lg:col-span-2">
          <div class="mb-3 flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
            <div>
          <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ $t('dashboard.apiKeys') }}</h2>
              <p class="text-sm text-gray-500 dark:text-gray-400">{{ $t('dashboard.apiKeysHint') }}</p>
              <p class="mt-1 text-xs text-amber-600 dark:text-amber-300">{{ $t('dashboard.familyWarning') }}</p>
            </div>
            <button class="btn btn-secondary" type="button" @click="refreshKeys">{{ $t('dashboard.refreshKeys') }}</button>
          </div>
          <div class="grid gap-3">
            <div v-for="key in gatewayKeys" :key="key.id" class="relative overflow-hidden rounded-2xl border border-gray-200 p-4 pl-5 dark:border-dark-700">
              <span aria-hidden="true" :class="['absolute inset-y-0 left-0 w-1.5', keyStripeClass(key)]"></span>
              <div class="flex flex-col justify-between gap-3 md:flex-row md:items-start">
                <div>
                  <div class="flex flex-wrap items-center gap-2">
                    <h3 class="font-semibold text-gray-900 dark:text-white">{{ key.name }}</h3>
                    <span class="rounded-full px-2 py-1 text-xs font-semibold" :class="key.status === 'enabled' ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-200' : 'bg-gray-100 text-gray-500 dark:bg-dark-800 dark:text-gray-400'">{{ key.status }}</span>
                  </div>
                  <div class="mt-1 flex flex-wrap items-center gap-2">
                    <p class="font-mono text-xs text-gray-500 dark:text-gray-400">{{ key.key_value || key.preview }}</p>
                    <button class="inline-flex rounded-lg border border-gray-200 p-1 text-gray-500 transition hover:border-primary-300 hover:text-primary-600 dark:border-dark-700 dark:hover:border-primary-700 dark:hover:text-primary-300" type="button" :title="copiedKeyID === key.id ? $t('common.copied') : $t('common.copy')" @click="copyKeyValue(key)">
                      <Icon name="copy" />
                    </button>
                  </div>
                  <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ describePolicy(key.routing_policy) }}</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-500">{{ $t('dashboard.createdLastUsed', { created: key.created_at, lastUsed: key.last_used_at || $t('common.never') }) }}</p>
                  <p v-if="key.note" class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ key.note }}</p>
                </div>
                <div class="flex flex-wrap gap-2">
                  <button class="btn btn-secondary" type="button" @click="openUsageModal(key)">{{ $t('dashboard.usage') }}</button>
                  <button class="btn btn-danger" type="button" @click="removeGatewayKey(key)">{{ $t('common.delete') }}</button>
                </div>
              </div>
            </div>
            <p v-if="gatewayKeys.length === 0" class="rounded-2xl border border-dashed border-gray-300 p-4 text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">{{ $t('dashboard.noKeys') }}</p>
          </div>
        </article>
        <article class="card">
          <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ $t('dashboard.createKey') }}</h2>
          <div class="mt-3 grid gap-3">
            <label class="grid gap-1 text-sm">{{ $t('dashboard.name') }}<input v-model="keyDraft.name" class="input" /></label>
            <label class="grid gap-1 text-sm">{{ $t('dashboard.customKeyOptional') }}<input v-model="keyDraft.customKey" class="input font-mono" placeholder="s2a_..." /></label>
            <label class="grid gap-1 text-sm">{{ $t('dashboard.group') }}<select v-model="keyDraft.groupIds" class="input" :disabled="groupOptions.length === 0"><option v-for="group in groupOptions" :key="group.id" :value="group.id">{{ group.name || group.id }} · {{ group.platform }}</option></select></label>
            <p v-if="selectedGroup" class="text-xs text-gray-500 dark:text-gray-400">{{ $t('dashboard.currentGroup', { group: selectedGroup.id }) }}</p>
            <p v-if="groupOptions.length === 0" class="text-xs text-red-500">{{ $t('dashboard.noActiveGroups') }}</p>
            <label class="grid gap-1 text-sm">{{ $t('dashboard.note') }}<textarea v-model="keyDraft.note" class="input min-h-20" /></label>
            <button class="btn btn-primary" type="button" :disabled="groupOptions.length === 0" @click="createGatewayKey">{{ $t('dashboard.createKey') }}</button>
          </div>
          <div v-if="newKeyValue" class="mt-4 rounded-xl border border-amber-200 bg-amber-50 p-3 dark:border-amber-900 dark:bg-amber-950/30">
            <p class="text-xs font-semibold uppercase tracking-wide text-amber-700 dark:text-amber-200">{{ $t('dashboard.copyNow') }}</p>
            <p class="mt-2 break-all font-mono text-xs text-amber-900 dark:text-amber-100">{{ newKeyValue }}</p>
            <button class="btn btn-secondary mt-3" type="button" @click="copyNewKey">{{ $t('common.copy') }}</button>
          </div>
        </article>
      </section>

      <UseKeyModal
        :show="Boolean(usageModalKey)"
        :api-key="usageModalKey?.key_value || usageModalKey?.preview || ''"
        :base-url="usageModalBaseUrl"
        :platform="usageModalPlatform"
        :allow-messages-dispatch="true"
        @close="closeUsageModal"
      />

      <section class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <MetricCard v-for="item in metricCards" :key="item.label" :label="item.label" :value="item.value" :hint="item.hint" />
      </section>

      <section class="grid gap-4 lg:grid-cols-3">
        <RecentUsageCard :rows="recentUsage" :loading="recentUsageLoading" :error="recentUsageError" @refresh="refreshRecentUsage" />
        <article class="card">
          <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ $t('dashboard.runtimeStatus') }}</h2>
          <pre class="mt-3 overflow-auto rounded-xl bg-gray-100 p-3 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300">{{ JSON.stringify({ version: state?.version, config_version: state?.config.config_version, gateway_key_configured: state?.gateway.key_configured }, null, 2) }}</pre>
        </article>
      </section>
    </main>
  </AppShell>
</template>
