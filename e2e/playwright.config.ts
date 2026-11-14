import { defineConfig, devices } from '@playwright/test'

// The browser tests run inside the project's own network (docker-compose.e2e.yml): the app is `web`, reached by its service
// name. One worker and no retries: the scenarios share one database and a retry would hide a flaw that a person would meet.
export default defineConfig({
  testDir: './tests',
  globalSetup: './support/global-setup.ts', // closes an outage a killed run left on
  forbidOnly: true, // a leftover test.only must fail the run, not shrink it
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 10_000 },
  reporter: [['list']],
  // The checkout is mounted read-only: results go to a place that can be written.
  outputDir: '/tmp/pw-results',
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://web:8080',
    locale: 'tr-TR',
    timezoneId: 'Europe/Istanbul',
    reducedMotion: 'reduce',
    trace: 'off',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 900 } } }],
})
