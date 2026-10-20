import { render } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import type { ReactNode } from 'react'
import { vi } from 'vitest'
import i18n from '../i18n'
import { AuthProvider } from '../auth'
import { saveSession } from '../api'

export type Route = (method: string, path: string, body: unknown) => unknown | Response

export type Call = { method: string; path: string; body: unknown }

// A fake fetch: each call is recorded and answered by `route` (JSON for plain values, 204 for undefined).
export function mockApi(route: Route) {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit = {}) => {
      const path = String(url).replace('/api/v1', '')
      const body = init.body ? JSON.parse(String(init.body)) : undefined
      const method = init.method ?? 'GET'
      calls.push({ method, path, body })
      const out = route(method, path, body)
      if (out instanceof Response) return out
      if (out === undefined) return new Response(null, { status: 204 })
      return new Response(JSON.stringify(out), { status: 200 })
    }),
  )
  return calls
}

export const err = (status: number, message: string, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify({ message }), { status, headers })

export function loggedIn(name = 'Parent 1') {
  saveSession({ token: 't', person: { id: 1, name }, familyCode: 'POC' })
}

export function renderApp(ui: ReactNode, path = '/') {
  void i18n.changeLanguage('en')
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MantineProvider env="test">
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[path]}>
          <AuthProvider>{ui}</AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>
    </MantineProvider>,
  )
}
