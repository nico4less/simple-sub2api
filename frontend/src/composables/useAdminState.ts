import { computed, ref } from 'vue'

import { loadDashboardState, type AdminState } from '@/api/client'

const state = ref<AdminState | null>(null)
const loading = ref(false)
const error = ref('')

export function useAdminState() {
  const accounts = computed(() => state.value?.account_pool?.accounts || [])
  const quotaState = computed(() => state.value?.quota_state || [])
  const metrics = computed(() => state.value?.metrics || {})

  async function refresh() {
    loading.value = true
    error.value = ''
    try {
      state.value = await loadDashboardState()
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
      throw err
    } finally {
      loading.value = false
    }
  }

  return { state, accounts, quotaState, metrics, loading, error, refresh }
}
