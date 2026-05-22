<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'

import { login } from '@/api/client'

const router = useRouter()
const password = ref('')
const loading = ref(false)
const error = ref('')

async function submit() {
  loading.value = true
  error.value = ''
  try {
    await login(password.value)
    router.push('/dashboard')
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="relative grid min-h-screen place-items-center overflow-hidden bg-mesh-gradient p-4">
    <div class="pointer-events-none absolute left-1/2 top-1/2 h-[520px] w-[520px] -translate-x-1/2 -translate-y-1/2 rounded-full border border-[rgba(58,51,40,0.08)]"></div>
    <form class="card w-full max-w-lg space-y-6 p-6 sm:p-8" @submit.prevent="submit">
      <span class="badge badge-primary">LOCAL ADMIN</span>
      <div class="space-y-3">
        <p class="font-mono text-xs uppercase tracking-[0.3em] text-[var(--xforce-teal-dim)]">local admin console</p>
        <h1 class="simple-sub2api-wordmark text-5xl leading-none tracking-[-0.04em] text-gray-950 dark:text-white sm:text-6xl"><span>Simple</span><span> Sub2API</span></h1>
        <p class="text-base leading-7 text-gray-600 dark:text-gray-300">Simple Sub2API parchment console for local AI gateway routing, account checks, and proxy control.</p>
        <div class="grid gap-2 font-mono text-xs uppercase tracking-[0.14em] sm:grid-cols-2">
          <span class="xforce-terminal-line rounded px-3 py-2">AI-assisted network</span>
          <span class="xforce-terminal-line rounded px-3 py-2">Gateway layer live</span>
        </div>
      </div>
      <label class="block space-y-2">
        <span class="text-sm font-medium text-gray-700 dark:text-gray-200">Admin password</span>
        <input v-model="password" class="input" type="password" autocomplete="current-password" autofocus placeholder="Admin password" />
      </label>
      <button class="btn btn-primary w-full" type="submit" :disabled="loading">
        {{ loading ? 'Authenticating...' : 'Enter Dashboard' }}
      </button>
      <p v-if="error" class="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-200">
        {{ error }}
      </p>
    </form>
  </main>
</template>
