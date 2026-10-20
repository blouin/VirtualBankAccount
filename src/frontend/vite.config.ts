import react from '@vitejs/plugin-react'
import { VitePWA } from 'vite-plugin-pwa'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      manifest: {
        name: 'Family Bank',
        short_name: 'Bank',
        description: 'Virtual family bank accounts',
        theme_color: '#242424',
        background_color: '#242424',
        display: 'standalone',
        start_url: '/',
        icons: [
          { src: 'pwa-192.png', sizes: '192x192', type: 'image/png' },
          { src: 'pwa-512.png', sizes: '512x512', type: 'image/png', purpose: 'any maskable' },
        ],
      },
      // Only the app shell is cached: money data must never be served stale from a cache.
      workbox: { navigateFallback: '/index.html', navigateFallbackDenylist: [/^\/api\//] },
    }),
  ],
  // Same-origin like CloudFront in production, so there is no CORS in dev either.
  server: { proxy: { '/api': { target: 'http://127.0.0.1:8080' } } },
  // Threads: forked workers time out on start behind some antivirus setups.
  test: { environment: 'jsdom', globals: true, setupFiles: ['./src/test/setup.ts'], css: false, pool: 'threads', testTimeout: 20000, hookTimeout: 20000 },
})
