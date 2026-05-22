<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'

import {
  applyImport,
  createAccount,
  deleteAccount,
  exportAccounts,
  loadAccounts,
  loadGroups,
  previewImport,
  refreshAccount,
  testAllAccounts,
  updateAccount,
  type AccountConfig,
  type AccountHealth,
  type AccountSummary,
  type AccountsResponse,
  type GroupConfig,
  type GroupsResponse,
  type ProxyConfig
} from '@/api/client'
import AppShell from '@/components/AppShell.vue'
import DataTable from '@/components/DataTable.vue'
import Icon from '@/components/Icon.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { copyTextToClipboard } from '@/composables/useClipboard'
import { useAdminState } from '@/composables/useAdminState'
import { allModels, getModelsByPlatform } from '@/composables/useModelWhitelist'
import type { Column } from '@/types/ui'

interface AccountRow extends Record<string, unknown> {
  id: string
  label: string
  type: string
  tier: string
  tags: string[]
  source: string
  proxy: string
  model: string
  baseURL: string
  enabled: boolean
  status: string
  healthStatus: string
  lastChecked: string
  lastError: string
  quotaStatus: string
  hits: number
  errors: number
  summary: AccountSummary
}

const { refresh } = useAdminState()
const loading = ref(false)
const error = ref('')
const notice = ref('')
const accountsState = ref<AccountsResponse | null>(null)
const groupsState = ref<GroupsResponse | null>(null)
const search = ref('')
const statusFilter = ref('all')
const typeFilter = ref('all')
const importOpen = ref(false)
const importContent = ref('')
const importKind = ref<'line_tokens' | 'json_bundle' | 'inline_bundle'>('line_tokens')
const operationOutput = ref('')
const testResult = ref<AccountHealth | null>(null)
const editorOpen = ref(false)
const editorMode = ref<'create' | 'edit'>('create')
const editorStep = ref<1 | 2>(1)
const showAdvancedAccountOptions = ref(false)
const step = editorStep
const oauthStepTitle = computed(() => authorizationStepTitle.value)
const showAdvancedOAuth = ref(false)
const geminiAIStudioOAuthEnabled = ref(true)
const showGeminiHelpDialog = ref(false)
const modelSearchQuery = ref('')
const customModelInput = ref('')
const isCustomModelComposing = ref(false)

const computedAuthUrl = ref('')
const copiedUrl = ref(false)
const activeOAuthTab = ref('manual')
const authCodeInput = ref('')
const accountNameInput = ref<HTMLInputElement | null>(null)

const OAUTH_CALLBACK_URL = 'http://localhost:1455/auth/callback'
const ANTIGRAVITY_CALLBACK_URL = 'http://localhost:8085/callback'
const CLAUDE_OAUTH_AUTHORIZE_URL = 'https://claude.ai/oauth/authorize'
const CLAUDE_OAUTH_CLIENT_ID = '9d1c250a-e61b-44d9-88ed-5944d1962f5e'
const CLAUDE_OAUTH_REDIRECT_URI = 'https://platform.claude.com/oauth/code/callback'
const CLAUDE_OAUTH_SCOPE = 'org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload'
const CLAUDE_SETUP_TOKEN_SCOPE = 'user:inference'
const OPENAI_CODEX_CLIENT_ID = 'app_EMoamEEZ73f0CkXaXp7hrann'
const GEMINI_CODE_ASSIST_CALLBACK_URL = 'https://codeassist.google.com/authcode'
const GEMINI_CLI_CLIENT_ID = '681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com'
const ANTIGRAVITY_CLIENT_ID = '1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com'

function base64UrlEncode(bytes: Uint8Array): string {
  let binary = ''
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte)
  })
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')
}

function rightRotate(value: number, amount: number): number {
  return (value >>> amount) | (value << (32 - amount))
}

function sha256Digest(input: Uint8Array): Uint8Array {
  const k = new Uint32Array([
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
  ])
  const h = new Uint32Array([
    0x6a09e667,
    0xbb67ae85,
    0x3c6ef372,
    0xa54ff53a,
    0x510e527f,
    0x9b05688c,
    0x1f83d9ab,
    0x5be0cd19
  ])
  const bitLength = input.length * 8
  const paddedLength = Math.ceil((input.length + 9) / 64) * 64
  const padded = new Uint8Array(paddedLength)
  padded.set(input)
  padded[input.length] = 0x80
  const view = new DataView(padded.buffer)
  view.setUint32(paddedLength - 4, bitLength, false)
  const w = new Uint32Array(64)

  for (let offset = 0; offset < paddedLength; offset += 64) {
    for (let index = 0; index < 16; index += 1) {
      w[index] = view.getUint32(offset + index * 4, false)
    }
    for (let index = 16; index < 64; index += 1) {
      const s0 = rightRotate(w[index - 15], 7) ^ rightRotate(w[index - 15], 18) ^ (w[index - 15] >>> 3)
      const s1 = rightRotate(w[index - 2], 17) ^ rightRotate(w[index - 2], 19) ^ (w[index - 2] >>> 10)
      w[index] = (w[index - 16] + s0 + w[index - 7] + s1) >>> 0
    }
    let a = h[0]
    let b = h[1]
    let c = h[2]
    let d = h[3]
    let e = h[4]
    let f = h[5]
    let g = h[6]
    let hh = h[7]
    for (let index = 0; index < 64; index += 1) {
      const s1 = rightRotate(e, 6) ^ rightRotate(e, 11) ^ rightRotate(e, 25)
      const ch = (e & f) ^ (~e & g)
      const temp1 = (hh + s1 + ch + k[index] + w[index]) >>> 0
      const s0 = rightRotate(a, 2) ^ rightRotate(a, 13) ^ rightRotate(a, 22)
      const maj = (a & b) ^ (a & c) ^ (b & c)
      const temp2 = (s0 + maj) >>> 0
      hh = g
      g = f
      f = e
      e = (d + temp1) >>> 0
      d = c
      c = b
      b = a
      a = (temp1 + temp2) >>> 0
    }
    h[0] = (h[0] + a) >>> 0
    h[1] = (h[1] + b) >>> 0
    h[2] = (h[2] + c) >>> 0
    h[3] = (h[3] + d) >>> 0
    h[4] = (h[4] + e) >>> 0
    h[5] = (h[5] + f) >>> 0
    h[6] = (h[6] + g) >>> 0
    h[7] = (h[7] + hh) >>> 0
  }

  const digest = new Uint8Array(32)
  const digestView = new DataView(digest.buffer)
  h.forEach((value, index) => {
    digestView.setUint32(index * 4, value, false)
  })
  return digest
}

function randomOAuthToken(byteLength = 32): string {
  const bytes = new Uint8Array(byteLength)
  if (globalThis.crypto?.getRandomValues) {
    globalThis.crypto.getRandomValues(bytes)
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256)
    }
  }
  return base64UrlEncode(bytes)
}

function randomOAuthHexToken(byteLength: number): string {
  const bytes = new Uint8Array(byteLength)
  if (globalThis.crypto?.getRandomValues) {
    globalThis.crypto.getRandomValues(bytes)
  } else {
    for (let index = 0; index < bytes.length; index += 1) {
      bytes[index] = Math.floor(Math.random() * 256)
    }
  }
  return Array.from(bytes)
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('')
}

function buildClaudeAuthorizationURL(state: string, codeChallenge: string, scope: string): string {
  const encodedRedirectURI = encodeURIComponent(CLAUDE_OAUTH_REDIRECT_URI)
  const encodedScope = encodeURIComponent(scope).replace(/%20/g, '+')
  return `${CLAUDE_OAUTH_AUTHORIZE_URL}?code=true&client_id=${CLAUDE_OAUTH_CLIENT_ID}&response_type=code&redirect_uri=${encodedRedirectURI}&scope=${encodedScope}&code_challenge=${codeChallenge}&code_challenge_method=S256&state=${state}`
}

async function buildCodeChallenge(verifier: string): Promise<string> {
  const verifierBytes = new TextEncoder().encode(verifier)
  if (globalThis.crypto?.subtle) {
    const digest = await globalThis.crypto.subtle.digest('SHA-256', verifierBytes)
    return base64UrlEncode(new Uint8Array(digest))
  }
  return base64UrlEncode(sha256Digest(verifierBytes))
}

function resetOAuthAssistantState() {
  activeOAuthTab.value = 'manual'
  computedAuthUrl.value = ''
  copiedUrl.value = false
  authCodeInput.value = ''
}

watch(editorOpen, (isOpen: boolean) => {
  if (isOpen) {
    resetOAuthAssistantState()
  }
})


function t(key: string, params?: Record<string, string | number>) {
  const messages: Record<string, string> = {
    'admin.accounts.createAccount': 'Create Account',
    'admin.accounts.accountName': 'Account Name',
    'admin.accounts.enterAccountName': 'Primary Claude / OpenAI / Gemini account',
    'admin.accounts.notes': 'Notes',
    'admin.accounts.notesPlaceholder': 'Local notes stored in JSON metadata',
    'admin.accounts.notesHint': 'Optional notes are stored in AccountConfig.metadata.notes.',
    'admin.accounts.platform': 'Platform',
    'admin.accounts.accountType': 'Account Type',
    'admin.accounts.oauth.authMethod': 'Authorization Method',
    'admin.accounts.oauth.title': 'Claude Account Authorization',
    'admin.accounts.oauth.openai.title': 'OpenAI Account Authorization',
    'admin.accounts.oauth.gemini.title': 'Gemini Account Authorization',
    'admin.accounts.oauth.antigravity.title': 'Antigravity Account Authorization',
    'admin.accounts.claudeCode': 'Claude Code',
    'admin.accounts.oauthSetupToken': 'OAuth / Setup Token',
    'admin.accounts.claudeConsole': 'Claude Console',
    'admin.accounts.apiKey': 'API Key',
    'admin.accounts.bedrockLabel': 'Bedrock',
    'admin.accounts.bedrockDesc': 'AWS Bedrock',
    'admin.accounts.vertexAnthropicHint': 'Use a Vertex AI service account for Claude models on Google Cloud.',
    'admin.accounts.vertexGeminiHint': 'Use a Vertex AI service account for Gemini models on Google Cloud.',
    'admin.accounts.types.chatgptOauth': 'ChatGPT / Codex OAuth',
    'admin.accounts.types.responsesApi': 'Responses API key',
    'admin.accounts.types.antigravityOauth': 'Antigravity OAuth',
    'admin.accounts.types.antigravityApikey': 'Upstream API Key relay',
    'admin.accounts.gemini.helpButton': 'Help',
    'admin.accounts.gemini.accountType.oauthTitle': 'OAuth',
    'admin.accounts.gemini.accountType.oauthDesc': 'Google One / GCP Code Assist / AI Studio',
    'admin.accounts.gemini.accountType.apiKeyTitle': 'API Key',
    'admin.accounts.gemini.accountType.apiKeyDesc': 'AI Studio API key',
    'admin.accounts.gemini.accountType.apiKeyNote': 'Use Google AI Studio API keys for Gemini API Key accounts.',
    'admin.accounts.gemini.accountType.apiKeyLink': 'Get API key',
    'admin.accounts.oauth.gemini.oauthTypeLabel': 'Gemini OAuth Type',
    'admin.accounts.oauth.gemini.aiStudioNotConfiguredShort': 'Not Configured',
    'admin.accounts.oauth.gemini.aiStudioNotConfiguredTip': 'AI Studio OAuth requires a self-managed OAuth client configuration.',
    'admin.accounts.gemini.oauthType.gcpProjectLink': 'Create GCP project',
    'admin.accounts.gemini.oauthType.customTitle': 'Custom AI Studio',
    'admin.accounts.gemini.oauthType.customDesc': 'Advanced self-managed OAuth Client.',
    'admin.accounts.gemini.oauthType.customRequirement': 'Requires organization-managed OAuth consent/client credentials.',
    'admin.accounts.gemini.oauthType.badges.orgManaged': 'Org managed',
    'admin.accounts.gemini.oauthType.badges.adminRequired': 'Admin required',
    'admin.accounts.gemini.tier.label': 'Tier',
    'admin.accounts.gemini.tier.googleOne.free': 'Google One Free',
    'admin.accounts.gemini.tier.googleOne.pro': 'Google AI Pro',
    'admin.accounts.gemini.tier.googleOne.ultra': 'Google AI Ultra',
    'admin.accounts.gemini.tier.gcp.standard': 'GCP Standard',
    'admin.accounts.gemini.tier.gcp.enterprise': 'GCP Enterprise',
    'admin.accounts.gemini.tier.aiStudio.free': 'AI Studio Free',
    'admin.accounts.gemini.tier.aiStudio.paid': 'AI Studio Paid',
    'admin.accounts.gemini.tier.hint': 'Stored as quota/tier fallback metadata.',
    'admin.accounts.gemini.tier.aiStudioHint': 'AI Studio API key quota tier fallback.',
    'admin.accounts.upstream.baseUrl': 'Base URL',
    'admin.accounts.upstream.baseUrlHint': 'Antigravity upstream relay base URL.',
    'admin.accounts.upstream.apiKey': 'API Key',
    'admin.accounts.upstream.apiKeyHint': 'Stored as the local JSON credential envelope.',
    'admin.accounts.baseUrl': 'Base URL',
    'admin.accounts.baseUrlHint': 'Optional upstream base URL override.',
    'admin.accounts.openai.baseUrlHint': 'OpenAI base URL, usually https://api.openai.com.',
    'admin.accounts.gemini.baseUrlHint': 'Gemini API base URL, usually https://generativelanguage.googleapis.com.',
    'admin.accounts.apiKeyRequired': 'API Key',
    'admin.accounts.apiKeyHint': 'Stored locally in the account credential field.',
    'admin.accounts.openai.apiKeyHint': 'Use an OpenAI project key or compatible bearer token.',
    'admin.accounts.gemini.apiKeyHint': 'Use an AI Studio API key.',
    'admin.accounts.addMethod': 'Add Method',
    'admin.accounts.types.oauth': 'OAuth',
    'admin.accounts.setupTokenLongLived': 'Setup Token (Long-lived)',
    'admin.accounts.bedrockAuthMode': 'Bedrock Auth Mode',
    'admin.accounts.bedrockAuthModeSigv4': 'AWS SigV4',
    'admin.accounts.bedrockAuthModeApikey': 'Bedrock API Key',
    'admin.accounts.bedrockAccessKeyId': 'Access Key ID',
    'admin.accounts.bedrockSecretAccessKey': 'Secret Access Key',
    'admin.accounts.bedrockSessionToken': 'Session Token',
    'admin.accounts.bedrockSessionTokenHint': 'Optional STS session token.',
    'admin.accounts.bedrockApiKeyInput': 'Bedrock API Key',
    'admin.accounts.bedrockRegion': 'Region',
    'admin.accounts.bedrockRegionHint': 'AWS region used for Bedrock requests.',
    'admin.accounts.bedrockForceGlobal': 'Force Global',
    'admin.accounts.bedrockForceGlobalHint': 'Preserved in metadata for Bedrock routing compatibility.',
    'admin.accounts.modelRestriction': 'Model Restriction',
    'admin.accounts.modelWhitelist': 'Model Whitelist',
    'admin.accounts.modelMapping': 'Model Mapping',
    'admin.accounts.mapRequestModels': 'Map request models to actual upstream models.',
    'admin.accounts.requestModel': 'Request Model',
    'admin.accounts.actualModel': 'Actual Model',
    'admin.accounts.addMapping': 'Add Mapping',
    'admin.accounts.selectedModels': 'Selected {count} model(s).',
    'admin.accounts.supportsAllModels': ' Supports all models when empty.',
    'admin.accounts.poolMode': 'Pool Mode',
    'admin.accounts.poolModeHint': 'Retry failed requests through account pool metadata.',
    'admin.accounts.poolModeInfo': 'Pool mode compatibility is persisted only; Simple Sub2API keeps its local runtime boundary.',
    'admin.accounts.poolModeRetryCount': 'Pool Mode Retry Count',
    'admin.accounts.customErrorCodes': 'Custom Error Codes',
    'admin.accounts.customErrorCodesHint': 'Override error codes treated as account failures.',
    'admin.accounts.customErrorCodesWarning': 'Use only when upstream returns non-standard failure codes.',
    'admin.accounts.enterErrorCode': 'Enter error code',
    'admin.accounts.noneSelectedUsesDefault': 'None selected; uses default handling.',
    'admin.accounts.tempUnschedulable.title': 'Temp Unschedulable',
    'admin.accounts.tempUnschedulable.hint': 'Temporarily pause scheduling after matching errors.',
    'admin.accounts.tempUnschedulable.notice': 'Rules are persisted as metadata; runtime wiring remains local/simple.',
    'admin.accounts.tempUnschedulable.errorCode': 'Error Code',
    'admin.accounts.tempUnschedulable.durationMinutes': 'Duration Minutes',
    'admin.accounts.tempUnschedulable.keywords': 'Keywords',
    'admin.accounts.tempUnschedulable.description': 'Description',
    'admin.accounts.tempUnschedulable.addRule': 'Add Rule',
    'admin.accounts.interceptWarmupRequests': 'Intercept Warmup Requests',
    'admin.accounts.interceptWarmupRequestsDesc': 'Anthropic/Antigravity warmup interception compatibility flag.',
    'admin.accounts.quotaControl.title': 'Quota Control',
    'admin.accounts.quotaControl.hint': 'Client affinity, cost-window, session and rate metadata.',
    'admin.accounts.quotaControl.windowCost.label': '5h Window Cost Limit',
    'admin.accounts.quotaControl.windowCost.hint': 'Limit 5-hour window cost for Claude OAuth/setup-token accounts.',
    'admin.accounts.quotaControl.sessionLimit.label': 'Session Count Limit',
    'admin.accounts.quotaControl.rpmLimit.label': 'RPM Limit',
    'admin.accounts.quotaControl.tlsFingerprint.label': 'TLS Fingerprint Simulation',
    'admin.accounts.quotaControl.sessionIdMasking.label': 'Session ID Masking',
    'admin.accounts.quotaControl.cacheTTLOverride.label': 'Cache TTL Override',
    'admin.accounts.quotaControl.customBaseUrl.label': 'Custom Relay URL',
    'admin.accounts.proxy': 'Proxy',
    'admin.accounts.concurrency': 'Concurrency',
    'admin.accounts.loadFactor': 'Load Factor',
    'admin.accounts.priority': 'Priority',
    'admin.accounts.expiresAt': 'Expires At',
    'admin.accounts.autoPauseOnExpired': 'Auto Pause On Expired',
    'admin.accounts.openai.oauthPassthrough': 'OpenAI Passthrough',
    'admin.accounts.openai.wsMode': 'OpenAI WS Mode',
    'admin.accounts.openai.codexCLIOnly': 'Codex CLI Only',
    'admin.accounts.openai.compactMode': 'OpenAI Compact Mode',
    'admin.accounts.openai.compactModelMapping': 'Compact Model Mapping',
    'admin.accounts.anthropic.apiKeyPassthrough': 'Anthropic API Key Passthrough',
    'admin.accounts.anthropic.webSearchEmulation': 'Web Search Emulation',
    'admin.accounts.mixedScheduling': 'Mixed Scheduling',
    'admin.accounts.allowOverages': 'Allow Overages',
    'admin.accounts.gemini.helpDialog.title': 'Gemini Help',
    'common.cancel': 'Cancel',
    'common.create': 'Create',
    'common.next': 'Next',
    'common.back': 'Back',
    'common.close': 'Close'
  }
  let message = messages[key] || key
  Object.entries(params || {}).forEach(([param, value]) => {
    message = message.replace(`{${param}}`, String(value))
  })
  return message
}

type PlatformOption = 'anthropic' | 'openai' | 'gemini' | 'antigravity'
type AccountCategory = 'oauth-based' | 'apikey' | 'bedrock' | 'service_account' | 'upstream'
type ModelMode = 'whitelist' | 'mapping'
type OpenAIWSMode = 'off' | 'ctx_pool' | 'passthrough'
type OpenAICompactMode = 'off' | 'auto' | 'force'

interface ModelMapping {
  from: string
  to: string
}

interface TempUnschedRule {
  error_code: number
  duration_minutes: number
  keywords: string
  description: string
}

const platformCards: Array<{ value: PlatformOption; title: string; subtitle: string; count: string; active: string; inactive: string }> = [
  { value: 'anthropic', title: 'Anthropic', subtitle: 'Claude Code / Console / Bedrock / Vertex', count: '4 types', active: 'bg-white text-orange-600 shadow-sm dark:bg-dark-600 dark:text-orange-400', inactive: 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200' },
  { value: 'openai', title: 'OpenAI', subtitle: 'OAuth / API Key / Responses', count: '2 types', active: 'bg-white text-green-600 shadow-sm dark:bg-dark-600 dark:text-green-400', inactive: 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200' },
  { value: 'gemini', title: 'Gemini', subtitle: 'Google One / Code Assist / AI Studio / Vertex', count: '3 types', active: 'bg-white text-blue-600 shadow-sm dark:bg-dark-600 dark:text-blue-400', inactive: 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200' },
  { value: 'antigravity', title: 'Antigravity', subtitle: 'OAuth / upstream relay', count: '2 types', active: 'bg-white text-purple-600 shadow-sm dark:bg-dark-600 dark:text-purple-400', inactive: 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200' }
]

const categoryCards: Record<PlatformOption, Array<{ value: AccountCategory; title: string; subtitle: string }>> = {
  openai: [
    { value: 'oauth-based', title: 'OAuth', subtitle: 'ChatGPT / Codex refresh-token style credential' },
    { value: 'apikey', title: 'API Key', subtitle: 'Project key or compatible bearer key' }
  ],
  anthropic: [
    { value: 'oauth-based', title: 'Claude Code', subtitle: 'OAuth / Setup Token' },
    { value: 'apikey', title: 'Claude Console', subtitle: 'API Key' },
    { value: 'bedrock', title: 'Bedrock', subtitle: 'AWS Bedrock' },
    { value: 'service_account', title: 'Vertex', subtitle: 'Service Account' }
  ],
  gemini: [
    { value: 'oauth-based', title: 'OAuth', subtitle: 'Google One / Code Assist / AI Studio' },
    { value: 'apikey', title: 'API Key', subtitle: 'AI Studio key' },
    { value: 'service_account', title: 'Vertex', subtitle: 'Service-account JSON' }
  ],
  antigravity: [
    { value: 'oauth-based', title: 'OAuth', subtitle: 'Refresh-token account' },
    { value: 'upstream', title: 'API Key', subtitle: 'Upstream OpenAI-compatible relay' }
  ]
}

const commonErrorCodes = [400, 401, 403, 404, 408, 409, 429, 500, 502, 503, 504]
const platformModelPresets: Record<PlatformOption, string[]> = {
  openai: [
    'gpt-5.2',
    'gpt-5.2-2025-12-11',
    'gpt-5.2-chat-latest',
    'gpt-5.2-pro',
    'gpt-5.2-pro-2025-12-11',
    'gpt-5.5',
    'gpt-5.4',
    'gpt-5.4-mini',
    'gpt-5.4-2026-03-05',
    'gpt-5.3-codex',
    'gpt-5.3-codex-spark',
    'codex-auto-review',
    'gpt-4o-audio-preview',
    'gpt-4o-realtime-preview',
    'gpt-image-1',
    'gpt-image-1.5',
    'gpt-image-2'
  ],
  anthropic: [
    'claude-3-5-sonnet-20241022',
    'claude-3-5-sonnet-20240620',
    'claude-3-5-haiku-20241022',
    'claude-3-7-sonnet-20250219',
    'claude-sonnet-4-20250514',
    'claude-opus-4-20250514',
    'claude-opus-4-1-20250805',
    'claude-sonnet-4-5-20250929',
    'claude-haiku-4-5-20251001',
    'claude-opus-4-5-20251101',
    'claude-opus-4-6',
    'claude-opus-4-7',
    'claude-sonnet-4-6'
  ],
  gemini: [
    'gemini-3.1-flash-image',
    'gemini-2.5-flash-image',
    'gemini-2.0-flash',
    'gemini-2.5-flash',
    'gemini-2.5-pro',
    'gemini-3-flash-preview',
    'gemini-3-pro-preview'
  ],
  antigravity: [
    'claude-opus-4-6',
    'claude-opus-4-6-thinking',
    'claude-opus-4-7',
    'claude-opus-4-5-thinking',
    'claude-sonnet-4-6',
    'claude-sonnet-4-5',
    'claude-sonnet-4-5-thinking',
    'gemini-3.1-flash-image',
    'gemini-2.5-flash-image',
    'gemini-2.5-flash',
    'gemini-2.5-flash-lite',
    'gemini-2.5-flash-thinking',
    'gemini-2.5-pro'
  ]
}

const form = reactive({
  id: '',
  label: '',
  type: 'openai_api_key',
  platform: 'anthropic' as PlatformOption,
  category: 'oauth-based' as AccountCategory,
  add_method: 'oauth' as 'oauth' | 'setup-token',
  notes: '',
  tier: 'simple',
  tags: '',
  source_id: '',
  credential: '',
  base_url: '',
  model: '',
  proxy_ref: '',
  quota_policy: '',
  enabled: true,
  api_key: '',
  refresh_token: '',
  setup_token: '',
  session_token: '',
  access_token: '',
  codex_session: '',
  service_account_json: '',
  aws_access_key_id: '',
  aws_secret_access_key: '',
  aws_session_token: '',
  aws_region: 'us-east-1',
  bedrock_auth_mode: 'sigv4',
  bedrock_force_global: false,
  vertex_project_id: '',
  vertex_location: 'us-central1',
  gemini_oauth_type: 'google_one',
  gemini_tier: 'google_one_free',
  model_mode: 'whitelist' as ModelMode,
  allowed_models: '',
  pool_mode: false,
  pool_mode_retry_count: 1,
  custom_error_codes_enabled: false,
  custom_error_codes: '',
  temp_unsched_enabled: false,
  temp_unsched_rules: [] as TempUnschedRule[],
  intercept_warmup_requests: false,
  openai_passthrough: false,
  openai_ws_mode: 'off' as OpenAIWSMode,
  openai_compact_mode: 'auto' as OpenAICompactMode,
  openai_compact_mappings: [] as ModelMapping[],
  anthropic_passthrough: false,
  web_search_mode: 'default',
  codex_cli_only: false,
  window_cost_enabled: false,
  window_cost_limit: 0,
  window_cost_sticky_reserve: 0,
  session_limit_enabled: false,
  max_sessions: 1,
  session_idle_timeout_minutes: 30,
  rpm_limit_enabled: false,
  base_rpm: 60,
  rpm_strategy: 'tiered',
  rpm_sticky_buffer: 10,
  user_msg_queue_mode: 'default',
  tls_fingerprint_enabled: false,
  tls_fingerprint_profile_id: '',
  session_id_masking_enabled: false,
  cache_ttl_override_enabled: false,
  cache_ttl_override_target: '5m',
  custom_base_url_enabled: false,
  custom_base_url: '',
  concurrency: 1,
  load_factor: 1,
  priority: 1,
  expires_at: '',
  auto_pause_on_expired: true,
  mixed_scheduling: false,
  allow_overages: false,
  group_default: false,
  group_ids: '',
  model_mappings: [] as ModelMapping[]
})

const accountColumns: Column<AccountRow>[] = [
  { key: 'label', label: 'Account' },
  { key: 'status', label: 'Health' },
  { key: 'type', label: 'Type' },
  { key: 'tier', label: 'Tier / Tags' },
  { key: 'source', label: 'Source / Proxy' },
  { key: 'baseURL', label: 'Model / Base URL' },
  { key: 'actions', label: 'Actions' }
]

const rows = computed<AccountRow[]>(() =>
  (accountsState.value?.accounts || []).map((summary) => {
    const account = summary.config
    const health = summary.health
    const quotaStatus = String(summary.quota?.status || 'unknown')
    return {
      id: account.id,
      label: account.label || account.id,
      type: account.type || 'openai_api_key',
      tier: account.tier || 'simple',
      tags: account.tags || [],
      source: account.source_id || summary.source || 'manual',
      proxy: account.proxy_ref || 'direct',
      model: account.model || 'default',
      baseURL: account.base_url || 'same gateway default',
      enabled: account.enabled !== false,
      status: summary.runtime_status || (account.enabled === false ? 'disabled' : 'unknown'),
      healthStatus: health?.status || 'unknown',
      lastChecked: health?.checked_at || '',
      lastError: health?.message || '',
      quotaStatus,
      hits: Number(summary.metrics?.hits || 0),
      errors: Number(summary.metrics?.errors || 0),
      summary
    }
  })
)

const filteredRows = computed(() => {
  const q = search.value.trim().toLowerCase()
  return rows.value.filter((row) => {
    const matchesQuery =
      q === '' ||
      [row.id, row.label, row.type, row.tier, row.source, row.proxy, row.model, row.baseURL, row.lastError, ...row.tags]
        .join(' ')
        .toLowerCase()
        .includes(q)
    const matchesStatus = statusFilter.value === 'all' || row.status === statusFilter.value || row.healthStatus === statusFilter.value
    const matchesType = typeFilter.value === 'all' || row.type === typeFilter.value
    return matchesQuery && matchesStatus && matchesType
  })
})

const statusOptions = computed(() => ['all', ...Array.from(new Set(rows.value.flatMap((row) => [row.status, row.healthStatus]).filter(Boolean))).sort()])
const typeOptions = computed(() => ['all', ...Array.from(new Set(rows.value.map((row) => row.type))).sort()])

const healthCounts = computed(() => {
  const total = rows.value.length
  const enabled = rows.value.filter((row) => row.enabled).length
  const healthy = rows.value.filter((row) => row.status === 'healthy').length
  const disabled = rows.value.filter((row) => row.status === 'disabled').length
  return { total, enabled, healthy, disabled }
})

const currentCategoryCards = computed(() => categoryCards[form.platform] || categoryCards.anthropic)
const currentModelPresets = computed(() => getModelsByPlatform(form.platform))
const currentModelOptions = computed<{ value: string; label: string }[]>(() => {
  const platformModels = new Set(currentModelPresets.value)
  const options = allModels.filter((model) => platformModels.has(model.value))
  return options.length > 0 ? options : currentModelPresets.value.map((model) => ({ value: model, label: model }))
})
const filteredModelOptions = computed(() => {
  const query = modelSearchQuery.value.trim().toLowerCase()
  if (!query) return currentModelOptions.value
  return currentModelOptions.value.filter((model) => model.value.toLowerCase().includes(query) || model.label.toLowerCase().includes(query))
})
const allowedModelList = computed(() => splitList(form.allowed_models))
const proxyOptions = computed(() => accountsState.value?.proxies || [])
const availableGroups = computed(() => (groupsState.value?.groups || []).map((summary) => summary.config))
const selectedGroupIDs = computed(() => splitList(form.group_ids))
const filteredGroupsForPlatform = computed(() => {
  const groups = availableGroups.value
  return groups.filter((group) => group.platform === form.platform)
})
const isOAuthFlow = computed(() => form.category === 'oauth-based')
const showAPICredential = computed(() => form.category === 'apikey' || form.category === 'upstream')
const showOAuthCredential = computed(() => isOAuthFlow.value)
const showServiceAccount = computed(() => form.category === 'service_account')
const showBedrock = computed(() => form.category === 'bedrock')
const showOpenAIAdvanced = computed(() => form.platform === 'openai')
const showAnthropicAdvanced = computed(() => form.platform === 'anthropic')
const showGeminiAdvanced = computed(() => form.platform === 'gemini')
const showAntigravityAdvanced = computed(() => form.platform === 'antigravity')
const isClaudeOAuthFlow = computed(() => form.platform === 'anthropic' && form.category === 'oauth-based')
const showClaudeQuotaControls = computed(() => form.platform === 'anthropic' && form.category === 'oauth-based')
const authorizationStepTitle = computed(() => {
  if (form.platform === 'anthropic') return 'Claude Account Authorization'
  if (form.platform === 'openai') return 'OpenAI Account Authorization'
  if (form.platform === 'gemini') return 'Gemini Account Authorization'
  return 'Antigravity Account Authorization'
})
const canContinueAccountSetup = computed(() => true)
const canSubmit = computed(() => canContinueAccountSetup.value && buildCredential().trim() !== '')

function toggleNumberCode(code: number) {
  const codes = numberList(form.custom_error_codes)
  if (codes.includes(code)) {
    form.custom_error_codes = codes.filter((item) => item !== code).join(', ')
    return
  }
  form.custom_error_codes = [...codes, code].join(', ')
}

function toggleSwitchClass(enabled: boolean) {
  return [
    'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
    enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
  ]
}

function toggleKnobClass(enabled: boolean) {
  return [
    'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
    enabled ? 'translate-x-5' : 'translate-x-0'
  ]
}

function addTempUnschedPreset(kind: 'overload' | 'rate_limit' | 'unavailable') {
  const presets: Record<typeof kind, TempUnschedRule> = {
    overload: { error_code: 529, duration_minutes: 60, keywords: 'overloaded, too many', description: 'Temporary overloaded response' },
    rate_limit: { error_code: 429, duration_minutes: 10, keywords: 'rate limit, too many requests', description: 'Rate limit response' },
    unavailable: { error_code: 503, duration_minutes: 30, keywords: 'unavailable, maintenance', description: 'Temporary service unavailable' }
  }
  form.temp_unsched_rules.push({ ...presets[kind] })
}

function generatedAccountID() {
  const prefix = form.platform === 'anthropic' ? 'claude' : form.platform
  const source = `${prefix}_${form.category}_${form.label || form.model || Date.now()}`
  const slug = source
    .toLowerCase()
    .replace(/[^a-z0-9_.-]+/g, '_')
    .replace(/^[_\-.]+|[_\-.]+$/g, '')
    .slice(0, 44)
  return `acct_${slug || prefix}`
}

function ensureAccountID() {
  if (form.id.trim()) return
  form.id = generatedAccountID()
}

async function loadAll() {
  loading.value = true
  error.value = ''
  try {
    await refresh()
    const [accounts, groups] = await Promise.all([loadAccounts(), loadGroups()])
    accountsState.value = accounts
    groupsState.value = groups
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

function proxyLabel(proxy: ProxyConfig) {
  const proxyAny = proxy as ProxyConfig & { name?: string; protocol?: string; host?: string; port?: number }
  const name = proxyAny.name || proxy.id
  if (proxyAny.protocol && proxyAny.host) {
    return `${name} (${proxyAny.protocol}://${proxyAny.host}${proxyAny.port ? `:${proxyAny.port}` : ''})`
  }
  try {
    const parsed = new URL(proxy.url)
    const protocol = parsed.protocol.replace(/:$/, '')
    return `${name} (${protocol}://${parsed.host})`
  } catch {
    return `${name} (${proxy.url})`
  }
}

function groupLabel(group: GroupConfig) {
  return group.name || group.id
}

function resetForm() {
  editorStep.value = 1
  showAdvancedAccountOptions.value = false
  showAdvancedOAuth.value = false
  showGeminiHelpDialog.value = false
  form.id = ''
  form.label = ''
  form.type = 'openai_api_key'
  form.platform = 'anthropic'
  form.category = 'oauth-based'
  form.add_method = 'oauth'
  form.notes = ''
  form.tier = 'simple'
  form.tags = ''
  form.source_id = ''
  form.credential = ''
  form.base_url = ''
  form.model = ''
  form.proxy_ref = ''
  form.quota_policy = ''
  form.enabled = true
  form.api_key = ''
  form.refresh_token = ''
  form.setup_token = ''
  form.session_token = ''
  form.access_token = ''
  form.codex_session = ''
  form.service_account_json = ''
  form.aws_access_key_id = ''
  form.aws_secret_access_key = ''
  form.aws_session_token = ''
  form.aws_region = 'us-east-1'
  form.bedrock_auth_mode = 'sigv4'
  form.bedrock_force_global = false
  form.vertex_project_id = ''
  form.vertex_location = 'us-central1'
  form.gemini_oauth_type = 'google_one'
  form.gemini_tier = 'google_one_free'
  form.model_mode = 'whitelist'
  form.allowed_models = getModelsByPlatform(form.platform).join(', ')
  form.pool_mode = false
  form.pool_mode_retry_count = 1
  form.custom_error_codes_enabled = false
  form.custom_error_codes = ''
  form.temp_unsched_enabled = false
  form.temp_unsched_rules = []
  form.intercept_warmup_requests = false
  form.openai_passthrough = false
  form.openai_ws_mode = 'off'
  form.openai_compact_mode = 'auto'
  form.openai_compact_mappings = []
  form.anthropic_passthrough = false
  form.web_search_mode = 'default'
  form.codex_cli_only = false
  form.window_cost_enabled = false
  form.window_cost_limit = 0
  form.window_cost_sticky_reserve = 0
  form.session_limit_enabled = false
  form.max_sessions = 1
  form.session_idle_timeout_minutes = 30
  form.rpm_limit_enabled = false
  form.base_rpm = 60
  form.rpm_strategy = 'tiered'
  form.rpm_sticky_buffer = 10
  form.user_msg_queue_mode = 'default'
  form.tls_fingerprint_enabled = false
  form.tls_fingerprint_profile_id = ''
  form.session_id_masking_enabled = false
  form.cache_ttl_override_enabled = false
  form.cache_ttl_override_target = '5m'
  form.custom_base_url_enabled = false
  form.custom_base_url = ''
  form.concurrency = 1
  form.load_factor = 1
  form.priority = 1
  form.expires_at = ''
  form.auto_pause_on_expired = true
  form.mixed_scheduling = false
  form.allow_overages = false
  form.group_default = false
  form.group_ids = ''
  form.model_mappings = []
  modelSearchQuery.value = ''
  customModelInput.value = ''
}

function normalizeCategoryForPlatform(platform: PlatformOption, category: AccountCategory): AccountCategory {
  const allowed = (categoryCards[platform] || categoryCards.anthropic).map((item) => item.value)
  return allowed.includes(category) ? category : allowed[0]
}

function selectPlatform(platform: PlatformOption) {
  if (form.platform !== platform) resetOAuthAssistantState()
  form.platform = platform
  form.category = normalizeCategoryForPlatform(platform, form.category)
  if (form.model_mode === 'whitelist') syncPlatformModels()
  inferSimpleType()
}

function selectCategory(category: AccountCategory) {
  if (form.category !== category) resetOAuthAssistantState()
  form.category = category
  inferSimpleType()
}

function syncPlatformModels() {
  form.allowed_models = currentModelPresets.value.join(', ')
}

function clearAllowedModels() {
  form.allowed_models = ''
}

function toggleAllowedModel(model: string) {
  const current = allowedModelList.value
  if (current.includes(model)) {
    form.allowed_models = current.filter((item) => item !== model).join(', ')
    return
  }
  form.allowed_models = [...current, model].join(', ')
}

function addCustomModel() {
  const model = customModelInput.value.trim()
  if (!model) return
  const current = allowedModelList.value
  if (!current.includes(model)) {
    form.allowed_models = [...current, model].join(', ')
  }
  customModelInput.value = ''
}

function handleCustomModelEnter() {
  if (!isCustomModelComposing.value) addCustomModel()
}

function toggleGroupID(groupID: string) {
  const current = selectedGroupIDs.value
  if (current.includes(groupID)) {
    form.group_ids = current.filter((item) => item !== groupID).join(', ')
    return
  }
  form.group_ids = [...current, groupID].join(', ')
}

function inferSimpleType() {
  if (form.category === 'oauth-based') {
    form.type = 'oauth'
    return
  }
  if (form.category === 'upstream') {
    form.type = 'openai_compatible'
    return
  }
  form.type = 'openai_api_key'
}

function parseCredentialEnvelope(credential: string): Record<string, string> {
  const out: Record<string, string> = {}
  credential
    .split(/[;\n\r]+/)
    .map((part) => part.trim())
    .filter(Boolean)
    .forEach((part) => {
      const index = part.indexOf('=')
      if (index > 0) {
        out[part.slice(0, index).trim()] = part.slice(index + 1).trim()
      } else if (!out.api_key) {
        out.api_key = part
      }
    })
  return out
}

function applyCredentialEnvelope(credential: string) {
  const envelope = parseCredentialEnvelope(credential)
  form.api_key = envelope.api_key || ''
  form.refresh_token = envelope.refresh_token || ''
  form.setup_token = envelope.setup_token || ''
  form.session_token = envelope.session_token || ''
  form.access_token = envelope.access_token || ''
  form.codex_session = envelope.codex_session || ''
  form.service_account_json = envelope.service_account_json || ''
  form.aws_access_key_id = envelope.aws_access_key_id || ''
  form.aws_secret_access_key = envelope.aws_secret_access_key || ''
  form.aws_session_token = envelope.aws_session_token || ''
}

function metadataFromAccount(account: AccountConfig): Record<string, unknown> {
  return account.metadata && typeof account.metadata === 'object' ? account.metadata : {}
}

function applyMetadata(metadata: Record<string, unknown>) {
  form.platform = String(metadata.platform || form.platform || 'openai') as PlatformOption
  form.category = normalizeCategoryForPlatform(form.platform, String(metadata.account_category || form.category || 'apikey') as AccountCategory)
  form.add_method = String(metadata.add_method || 'oauth') as 'oauth' | 'setup-token'
  form.notes = String(metadata.notes || '')
  form.bedrock_auth_mode = String(metadata.bedrock_auth_mode || 'sigv4')
  form.bedrock_force_global = Boolean(metadata.bedrock_force_global)
  form.aws_region = String(metadata.aws_region || 'us-east-1')
  form.vertex_project_id = String(metadata.vertex_project_id || '')
  form.vertex_location = String(metadata.vertex_location || 'us-central1')
  form.gemini_oauth_type = String(metadata.gemini_oauth_type || 'google_one')
  form.gemini_tier = String(metadata.gemini_tier || 'google_one_free')
  form.model_mode = String(metadata.model_mode || 'whitelist') as ModelMode
  form.allowed_models = Array.isArray(metadata.allowed_models) ? metadata.allowed_models.join(', ') : ''
  form.pool_mode = Boolean(metadata.pool_mode)
  form.pool_mode_retry_count = Number(metadata.pool_mode_retry_count || 1)
  form.custom_error_codes_enabled = Boolean(metadata.custom_error_codes_enabled)
  form.custom_error_codes = Array.isArray(metadata.custom_error_codes) ? metadata.custom_error_codes.join(', ') : ''
  form.temp_unsched_enabled = Boolean(metadata.temp_unsched_enabled)
  form.temp_unsched_rules = Array.isArray(metadata.temp_unsched_rules) ? (metadata.temp_unsched_rules as TempUnschedRule[]) : []
  form.intercept_warmup_requests = Boolean(metadata.intercept_warmup_requests)
  form.openai_passthrough = Boolean(metadata.openai_passthrough)
  form.openai_ws_mode = String(metadata.openai_ws_mode || 'off') as OpenAIWSMode
  form.openai_compact_mode = String(metadata.openai_compact_mode || 'auto') as OpenAICompactMode
  form.openai_compact_mappings = Array.isArray(metadata.openai_compact_mappings) ? (metadata.openai_compact_mappings as ModelMapping[]) : []
  form.anthropic_passthrough = Boolean(metadata.anthropic_passthrough)
  form.web_search_mode = String(metadata.web_search_mode || 'default')
  form.codex_cli_only = Boolean(metadata.codex_cli_only)
  form.window_cost_enabled = Boolean(metadata.window_cost_enabled)
  form.window_cost_limit = Number(metadata.window_cost_limit || 0)
  form.window_cost_sticky_reserve = Number(metadata.window_cost_sticky_reserve || 0)
  form.session_limit_enabled = Boolean(metadata.session_limit_enabled)
  form.max_sessions = Number(metadata.max_sessions || 1)
  form.session_idle_timeout_minutes = Number(metadata.session_idle_timeout_minutes || 30)
  form.rpm_limit_enabled = Boolean(metadata.rpm_limit_enabled)
  form.base_rpm = Number(metadata.base_rpm || 60)
  form.rpm_strategy = String(metadata.rpm_strategy || 'tiered')
  form.rpm_sticky_buffer = Number(metadata.rpm_sticky_buffer || 10)
  form.user_msg_queue_mode = String(metadata.user_msg_queue_mode || 'default')
  form.tls_fingerprint_enabled = Boolean(metadata.tls_fingerprint_enabled)
  form.tls_fingerprint_profile_id = String(metadata.tls_fingerprint_profile_id || '')
  form.session_id_masking_enabled = Boolean(metadata.session_id_masking_enabled)
  form.cache_ttl_override_enabled = Boolean(metadata.cache_ttl_override_enabled)
  form.cache_ttl_override_target = String(metadata.cache_ttl_override_target || '5m')
  form.custom_base_url_enabled = Boolean(metadata.custom_base_url_enabled)
  form.custom_base_url = String(metadata.custom_base_url || '')
  form.concurrency = Number(metadata.concurrency || 1)
  form.load_factor = Number(metadata.load_factor || 1)
  form.priority = Number(metadata.priority || 1)
  form.expires_at = String(metadata.expires_at || '')
  form.auto_pause_on_expired = metadata.auto_pause_on_expired !== false
  form.mixed_scheduling = Boolean(metadata.mixed_scheduling)
  form.allow_overages = Boolean(metadata.allow_overages)
  form.group_ids = Array.isArray(metadata.group_ids)
    ? metadata.group_ids.join(', ')
    : Array.isArray(metadata.groups)
      ? metadata.groups.join(', ')
      : ''
  form.group_default = selectedGroupIDs.value.includes('default1x0')
  form.model_mappings = Array.isArray(metadata.model_mappings) ? (metadata.model_mappings as ModelMapping[]) : []
}

function openCreate() {
  editorMode.value = 'create'
  resetForm()
  editorOpen.value = true
}

function openEdit(row: AccountRow) {
  const account = row.summary.config
  editorMode.value = 'edit'
  showAdvancedAccountOptions.value = false
  showAdvancedOAuth.value = false
  showGeminiHelpDialog.value = false
  form.id = account.id
  form.label = account.label || account.id
  form.type = account.type || 'openai_api_key'
  form.tier = account.tier || 'simple'
  form.tags = (account.tags || []).join(', ')
  form.source_id = account.source_id || ''
  form.credential = account.credential || ''
  form.base_url = account.base_url || ''
  form.model = account.model || ''
  form.proxy_ref = account.proxy_ref || ''
  form.quota_policy = account.quota_policy || ''
  form.enabled = account.enabled !== false
  applyCredentialEnvelope(account.credential || '')
  applyMetadata(metadataFromAccount(account))
  editorStep.value = 1
  editorOpen.value = true
}

function validateStep1(): boolean {
  error.value = ''

  if (!form.label.trim()) {
    const message = 'Please enter account name'
    error.value = message
    accountNameInput.value?.setCustomValidity(message)
    accountNameInput.value?.reportValidity()
    accountNameInput.value?.focus()
    return false
  }
  accountNameInput.value?.setCustomValidity('')
  
  if (form.category === 'bedrock') {
    if (!form.aws_region.trim()) {
      error.value = 'AWS Bedrock region is required'
      return false
    }
    if (form.bedrock_auth_mode === 'sigv4') {
      if (!form.aws_access_key_id.trim()) {
        error.value = 'AWS Access Key ID is required'
        return false
      }
      if (!form.aws_secret_access_key.trim()) {
        error.value = 'AWS Secret Access Key is required'
        return false
      }
    } else {
      if (!form.api_key.trim()) {
        error.value = 'AWS Bedrock API Key is required'
        return false
      }
    }
  }
  
  if (form.category === 'service_account') {
    const raw = form.service_account_json.trim()
    if (!raw) {
      error.value = 'Service Account JSON is required'
      return false
    }
    try {
      const parsed = JSON.parse(raw)
      const projectId = parsed.project_id
      const clientEmail = parsed.client_email
      const privateKey = parsed.private_key
      if (!projectId || !clientEmail || !privateKey) {
        error.value = 'Service Account JSON missing project_id, client_email, or private_key fields'
        return false
      }
      form.vertex_project_id = projectId
    } catch {
      error.value = 'Service Account JSON is invalid'
      return false
    }
    if (!form.vertex_location.trim()) {
      error.value = 'Vertex Location is required'
      return false
    }
  }
  
  if (form.category === 'apikey') {
    if (!form.api_key.trim()) {
      error.value = 'API Key is required'
      return false
    }
  }
  
  if (form.platform === 'antigravity' && form.category === 'upstream') {
    if (!form.base_url.trim()) {
      error.value = 'Antigravity Base URL is required'
      return false
    }
    if (!form.api_key.trim()) {
      error.value = 'Antigravity API Key is required'
      return false
    }
  }

  return true
}

function extractOAuthCallbackParam(raw: string, param: 'code' | 'state'): string {
  const trimmed = raw.trim()
  if (!trimmed) return ''
  try {
    const parsed = new URL(trimmed)
    return parsed.searchParams.get(param) || ''
  } catch {
    const match = trimmed.match(new RegExp(`[?&]${param}=([^&#]+)`))
    if (!match?.[1]) return ''
    try {
      return decodeURIComponent(match[1].replace(/\+/g, ' '))
    } catch {
      return match[1]
    }
  }
}

watch(authCodeInput, (newVal: string) => {
  if (form.platform !== 'openai' && form.platform !== 'gemini' && form.platform !== 'antigravity') return
  const trimmed = newVal.trim()
  const code = extractOAuthCallbackParam(trimmed, 'code')
  if (code) {
    authCodeInput.value = code
    form.refresh_token = code
  } else {
    form.refresh_token = trimmed
  }
})

async function handleGenerateAuthLink() {
  try {
    const params = new URLSearchParams()

    if (form.platform === 'openai') {
      const state = randomOAuthHexToken(32)
      const codeVerifier = randomOAuthHexToken(64)
      const codeChallenge = await buildCodeChallenge(codeVerifier)
      params.set('client_id', OPENAI_CODEX_CLIENT_ID)
      params.set('code_challenge', codeChallenge)
      params.set('code_challenge_method', 'S256')
      params.set('codex_cli_simplified_flow', 'true')
      params.set('id_token_add_organizations', 'true')
      params.set('redirect_uri', OAUTH_CALLBACK_URL)
      params.set('response_type', 'code')
      params.set('scope', 'openid profile email offline_access')
      params.set('state', state)
      computedAuthUrl.value = `https://auth.openai.com/oauth/authorize?${params.toString()}`
    } else if (form.platform === 'anthropic') {
      const state = randomOAuthToken(32)
      const codeVerifier = randomOAuthToken(32)
      const codeChallenge = await buildCodeChallenge(codeVerifier)
      const scope = form.add_method === 'setup-token' ? CLAUDE_SETUP_TOKEN_SCOPE : CLAUDE_OAUTH_SCOPE
      computedAuthUrl.value = buildClaudeAuthorizationURL(state, codeChallenge, scope)
    } else if (form.platform === 'gemini') {
      const state = randomOAuthToken(32)
      const codeVerifier = randomOAuthToken(32)
      const codeChallenge = await buildCodeChallenge(codeVerifier)
      const isAIStudio = form.gemini_oauth_type === 'ai_studio'
      const redirectUri = isAIStudio ? OAUTH_CALLBACK_URL : GEMINI_CODE_ASSIST_CALLBACK_URL
      const scope = isAIStudio
        ? 'https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/generative-language.retriever'
        : 'https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile'
      params.set('access_type', 'offline')
      params.set('client_id', GEMINI_CLI_CLIENT_ID)
      params.set('code_challenge', codeChallenge)
      params.set('code_challenge_method', 'S256')
      params.set('include_granted_scopes', 'true')
      params.set('prompt', 'consent')
      params.set('redirect_uri', redirectUri)
      params.set('response_type', 'code')
      params.set('scope', scope)
      params.set('state', state)
      if (form.gemini_oauth_type === 'code_assist' && form.vertex_project_id.trim()) {
        params.set('project_id', form.vertex_project_id.trim())
      }
      computedAuthUrl.value = `https://accounts.google.com/o/oauth2/v2/auth?${params.toString()}`
    } else if (form.platform === 'antigravity') {
      const state = randomOAuthToken(32)
      const codeVerifier = randomOAuthToken(32)
      const codeChallenge = await buildCodeChallenge(codeVerifier)
      params.set('access_type', 'offline')
      params.set('client_id', ANTIGRAVITY_CLIENT_ID)
      params.set('code_challenge', codeChallenge)
      params.set('code_challenge_method', 'S256')
      params.set('include_granted_scopes', 'true')
      params.set('prompt', 'consent')
      params.set('redirect_uri', ANTIGRAVITY_CALLBACK_URL)
      params.set('response_type', 'code')
      params.set('scope', 'https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs')
      params.set('state', state)
      computedAuthUrl.value = `https://accounts.google.com/o/oauth2/v2/auth?${params.toString()}`
    }
    error.value = ''
  } catch (err) {
    error.value = `Failed to generate authorization URL: ${err instanceof Error ? err.message : String(err)}`
  }
}

async function handleCopyAuthUrl() {
  if (computedAuthUrl.value) {
    const copied = await copyTextToClipboard(computedAuthUrl.value)
    if (copied) {
      copiedUrl.value = true
      notice.value = 'URL copied to clipboard'
      setTimeout(() => { copiedUrl.value = false }, 2000)
      return
    }
    error.value = 'Copy failed. Select the authorization URL and copy it manually.'
  }
}

function handlePrimaryAccountAction() {
  if (!validateStep1()) {
    return
  }
  if (isOAuthFlow.value && editorStep.value === 1) {
    ensureAccountID()
    applyDurableFieldAliases()
    editorStep.value = 2
    return
  }
  saveAccount().catch((err) => {
    error.value = err instanceof Error ? err.message : String(err)
  })
}

function goBackToAccountSetup() {
  editorStep.value = 1
  resetOAuthAssistantState()
}


function buildCredential(): string {
  const parts: string[] = []
  const add = (key: string, value: string) => {
    const trimmed = value.trim()
    if (trimmed) parts.push(`${key}=${trimmed}`)
  }
  if (showAPICredential.value) add('api_key', form.api_key || form.credential)
  if (showOAuthCredential.value) {
    if (form.add_method === 'setup-token') {
      add('setup_token', form.setup_token)
    } else {
      add('refresh_token', form.refresh_token)
      add('setup_token', form.setup_token)
    }
    add('session_token', form.session_token)
    add('access_token', form.access_token)
    add('codex_session', form.codex_session)
  }
  if (showServiceAccount.value) add('service_account_json', form.service_account_json)
  if (showBedrock.value) {
    if (form.bedrock_auth_mode === 'apikey') {
      add('api_key', form.api_key || form.credential)
    } else {
      add('aws_access_key_id', form.aws_access_key_id)
      add('aws_secret_access_key', form.aws_secret_access_key)
      add('aws_session_token', form.aws_session_token)
    }
    add('aws_region', form.aws_region)
  }
  return parts.join(';') || form.credential.trim()
}

function applyDurableFieldAliases() {
  if (form.custom_base_url_enabled && form.custom_base_url.trim()) {
    form.base_url = form.custom_base_url.trim()
  }
  if (form.platform === 'antigravity' && form.category === 'upstream') {
    form.type = 'openai_compatible'
  } else if (form.category === 'oauth-based') {
    form.type = 'oauth'
  } else if (form.category === 'service_account') {
    form.type = form.platform === 'gemini' ? 'gemini_vertex_service_account' : 'anthropic_vertex_service_account'
  } else if (form.category === 'bedrock') {
    form.type = 'anthropic_bedrock'
  } else if (form.platform === 'gemini') {
    form.type = 'gemini_api_key'
  } else if (form.platform === 'anthropic') {
    form.type = 'anthropic_api_key'
  } else if (form.platform === 'openai') {
    form.type = 'openai_api_key'
  }
}

function splitList(value: string): string[] {
  return value
    .split(/[\n,]+/)
    .map((item) => item.trim())
    .filter(Boolean)
}

function numberList(value: string): number[] {
  return splitList(value)
    .map((item) => Number(item))
    .filter((item) => Number.isFinite(item))
}

function compactMappings(mappings: ModelMapping[]): ModelMapping[] {
  return mappings
    .map((mapping) => ({ from: mapping.from.trim(), to: mapping.to.trim() }))
    .filter((mapping) => mapping.from && mapping.to)
}

function buildMetadata(): Record<string, unknown> {
  const selectedGroups = selectedGroupIDs.value
  return {
    upstream_create_account_compat: true,
    platform: form.platform,
    account_category: form.category,
    add_method: form.add_method,
    notes: form.notes.trim() || undefined,
    bedrock_auth_mode: form.bedrock_auth_mode,
    bedrock_force_global: form.bedrock_force_global,
    aws_region: form.aws_region.trim() || undefined,
    vertex_project_id: form.vertex_project_id.trim() || undefined,
    vertex_location: form.vertex_location.trim() || undefined,
    gemini_oauth_type: form.gemini_oauth_type,
    gemini_tier: form.gemini_tier,
    model_mode: form.model_mode,
    allowed_models: splitList(form.allowed_models),
    model_mappings: compactMappings(form.model_mappings),
    pool_mode: form.pool_mode,
    pool_mode_retry_count: form.pool_mode_retry_count,
    custom_error_codes_enabled: form.custom_error_codes_enabled,
    custom_error_codes: numberList(form.custom_error_codes),
    temp_unsched_enabled: form.temp_unsched_enabled,
    temp_unsched_rules: form.temp_unsched_rules,
    intercept_warmup_requests: form.intercept_warmup_requests,
    openai_passthrough: form.openai_passthrough,
    openai_ws_mode: form.openai_ws_mode,
    openai_compact_mode: form.openai_compact_mode,
    openai_compact_mappings: compactMappings(form.openai_compact_mappings),
    anthropic_passthrough: form.anthropic_passthrough,
    web_search_mode: form.web_search_mode,
    codex_cli_only: form.codex_cli_only,
    window_cost_enabled: form.window_cost_enabled,
    window_cost_limit: form.window_cost_limit,
    window_cost_sticky_reserve: form.window_cost_sticky_reserve,
    session_limit_enabled: form.session_limit_enabled,
    max_sessions: form.max_sessions,
    session_idle_timeout_minutes: form.session_idle_timeout_minutes,
    rpm_limit_enabled: form.rpm_limit_enabled,
    base_rpm: form.base_rpm,
    rpm_strategy: form.rpm_strategy,
    rpm_sticky_buffer: form.rpm_sticky_buffer,
    user_msg_queue_mode: form.user_msg_queue_mode,
    tls_fingerprint_enabled: form.tls_fingerprint_enabled,
    tls_fingerprint_profile_id: form.tls_fingerprint_profile_id.trim() || undefined,
    session_id_masking_enabled: form.session_id_masking_enabled,
    cache_ttl_override_enabled: form.cache_ttl_override_enabled,
    cache_ttl_override_target: form.cache_ttl_override_target,
    custom_base_url_enabled: form.custom_base_url_enabled,
    custom_base_url: form.custom_base_url.trim() || undefined,
    concurrency: form.concurrency,
    load_factor: form.load_factor,
    priority: form.priority,
    expires_at: form.expires_at || undefined,
    auto_pause_on_expired: form.auto_pause_on_expired,
    mixed_scheduling: form.mixed_scheduling,
    allow_overages: form.allow_overages,
    group_ids: selectedGroups,
    groups: selectedGroups
  }
}

function addModelMapping(target: 'main' | 'compact' = 'main', from = '', to = '') {
  const mapping = { from, to }
  if (target === 'compact') {
    form.openai_compact_mappings.push(mapping)
    return
  }
  form.model_mappings.push(mapping)
}

function addTempUnschedRule(errorCode = 429) {
  form.temp_unsched_rules.push({ error_code: errorCode, duration_minutes: 10, keywords: '', description: '' })
}

function buildAccount(): AccountConfig {
  ensureAccountID()
  applyDurableFieldAliases()
  const selectedGroups = selectedGroupIDs.value
  return {
    id: form.id.trim(),
    source_id: form.source_id.trim() || undefined,
    type: form.type.trim() || 'openai_api_key',
    label: form.label.trim() || form.id.trim(),
    tier: form.tier.trim() || 'simple',
    tags: Array.from(
      new Set([
        ...form.tags
          .split(/[\n,]+/)
          .map((tag) => tag.trim())
          .filter(Boolean),
        ...selectedGroups
      ])
    ),
    credential: buildCredential(),
    metadata: buildMetadata(),
    base_url: form.base_url.trim() || undefined,
    model: form.model.trim() || undefined,
    proxy_ref: form.proxy_ref.trim() || undefined,
    quota_policy: form.quota_policy.trim() || undefined,
    enabled: form.enabled
  }
}

async function saveAccount() {
  if (!accountsState.value || !canSubmit.value) return
  const account = buildAccount()
  if (editorMode.value === 'create') {
    await createAccount(accountsState.value.config_version, account)
    notice.value = `Created ${account.id}.`
  } else {
    await updateAccount(accountsState.value.config_version, account)
    notice.value = `Updated ${account.id}.`
  }
  editorOpen.value = false
  await loadAll()
}

async function toggleAccount(row: AccountRow) {
  if (!accountsState.value) return
  const account = structuredClone(row.summary.config)
  account.enabled = !row.enabled
  await updateAccount(accountsState.value.config_version, account)
  notice.value = `${account.enabled ? 'Enabled' : 'Disabled'} ${account.id}.`
  await loadAll()
}

async function runAccountTest(accountID?: string) {
  testResult.value = null
  if (accountID) {
    testResult.value = await refreshAccount(accountID)
    operationOutput.value = JSON.stringify(testResult.value, null, 2)
  } else {
    operationOutput.value = JSON.stringify(await testAllAccounts(), null, 2)
  }
  await loadAll()
}

async function removeAccount(row: AccountRow) {
  if (!accountsState.value) return
  await deleteAccount(accountsState.value.config_version, row.id)
  notice.value = `Deleted ${row.id}.`
  await loadAll()
}

async function preview() {
  operationOutput.value = JSON.stringify(await previewImport(importContent.value, importKind.value), null, 2)
}

async function apply() {
  if (!accountsState.value) return
  operationOutput.value = JSON.stringify(await applyImport(accountsState.value.config_version, importContent.value, importKind.value), null, 2)
  importOpen.value = false
  notice.value = 'Import applied to local JSON config.'
  await loadAll()
}

async function exportLocalJSON() {
  operationOutput.value = JSON.stringify(await exportAccounts(), null, 2)
  notice.value = 'Exported redacted local JSON.'
}

onMounted(() => {
  loadAll().catch(() => undefined)
})
</script>

<template>
  <AppShell>
    <main class="grid gap-4">
      <section class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <article v-for="(value, key) in healthCounts" :key="key" class="card">
          <p class="text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{{ key }}</p>
          <p class="mt-2 text-3xl font-black text-gray-950 dark:text-white">{{ value }}</p>
        </article>
      </section>

      <section class="card">
        <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div class="grid flex-1 gap-3 sm:grid-cols-3">
            <input v-model="search" class="input" placeholder="Search ID, label, type, tier, tags, source, proxy" />
            <select v-model="statusFilter" class="input">
              <option v-for="item in statusOptions" :key="item" :value="item">status: {{ item }}</option>
            </select>
            <select v-model="typeFilter" class="input">
              <option v-for="item in typeOptions" :key="item" :value="item">type: {{ item }}</option>
            </select>
          </div>
          <div class="flex flex-wrap gap-2">
            <button class="btn btn-secondary" type="button" :disabled="loading" @click="loadAll">
              <Icon name="refresh" />
              <span class="ml-2">Refresh</span>
            </button>
            <button class="btn btn-secondary" type="button" @click="runAccountTest()">Test All</button>
            <button class="btn btn-secondary" type="button" @click="importOpen = true">Import</button>
            <button class="btn btn-secondary" type="button" @click="exportLocalJSON">Export JSON</button>
            <button class="btn btn-primary" type="button" @click="openCreate">Create Account</button>
          </div>
        </div>
      </section>

      <p v-if="notice" class="rounded-xl border border-primary-200 bg-primary-50 p-3 text-sm text-primary-700 dark:border-primary-900 dark:bg-primary-950/40 dark:text-primary-200">{{ notice }}</p>
      <p v-if="error" class="rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-200">{{ error }}</p>

      <section class="card">
        <div class="mb-3 flex items-center justify-between gap-3">
          <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Accounts Core</h2>
          <span class="badge badge-primary">{{ filteredRows.length }} / {{ rows.length }} shown</span>
        </div>
        <DataTable :columns="accountColumns" :rows="filteredRows" empty-text="No accounts match the current filter.">
          <template #cell-label="{ row }">
            <div class="space-y-1">
              <p class="font-semibold text-gray-950 dark:text-white">{{ row.label }}</p>
              <p class="font-mono text-xs text-gray-500 dark:text-gray-400">{{ row.id }}</p>
              <StatusBadge :status="row.enabled ? 'enabled' : 'disabled'" />
            </div>
          </template>
          <template #cell-status="{ row }">
            <div class="space-y-1">
              <StatusBadge :status="String(row.status)" />
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ row.lastChecked || 'not checked' }}</p>
              <p v-if="row.lastError" class="max-w-xs text-xs text-red-500">{{ row.lastError }}</p>
            </div>
          </template>
          <template #cell-type="{ row }">
            <span class="badge badge-primary">{{ row.type }}</span>
          </template>
          <template #cell-tier="{ row }">
            <div class="space-y-2">
              <span class="font-semibold">{{ row.tier }}</span>
              <div class="flex flex-wrap gap-1">
                <span v-for="tag in row.tags" :key="tag" class="badge bg-gray-100 text-gray-700 dark:bg-dark-800 dark:text-gray-200">{{ tag }}</span>
              </div>
              <p class="text-xs text-gray-500">quota: {{ row.quotaStatus }}</p>
            </div>
          </template>
          <template #cell-source="{ row }">
            <div class="space-y-1 text-xs">
              <p><span class="text-gray-500">source</span> {{ row.source }}</p>
              <p><span class="text-gray-500">proxy</span> {{ row.proxy }}</p>
              <p><span class="text-gray-500">hits</span> {{ row.hits }} / errors {{ row.errors }}</p>
            </div>
          </template>
          <template #cell-baseURL="{ row }">
            <div class="max-w-xs space-y-1 text-xs">
              <p><span class="text-gray-500">model</span> {{ row.model }}</p>
              <p class="break-all"><span class="text-gray-500">base</span> {{ row.baseURL }}</p>
            </div>
          </template>
          <template #cell-actions="{ row }">
            <div class="flex flex-wrap justify-end gap-2 md:justify-start">
              <button class="btn btn-secondary px-3 py-1.5" type="button" @click="openEdit(row)">Edit</button>
              <button class="btn btn-secondary px-3 py-1.5" type="button" @click="toggleAccount(row)">{{ row.enabled ? 'Disable' : 'Enable' }}</button>
              <button class="btn btn-secondary px-3 py-1.5" type="button" @click="runAccountTest(String(row.id))">Test</button>
              <button class="btn btn-danger px-3 py-1.5" type="button" @click="removeAccount(row)">Delete</button>
            </div>
          </template>
        </DataTable>
      </section>

      <section class="card">
        <div class="mb-2 flex items-center justify-between">
          <h2 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Operation Output</h2>
          <StatusBadge v-if="testResult" :status="testResult.status" />
        </div>
        <pre class="max-h-96 overflow-auto rounded-xl bg-gray-100 p-3 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300">{{ operationOutput || 'No operation output yet.' }}</pre>
      </section>

      <div v-if="editorOpen" class="xforce-modal-backdrop fixed inset-0 z-50 grid place-items-center">
        <section class="card xforce-modal-panel max-h-[92vh] w-full max-w-6xl overflow-auto">
          <div class="flex items-center justify-between gap-3">
            <div>
              <h2 class="text-lg font-black">{{ editorMode === 'create' ? 'Create Account' : 'Edit Account' }}</h2>
              <p class="text-xs text-gray-500 dark:text-gray-400">Upstream-inspired account creation surface. Values are persisted into the local JSON account config and metadata.</p>
            </div>
            <button class="btn btn-secondary" type="button" @click="editorOpen = false">Close</button>
          </div>
          <div class="mt-4">
            <div v-if="isOAuthFlow" class="mb-6 flex items-center justify-center">
              <div class="flex items-center space-x-4">
                <div class="flex items-center">
                  <span :class="['flex h-8 w-8 items-center justify-center rounded-full text-sm font-semibold', step >= 1 ? 'bg-primary-500 text-white' : 'bg-gray-200 text-gray-500 dark:bg-dark-600']">1</span>
                  <span class="ml-2 text-sm font-medium text-gray-700 dark:text-gray-300">Authorization Method</span>
                </div>
                <div class="h-0.5 w-8 bg-gray-300 dark:bg-dark-600"></div>
                <div class="flex items-center">
                  <span :class="['flex h-8 w-8 items-center justify-center rounded-full text-sm font-semibold', step >= 2 ? 'bg-primary-500 text-white' : 'bg-gray-200 text-gray-500 dark:bg-dark-600']">2</span>
                  <span class="ml-2 text-sm font-medium text-gray-700 dark:text-gray-300">{{ oauthStepTitle }}</span>
                </div>
              </div>
            </div>

            <div v-if="step === 1" id="create-account-form" class="space-y-5">
              <input v-model="form.id" type="hidden" />
              <div>
                <label class="input-label">{{ t('admin.accounts.accountName') }}</label>
                <input ref="accountNameInput" v-model="form.label" type="text" required class="input normal-case" :placeholder="t('admin.accounts.enterAccountName')" data-tour="account-form-name" @input="accountNameInput?.setCustomValidity('')" />
              </div>
              <div>
                <label class="input-label">{{ t('admin.accounts.notes') }}</label>
                <textarea v-model="form.notes" rows="3" class="input normal-case" :placeholder="t('admin.accounts.notesPlaceholder')"></textarea>
                <p class="input-hint">{{ t('admin.accounts.notesHint') }}</p>
              </div>

              <div>
                <label class="input-label">{{ t('admin.accounts.platform') }}</label>
                <div class="mt-2 flex rounded-lg bg-gray-100 p-1 dark:bg-dark-700" data-tour="account-form-platform">
                  <button
                    v-for="item in platformCards"
                    :key="item.value"
                    type="button"
                    :class="[
                      'flex flex-1 items-center justify-center gap-2 rounded-md px-4 py-2.5 text-sm font-medium transition-all',
                      form.platform === item.value ? item.active : item.inactive
                    ]"
                    @click="selectPlatform(item.value)"
                  >
                    <svg v-if="item.value === 'anthropic'" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09z" />
                    </svg>
                    <svg v-else-if="item.value === 'openai'" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 13.5l10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z" />
                    </svg>
                    <svg v-else-if="item.value === 'gemini'" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M12 2l1.5 6.5L20 10l-6.5 1.5L12 18l-1.5-6.5L4 10l6.5-1.5L12 2z" />
                    </svg>
                    <svg v-else class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 15a4.5 4.5 0 004.5 4.5h10.5a4.5 4.5 0 10-.9-8.91A6.001 6.001 0 005.12 12.75 4.502 4.502 0 002.25 15z" />
                    </svg>
                    <span>{{ item.title }}</span>
                    <span class="hidden rounded bg-gray-200 px-1.5 py-0.5 text-[10px] font-semibold text-gray-600 dark:bg-dark-500 dark:text-gray-300 lg:inline">{{ item.count }}</span>
                  </button>
                </div>
              </div>

              <div>
                <div class="flex items-center justify-between">
                  <label class="input-label">{{ t('admin.accounts.accountType') }}</label>
                  <button v-if="form.platform === 'gemini'" type="button" @click="showGeminiHelpDialog = true" class="flex items-center gap-1 rounded px-2 py-1 text-xs text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/20">
                    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M9.879 7.519c1.171-1.025 3.071-1.025 4.242 0 1.172 1.025 1.172 2.687 0 3.712-.203.179-.43.326-.67.442-.745.361-1.45.999-1.45 1.827v.75M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9 5.25h.008v.008H12v-.008z" />
                    </svg>
                    {{ t('admin.accounts.gemini.helpButton') }}
                  </button>
                </div>
                <div class="mt-2 grid grid-cols-2 gap-3" :class="form.platform === 'anthropic' ? 'sm:grid-cols-4' : form.platform === 'gemini' ? 'sm:grid-cols-3' : ''" data-tour="account-form-type">
                  <button
                    v-for="item in currentCategoryCards"
                    :key="item.value"
                    type="button"
                    :class="[
                      'flex items-center gap-3 rounded-lg border-2 p-3 text-left transition-all',
                      form.category === item.value
                        ? form.platform === 'anthropic' && item.value === 'oauth-based' ? 'border-orange-500 bg-orange-50 dark:bg-orange-900/20'
                          : item.value === 'apikey' ? 'border-purple-500 bg-purple-50 dark:bg-purple-900/20'
                            : item.value === 'bedrock' ? 'border-amber-500 bg-amber-50 dark:bg-amber-900/20'
                              : item.value === 'service_account' ? 'border-sky-500 bg-sky-50 dark:bg-sky-900/20'
                                : form.platform === 'openai' ? 'border-green-500 bg-green-50 dark:bg-green-900/20'
                                  : form.platform === 'gemini' ? 'border-blue-500 bg-blue-50 dark:bg-blue-900/20'
                                    : 'border-purple-500 bg-purple-50 dark:bg-purple-900/20'
                        : form.platform === 'anthropic' && item.value === 'oauth-based' ? 'border-gray-200 hover:border-orange-300 dark:border-dark-600 dark:hover:border-orange-700'
                          : item.value === 'apikey' ? 'border-gray-200 hover:border-purple-300 dark:border-dark-600 dark:hover:border-purple-700'
                            : item.value === 'bedrock' ? 'border-gray-200 hover:border-amber-300 dark:border-dark-600 dark:hover:border-amber-700'
                              : item.value === 'service_account' ? 'border-gray-200 hover:border-sky-300 dark:border-dark-600 dark:hover:border-sky-700'
                                : form.platform === 'openai' ? 'border-gray-200 hover:border-green-300 dark:border-dark-600 dark:hover:border-green-700'
                                  : form.platform === 'gemini' ? 'border-gray-200 hover:border-blue-300 dark:border-dark-600 dark:hover:border-blue-700'
                                    : 'border-gray-200 hover:border-purple-300 dark:border-dark-600 dark:hover:border-purple-700'
                    ]"
                    @click="selectCategory(item.value)"
                  >
                    <span :class="['flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-sm font-black', form.category === item.value ? item.value === 'apikey' ? 'bg-purple-500 text-white' : item.value === 'bedrock' ? 'bg-amber-500 text-white' : item.value === 'service_account' ? 'bg-sky-500 text-white' : form.platform === 'openai' ? 'bg-green-500 text-white' : form.platform === 'gemini' ? 'bg-blue-500 text-white' : form.platform === 'antigravity' ? 'bg-purple-500 text-white' : 'bg-orange-500 text-white' : 'bg-gray-100 text-gray-500 dark:bg-dark-600 dark:text-gray-400']">{{ item.title.slice(0, 1) }}</span>
                    <span>
                      <span class="block text-sm font-medium text-gray-900 dark:text-white">{{ item.title }}</span>
                      <span class="text-xs text-gray-500 dark:text-gray-400">{{ item.subtitle }}</span>
                    </span>
                  </button>
                </div>
                <div v-if="form.platform === 'anthropic' && form.category === 'service_account'" class="mt-3 rounded-lg border border-sky-200 bg-sky-50 px-3 py-2 text-xs text-sky-800 dark:border-sky-800/40 dark:bg-sky-900/20 dark:text-sky-200">
                  <p>{{ t('admin.accounts.vertexAnthropicHint') }}</p>
                </div>
              </div>

              <div v-if="form.platform === 'gemini' && form.category === 'oauth-based'" class="mt-4">
                <label class="input-label">{{ t('admin.accounts.oauth.gemini.oauthTypeLabel') }}</label>
                <div class="mt-2 grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <button type="button" :class="['flex items-center gap-3 rounded-lg border-2 p-3 text-left transition-all', form.gemini_oauth_type === 'google_one' ? 'border-purple-500 bg-purple-50 dark:bg-purple-900/20' : 'border-gray-200 hover:border-purple-300 dark:border-dark-600 dark:hover:border-purple-700']" @click="form.gemini_oauth_type = 'google_one'; form.gemini_tier = 'google_one_free'">
                    <span>
                      <span class="block text-sm font-medium text-gray-900 dark:text-white">Google One</span>
                      <span class="text-xs text-gray-500 dark:text-gray-400">个人账号，享受 Google One 订阅配额</span>
                      <span class="mt-2 flex flex-wrap gap-1"><span class="rounded bg-purple-100 px-2 py-0.5 text-[10px] font-semibold text-purple-700 dark:bg-purple-900/40 dark:text-purple-300">推荐个人用户</span><span class="rounded bg-emerald-100 px-2 py-0.5 text-[10px] font-semibold text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300">无需 GCP</span></span>
                    </span>
                  </button>
                  <button type="button" :class="['flex items-center gap-3 rounded-lg border-2 p-3 text-left transition-all', form.gemini_oauth_type === 'code_assist' ? 'border-blue-500 bg-blue-50 dark:bg-blue-900/20' : 'border-gray-200 hover:border-blue-300 dark:border-dark-600 dark:hover:border-blue-700']" @click="form.gemini_oauth_type = 'code_assist'; form.gemini_tier = 'gcp_standard'">
                    <span>
                      <span class="block text-sm font-medium text-gray-900 dark:text-white">GCP Code Assist</span>
                      <span class="text-xs text-gray-500 dark:text-gray-400">企业级，需要 GCP 项目</span>
                      <span class="mt-2 flex flex-wrap gap-1"><span class="rounded bg-blue-100 px-2 py-0.5 text-[10px] font-semibold text-blue-700 dark:bg-blue-900/40 dark:text-blue-300">企业用户</span><span class="rounded bg-emerald-100 px-2 py-0.5 text-[10px] font-semibold text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300">高并发</span></span>
                    </span>
                  </button>
                </div>
                <div class="mt-3">
                  <button type="button" @click="showAdvancedOAuth = !showAdvancedOAuth" class="flex items-center gap-2 text-sm text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200">
                    <span :class="['h-4 w-4 transition-transform', showAdvancedOAuth ? 'rotate-90' : '']">›</span>
                    <span>{{ showAdvancedOAuth ? '隐藏' : '显示' }}高级选项（自建 OAuth Client）</span>
                  </button>
                </div>
                <div v-if="showAdvancedOAuth" class="mt-3 group relative">
                  <button type="button" :disabled="!geminiAIStudioOAuthEnabled" :class="['flex w-full items-center gap-3 rounded-lg border-2 p-3 text-left transition-all', !geminiAIStudioOAuthEnabled ? 'cursor-not-allowed opacity-60' : '', form.gemini_oauth_type === 'ai_studio' ? 'border-amber-500 bg-amber-50 dark:bg-amber-900/20' : 'border-gray-200 hover:border-amber-300 dark:border-dark-600 dark:hover:border-amber-700']" @click="form.gemini_oauth_type = 'ai_studio'; form.gemini_tier = 'aistudio_free'">
                    <span>
                      <span class="block text-sm font-medium text-gray-900 dark:text-white">Custom AI Studio</span>
                      <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.gemini.oauthType.customDesc') }}</span>
                      <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.gemini.oauthType.customRequirement') }}</span>
                      <span class="mt-2 flex flex-wrap gap-1"><span class="rounded bg-amber-100 px-2 py-0.5 text-[10px] font-semibold text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">{{ t('admin.accounts.gemini.oauthType.badges.orgManaged') }}</span><span class="rounded bg-amber-100 px-2 py-0.5 text-[10px] font-semibold text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">{{ t('admin.accounts.gemini.oauthType.badges.adminRequired') }}</span></span>
                      <span v-if="!geminiAIStudioOAuthEnabled" class="ml-2 rounded bg-amber-100 px-2 py-0.5 text-[10px] font-semibold text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">{{ 'Not Configured' }}</span>
                    </span>
                  </button>
                </div>
              </div>

              <div v-if="form.platform === 'gemini' && form.category === 'apikey'" class="mt-3 rounded-lg border border-purple-200 bg-purple-50 px-3 py-2 text-xs text-purple-800 dark:border-purple-800/40 dark:bg-purple-900/20 dark:text-purple-200">
                <p>{{ t('admin.accounts.gemini.accountType.apiKeyNote') }}</p>
              </div>
              <div v-if="form.platform === 'gemini' && form.category === 'service_account'" class="mt-3 rounded-lg border border-sky-200 bg-sky-50 px-3 py-2 text-xs text-sky-800 dark:border-sky-800/40 dark:bg-sky-900/20 dark:text-sky-200">
                <p>{{ t('admin.accounts.vertexGeminiHint') }}</p>
              </div>

              <div v-if="form.platform === 'gemini' && form.category !== 'service_account'" class="mt-4">
                <label class="input-label">{{ t('admin.accounts.gemini.tier.label') }}</label>
                <select v-model="form.gemini_tier" class="input normal-case">
                  <option v-if="form.gemini_oauth_type === 'google_one'" value="google_one_free">{{ t('admin.accounts.gemini.tier.googleOne.free') }}</option>
                  <option v-if="form.gemini_oauth_type === 'google_one'" value="google_ai_pro">{{ t('admin.accounts.gemini.tier.googleOne.pro') }}</option>
                  <option v-if="form.gemini_oauth_type === 'google_one'" value="google_ai_ultra">{{ t('admin.accounts.gemini.tier.googleOne.ultra') }}</option>
                  <option v-if="form.gemini_oauth_type === 'code_assist'" value="gcp_standard">{{ t('admin.accounts.gemini.tier.gcp.standard') }}</option>
                  <option v-if="form.gemini_oauth_type === 'code_assist'" value="gcp_enterprise">{{ t('admin.accounts.gemini.tier.gcp.enterprise') }}</option>
                  <option v-if="form.gemini_oauth_type === 'ai_studio' || form.category === 'apikey'" value="aistudio_free">{{ t('admin.accounts.gemini.tier.aiStudio.free') }}</option>
                  <option v-if="form.gemini_oauth_type === 'ai_studio' || form.category === 'apikey'" value="aistudio_paid">{{ t('admin.accounts.gemini.tier.aiStudio.paid') }}</option>
                </select>
                <p class="input-hint">{{ form.category === 'apikey' ? t('admin.accounts.gemini.tier.aiStudioHint') : t('admin.accounts.gemini.tier.hint') }}</p>
              </div>

              <div v-if="form.platform === 'anthropic' && isOAuthFlow">
                <label class="input-label">{{ t('admin.accounts.addMethod') }}</label>
                <div class="mt-2 flex gap-4">
                  <label class="flex cursor-pointer items-center text-sm text-gray-700 dark:text-gray-300"><input v-model="form.add_method" type="radio" value="oauth" class="mr-2 text-primary-600 focus:ring-primary-500" /> {{ t('admin.accounts.types.oauth') }}</label>
                  <label class="flex cursor-pointer items-center text-sm text-gray-700 dark:text-gray-300"><input v-model="form.add_method" type="radio" value="setup-token" class="mr-2 text-primary-600 focus:ring-primary-500" /> {{ t('admin.accounts.setupTokenLongLived') }}</label>
                </div>
              </div>

              <div v-if="!isOAuthFlow" class="border-t border-gray-200 pt-4 dark:border-dark-600">
                <div v-if="form.platform === 'antigravity' && form.category === 'upstream'" class="space-y-4">
                  <div>
                    <label class="input-label">{{ t('admin.accounts.upstream.baseUrl') }}</label>
                    <input v-model="form.base_url" type="text" required class="input normal-case" placeholder="https://cloudcode-pa.googleapis.com" />
                    <p class="input-hint">{{ t('admin.accounts.upstream.baseUrlHint') }}</p>
                  </div>
                  <div>
                    <label class="input-label">{{ t('admin.accounts.upstream.apiKey') }}</label>
                    <input v-model="form.api_key" type="password" required class="input font-mono normal-case" placeholder="sk-..." />
                    <p class="input-hint">{{ t('admin.accounts.upstream.apiKeyHint') }}</p>
                  </div>
                </div>

                <div v-if="showAPICredential && form.category !== 'upstream'" class="space-y-4">
                  <div>
                    <label class="input-label">{{ t('admin.accounts.baseUrl') }}</label>
                    <input v-model="form.base_url" type="text" class="input normal-case" :placeholder="form.platform === 'openai' ? 'https://api.openai.com' : form.platform === 'gemini' ? 'https://generativelanguage.googleapis.com' : 'https://api.anthropic.com'" />
                    <p class="input-hint">{{ form.platform === 'openai' ? t('admin.accounts.openai.baseUrlHint') : form.platform === 'gemini' ? t('admin.accounts.gemini.baseUrlHint') : t('admin.accounts.baseUrlHint') }}</p>
                  </div>
                  <div>
                    <label class="input-label">{{ t('admin.accounts.apiKeyRequired') }}</label>
                    <input v-model="form.api_key" type="password" required class="input font-mono normal-case" :placeholder="form.platform === 'openai' ? 'sk-proj-...' : form.platform === 'gemini' ? 'AIza...' : 'sk-ant-...'" />
                    <p class="input-hint">{{ form.platform === 'openai' ? t('admin.accounts.openai.apiKeyHint') : form.platform === 'gemini' ? t('admin.accounts.gemini.apiKeyHint') : t('admin.accounts.apiKeyHint') }}</p>
                  </div>
                </div>

                <div v-if="showServiceAccount" class="space-y-4">
                  <div>
                    <label class="input-label">Service Account JSON</label>
                    <textarea v-model="form.service_account_json" class="input min-h-36 font-mono normal-case" placeholder="Paste service account JSON"></textarea>
                    <p class="input-hint">Project ID is preserved separately below for local JSON durability.</p>
                  </div>
                  <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    <label class="grid gap-1"><span class="input-label">Project ID</span><input v-model="form.vertex_project_id" class="input font-mono normal-case" placeholder="project-id" /></label>
                    <label class="grid gap-1"><span class="input-label">Location</span><input v-model="form.vertex_location" required class="input font-mono normal-case" placeholder="us-central1" /></label>
                  </div>
                </div>

                <div v-if="showBedrock" class="space-y-4">
                  <div>
                    <label class="input-label">{{ t('admin.accounts.bedrockAuthMode') }}</label>
                    <div class="mt-2 flex gap-4">
                      <label class="flex cursor-pointer items-center"><input v-model="form.bedrock_auth_mode" type="radio" value="sigv4" class="mr-2 text-primary-600 focus:ring-primary-500" /><span class="text-sm text-gray-700 dark:text-gray-300">{{ t('admin.accounts.bedrockAuthModeSigv4') }}</span></label>
                      <label class="flex cursor-pointer items-center"><input v-model="form.bedrock_auth_mode" type="radio" value="apikey" class="mr-2 text-primary-600 focus:ring-primary-500" /><span class="text-sm text-gray-700 dark:text-gray-300">{{ t('admin.accounts.bedrockAuthModeApikey') }}</span></label>
                    </div>
                  </div>
                  <template v-if="form.bedrock_auth_mode === 'sigv4'">
                    <div><label class="input-label">{{ t('admin.accounts.bedrockAccessKeyId') }}</label><input v-model="form.aws_access_key_id" class="input font-mono normal-case" placeholder="AKIA..." /></div>
                    <div><label class="input-label">{{ t('admin.accounts.bedrockSecretAccessKey') }}</label><input v-model="form.aws_secret_access_key" type="password" class="input font-mono normal-case" /></div>
                    <div><label class="input-label">{{ t('admin.accounts.bedrockSessionToken') }}</label><input v-model="form.aws_session_token" type="password" class="input font-mono normal-case" /><p class="input-hint">{{ t('admin.accounts.bedrockSessionTokenHint') }}</p></div>
                  </template>
                  <div v-else><label class="input-label">{{ t('admin.accounts.bedrockApiKeyInput') }}</label><input v-model="form.api_key" type="password" class="input font-mono normal-case" /></div>
                  <div><label class="input-label">{{ t('admin.accounts.bedrockRegion') }}</label><input v-model="form.aws_region" class="input font-mono normal-case" placeholder="us-east-1" /><p class="input-hint">{{ t('admin.accounts.bedrockRegionHint') }}</p></div>
                  <div><label class="flex cursor-pointer items-center gap-2"><input v-model="form.bedrock_force_global" type="checkbox" class="rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500" /><span class="text-sm text-gray-700 dark:text-gray-300">{{ t('admin.accounts.bedrockForceGlobal') }}</span></label><p class="input-hint mt-1">{{ t('admin.accounts.bedrockForceGlobalHint') }}</p></div>
                </div>

                <div class="mt-4 grid gap-3 sm:grid-cols-2">
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Simple Type
                    <select v-model="form.type" class="input normal-case">
                      <option value="openai_api_key">openai_api_key</option>
                      <option value="openai_compatible">openai_compatible</option>
                      <option value="oauth">oauth</option>
                    </select>
                  </label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Tier<input v-model="form.tier" class="input normal-case" placeholder="simple / pro / ultra" /></label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Source ID<input v-model="form.source_id" class="input normal-case" placeholder="optional imported source" /></label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Default Model<input v-model="form.model" class="input normal-case" placeholder="optional" /></label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">{{ t('admin.accounts.proxy') }}
                    <select v-model="form.proxy_ref" class="input normal-case">
                      <option value="">direct</option>
                      <option v-for="proxy in proxyOptions" :key="proxy.id" :value="proxy.id">{{ proxyLabel(proxy) }}</option>
                    </select>
                  </label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Quota Policy
                    <select v-model="form.quota_policy" class="input normal-case">
                      <option value="">none</option>
                      <option v-for="policy in accountsState?.quota_policies || []" :key="policy.id" :value="policy.id">{{ policy.id }}</option>
                    </select>
                  </label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Tags<input v-model="form.tags" class="input normal-case" placeholder="comma separated" /></label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500 sm:col-span-2">Raw Credential Envelope<textarea v-model="form.credential" class="input min-h-20 font-mono normal-case" placeholder="Optional fallback or redacted value; structured fields above build credential automatically"></textarea></label>
                  <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.enabled" type="checkbox" /> Enabled for gateway pool</label>
                </div>
              </div>
            </div>

            <section v-else class="rounded-2xl border border-gray-200 p-4 dark:border-dark-700 space-y-4">
              <div class="flex items-center justify-between border-b border-gray-200 pb-3 dark:border-dark-700">
                <h3 class="text-sm font-bold uppercase tracking-wider text-gray-800 dark:text-gray-200">{{ authorizationStepTitle }}</h3>
                <span class="badge badge-primary uppercase">{{ form.platform }}</span>
              </div>

              <!-- Auth Method Selection Tabs -->
              <div class="mt-4 flex flex-wrap gap-2 border-b border-gray-100 pb-2 dark:border-dark-800">
                <button
                  type="button"
                  :class="['px-3 py-1.5 text-xs font-semibold rounded-lg transition', activeOAuthTab === 'manual' ? 'bg-primary-500 text-white' : 'bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700']"
                  @click="activeOAuthTab = 'manual'"
                >
                  Manual Authorization
                </button>
                <button
                  v-if="isClaudeOAuthFlow"
                  type="button"
                  :class="['px-3 py-1.5 text-xs font-semibold rounded-lg transition', activeOAuthTab === 'cookie' ? 'bg-primary-500 text-white' : 'bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700']"
                  @click="activeOAuthTab = 'cookie'"
                >
                  Claude Cookie Auto-Auth
                </button>
                <button
                  v-if="form.platform === 'openai' || form.platform === 'antigravity'"
                  type="button"
                  :class="['px-3 py-1.5 text-xs font-semibold rounded-lg transition', activeOAuthTab === 'refresh_token' ? 'bg-primary-500 text-white' : 'bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700']"
                  @click="activeOAuthTab = 'refresh_token'"
                >
                  Refresh Token
                </button>
                <button
                  v-if="form.platform === 'openai'"
                  type="button"
                  :class="['px-3 py-1.5 text-xs font-semibold rounded-lg transition', activeOAuthTab === 'mobile_refresh_token' ? 'bg-primary-500 text-white' : 'bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700']"
                  @click="activeOAuthTab = 'mobile_refresh_token'"
                >
                  Mobile RT
                </button>
                <button
                  v-if="form.platform === 'openai'"
                  type="button"
                  :class="['px-3 py-1.5 text-xs font-semibold rounded-lg transition', activeOAuthTab === 'session_token' ? 'bg-primary-500 text-white' : 'bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700']"
                  @click="activeOAuthTab = 'session_token'"
                >
                  Session Token
                </button>
                <button
                  v-if="form.platform === 'openai'"
                  type="button"
                  :class="['px-3 py-1.5 text-xs font-semibold rounded-lg transition', activeOAuthTab === 'access_token' ? 'bg-primary-500 text-white' : 'bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700']"
                  @click="activeOAuthTab = 'access_token'"
                >
                  Access Token
                </button>
                <button
                  v-if="form.platform === 'openai'"
                  type="button"
                  :class="['px-3 py-1.5 text-xs font-semibold rounded-lg transition', activeOAuthTab === 'codex_session' ? 'bg-primary-500 text-white' : 'bg-gray-100 text-gray-700 hover:bg-gray-200 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700']"
                  @click="activeOAuthTab = 'codex_session'"
                >
                  Codex Session JSON
                </button>
              </div>

              <!-- Tab Content -->
              <div class="mt-4">
                <!-- 1. MANUAL AUTHORIZATION FLOW -->
                <div v-if="activeOAuthTab === 'manual'" class="space-y-4">
                  <!-- Substep 1 -->
                  <div class="rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-900/30 dark:bg-blue-950/10">
                    <div class="flex items-start gap-3">
                      <div class="flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full bg-blue-600 text-xs font-bold text-white">1</div>
                      <div class="flex-1">
                        <p class="font-medium text-blue-900 dark:text-blue-200">Generate authorization link</p>
                        
                        <div v-if="form.platform === 'gemini' && form.gemini_oauth_type === 'code_assist'" class="mt-3">
                          <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">
                            GCP Project ID
                            <input v-model="form.vertex_project_id" class="input font-mono normal-case" placeholder="my-gcp-project-123" />
                          </label>
                        </div>
                        
                        <div class="mt-3">
                          <button
                            v-if="!computedAuthUrl"
                            type="button"
                            class="btn btn-primary px-4 py-2 text-sm"
                            @click="handleGenerateAuthLink"
                          >
                            Generate Authorization URL
                          </button>
                          <div v-else class="space-y-3">
                            <div class="flex items-center gap-2">
                              <input :value="computedAuthUrl" readonly class="input flex-1 bg-gray-50 font-mono text-xs normal-case dark:bg-gray-700" />
                              <button type="button" class="btn btn-secondary px-3 py-1.5 text-xs" @click="handleCopyAuthUrl">
                                {{ copiedUrl ? 'Copied!' : 'Copy' }}
                              </button>
                            </div>
                            <button type="button" class="text-xs text-blue-600 hover:underline dark:text-blue-400" @click="computedAuthUrl = ''">
                              Regenerate Link
                            </button>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>

                  <!-- Substep 2 -->
                  <div class="rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-900/30 dark:bg-blue-950/10">
                    <div class="flex items-start gap-3">
                      <div class="flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full bg-blue-600 text-xs font-bold text-white">2</div>
                      <div class="flex-1">
                        <p class="font-medium text-blue-900 dark:text-blue-200">Open link & authorize account</p>
                        <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">
                          Click the generated link (or paste it into a new window) to log in. After complete authentication, your browser will redirect to a local callback URL.
                        </p>
                        <!-- warnings -->
                        <div class="mt-2 rounded-lg border border-amber-300 bg-amber-50 p-3 dark:border-amber-900/40 dark:bg-amber-950/20">
                          <p class="text-xs text-amber-800 dark:text-amber-300">
                            <strong>Note:</strong> Since the local dashboard runs offline, the redirect will show a "Site can't be reached" error. This is 100% expected! Just copy the entire failed URL from your browser's address bar.
                          </p>
                        </div>
                        <div v-if="form.platform === 'openai'" class="mt-2 rounded-lg border border-yellow-300 bg-yellow-50 p-3 dark:border-yellow-900/40 dark:bg-yellow-950/20">
                          <p class="text-xs text-yellow-800 dark:text-yellow-300">
                            OpenAI OAuth uses strict PKCE client validation. Do not block or clear local cookies during auth.
                          </p>
                        </div>
                      </div>
                    </div>
                  </div>

                  <!-- Substep 3 -->
                  <div class="rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-900/30 dark:bg-blue-950/10">
                    <div class="flex items-start gap-3">
                      <div class="flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full bg-blue-600 text-xs font-bold text-white">3</div>
                      <div class="flex-1">
                        <p class="font-medium text-blue-900 dark:text-blue-200">Paste code or callback URL</p>
                        <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">
                          Paste either the clean authorization code, or the full callback URL from address bar. We will extract the code parameter automatically!
                        </p>
                        <label class="mt-3 grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">
                          Authorization Code / Redirect URL
                          <textarea v-model="authCodeInput" rows="3" class="input font-mono normal-case" placeholder="http://localhost:1455/auth/callback?code=... or raw authorization code"></textarea>
                        </label>
                      </div>
                    </div>
                  </div>
                </div>

                <!-- 2. CLAUDE COOKIE AUTO-AUTH -->
                <div v-if="activeOAuthTab === 'cookie'" class="space-y-4">
                  <div class="rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-900/30 dark:bg-blue-950/10">
                    <h5 class="font-semibold text-blue-900 dark:text-blue-200">Claude Session Cookie Import</h5>
                    <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">
                      Import session credentials directly by capturing the sessionKey cookie from your logged-in session on claude.ai.
                    </p>
                    <div class="mt-3 rounded-lg border border-amber-200 bg-amber-50/50 p-3 dark:border-amber-900/30 dark:bg-amber-950/10">
                      <h6 class="text-xs font-bold uppercase tracking-wider text-amber-800 dark:text-amber-200">How to get sessionKey cookie:</h6>
                      <ol class="mt-1 list-inside list-decimal text-xs text-amber-700 dark:text-amber-300 space-y-1">
                        <li>Log in to claude.ai in your browser.</li>
                        <li>Press F12 to open developer tools, select the Application / Storage tab, and look under Cookies for 'claude.ai'.</li>
                        <li>Find the cookie named 'sessionKey' and copy its entire value (starting with 'sk-ant-sid01-...').</li>
                      </ol>
                    </div>
                    <label class="mt-3 grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">
                      sessionKey Cookie
                      <textarea v-model="form.session_token" rows="3" class="input font-mono normal-case" placeholder="sk-ant-sid01-..."></textarea>
                    </label>
                  </div>
                </div>

                <!-- 3. REFRESH TOKEN TABS -->
                <div v-if="activeOAuthTab === 'refresh_token'" class="space-y-4">
                  <div class="rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-900/30 dark:bg-blue-950/10">
                    <h5 class="font-semibold text-blue-900 dark:text-blue-200">Manual Refresh Token</h5>
                    <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">
                      Input the long-lived refresh token directly. This token is used to exchange for short-lived access tokens during API execution.
                    </p>
                    <label class="mt-3 grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">
                      Refresh Token
                      <textarea v-model="form.refresh_token" rows="3" class="input font-mono normal-case" placeholder="Paste refresh token here"></textarea>
                    </label>
                  </div>
                </div>

                <div v-if="activeOAuthTab === 'mobile_refresh_token'" class="space-y-4">
                  <div class="rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-900/30 dark:bg-blue-950/10">
                    <h5 class="font-semibold text-blue-900 dark:text-blue-200">OpenAI Mobile Refresh Token</h5>
                    <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">
                      Input your mobile-app client refresh token (often extracted from official ChatGPT iOS/Android client logs).
                    </p>
                    <label class="mt-3 grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">
                      Mobile Refresh Token
                      <textarea v-model="form.refresh_token" rows="3" class="input font-mono normal-case" placeholder="Paste mobile refresh token here"></textarea>
                    </label>
                  </div>
                </div>

                <div v-if="activeOAuthTab === 'session_token'" class="space-y-4">
                  <div class="rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-900/30 dark:bg-blue-950/10">
                    <h5 class="font-semibold text-blue-900 dark:text-blue-200">Session Token</h5>
                    <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">
                      Paste a direct ChatGPT/OpenAI web session token (e.g. from cookies) for short-lived authorization.
                    </p>
                    <label class="mt-3 grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">
                      Session Token
                      <textarea v-model="form.session_token" rows="3" class="input font-mono normal-case" placeholder="Paste session token here"></textarea>
                    </label>
                  </div>
                </div>

                <div v-if="activeOAuthTab === 'access_token'" class="space-y-4">
                  <div class="rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-900/30 dark:bg-blue-950/10">
                    <h5 class="font-semibold text-blue-900 dark:text-blue-200">Access Token</h5>
                    <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">
                      Input a short-lived OpenAI web access token (usually starting with eyJ...).
                    </p>
                    <label class="mt-3 grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">
                      Access Token
                      <textarea v-model="form.access_token" rows="3" class="input font-mono normal-case" placeholder="eyJ..."></textarea>
                    </label>
                  </div>
                </div>

                <div v-if="activeOAuthTab === 'codex_session'" class="space-y-4">
                  <div class="rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-900/30 dark:bg-blue-950/10">
                    <h5 class="font-semibold text-blue-900 dark:text-blue-200">Codex Session JSON Import</h5>
                    <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">
                      Paste Codex session JSON blocks to auto-extract credentials and configuration settings.
                    </p>
                    <label class="mt-3 grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">
                      Codex Session JSON
                      <textarea v-model="form.codex_session" rows="8" class="input font-mono normal-case" placeholder="Paste Codex session JSON here"></textarea>
                    </label>
                  </div>
                </div>
              </div>

              <!-- General Fields in Step 2 -->
              <div class="mt-6 border-t border-gray-200 pt-4 dark:border-dark-700 space-y-4">
                <h4 class="text-xs font-black uppercase tracking-wider text-gray-500 dark:text-gray-400">Account Routing & Quota Rules</h4>
                <div class="grid gap-3 sm:grid-cols-2">
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">
                    Proxy
                    <select v-model="form.proxy_ref" class="input normal-case">
                      <option value="">direct</option>
                      <option v-for="proxy in proxyOptions" :key="proxy.id" :value="proxy.id">{{ proxyLabel(proxy) }}</option>
                    </select>
                  </label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">
                    Quota Policy
                    <select v-model="form.quota_policy" class="input normal-case">
                      <option value="">none</option>
                      <option v-for="policy in accountsState?.quota_policies || []" :key="policy.id" :value="policy.id">{{ policy.id }}</option>
                    </select>
                  </label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500 sm:col-span-2">
                    Tags
                    <input v-model="form.tags" class="input normal-case" placeholder="comma separated tags" />
                  </label>
                  <label class="flex items-center gap-2 text-sm font-semibold sm:col-span-2">
                    <input v-model="form.enabled" type="checkbox" />
                    Enabled for gateway pool
                  </label>
                </div>
              </div>
            </section>


            <section v-if="showAdvancedAccountOptions" class="rounded-2xl border border-gray-200 p-4 dark:border-dark-700">
              <div class="flex items-center justify-between gap-3">
                <h3 class="text-sm font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">Create Account Advanced Options</h3>
                <button class="btn btn-secondary px-3 py-1.5" type="button" @click="showAdvancedAccountOptions = false">Collapse</button>
              </div>
              <div class="mt-4 grid gap-4">
                <div v-if="showGeminiAdvanced" class="grid gap-3 sm:grid-cols-2">
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Gemini OAuth Type
                    <select v-model="form.gemini_oauth_type" class="input normal-case">
                      <option value="google_one">Google One</option>
                      <option value="code_assist">GCP Code Assist</option>
                      <option value="ai_studio">Custom AI Studio OAuth Client</option>
                    </select>
                  </label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Gemini Tier
                    <select v-model="form.gemini_tier" class="input normal-case">
                      <option value="google_one_free">Google One Free</option>
                      <option value="google_ai_pro">Google AI Pro</option>
                      <option value="google_ai_ultra">Google AI Ultra</option>
                      <option value="gcp_standard">GCP Standard</option>
                      <option value="gcp_enterprise">GCP Enterprise</option>
                      <option value="aistudio_free">AI Studio Free</option>
                      <option value="aistudio_paid">AI Studio Paid</option>
                    </select>
                  </label>
                </div>

                <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
                  <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                    <div>
                      <h4 class="text-sm font-bold">Model Restriction / Mapping</h4>
                      <p class="text-xs text-gray-500 dark:text-gray-400">Preserved in JSON metadata for later routing compatibility decisions.</p>
                    </div>
                    <select v-model="form.model_mode" class="input max-w-xs normal-case">
                      <option value="whitelist">Whitelist</option>
                      <option value="mapping">Mapping</option>
                    </select>
                  </div>
                  <div v-if="form.model_mode === 'whitelist'" class="mt-3 grid gap-2">
                    <div class="rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900/40">
                      <div class="grid grid-cols-2 gap-1.5 sm:grid-cols-3 lg:grid-cols-4">
                        <button
                          v-for="model in allowedModelList"
                          :key="model"
                          type="button"
                          class="inline-flex items-center justify-between gap-1 rounded bg-gray-100 px-2 py-1 text-xs text-gray-700 dark:bg-dark-600 dark:text-gray-300"
                          @click="toggleAllowedModel(model)"
                        >
                          <span class="truncate">{{ model }}</span>
                          <span class="text-gray-400">×</span>
                        </button>
                      </div>
                      <p v-if="allowedModelList.length === 0" class="text-sm text-gray-500 dark:text-gray-400">No whitelist models selected. Empty means this account can support all models.</p>
                      <div class="mt-2 flex items-center justify-between border-t border-gray-200 pt-2 dark:border-dark-600">
                        <span class="text-xs text-gray-400">{{ allowedModelList.length }} models</span>
                        <span class="text-xs text-gray-400">Model Whitelist</span>
                      </div>
                    </div>
                    <input v-model="modelSearchQuery" class="input normal-case" placeholder="Search supported models" />
                    <div class="max-h-52 overflow-auto rounded-lg border border-gray-200 dark:border-dark-700">
                      <button
                        v-for="model in filteredModelOptions"
                        :key="model.value"
                        type="button"
                        class="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-gray-100 dark:hover:bg-dark-600"
                        @click="toggleAllowedModel(model.value)"
                      >
                        <span :class="['flex h-4 w-4 shrink-0 items-center justify-center rounded border', allowedModelList.includes(model.value) ? 'border-primary-500 bg-primary-500 text-white' : 'border-gray-300 dark:border-dark-500']">
                          <span v-if="allowedModelList.includes(model.value)" class="text-[10px] leading-none">✓</span>
                        </span>
                        <span class="truncate text-gray-900 dark:text-white">{{ model.label }}</span>
                      </button>
                      <div v-if="filteredModelOptions.length === 0" class="px-3 py-4 text-center text-sm text-gray-500 dark:text-gray-400">No matching models</div>
                    </div>
                    <div class="flex flex-wrap gap-2">
                      <button class="rounded-lg border border-blue-200 px-3 py-1.5 text-sm text-blue-600 hover:bg-blue-50 dark:border-blue-800 dark:text-blue-400 dark:hover:bg-blue-900/30" type="button" @click="syncPlatformModels">Sync latest supported models</button>
                      <button class="rounded-lg border border-red-200 px-3 py-1.5 text-sm text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-900/30" type="button" @click="clearAllowedModels">Clear all models</button>
                    </div>
                    <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Custom Model Name
                      <span class="flex gap-2">
                        <input v-model="customModelInput" class="input normal-case" placeholder="enter custom model name" @keydown.enter.prevent="handleCustomModelEnter" @compositionstart="isCustomModelComposing = true" @compositionend="isCustomModelComposing = false" />
                        <button class="btn btn-secondary shrink-0 px-3 py-2" type="button" @click="addCustomModel">Add</button>
                      </span>
                    </label>
                    <textarea v-model="form.allowed_models" class="input min-h-20 font-mono normal-case" placeholder="Selected whitelist models, comma separated; empty means all models"></textarea>
                    <p class="text-xs text-gray-500 dark:text-gray-400">Selected {{ allowedModelList.length }} model(s)<span v-if="allowedModelList.length === 0"> (supports all models)</span></p>
                  </div>
                  <div v-else class="mt-3 grid gap-2">
                    <div class="rounded-lg bg-purple-50 p-3 dark:bg-purple-900/20">
                      <p class="text-xs text-purple-700 dark:text-purple-400">Map request models to actual models. Left is the requested model, right is the actual model sent to API.</p>
                    </div>
                    <div v-for="(mapping, index) in form.model_mappings" :key="index" class="grid gap-2 sm:grid-cols-[1fr_auto_1fr_auto] sm:items-center">
                      <input v-model="mapping.from" class="input normal-case" placeholder="request model" />
                      <span class="text-center text-gray-400">→</span>
                      <input v-model="mapping.to" class="input normal-case" placeholder="actual model" />
                      <button class="btn btn-danger px-3 py-1.5" type="button" @click="form.model_mappings.splice(index, 1)">Remove</button>
                    </div>
                    <button class="btn btn-secondary" type="button" @click="addModelMapping('main')">Add Model Mapping</button>
                  </div>
                </div>

                <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                  <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.pool_mode" type="checkbox" /> Pool mode</label>
                  <label v-if="isClaudeOAuthFlow" class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.temp_unsched_enabled" type="checkbox" /> Temp Unschedulable</label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Pool Retry Count<input v-model.number="form.pool_mode_retry_count" type="number" min="0" class="input" /></label>
                  <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.intercept_warmup_requests" type="checkbox" /> Intercept warmup requests</label>
                  <label v-if="showOpenAIAdvanced" class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.openai_passthrough" type="checkbox" /> OpenAI passthrough</label>
                  <label v-if="showOpenAIAdvanced" class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">OpenAI WS Mode
                    <select v-model="form.openai_ws_mode" class="input normal-case">
                      <option value="off">off</option>
                      <option value="ctx_pool">ctx_pool</option>
                      <option value="passthrough">passthrough</option>
                    </select>
                  </label>
                  <label v-if="showAnthropicAdvanced" class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.anthropic_passthrough" type="checkbox" /> Anthropic API key passthrough</label>
                  <label v-if="showAnthropicAdvanced" class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Web Search Emulation
                    <select v-model="form.web_search_mode" class="input normal-case">
                      <option value="default">default</option>
                      <option value="enabled">enabled</option>
                      <option value="disabled">disabled</option>
                    </select>
                  </label>
                  <label v-if="showOpenAIAdvanced" class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.codex_cli_only" type="checkbox" /> Codex CLI only</label>
                  <label v-if="showAntigravityAdvanced" class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.mixed_scheduling" type="checkbox" /> Mixed scheduling</label>
                  <label v-if="showAntigravityAdvanced" class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.allow_overages" type="checkbox" /> Allow overages</label>
                </div>

                <div v-if="showOpenAIAdvanced" class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
                  <div class="grid gap-3 sm:grid-cols-2">
                    <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Compact Mode
                      <select v-model="form.openai_compact_mode" class="input normal-case">
                        <option value="off">off</option>
                        <option value="auto">auto</option>
                        <option value="force">force</option>
                      </select>
                    </label>
                  </div>
                  <div class="mt-3 grid gap-2">
                    <div v-for="(mapping, index) in form.openai_compact_mappings" :key="index" class="grid gap-2 sm:grid-cols-[1fr_auto_1fr_auto] sm:items-center">
                      <input v-model="mapping.from" class="input normal-case" placeholder="compact request model" />
                      <span class="text-center text-gray-400">→</span>
                      <input v-model="mapping.to" class="input normal-case" placeholder="actual model" />
                      <button class="btn btn-danger px-3 py-1.5" type="button" @click="form.openai_compact_mappings.splice(index, 1)">Remove</button>
                    </div>
                    <button class="btn btn-secondary" type="button" @click="addModelMapping('compact')">Add Compact Mapping</button>
                  </div>
                </div>

                <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Concurrency<input v-model.number="form.concurrency" type="number" min="1" class="input" /></label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Load Factor<input v-model.number="form.load_factor" type="number" min="1" class="input" /></label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Priority<input v-model.number="form.priority" type="number" min="1" class="input" /></label>
                  <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Expires At<input v-model="form.expires_at" type="datetime-local" class="input" /></label>
                  <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.auto_pause_on_expired" type="checkbox" /> Auto pause on expired</label>
                </div>

                <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
                  <div class="mb-3">
                    <h4 class="text-sm font-bold">Quota Control</h4>
                    <p class="text-xs text-gray-500 dark:text-gray-400">Configure cost window, session limits, client affinity and other scheduling controls.</p>
                  </div>
                  <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                    <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.window_cost_enabled" type="checkbox" /> 5h Window Cost Limit</label>
                    <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Window Cost Limit<input v-model.number="form.window_cost_limit" type="number" min="0" class="input" /></label>
                    <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Sticky Reserve<input v-model.number="form.window_cost_sticky_reserve" type="number" min="0" class="input" /></label>
                    <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.session_limit_enabled" type="checkbox" /> Session Count Limit</label>
                    <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Max Sessions<input v-model.number="form.max_sessions" type="number" min="1" class="input" /></label>
                    <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Idle Timeout Minutes<input v-model.number="form.session_idle_timeout_minutes" type="number" min="1" class="input" /></label>
                    <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.rpm_limit_enabled" type="checkbox" /> RPM Limit</label>
                    <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">Base RPM<input v-model.number="form.base_rpm" type="number" min="1" class="input" /></label>
                    <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">RPM Strategy
                      <select v-model="form.rpm_strategy" class="input normal-case">
                        <option value="tiered">tiered</option>
                        <option value="sticky_exempt">sticky_exempt</option>
                      </select>
                    </label>
                    <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">RPM Sticky Buffer<input v-model.number="form.rpm_sticky_buffer" type="number" min="1" class="input" /></label>
                    <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">User Message Rate Control<input v-model="form.user_msg_queue_mode" class="input normal-case" /></label>
                  </div>
                </div>

                <div class="grid gap-4 lg:grid-cols-2">
                  <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
                    <div class="flex items-center justify-between gap-4">
                      <div>
                        <label class="input-label mb-0">TLS Fingerprint Simulation</label>
                        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">Simulate Node.js/Claude Code client TLS fingerprint</p>
                      </div>
                      <button
                        type="button"
                        @click="form.tls_fingerprint_enabled = !form.tls_fingerprint_enabled"
                        :class="[
                          'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
                          form.tls_fingerprint_enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
                        ]"
                      >
                        <span
                          :class="[
                            'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                            form.tls_fingerprint_enabled ? 'translate-x-5' : 'translate-x-0'
                          ]"
                        />
                      </button>
                    </div>
                    <div v-if="form.tls_fingerprint_enabled" class="mt-3">
                      <select v-model="form.tls_fingerprint_profile_id" class="input normal-case">
                        <option value="">Built-in Default</option>
                      </select>
                    </div>
                  </div>

                  <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
                    <div class="flex items-center justify-between gap-4">
                      <div>
                        <label class="input-label mb-0">Session ID Masking</label>
                        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">When enabled, fixes the session ID in metadata.user_id for 15 minutes, making upstream think requests come from the same session</p>
                      </div>
                      <button
                        type="button"
                        @click="form.session_id_masking_enabled = !form.session_id_masking_enabled"
                        :class="[
                          'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
                          form.session_id_masking_enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
                        ]"
                      >
                        <span
                          :class="[
                            'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                            form.session_id_masking_enabled ? 'translate-x-5' : 'translate-x-0'
                          ]"
                        />
                      </button>
                    </div>
                  </div>

                  <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
                    <div class="flex items-center justify-between gap-4">
                      <div>
                        <label class="input-label mb-0">Cache TTL Override</label>
                        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">Force all cache creation tokens to be billed as the selected TTL tier (5m or 1h)</p>
                      </div>
                      <button
                        type="button"
                        @click="form.cache_ttl_override_enabled = !form.cache_ttl_override_enabled"
                        :class="[
                          'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
                          form.cache_ttl_override_enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
                        ]"
                      >
                        <span
                          :class="[
                            'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                            form.cache_ttl_override_enabled ? 'translate-x-5' : 'translate-x-0'
                          ]"
                        />
                      </button>
                    </div>
                    <div v-if="form.cache_ttl_override_enabled" class="mt-3">
                      <label class="input-label text-xs">Target TTL</label>
                      <select v-model="form.cache_ttl_override_target" class="input normal-case">
                        <option value="5m">5m</option>
                        <option value="1h">1h</option>
                      </select>
                      <p class="input-hint">Select the TTL tier for billing</p>
                    </div>
                  </div>

                  <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
                    <div class="flex items-center justify-between gap-4">
                      <div>
                        <label class="input-label mb-0">Custom Relay URL</label>
                        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">Forward requests to a custom relay service. Proxy URL will be passed as a query parameter.</p>
                      </div>
                      <button
                        type="button"
                        @click="form.custom_base_url_enabled = !form.custom_base_url_enabled"
                        :class="[
                          'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
                          form.custom_base_url_enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
                        ]"
                      >
                        <span
                          :class="[
                            'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                            form.custom_base_url_enabled ? 'translate-x-5' : 'translate-x-0'
                          ]"
                        />
                      </button>
                    </div>
                    <div v-if="form.custom_base_url_enabled" class="mt-3">
                      <input v-model="form.custom_base_url" class="input normal-case" placeholder="Relay service URL (e.g., https://relay.example.com)" />
                    </div>
                  </div>
                </div>

                <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.custom_error_codes_enabled" type="checkbox" /> {{ t('admin.accounts.customErrorCodes') }}</label>
                    <div class="flex flex-wrap gap-2">
                      <button
                        v-for="code in commonErrorCodes"
                        :key="code"
                        :class="[
                          'rounded-lg px-3 py-1.5 text-sm font-medium transition-colors',
                          numberList(form.custom_error_codes).includes(code)
                            ? 'bg-red-100 text-red-700 ring-1 ring-red-500 dark:bg-red-900/30 dark:text-red-400'
                            : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-400 dark:hover:bg-dark-500'
                        ]"
                        type="button"
                        @click="toggleNumberCode(code)"
                      >
                        {{ code }}
                      </button>
                    </div>
                  </div>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.customErrorCodesHint') }}</p>
                  <input v-model="form.custom_error_codes" class="input mt-3 normal-case" :placeholder="t('admin.accounts.enterErrorCode')" />
                  <div class="mt-2 flex flex-wrap gap-1.5">
                    <span v-for="code in numberList(form.custom_error_codes).sort((a, b) => a - b)" :key="code" class="inline-flex items-center gap-1 rounded-full bg-red-100 px-2.5 py-0.5 text-sm font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400">{{ code }}</span>
                    <span v-if="numberList(form.custom_error_codes).length === 0" class="text-xs text-gray-400">{{ t('admin.accounts.noneSelectedUsesDefault') }}</span>
                  </div>
                </div>

                <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <label class="flex items-center gap-2 text-sm font-semibold"><input v-model="form.temp_unsched_enabled" type="checkbox" /> {{ t('admin.accounts.tempUnschedulable.title') }}</label>
                      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.tempUnschedulable.hint') }}</p>
                    </div>
                    <button class="btn btn-secondary px-3 py-1.5" type="button" @click="addTempUnschedRule()">{{ t('admin.accounts.tempUnschedulable.addRule') }}</button>
                  </div>
                  <div v-if="form.temp_unsched_enabled" class="mt-3 rounded-lg bg-blue-50 p-3 dark:bg-blue-900/20">
                    <p class="text-xs text-blue-700 dark:text-blue-400">{{ t('admin.accounts.tempUnschedulable.notice') }}</p>
                  </div>
                  <div v-if="form.temp_unsched_enabled" class="mt-3 flex flex-wrap gap-2">
                    <button type="button" class="rounded-lg bg-gray-100 px-3 py-1.5 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500" @click="addTempUnschedPreset('overload')">+ overload</button>
                    <button type="button" class="rounded-lg bg-gray-100 px-3 py-1.5 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500" @click="addTempUnschedPreset('rate_limit')">+ rate limit</button>
                    <button type="button" class="rounded-lg bg-gray-100 px-3 py-1.5 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500" @click="addTempUnschedPreset('unavailable')">+ unavailable</button>
                  </div>
                  <div class="mt-3 grid gap-3">
                    <div v-for="(rule, index) in form.temp_unsched_rules" :key="index" class="grid gap-2 rounded-xl border border-gray-100 p-3 dark:border-dark-800 sm:grid-cols-2 lg:grid-cols-[120px_160px_1fr_1fr_auto]">
                      <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">{{ t('admin.accounts.tempUnschedulable.errorCode') }}<input v-model.number="rule.error_code" class="input" type="number" min="100" max="599" placeholder="429" /></label>
                      <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">{{ t('admin.accounts.tempUnschedulable.durationMinutes') }}<input v-model.number="rule.duration_minutes" class="input" type="number" min="1" placeholder="minutes" /></label>
                      <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">{{ t('admin.accounts.tempUnschedulable.keywords') }}<input v-model="rule.keywords" class="input normal-case" placeholder="keywords" /></label>
                      <label class="grid gap-1 text-xs font-semibold uppercase tracking-wider text-gray-500">{{ t('admin.accounts.tempUnschedulable.description') }}<input v-model="rule.description" class="input normal-case" placeholder="description" /></label>
                      <button class="btn btn-danger px-3 py-1.5" type="button" @click="form.temp_unsched_rules.splice(index, 1)">Remove</button>
                    </div>
                  </div>
                </div>

                <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <h4 class="text-sm font-bold">Groups ({{ selectedGroupIDs.length }} selected)</h4>
                      <p class="text-xs text-gray-500 dark:text-gray-400">Bind this account to existing routing groups. Selected group IDs are persisted in metadata and mirrored into account tags for local group routing.</p>
                    </div>
                    <span class="badge badge-primary">{{ form.platform }}</span>
                  </div>
                  <div class="mt-3 grid max-h-40 gap-2 overflow-auto sm:grid-cols-2">
                    <label
                      v-for="group in filteredGroupsForPlatform"
                      :key="group.id"
                      :class="['cursor-pointer rounded-lg border-2 p-3 transition', selectedGroupIDs.includes(group.id) ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-200' : 'border-gray-200 hover:border-primary-300 dark:border-dark-700 dark:hover:border-primary-700']"
                    >
                      <input :checked="selectedGroupIDs.includes(group.id)" type="checkbox" class="mr-2" @change="toggleGroupID(group.id)" />
                      <span class="font-semibold">{{ groupLabel(group) }}</span>
                      <span class="ml-2 text-xs text-gray-500 dark:text-gray-400">{{ group.platform }} · {{ group.id }}</span>
                    </label>
                    <p v-if="filteredGroupsForPlatform.length === 0" class="text-sm text-gray-500 dark:text-gray-400 sm:col-span-2">No matching groups for this platform. Create groups in Group Management first.</p>
                  </div>
                  <input v-model="form.group_ids" class="input mt-3 font-mono normal-case" placeholder="group IDs, comma separated" />
                </div>
              </div>
            </section>
            <button v-else class="btn btn-secondary" type="button" @click="showAdvancedAccountOptions = true">Show Advanced Create Account Options</button>
          </div>
          <div class="mt-4 flex flex-wrap justify-end gap-2">
            <button v-if="isOAuthFlow && step === 2" class="btn btn-secondary" type="button" @click="goBackToAccountSetup">Back</button>
            <button class="btn btn-secondary" type="button" @click="editorOpen = false">Cancel</button>
            <button class="btn btn-primary" type="button" :disabled="isOAuthFlow && step === 1 ? false : !canSubmit" @click="handlePrimaryAccountAction">
              {{ isOAuthFlow && step === 1 ? 'Next' : 'Save Account' }}
            </button>
          </div>
        </section>
      </div>

      <div v-if="importOpen" class="xforce-modal-backdrop fixed inset-0 z-50 grid place-items-center">
        <section class="card xforce-modal-panel max-h-[92vh] w-full max-w-3xl overflow-auto">
          <div class="flex items-center justify-between gap-3">
            <h2 class="text-lg font-black">Import Accounts</h2>
            <button class="btn btn-secondary" type="button" @click="importOpen = false">Close</button>
          </div>
          <div class="mt-4 grid gap-3">
            <select v-model="importKind" class="input">
              <option value="line_tokens">Line tokens</option>
              <option value="json_bundle">sub2api JSON bundle</option>
              <option value="inline_bundle">Inline JSON bundle</option>
            </select>
            <textarea v-model="importContent" class="input min-h-56 font-mono" placeholder="Paste one token per line or a sub2api JSON bundle" />
          </div>
          <div class="mt-4 flex flex-wrap justify-end gap-2">
            <button class="btn btn-secondary" type="button" @click="preview">Preview Import</button>
            <button class="btn btn-primary" type="button" @click="apply">Apply Import</button>
          </div>
        </section>
      </div>

      <div v-if="showGeminiHelpDialog" class="xforce-modal-backdrop fixed inset-0 z-50 grid place-items-center">
        <section class="card xforce-modal-panel max-h-[92vh] w-full max-w-3xl overflow-auto">
          <div class="flex items-center justify-between gap-3">
            <h2 class="text-lg font-black">Gemini Help</h2>
            <button class="btn btn-secondary" type="button" @click="showGeminiHelpDialog = false">Close</button>
          </div>
          <div class="mt-4 space-y-3 text-sm text-gray-600 dark:text-gray-300">
            <p>Google One, GCP Code Assist, and AI Studio OAuth variants are preserved here as upstream-compatible create flows.</p>
            <p>Use the help options to guide project activation, OAuth client setup, and tier selection before saving the account into local JSON.</p>
          </div>
          <div class="mt-4 flex justify-end">
            <button @click="showGeminiHelpDialog = false" type="button" class="btn btn-primary">Close</button>
          </div>
        </section>
      </div>
    </main>
  </AppShell>
</template>
