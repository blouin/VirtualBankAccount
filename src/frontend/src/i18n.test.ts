import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import i18n, { detectInitialLanguage, getCookie, setLang, LANG_KEY } from './i18n'

describe('i18n language detection and persistence', () => {
  beforeEach(() => {
    localStorage.clear()
    document.cookie = `${LANG_KEY}=; expires=Thu, 01 Jan 1970 00:00:00 UTC; path=/;`
    window.history.pushState(null, '', '/')
  })

  afterEach(() => {
    window.history.pushState(null, '', '/')
    vi.restoreAllMocks()
  })

  it('detects language from URL query string ?lng=fr', () => {
    window.history.pushState(null, '', '/?lng=fr')
    expect(detectInitialLanguage()).toBe('fr')
  })

  it('detects language from URL query string ?lng=en', () => {
    window.history.pushState(null, '', '/?lng=en')
    expect(detectInitialLanguage()).toBe('en')
  })

  it('detects language from URL query string ?lang=fr as fallback query key', () => {
    window.history.pushState(null, '', '/?lang=fr')
    expect(detectInitialLanguage()).toBe('fr')
  })

  it('prioritizes URL parameter over localStorage', () => {
    localStorage.setItem(LANG_KEY, 'en')
    window.history.pushState(null, '', '/?lng=fr')
    expect(detectInitialLanguage()).toBe('fr')
  })

  it('detects language from localStorage when URL has no query parameter', () => {
    localStorage.setItem(LANG_KEY, 'fr')
    expect(detectInitialLanguage()).toBe('fr')
  })

  it('detects language from cookie when neither URL nor localStorage is set', () => {
    document.cookie = `${LANG_KEY}=fr; path=/;`
    expect(detectInitialLanguage()).toBe('fr')
  })

  it('falls back to navigator.language when no URL, localStorage or cookie is set', () => {
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('fr-CA')
    expect(detectInitialLanguage()).toBe('fr')
  })

  it('falls back to "en" when navigator.language is not French', () => {
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-US')
    expect(detectInitialLanguage()).toBe('en')
  })

  it('setLang updates localStorage, cookie, document lang, URL, and i18n instance', () => {
    setLang('fr')
    expect(localStorage.getItem(LANG_KEY)).toBe('fr')
    expect(getCookie(LANG_KEY)).toBe('fr')
    expect(document.documentElement.lang).toBe('fr')
    expect(window.location.search).toContain('lng=fr')
    expect(i18n.language).toBe('fr')

    setLang('en')
    expect(localStorage.getItem(LANG_KEY)).toBe('en')
    expect(getCookie(LANG_KEY)).toBe('en')
    expect(document.documentElement.lang).toBe('en')
    expect(window.location.search).toContain('lng=en')
    expect(i18n.language).toBe('en')
  })
})
