// Checks development proxy defaults, origin validation, env-file loading, and separation
// from browser-exposed settings.

import { afterEach, expect, it, vi } from 'vitest'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { loadEnv } from 'vite'
import config from './vite.config'

afterEach(() => vi.unstubAllEnvs())

async function proxyTarget() {
  const resolved = typeof config === 'function' ? await config({ mode: 'test', command: 'serve' }) : await config
  const proxy = resolved.server?.proxy?.['/v1']
  return typeof proxy === 'object' ? proxy.target : proxy
}

it('uses the default origin and accepts a backend on another port', async () => {
  vi.stubEnv('DEV_API_PROXY_TARGET', '')
  expect(await proxyTarget()).toBe('http://localhost:8080')
  vi.stubEnv('DEV_API_PROXY_TARGET', ' http://127.0.0.1:9080/ ')
  expect(await proxyTarget()).toBe('http://127.0.0.1:9080')
})

it.each(['file:///tmp/api', 'http://user:password@localhost:8080', 'https://example.test/api', 'https://example.test/?token=x', 'https://example.test/#api'])('rejects invalid proxy origin %s', async (target) => {
  vi.stubEnv('DEV_API_PROXY_TARGET', target)
  await expect(proxyTarget()).rejects.toThrow('DEV_API_PROXY_TARGET')
})

it('accepts an empty or origin-only API URL and rejects one the image policy cannot allow', async () => {
  vi.stubEnv('DEV_API_PROXY_TARGET', '')
  for (const api of ['', 'https://api.example.test', 'https://api.example.test/']) {
    vi.stubEnv('VITE_API_URL', api)
    expect(await proxyTarget()).toBe('http://localhost:8080')
  }
  vi.stubEnv('VITE_API_URL', 'https://api.example.test/base')
  await expect(proxyTarget()).rejects.toThrow('VITE_API_URL')
})

it('loads the proxy setting from env files without exposing it as a VITE variable', () => {
  const directory = mkdtempSync(join(tmpdir(), 'battlefield-vite-env-'))
  try {
    vi.stubEnv('DEV_API_PROXY_TARGET', undefined)
    writeFileSync(join(directory, '.env.test'), 'DEV_API_PROXY_TARGET=http://localhost:9080\n')
    expect(loadEnv('test', directory, 'DEV_API_PROXY_TARGET').DEV_API_PROXY_TARGET).toBe('http://localhost:9080')
    expect(loadEnv('test', directory).DEV_API_PROXY_TARGET).toBeUndefined()
  } finally { rmSync(directory, { recursive: true, force: true }) }
})
