<script setup lang="ts">
defineProps<{ label: string; value: string | number; hint?: string }>()
</script>

<template>
  <article class="card p-4">
    <p class="text-xs font-medium uppercase tracking-wider text-primary-700 dark:text-primary-200">{{ label }}</p>
    <p class="mt-2 font-mono text-2xl font-black tracking-tight text-gray-950 dark:text-white">{{ value }}</p>
    <p v-if="hint" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ hint }}</p>
  </article>
</template>
