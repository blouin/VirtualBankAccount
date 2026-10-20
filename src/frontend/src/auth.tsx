import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api, loadSession, saveSession, setUnauthorizedHandler, type Session } from './api'

type Auth = { session: Session | null; start: (s: Session) => void; end: (callApi?: boolean) => Promise<void> }
const Ctx = createContext<Auth>(null as never)
export const useAuth = () => useContext(Ctx)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null>(loadSession)
  const qc = useQueryClient()

  const value = useMemo<Auth>(() => {
    const clear = () => {
      saveSession(null)
      setSession(null)
      qc.clear()
    }
    return {
      session,
      start: (s) => {
        saveSession(s)
        setSession(s)
      },
      end: async (callApi = true) => {
        if (callApi) await api('POST', '/auth/logoff').catch(() => {})
        clear()
      },
    }
  }, [session, qc])

  useEffect(() => {
    setUnauthorizedHandler(() => value.end(false))
  }, [value])

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}
