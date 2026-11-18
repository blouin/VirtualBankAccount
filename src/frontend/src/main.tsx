import '@mantine/core/styles.css'
import './i18n'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MantineProvider } from '@mantine/core'
import { registerSW } from 'virtual:pwa-register'
import App from './App'
import { AuthProvider } from './auth'
import { OfflineProvider } from './offline'

registerSW({ immediate: true })

// Money data is always refetched when the app is reopened; no retries on 4xx.
const client = new QueryClient({
  defaultOptions: { queries: { retry: false, refetchOnWindowFocus: true, staleTime: 0 } },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider defaultColorScheme="dark" theme={{ primaryColor: 'cyan', defaultRadius: 'md' }}>
      <QueryClientProvider client={client}>
        <BrowserRouter>
          <AuthProvider>
            <OfflineProvider>
              <App />
            </OfflineProvider>
          </AuthProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </MantineProvider>
  </StrictMode>,
)
