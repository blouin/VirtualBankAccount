import { describe, it, expect } from 'vitest'
import code from '../../../../iac/spa-rewrite.js?raw'

// Evaluate the exact CloudFront Function code from iac/spa-rewrite.js
const handler: (event: any) => any = new Function(`
  ${code}
  return handler;
`)()

describe('CloudFront spa-rewrite function', () => {
  function makeEvent({
    uri = '/',
    country = '',
    region = '',
    cookie = '',
    querystring = {},
  }: {
    uri?: string
    country?: string
    region?: string
    cookie?: string
    querystring?: Record<string, { value: string }>
  }) {
    const headers: Record<string, { value: string }> = {}
    if (country) headers['cloudfront-viewer-country'] = { value: country }
    if (region) headers['cloudfront-viewer-country-region'] = { value: region }

    const cookies: Record<string, { value: string }> = {}
    if (cookie) cookies['vbank.lang'] = { value: cookie }

    return {
      request: {
        uri,
        headers,
        cookies,
        querystring,
      },
    }
  }

  it('redirects Quebec IP (CA-QC) on root to /?lng=fr and sets vbank.lang cookie', () => {
    const event = makeEvent({ uri: '/', country: 'CA', region: 'QC' })
    const res = handler(event)

    expect(res.statusCode).toBe(302)
    expect(res.headers.location.value).toBe('/?lng=fr')
    expect(res.cookies['vbank.lang']).toEqual({
      value: 'fr',
      attributes: 'Path=/; Max-Age=31536000; SameSite=Lax',
    })
  })

  it('redirects Quebec IP requesting /index.html to /?lng=fr', () => {
    const event = makeEvent({ uri: '/index.html', country: 'CA', region: 'QC' })
    const res = handler(event)

    expect(res.statusCode).toBe(302)
    expect(res.headers.location.value).toBe('/?lng=fr')
  })

  it('preserves existing query parameters when redirecting Quebec IP', () => {
    const event = makeEvent({
      uri: '/',
      country: 'CA',
      region: 'QC',
      querystring: { ref: { value: 'promo' } },
    })
    const res = handler(event)

    expect(res.statusCode).toBe(302)
    expect(res.headers.location.value).toBe('/?ref=promo&lng=fr')
  })

  it('honors existing English cookie preference even for Quebec IP', () => {
    const event = makeEvent({
      uri: '/',
      country: 'CA',
      region: 'QC',
      cookie: 'en',
    })
    const res = handler(event)

    expect(res.statusCode).toBe(302)
    expect(res.headers.location.value).toBe('/?lng=en')
    expect(res.cookies['vbank.lang'].value).toBe('en')
  })

  it('honors existing French cookie preference for non-Quebec IP', () => {
    const event = makeEvent({
      uri: '/',
      country: 'US',
      region: 'NY',
      cookie: 'fr',
    })
    const res = handler(event)

    expect(res.statusCode).toBe(302)
    expect(res.headers.location.value).toBe('/?lng=fr')
    expect(res.cookies['vbank.lang'].value).toBe('fr')
  })

  it('does not redirect non-Quebec IP with no cookie, and rewrites root to /index.html', () => {
    const event = makeEvent({ uri: '/', country: 'CA', region: 'ON' })
    const res = handler(event)

    expect(res.statusCode).toBeUndefined()
    expect(res.uri).toBe('/index.html')
  })

  it('does not redirect USA visitor with no cookie, and rewrites root to /index.html', () => {
    const event = makeEvent({ uri: '/', country: 'US', region: 'CA' })
    const res = handler(event)

    expect(res.statusCode).toBeUndefined()
    expect(res.uri).toBe('/index.html')
  })

  it('does not redirect when ?lng=fr is already in query string', () => {
    const event = makeEvent({
      uri: '/',
      country: 'CA',
      region: 'QC',
      querystring: { lng: { value: 'fr' } },
    })
    const res = handler(event)

    expect(res.statusCode).toBeUndefined()
    expect(res.uri).toBe('/index.html')
  })

  it('rewrites extension-less SPA route (/requests) to /index.html without redirect', () => {
    const event = makeEvent({
      uri: '/requests',
      country: 'CA',
      region: 'QC',
    })
    const res = handler(event)

    expect(res.statusCode).toBeUndefined()
    expect(res.uri).toBe('/index.html')
  })

  it('does not rewrite static assets with extensions (e.g., .js, .css, .ico)', () => {
    const event = makeEvent({
      uri: '/assets/index-abc.js',
      country: 'CA',
      region: 'QC',
    })
    const res = handler(event)

    expect(res.statusCode).toBeUndefined()
    expect(res.uri).toBe('/assets/index-abc.js')
  })
})
