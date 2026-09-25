import { defineConfig, devices } from '@playwright/test'

/**
 * The browser suite drives the real embedded binary over loopback. Nothing is
 * stubbed: the server under test is `bin/traceboard` started against a
 * throwaway config and database, so the suite exercises the same assets and the
 * same routes an operator uses.
 */
export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global-setup.ts',
  globalTeardown: './e2e/global-setup.ts',
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['list']] : [['list']],
  timeout: 60_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: process.env.TRACEBOARD_E2E_URL ?? 'http://127.0.0.1:47821',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
