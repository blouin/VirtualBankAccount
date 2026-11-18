import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'

type OfflineContextType = {
  isOffline: boolean
}

const OfflineContext = createContext<OfflineContextType>({ isOffline: false })

export function OfflineProvider({
  children,
  forcedOffline,
}: {
  children: ReactNode
  forcedOffline?: boolean
}) {
  const [isOffline, setIsOffline] = useState(() => {
    if (typeof forcedOffline === 'boolean') return forcedOffline
    return typeof navigator !== 'undefined' ? !navigator.onLine : false
  })

  useEffect(() => {
    if (typeof forcedOffline === 'boolean') {
      setIsOffline(forcedOffline)
      return
    }

    const onOnline = () => setIsOffline(false)
    const onOffline = () => setIsOffline(true)

    window.addEventListener('online', onOnline)
    window.addEventListener('offline', onOffline)

    return () => {
      window.removeEventListener('online', onOnline)
      window.removeEventListener('offline', onOffline)
    }
  }, [forcedOffline])

  return (
    <OfflineContext.Provider value={{ isOffline }}>
      {children}
    </OfflineContext.Provider>
  )
}

export function useOffline() {
  return useContext(OfflineContext)
}
