import { defineConfig, devices } from '@playwright/test';

/**
 * Live site-database checks for a disposable daemon with dashboard auth
 * disabled. The spec is skipped unless DEVSTACK_E2E_LIVE=1. It never starts,
 * seeds, or mutates the daemon; it only reads Site Detail and the Add Site
 * review. Point DEVSTACK_UI_URL at the daemon under test.
 */
const BASE_URL = process.env.DEVSTACK_UI_URL?.trim() || 'http://127.0.0.1:27170';

export default defineConfig({
  testDir: './tests/live',
  testMatch: 'site-databases.live.spec.ts',
  fullyParallel: false,
  workers: 1,
  forbidOnly: true,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report-live-site' }]],
  use: {
    baseURL: BASE_URL,
    // Never persist a revealed password in a trace, screenshot, or video.
    trace: 'off',
    screenshot: 'off',
    video: 'off',
    ignoreHTTPSErrors: true,
  },
  projects: [
    {
      name: 'live-site-chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
