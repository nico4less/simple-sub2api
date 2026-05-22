export interface AdminState {
  version: Record<string, unknown>
  config: AdminConfig
  account_pool?: AccountPool
  quota_state?: QuotaState[]
  metrics?: MetricsSnapshot
  recent_usage?: UsageAggregate[]
  gateway: { key_configured: boolean }
}

export interface RecentUsageResponse {
  generated_at: string
  limit: number
  top_usage: UsageAggregate[]
}

export interface AdminConfig {
  config_version: number
  gateway_keys?: GatewayKey[]
  groups?: GroupConfig[]
  accounts?: AccountConfig[]
  proxies?: ProxyConfig[]
  quota?: { policies?: QuotaPolicy[]; [key: string]: unknown }
  [key: string]: unknown
}

export interface KeyRoutingPolicy {
  mode: 'all_enabled' | 'tier_preference' | 'tags' | 'account_ids' | 'groups'
  group_ids?: string[]
  tiers?: string[]
  tags?: string[]
  account_ids?: string[]
}

export interface GatewayKey {
  id: string
  name: string
  key_hash?: string
  key_value?: string
  preview: string
  status: 'enabled' | 'disabled'
  routing_policy: KeyRoutingPolicy
  created_at: string
  updated_at: string
  last_used_at?: string
  note?: string
}

export interface KeysResponse {
  config_version: number
  keys: GatewayKey[]
  accounts: AccountConfig[]
  groups: GroupConfig[]
}

export interface KeyMutationResponse {
  config_version: number
  key: GatewayKey
  key_value: string
}

export interface AccountConfig {
  id: string
  source_id?: string
  type: string
  label?: string
  tier?: string
  tags?: string[]
  credential?: string
  metadata?: Record<string, unknown>
  base_url?: string
  model?: string
  proxy_ref?: string
  quota_policy?: string
  enabled?: boolean
}

export interface GroupConfig {
  id: string
  name: string
  platform: 'openai' | 'anthropic' | 'gemini' | 'antigravity'
  description?: string
  status: 'active' | 'disabled'
  tags?: string[]
  account_ids?: string[]
  created_at: string
  updated_at: string
}

export interface GroupSummary {
  config: GroupConfig
  account_count: number
}

export interface GroupsResponse {
  config_version: number
  groups: GroupSummary[]
  accounts: AccountConfig[]
}

export interface ProxyConfig {
  id: string
  url: string
}

export interface ProxyRowConfig extends ProxyConfig {
  name?: string
  protocol?: string
  host?: string
  port?: number
  username?: string
  password?: string
  status?: 'active' | 'disabled'
}

export interface QuotaPolicy {
  id: string
  source?: string
  daily_limit_tokens?: number
  weekly_limit_tokens?: number
}

export interface AccountHealth {
  account_id: string
  status: string
  message?: string
  checked_at?: string
  proxy_id?: string
}

export interface AccountSummary {
  config: AccountConfig
  runtime_status: string
  health?: AccountHealth
  quota?: Record<string, unknown>
  metrics?: { hits?: number; errors?: number }
  source?: string
  references?: Record<string, string>
}

export interface AccountsResponse {
  config_version: number
  accounts: AccountSummary[]
  proxies: ProxyConfig[]
  groups?: GroupConfig[]
  quota_policies: QuotaPolicy[]
  sources: string[]
}

export interface RuntimeAccount {
  account_id: string
  tier: string
  status: string
  quota?: { status?: string; [key: string]: unknown }
  [key: string]: unknown
}

export interface AccountPool {
  accounts: RuntimeAccount[]
  [key: string]: unknown
}

export interface QuotaState {
  account_id: string
  status: string
  usage_ratio?: number
  [key: string]: unknown
}

export interface MetricsSnapshot {
  qps?: number
  total_requests?: number
  success_requests?: number
  error_requests?: number
  success_rate?: number
  uptime_seconds?: number
  per_account_hits?: Record<string, number>
  per_account_errors?: Record<string, number>
  cooldown_counts?: Record<string, number>
  quota_switch_counts?: Record<string, number>
  recent_errors?: unknown[]
  recent_usage?: unknown[]
  top_usage?: UsageAggregate[]
  [key: string]: unknown
}

export interface UsageAggregate {
  rank?: number
  account_id: string
  account_label?: string
  gateway_key_id?: string
  gateway_key_preview?: string
  gateway_key_label?: string
  gateway_key_ref?: string
  requests: number
  successes: number
  errors: number
  success_rate?: number
  last_used_at?: string
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', ...(init.headers || {}) },
    ...init
  })
  if (!response.ok) {
    throw new Error(await response.text())
  }
  const contentType = response.headers.get('content-type') || ''
  if (!contentType.includes('application/json')) {
    return (await response.text()) as T
  }
  return response.json() as Promise<T>
}

export function login(password: string) {
  return api<{ ok: boolean }>('/api/admin/login', {
    method: 'POST',
    body: JSON.stringify({ password })
  })
}

export function logout() {
  return api<{ ok: boolean }>('/api/admin/logout', { method: 'POST' })
}

export function loadDashboardState() {
  return api<AdminState>('/api/admin/dashboard/state')
}

export function loadRecentUsage() {
  return api<RecentUsageResponse>('/api/admin/dashboard/recent-usage')
}

export function revealGatewayKey() {
  return api<{ gateway_key: string }>('/api/admin/gateway-key')
}

export function rotateGatewayKey() {
  return api<{ gateway_key: string }>('/api/admin/gateway-key/rotate', { method: 'POST' })
}

export function loadKeys() {
  return api<KeysResponse>('/api/keys')
}

export function loadGroups() {
  return api<GroupsResponse>('/api/admin/groups')
}

export function createGroup(configVersion: number, group: Partial<GroupConfig>) {
  return api<AdminConfig>('/api/admin/groups', {
    method: 'POST',
    body: JSON.stringify({ config_version: configVersion, group })
  })
}

export function updateGroup(configVersion: number, group: GroupConfig) {
  return api<AdminConfig>(`/api/admin/groups/${encodeURIComponent(group.id)}`, {
    method: 'PUT',
    body: JSON.stringify({ config_version: configVersion, group })
  })
}

export function deleteGroup(configVersion: number, groupID: string) {
  return api<AdminConfig>(`/api/admin/groups/${encodeURIComponent(groupID)}`, {
    method: 'DELETE',
    body: JSON.stringify({ config_version: configVersion })
  })
}

export function createKey(configVersion: number, key: Partial<GatewayKey>, keyValue = '') {
  return api<KeyMutationResponse>('/api/keys', {
    method: 'POST',
    body: JSON.stringify({ config_version: configVersion, key, key_value: keyValue })
  })
}

export function updateKey(configVersion: number, key: GatewayKey) {
  return api<AdminConfig>(`/api/keys/${encodeURIComponent(key.id)}`, {
    method: 'PUT',
    body: JSON.stringify({ config_version: configVersion, key })
  })
}

export function rotateKey(configVersion: number, keyID: string) {
  return api<KeyMutationResponse>(`/api/keys/${encodeURIComponent(keyID)}/rotate`, {
    method: 'POST',
    body: JSON.stringify({ config_version: configVersion })
  })
}

export function deleteKey(configVersion: number, keyID: string) {
  return api<AdminConfig>(`/api/keys/${encodeURIComponent(keyID)}`, {
    method: 'DELETE',
    body: JSON.stringify({ config_version: configVersion })
  })
}

export function saveConfig(configVersion: number, config: AdminConfig) {
  return api<AdminState>('/api/admin/config/save', {
    method: 'POST',
    body: JSON.stringify({ config_version: configVersion, config })
  })
}

export function loadAccounts() {
  return api<AccountsResponse>('/api/admin/accounts')
}

export function loadProxies() {
  return api<ProxyConfig[]>('/api/admin/proxies')
}

export function createProxy(configVersion: number, proxy: ProxyConfig) {
  return api<AdminConfig>('/api/admin/proxies', {
    method: 'POST',
    body: JSON.stringify({ config_version: configVersion, proxy })
  })
}

export function updateProxy(configVersion: number, proxy: ProxyConfig) {
  return api<AdminConfig>(`/api/admin/proxies/${encodeURIComponent(proxy.id)}`, {
    method: 'PUT',
    body: JSON.stringify({ config_version: configVersion, proxy })
  })
}

export function deleteProxy(configVersion: number, proxyID: string) {
  return api<AdminState>(`/api/admin/proxies/${encodeURIComponent(proxyID)}`, {
    method: 'DELETE',
    body: JSON.stringify({ config_version: configVersion })
  })
}

export function createAccount(configVersion: number, account: AccountConfig) {
  return api<AdminConfig>('/api/admin/accounts', {
    method: 'POST',
    body: JSON.stringify({ config_version: configVersion, account })
  })
}

export function updateAccount(configVersion: number, account: AccountConfig) {
  return api<AdminConfig>(`/api/admin/accounts/${encodeURIComponent(account.id)}`, {
    method: 'PUT',
    body: JSON.stringify({ config_version: configVersion, account })
  })
}

export function previewImport(content: string, kind = 'line_tokens') {
  return api<unknown>('/api/admin/accounts/import/preview', {
    method: 'POST',
    body: JSON.stringify({ kind, content, tier: 'simple', label: 'Dashboard Import' })
  })
}

export function applyImport(configVersion: number, content: string, kind = 'line_tokens') {
  return api<AdminState>('/api/admin/accounts/import/apply', {
    method: 'POST',
    body: JSON.stringify({
      config_version: configVersion,
      import: { kind, content, tier: 'simple', label: 'Dashboard Import' }
    })
  })
}

export function exportAccounts() {
  return api<AdminConfig>('/api/admin/accounts/export')
}

export function testAllAccounts() {
  return api<unknown>('/api/admin/account-check')
}

export function refreshAccount(accountID: string) {
  return api<AccountHealth>(`/api/admin/accounts/${encodeURIComponent(accountID)}/test`, { method: 'POST' })
}

export function deleteAccount(configVersion: number, accountID: string) {
  return api<AdminState>(`/api/admin/accounts/${encodeURIComponent(accountID)}`, {
    method: 'DELETE',
    body: JSON.stringify({ config_version: configVersion })
  })
}
