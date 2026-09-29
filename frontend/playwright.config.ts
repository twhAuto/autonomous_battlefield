import { defineConfig, devices } from '@playwright/test'

// Concurrent CI builds on one agent pass a free E2E_PORT; a fixed port would make the
// second build's strict dev server fail to start.
const port = Number(process.env.E2E_PORT ?? 4173)
const baseURL = `http://127.0.0.1:${port}`
export default defineConfig({
  testDir: './e2e', fullyParallel: true, forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0, workers: process.env.CI ? 1 : 2,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: { baseURL, screenshot: 'only-on-failure', trace: 'retain-on-failure' },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
    { name: 'mobile-chromium', use: { ...devices['Pixel 7'], viewport: { width: 390, height: 844 } } },
  ],
  webServer: { command: `npm run dev -- --host 127.0.0.1 --port ${port} --strictPort`, url: baseURL, reuseExistingServer: !process.env.CI },
})
