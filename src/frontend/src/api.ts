import type { components as Ops } from './gen/operations'

export type Balance = Ops['schemas']['Balance']
export type Transaction = Ops['schemas']['Transaction']
export type FundingRequest = Ops['schemas']['Request']
export type Page<T> = { items: T[]; page: number; pageSize: number; total: number }
export type Person = { id: number; name: string; isAdmin?: boolean }
export type Session = { token: string; person: Person; familyCode: string }
export type DeviceSession = {
  id: number
  device?: string | null
  createdAt: string
  lastUsedAt?: string | null
  expiresAt: string | null
  current: boolean
}

export class ApiError extends Error {
  constructor(public status: number, message: string, public retryAfter?: number) {
    super(message)
  }
}

const KEY = 'vbank.session'

export function loadSession(): Session | null {
  try {
    return JSON.parse(localStorage.getItem(KEY) ?? 'null')
  } catch {
    return null
  }
}
export function saveSession(s: Session | null) {
  if (s) localStorage.setItem(KEY, JSON.stringify(s))
  else localStorage.removeItem(KEY)
}

let onUnauthorized: () => void = () => {}
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

export async function api<T = void>(
  method: string,
  path: string,
  opts: { body?: unknown; key?: string; auth?: boolean } = {},
): Promise<T> {
  const headers: Record<string, string> = {}
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json'
  if (opts.key) headers['Idempotency-Key'] = opts.key
  const token = loadSession()?.token
  if (token && opts.auth !== false) headers.Authorization = `Bearer ${token}`

  const res = await fetch(`/api/v1${path}`, {
    method,
    headers,
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  const data = text ? JSON.parse(text) : undefined
  if (!res.ok) {
    // A wrong password is also a 401, so only a call that carried a token means "session ended".
    if (res.status === 401 && token && opts.auth !== false) onUnauthorized()
    const retry = Number(res.headers.get('Retry-After')) || undefined
    throw new ApiError(res.status, data?.message ?? res.statusText, retry)
  }
  return data as T
}

export const newKey = () => crypto.randomUUID()
