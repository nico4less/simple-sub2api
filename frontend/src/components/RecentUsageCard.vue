<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { UsageAggregate } from '@/api/client'
import DataTable from '@/components/DataTable.vue'
import Icon from '@/components/Icon.vue'
import type { Column } from '@/types/ui'

interface UsageRow extends Record<string, unknown> {
  rank: number
  account: string
  label: string
  key: string
  requests: number
  successes: number
  errors: number
  success_rate: string
  last_used_at: string
}

const { t } = useI18n()

const props = defineProps<{
  rows: UsageAggregate[]
  loading: boolean
  error: string
}>()

defineEmits<{
  refresh: []
}>()

const usageRows = computed<UsageRow[]>(() => {
  return props.rows.slice(0, 12).map((item, index) => {
    const successRate = typeof item.success_rate === 'number'
      ? item.success_rate
      : item.requests > 0
        ? item.successes / item.requests
        : 0
    return {
      rank: item.rank || index + 1,
      account: item.account_id || 'unassigned',
      label: item.account_label || item.gateway_key_label || 'n/a',
      key: item.gateway_key_preview || item.gateway_key_ref || item.gateway_key_id || 'n/a',
      requests: item.requests || 0,
      successes: item.successes || 0,
      errors: item.errors || 0,
      success_rate: `${Math.round(successRate * 100)}%`,
      last_used_at: item.last_used_at || 'n/a'
    }
  })
})

const usageColumns = computed<Column<UsageRow>[]>(() => [
  { key: 'rank', label: '#' },
  { key: 'account', label: t('recentUsage.columns.account') },
  { key: 'label', label: t('recentUsage.columns.label') },
  { key: 'key', label: t('recentUsage.columns.keyPreview') },
  { key: 'requests', label: t('recentUsage.columns.requests') },
  { key: 'successes', label: t('recentUsage.columns.ok') },
  { key: 'errors', label: t('recentUsage.columns.errors') },
  { key: 'success_rate', label: t('recentUsage.columns.successRate') },
  { key: 'last_used_at', label: t('recentUsage.columns.lastUsed') }
])
</script>

<template>
  <article class="card lg:col-span-2">
    <div class="mb-3 flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
      <div>
        <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ $t('recentUsage.title') }}</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ $t('recentUsage.subtitle') }}</p>
      </div>
      <button class="btn btn-secondary" type="button" :disabled="loading" @click="$emit('refresh')">
        <Icon name="refresh" />
        <span class="ml-2">{{ loading ? $t('common.refreshing') : $t('recentUsage.refreshUsage') }}</span>
      </button>
    </div>
    <p v-if="error" class="mb-3 rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-200">
      {{ error }}
    </p>
    <p v-if="loading && usageRows.length === 0" class="rounded-2xl border border-dashed border-gray-300 p-6 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
      {{ $t('recentUsage.loading') }}
    </p>
    <DataTable v-else :columns="usageColumns" :rows="usageRows" :empty-text="$t('recentUsage.empty')" />
  </article>
</template>
