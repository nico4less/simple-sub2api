<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ status?: string }>()

const badgeClass = computed(() => {
  const status = (props.status || 'unknown').toLowerCase()
  if (['healthy', 'ok', 'enabled', 'active'].includes(status)) return 'border-[rgba(74,138,72,0.28)] bg-[rgba(74,138,72,0.06)] text-[var(--xforce-green)]'
  if (['cooldown', 'warning', 'needs_auth', 'unknown'].includes(status)) return 'border-[rgba(138,100,40,0.30)] bg-[rgba(138,100,40,0.08)] text-[var(--xforce-warning)]'
  return 'border-[rgba(176,48,48,0.32)] bg-[rgba(176,48,48,0.08)] text-[var(--xforce-danger)]'
})
</script>

<template>
  <span :class="['badge', badgeClass]"><span class="h-1.5 w-1.5 rounded-full bg-current shadow-[0_0_8px_currentColor]"></span>{{ status || 'unknown' }}</span>
</template>
