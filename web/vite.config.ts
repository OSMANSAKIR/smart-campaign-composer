import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

const api = 'http://localhost:8080'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    // Same-origin in production (the Go API serves the SPA); in dev, proxy API calls to it.
    proxy: { '/api': api, '/healthz': api, '/readyz': api },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
})
