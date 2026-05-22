<script setup lang="ts">
type IconName = 'dashboard' | 'accounts' | 'key' | 'refresh' | 'logout' | 'copy' | 'trash' | 'play'

defineProps<{ name: IconName }>()

const paths: Record<IconName, string> = {
  dashboard: 'M3 13h8V3H3v10Zm0 8h8v-6H3v6Zm10 0h8V11h-8v10Zm0-18v6h8V3h-8Z',
  accounts: 'M16 11c1.66 0 2.99-1.34 2.99-3S17.66 5 16 5s-3 1.34-3 3 1.34 3 3 3ZM8 11c1.66 0 2.99-1.34 2.99-3S9.66 5 8 5 5 6.34 5 8s1.34 3 3 3Zm0 2c-2.33 0-7 1.17-7 3.5V19h14v-2.5C15 14.17 10.33 13 8 13Zm8 0c-.29 0-.62.02-.97.05 1.16.84 1.97 1.97 1.97 3.45V19h6v-2.5c0-2.33-4.67-3.5-7-3.5Z',
  key: 'M7 14a5 5 0 1 1 4.9-6H23v4h-3v3h-4v3h-4.1A5 5 0 0 1 7 14Zm0-3a2 2 0 1 0 0-4 2 2 0 0 0 0 4Z',
  refresh: 'M17.65 6.35A7.95 7.95 0 0 0 12 4a8 8 0 1 0 7.45 10.9h-2.1A6 6 0 1 1 12 6c1.66 0 3.14.69 4.22 1.78L13 11h8V3l-3.35 3.35Z',
  logout: 'M10 17v-3H3v-4h7V7l5 5-5 5Zm2-14h9v18h-9v-2h7V5h-7V3Z',
  copy: 'M16 1H4c-1.1 0-2 .9-2 2v14h2V3h12V1Zm3 4H8c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h11c1.1 0 2-.9 2-2V7c0-1.1-.9-2-2-2Zm0 16H8V7h11v14Z',
  trash: 'M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12ZM8 9h8v10H8V9Zm7.5-5-1-1h-5l-1 1H5v2h14V4h-3.5Z',
  play: 'M8 5v14l11-7L8 5Z'
}
</script>

<template>
  <svg class="h-4 w-4" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    <path :d="paths[name]" />
  </svg>
</template>
