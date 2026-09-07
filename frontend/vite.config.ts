import { defineConfig } from 'vitest/config'
import { loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

const coverageMinimum = 80

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', 'DEV_API_PROXY_TARGET')
  const target = env.DEV_API_PROXY_TARGET?.trim() || 'http://localhost:8080'
  const url = new URL(target)
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.pathname !== '/' || url.search || url.hash) {
    throw new Error('DEV_API_PROXY_TARGET must be an HTTP(S) origin without credentials, a path, query, or fragment.')
  }
  return {
    plugins: [react()],
    server: {
      port: 5173,
      proxy: {
        '/v1': {
          target: url.origin,
          changeOrigin: true,
        },
      },
    },
    test: {
      environment: 'jsdom',
      setupFiles: './src/test/setup.ts',
      coverage: {
        provider: 'v8',
        reporter: ['text', 'lcov', 'json-summary'],
        reportsDirectory: './coverage',
        thresholds: {
          statements: coverageMinimum,
          branches: coverageMinimum,
          functions: coverageMinimum,
          lines: coverageMinimum,
        },
      },
    },
  }
})
