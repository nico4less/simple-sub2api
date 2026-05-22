<script setup lang="ts" generic="T extends Record<string, unknown>">
import type { Column } from '@/types/ui'

defineProps<{
  columns: Column<T>[]
  rows: T[]
  emptyText?: string
  rowClass?: (row: T) => string
}>()
</script>

<template>
  <div class="overflow-hidden rounded-2xl border border-gray-200 bg-[rgba(0,5,8,0.28)] dark:border-dark-700">
    <div class="hidden overflow-x-auto md:block">
      <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
        <thead class="bg-gray-50/80 text-left text-xs uppercase tracking-wider text-gray-500 dark:bg-dark-800/80 dark:text-gray-400">
          <tr>
            <th v-for="column in columns" :key="String(column.key)" class="px-4 py-3 font-semibold">
              {{ column.label }}
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 bg-white/60 dark:divide-dark-800 dark:bg-dark-900/50">
          <tr v-for="(row, rowIndex) in rows" :key="rowIndex" :class="['hover:bg-primary-50/40 dark:hover:bg-primary-900/10']">
            <td v-for="(column, columnIndex) in columns" :key="String(column.key)" :class="[columnIndex === 0 ? rowClass?.(row) : '', 'px-4 py-3 align-top']">
              <slot :name="`cell-${String(column.key)}`" :row="row" :value="row[column.key as keyof T]">
                {{ row[column.key as keyof T] }}
              </slot>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="space-y-3 p-3 md:hidden">
      <article v-for="(row, rowIndex) in rows" :key="rowIndex" :class="['rounded-xl border border-gray-200 bg-white/70 p-3 dark:border-dark-700 dark:bg-dark-800/70', rowClass?.(row)]">
        <dl class="space-y-2 text-sm">
          <div v-for="column in columns" :key="String(column.key)" class="flex justify-between gap-3">
            <dt class="text-gray-500 dark:text-gray-400">{{ column.label }}</dt>
            <dd class="text-right font-medium">
              <slot :name="`cell-${String(column.key)}`" :row="row" :value="row[column.key as keyof T]">
                {{ row[column.key as keyof T] }}
              </slot>
            </dd>
          </div>
        </dl>
      </article>
    </div>
    <p v-if="rows.length === 0" class="p-6 text-center text-sm text-gray-500 dark:text-gray-400">
      {{ emptyText || 'No records yet.' }}
    </p>
  </div>
</template>
