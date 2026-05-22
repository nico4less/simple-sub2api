<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { logout } from '@/api/client'
import Icon from '@/components/Icon.vue'

const route = useRoute()
const router = useRouter()

const isAccounts = computed(() => route.path === '/admin/accounts')
const isGroups = computed(() => route.path === '/admin/groups')
const isProxies = computed(() => route.path === '/admin/proxies')
const pageTitle = computed(() => {
  if (isGroups.value) return 'Group Management'
  if (isAccounts.value) return 'Accounts'
  if (isProxies.value) return 'Proxies'
  return 'Dashboard / API Keys'
})
const subtitle = computed(() =>
  isGroups.value
    ? 'Manage API key routing groups before assigning upstream accounts.'
    : isAccounts.value
      ? 'Account/subscription input, import/export, testing, runtime status, and local enablement controls.'
      : isProxies.value
        ? 'Manage local proxy URLs used by accounts; supports standard add, batch import, copy, edit, and safe delete.'
        : 'Simple statistics, Recent Usage (Top 12), gateway endpoint hints, and key reveal/rotate actions.'
)

async function handleLogout() {
  await logout().catch(() => undefined)
  router.push('/login')
}
</script>

<template>
  <div class="relative min-h-screen bg-mesh-gradient">
    <div class="pointer-events-none fixed inset-x-0 top-0 z-10 h-28 bg-gradient-to-b from-[rgba(245,240,230,0.94)] to-transparent"></div>
    <div class="mx-auto grid max-w-7xl gap-4 px-3 py-4 sm:px-4 lg:px-6">
      <nav class="card sticky top-3 z-20 flex flex-col justify-between gap-3 p-3 sm:flex-row sm:items-center">
        <div class="flex min-w-0 items-center gap-3">
          <span class="grid h-10 w-10 place-items-center rounded-xl border border-[rgba(90,74,48,0.22)] bg-[rgba(250,247,240,0.72)] text-[var(--xforce-teal)] shadow-glow">
            <Icon name="key" />
          </span>
          <div class="min-w-0">
            <p class="simple-sub2api-wordmark truncate text-lg font-semibold"><span>Simple</span><span> Sub2API</span></p>
            <p class="truncate font-mono text-xs uppercase tracking-[0.22em] text-gray-500 dark:text-gray-400">Local gateway · account routing console</p>
          </div>
        </div>
        <div class="flex flex-wrap gap-2">
          <RouterLink :class="['btn', !isAccounts && !isGroups && !isProxies ? 'btn-primary' : 'btn-secondary']" to="/dashboard">
            <Icon name="dashboard" />
            <span class="ml-2">Dashboard / API Keys</span>
          </RouterLink>
          <RouterLink :class="['btn', isGroups ? 'btn-primary' : 'btn-secondary']" to="/admin/groups">
            <Icon name="accounts" />
            <span class="ml-2">Groups</span>
          </RouterLink>
          <RouterLink :class="['btn', isAccounts ? 'btn-primary' : 'btn-secondary']" to="/admin/accounts">
            <Icon name="accounts" />
            <span class="ml-2">Accounts</span>
          </RouterLink>
          <RouterLink :class="['btn', isProxies ? 'btn-primary' : 'btn-secondary']" to="/admin/proxies">
            <Icon name="play" />
            <span class="ml-2">Proxies</span>
          </RouterLink>
          <button class="btn btn-secondary" type="button" @click="handleLogout">
            <Icon name="logout" />
            <span class="ml-2">Logout</span>
          </button>
        </div>
      </nav>

      <header class="card p-5 sm:p-6">
        <span class="xforce-subtle-orbit"></span>
        <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <span class="badge badge-primary">LOCAL PERSONAL GATEWAY</span>
            <div class="mt-3 space-y-2">
              <h1 class="text-3xl font-light leading-tight tracking-[-0.04em] text-gray-950 dark:text-white sm:text-5xl" style="font-family: var(--xforce-font-display)">{{ pageTitle }}</h1>
              <p class="max-w-3xl text-sm leading-7 text-gray-600 dark:text-gray-300">{{ subtitle }}</p>
            </div>
          </div>
          <div class="grid gap-2 font-mono text-xs uppercase tracking-[0.16em] text-[var(--xforce-gray)] sm:grid-cols-2 lg:w-96">
            <span class="xforce-terminal-line rounded px-3 py-2">Mainnet local</span>
            <span class="xforce-terminal-line rounded px-3 py-2">AI route layer</span>
          </div>
        </div>
      </header>

      <slot />
    </div>
  </div>
</template>
