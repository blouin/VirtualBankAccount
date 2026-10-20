import { describe, expect, it } from 'vitest'
import i18n from './i18n'
import { formatCents, today, toCents } from './money'

describe('money', () => {
  it('formats cents in English and French', async () => {
    await i18n.changeLanguage('en')
    expect(formatCents(1250)).toBe('$12.50')
    await i18n.changeLanguage('fr')
    expect(formatCents(1250).replace(/\s/g, ' ')).toBe('12,50 $')
  })

  it('converts dollars to whole cents without float drift', () => {
    expect(toCents(0.1 + 0.2)).toBe(30)
    expect(toCents(2.5)).toBe(250)
    expect(toCents(1.005 * 100)).toBe(10050)
  })

  it('gives the local day as YYYY-MM-DD', () => {
    expect(today()).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  })
})
