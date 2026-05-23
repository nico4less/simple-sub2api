import { createI18n } from 'vue-i18n'

import en from './locales/en.json'
import zh from './locales/zh.json'

export type SupportedLocale = 'en' | 'zh'

export const LANGUAGE_STORAGE_KEY = 's2a_language'

export function normalizeLocale(value?: string | null): SupportedLocale {
  const normalized = String(value || '').trim().toLowerCase().replace('_', '-')
  if (normalized.startsWith('zh')) return 'zh'
  return 'en'
}

function getStoredLocale(): SupportedLocale | null {
  try {
    const stored = localStorage.getItem(LANGUAGE_STORAGE_KEY)
    if (stored === 'en' || stored === 'zh' || stored === 'zh-CN') return normalizeLocale(stored)
  } catch {
    return null
  }
  return null
}

function getBrowserLocale(): SupportedLocale {
  const candidates = [navigator.language, ...(navigator.languages || [])]
  return normalizeLocale(candidates.find(Boolean))
}

export const i18n = createI18n({
  legacy: false,
  locale: getStoredLocale() || getBrowserLocale(),
  fallbackLocale: 'en',
  messages: {
    en,
    zh,
    'zh-CN': zh
  }
})

export function setLocale(locale: SupportedLocale) {
  const next = normalizeLocale(locale)
  i18n.global.locale.value = next
  try {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, next)
  } catch {
    // Ignore storage failures in privacy-restricted browsers.
  }
}

export default i18n
