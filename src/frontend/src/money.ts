import i18n from './i18n'

// The API speaks integer cents; this is the only place that converts to and from dollars.
export function formatCents(cents: number): string {
  const locale = i18n.language === 'fr' ? 'fr-CA' : 'en-CA'
  return new Intl.NumberFormat(locale, { style: 'currency', currency: 'CAD' }).format(cents / 100)
}

export function toCents(dollars: number): number {
  return Math.round(dollars * 100)
}

// Local calendar day as YYYY-MM-DD (the browser and family time zones are the same).
export function today(): string {
  const d = new Date()
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}
