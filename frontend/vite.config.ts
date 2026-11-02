// Configures the React development server, local API proxy, and Vitest coverage gate.
// Production API routing is configured separately in the frontend deployment.
import { defineConfig } from 'vitest/config'
import { loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

const coverageMinimum = 80

// requireOrigin parses value as an HTTP(S) origin, rejecting credentials, a path, a
// query and a fragment, and names the setting in the error.
function requireOrigin(name: string, value: string) {
  const url = new URL(value)
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.pathname !== '/' || url.search || url.hash) {
    throw new Error(`${name} must be an HTTP(S) origin without credentials, a path, query, or fragment.`)
  }
  return url
}

export default defineConfig(({ mode }) => {
  // Read the proxy setting in the Vite process; it is not exposed to browser code.
  const env = loadEnv(mode, '.', ['DEV_API_PROXY_TARGET', 'VITE_API_URL'])
  // Require an origin so the proxy preserves the /v1 request path without a URL prefix.
  const url = requireOrigin('DEV_API_PROXY_TARGET', env.DEV_API_PROXY_TARGET?.trim() || 'http://localhost:8080')
  // The frontend image's Content-Security-Policy allows connections to this origin, so a
  // separate API must be named by its origin alone.
  const api = env.VITE_API_URL?.trim()
  if (api) requireOrigin('VITE_API_URL', api)
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
      include: ['src/**/*.test.{ts,tsx}', '*.test.ts'],
      environment: 'jsdom',
      setupFiles: './src/test/setup.ts',
      // Jenkins shares its agent with parallel Go, fuzz, and security checks.
      // Bound DOM test concurrency so workers do not compete for every CPU.
      maxWorkers: 2,
      // Multi-step DOM interactions need headroom on the shared CI agent.
      testTimeout: 15_000,
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
