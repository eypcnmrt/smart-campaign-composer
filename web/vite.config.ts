import { fileURLToPath, URL } from 'node:url'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

// Settings come from this file and from GET /api/config, never from .env files (envDir is off).
// API_PROXY_TARGET is a process variable that only the dev container sets (scripts/web.sh dev), because from inside a
// container the host's API is not at localhost.
const apiTarget = process.env.API_PROXY_TARGET ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [vue()],
  envDir: false,
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    strictPort: true,
    // The dev server forwards /api (including the event stream) to the Go API.
    proxy: { '/api': { target: apiTarget } },
  },
  build: {
    target: 'es2023',
    // The nginx policy allows fonts from 'self' only, so fonts must stay files and never become data: URLs.
    assetsInlineLimit: 0,
  },
  test: {
    environment: 'happy-dom',
    include: ['src/**/*.test.ts'],
    restoreMocks: true,
  },
})
