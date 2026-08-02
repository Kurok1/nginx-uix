/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 1.1.0
 */
import {
  LOCALE_STORAGE_KEY,
  createAppI18n,
  detectBrowserLocale,
  installLocaleRouting,
  parseLocale,
  persistLocale,
  resolveLocale,
  switchLocale,
} from './i18n'
import { createMemoryHistory, createRouter } from 'vue-router'

function storageWith(value: string | null): Pick<Storage, 'getItem' | 'setItem'> {
  return {
    getItem: vi.fn(() => value),
    setItem: vi.fn(),
  }
}

describe('locale resolution', () => {
  it('accepts only the two canonical URL and persisted locale values', () => {
    expect(parseLocale('zh-CN')).toBe('zh-CN')
    expect(parseLocale('en-US')).toBe('en-US')
    expect(parseLocale('zh')).toBeNull()
    expect(parseLocale('en-us')).toBeNull()
    expect(parseLocale(['zh-CN'])).toBeNull()
    expect(parseLocale(undefined)).toBeNull()
  })

  it('gives a valid URL locale priority over storage and browser preferences', () => {
    const storage = storageWith('zh-CN')

    expect(resolveLocale({
      urlLanguage: 'en-US',
      storage,
      browserLanguages: ['zh-CN'],
    })).toBe('en-US')
    expect(storage.getItem).not.toHaveBeenCalled()
  })

  it('uses a valid persisted locale when the URL locale is absent or invalid', () => {
    expect(resolveLocale({
      urlLanguage: 'fr-FR',
      storage: storageWith('zh-CN'),
      browserLanguages: ['en-US'],
    })).toBe('zh-CN')
  })

  it.each(['zh', 'zh-CN', 'zh-Hans', 'zh-TW'])('maps browser locale %s to zh-CN', (language) => {
    expect(detectBrowserLocale([language])).toBe('zh-CN')
  })

  it('falls back to en-US for unsupported browser languages', () => {
    expect(detectBrowserLocale(['fr-FR', 'de-DE'])).toBe('en-US')
    expect(detectBrowserLocale([])).toBe('en-US')
  })

  it('honors browser preference order when both supported language families are present', () => {
    expect(detectBrowserLocale(['en-US', 'zh-CN'])).toBe('en-US')
    expect(detectBrowserLocale(['fr-FR', 'zh-CN', 'en-US'])).toBe('zh-CN')
  })

  it('ignores invalid and unavailable storage without preventing startup', () => {
    const throwingStorage: Pick<Storage, 'getItem'> = {
      getItem: vi.fn(() => {
        throw new DOMException('blocked', 'SecurityError')
      }),
    }

    expect(resolveLocale({
      storage: storageWith('not-supported'),
      browserLanguages: ['zh-Hans'],
    })).toBe('zh-CN')
    expect(resolveLocale({
      storage: throwingStorage,
      browserLanguages: ['en-GB'],
    })).toBe('en-US')
  })
})

describe('locale persistence', () => {
  it('stores only the canonical locale preference under the documented key', () => {
    const storage = storageWith(null)

    persistLocale('zh-CN', storage)

    expect(storage.setItem).toHaveBeenCalledOnce()
    expect(storage.setItem).toHaveBeenCalledWith(LOCALE_STORAGE_KEY, 'zh-CN')
  })

  it('does not fail a language switch when storage is unavailable', () => {
    const storage: Pick<Storage, 'setItem'> = {
      setItem: vi.fn(() => {
        throw new DOMException('blocked', 'SecurityError')
      }),
    }

    expect(() => persistLocale('en-US', storage)).not.toThrow()
  })
})

describe('locale routing', () => {
  function testRouter() {
    return createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', name: 'home', component: { template: '<div />' } },
        { path: '/login', name: 'login', component: { template: '<div />' } },
      ],
    })
  }

  beforeEach(() => {
    document.documentElement.lang = ''
    document.title = ''
  })

  it('canonicalizes a route without lang and keeps the locale through later navigation', async () => {
    const router = testRouter()
    const i18n = createAppI18n('zh-CN')
    const storage = storageWith(null)
    installLocaleRouting(router, i18n, storage)

    await router.push('/')
    expect(router.currentRoute.value.fullPath).toBe('/?lang=zh-CN')
    expect(document.documentElement.lang).toBe('zh-CN')

    await router.push('/login?redirect=%2F')
    expect(router.currentRoute.value.query).toEqual({ redirect: '/', lang: 'zh-CN' })
  })

  it('uses and persists a valid URL locale before rendering the route', async () => {
    const router = testRouter()
    const i18n = createAppI18n('zh-CN')
    const storage = storageWith(null)
    installLocaleRouting(router, i18n, storage)

    await router.push('/?lang=en-US')

    expect(i18n.global.locale.value).toBe('en-US')
    expect(document.documentElement.lang).toBe('en-US')
    expect(document.title).toBe('Nginx UIX')
    expect(storage.setItem).toHaveBeenCalledWith(LOCALE_STORAGE_KEY, 'en-US')
  })

  it('replaces an invalid URL locale with the active canonical locale', async () => {
    const router = testRouter()
    const i18n = createAppI18n('en-US')
    installLocaleRouting(router, i18n, storageWith(null))

    await router.push('/?lang=fr-FR')

    expect(router.currentRoute.value.fullPath).toBe('/?lang=en-US')
  })

  it('switches with replace while preserving query, hash and a login redirect locale', async () => {
    const router = testRouter()
    const i18n = createAppI18n('en-US')
    installLocaleRouting(router, i18n, storageWith(null))
    await router.push('/login?lang=en-US&redirect=%2F%3Flang%3Den-US#form')
    const replace = vi.spyOn(router, 'replace')

    await switchLocale(router, router.currentRoute.value, 'zh-CN')

    expect(replace).toHaveBeenCalledOnce()
    expect(router.currentRoute.value.path).toBe('/login')
    expect(router.currentRoute.value.query.lang).toBe('zh-CN')
    expect(router.currentRoute.value.query.redirect).toBe('/?lang=zh-CN')
    expect(router.currentRoute.value.hash).toBe('#form')
    expect(i18n.global.locale.value).toBe('zh-CN')
  })
})

describe('locale-aware formatters', () => {
  it('formats dates through the active locale with deterministic UTC output', () => {
    const value = new Date('2026-01-02T03:04:00Z')
    const english = createAppI18n('en-US').global.d(value, 'short')
    const chinese = createAppI18n('zh-CN').global.d(value, 'short')

    expect(english).toContain('2026')
    expect(chinese).toContain('2026')
    expect(english).not.toBe(chinese)
  })

  it('selects natural English singular and plural duration messages', () => {
    const english = createAppI18n('en-US').global

    expect(english.t('auth.retryIn', { seconds: 1 }, 1)).toBe('Try again in 1 second')
    expect(english.t('auth.retryIn', { seconds: 2 }, 2)).toBe('Try again in 2 seconds')
    expect(english.t('operations.durationDays', { count: 1 }, 1)).toBe('1 day')
    expect(english.t('operations.durationDays', { count: 2 }, 2)).toBe('2 days')
    expect(english.t('workspace.draftChanges', { count: 1 }, 1)).toBe('1 draft change')
    expect(english.t('workspace.draftChanges', { count: 2 }, 2)).toBe('2 draft changes')
    expect(english.t('workspace.tree.members', { count: 1 }, 1)).toBe('1 member')
    expect(english.t('workspace.tree.members', { count: 2 }, 2)).toBe('2 members')
  })
})
