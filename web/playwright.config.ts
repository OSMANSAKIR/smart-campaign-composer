import { defineConfig, devices } from '@playwright/test'

// Runs against a running stack (`make e2e` starts it with the mock LLM and runs this in the Playwright image on
// the compose network). E2E_BASE_URL points elsewhere, e.g. http://localhost:8080 for a host run.
export default defineConfig({
  testDir: './e2e',
  timeout: 120_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1, // one shared database and provider: scenarios run one after another
  retries: 0,
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:8080',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
