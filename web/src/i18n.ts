/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 1.1.0
 */
import { createI18n } from 'vue-i18n'
import type {
  LocationQueryRaw,
  RouteLocationNormalizedLoaded,
  RouteLocationRaw,
  Router,
} from 'vue-router'

import { enUS, type MessageSchema } from './locales/en-US'
import { zhCN } from './locales/zh-CN'

export const SUPPORTED_LOCALES = ['zh-CN', 'en-US'] as const
export const LOCALE_STORAGE_KEY = 'nginx-uix.locale'
export const DEFAULT_LOCALE: SupportedLocale = 'en-US'

export type SupportedLocale = (typeof SUPPORTED_LOCALES)[number]

interface LocaleResolutionOptions {
  urlLanguage?: unknown
  storage?: Pick<Storage, 'getItem'> | null
  browserLanguages?: readonly string[]
}

export function parseLocale(value: unknown): SupportedLocale | null {
  if (typeof value !== 'string') return null
  return SUPPORTED_LOCALES.find((locale) => locale === value) ?? null
}

export function detectBrowserLocale(languages: readonly string[]): SupportedLocale {
  for (const language of languages) {
    const normalized = language.toLowerCase()
    if (normalized === 'zh' || normalized.startsWith('zh-')) {
      return 'zh-CN'
    }
    if (normalized === 'en' || normalized.startsWith('en-')) return 'en-US'
  }
  return DEFAULT_LOCALE
}

export function resolveLocale(options: LocaleResolutionOptions = {}): SupportedLocale {
  const urlLocale = parseLocale(options.urlLanguage)
  if (urlLocale !== null) return urlLocale

  if (options.storage !== null) {
    try {
      const storedLocale = parseLocale(options.storage?.getItem(LOCALE_STORAGE_KEY))
      if (storedLocale !== null) return storedLocale
    } catch {
      // Browser privacy settings can make localStorage unavailable.
    }
  }

  return detectBrowserLocale(options.browserLanguages ?? [])
}

export function persistLocale(
  locale: SupportedLocale,
  storage: Pick<Storage, 'setItem'> | null = runtimeStorage(),
): void {
  try {
    storage?.setItem(LOCALE_STORAGE_KEY, locale)
  } catch {
    // Language switching must keep working when storage is unavailable.
  }
}

function runtimeStorage(): Storage | null {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage
  } catch {
    return null
  }
}

function runtimeLanguages(): readonly string[] {
  if (typeof navigator === 'undefined') return []
  return navigator.languages.length > 0 ? navigator.languages : [navigator.language]
}

function runtimeURLLanguage(): string | null {
  if (typeof window === 'undefined') return null
  return new URL(window.location.href).searchParams.get('lang')
}

export function resolveRuntimeLocale(): SupportedLocale {
  return resolveLocale({
    urlLanguage: runtimeURLLanguage(),
    storage: runtimeStorage(),
    browserLanguages: runtimeLanguages(),
  })
}

export function createAppI18n(locale: SupportedLocale = resolveRuntimeLocale()) {
  return createI18n<[MessageSchema], SupportedLocale, false>({
    legacy: false,
    locale,
    fallbackLocale: DEFAULT_LOCALE,
    messages: {
      'en-US': enUS,
      'zh-CN': zhCN,
    },
    datetimeFormats: {
      'en-US': {
        short: {
          year: 'numeric',
          month: 'short',
          day: 'numeric',
          hour: 'numeric',
          minute: '2-digit',
          timeZone: 'UTC',
        },
      },
      'zh-CN': {
        short: {
          year: 'numeric',
          month: 'short',
          day: 'numeric',
          hour: '2-digit',
          minute: '2-digit',
          hour12: false,
          timeZone: 'UTC',
        },
      },
    },
    numberFormats: {
      'en-US': {
        decimal: { maximumFractionDigits: 2 },
      },
      'zh-CN': {
        decimal: { maximumFractionDigits: 2 },
      },
    },
  })
}

export const appI18n = createAppI18n()

export type AppI18n = ReturnType<typeof createAppI18n>

export function applyLocale(
  locale: SupportedLocale,
  i18n: AppI18n = appI18n,
  storage: Pick<Storage, 'setItem'> | null = runtimeStorage(),
): void {
  i18n.global.locale.value = locale
  persistLocale(locale, storage)
  if (typeof document !== 'undefined') {
    document.documentElement.lang = locale
    document.title = i18n.global.t('app.title')
  }
}

export function installLocaleRouting(
  router: Router,
  i18n: AppI18n = appI18n,
  storage: Pick<Storage, 'setItem'> | null = runtimeStorage(),
): () => void {
  return router.beforeEach((to) => {
    const requestedLocale = parseLocale(to.query.lang)
    if (requestedLocale !== null) {
      applyLocale(requestedLocale, i18n, storage)
      return true
    }

    return {
      path: to.path,
      query: { ...to.query, lang: i18n.global.locale.value },
      hash: to.hash,
      replace: true,
    }
  })
}

export function switchLocale(
  router: Router,
  route: RouteLocationNormalizedLoaded,
  locale: SupportedLocale,
): ReturnType<Router['replace']> {
  const query: LocationQueryRaw = { ...route.query, lang: locale }
  const redirect = query.redirect
  if (typeof redirect === 'string') {
    const resolvedRedirect = router.resolve(redirect)
    query.redirect = router.resolve({
      path: resolvedRedirect.path,
      query: { ...resolvedRedirect.query, lang: locale },
      hash: resolvedRedirect.hash,
    }).fullPath
  }

  const target: RouteLocationRaw = {
    path: route.path,
    query,
    hash: route.hash,
  }
  return router.replace(target)
}
