import { defineConfig, devices } from '@playwright/test';

/**
 * Live dashboard smoke configuration.
 *
 * This config never starts a daemon and never seeds fixtures. Point it at a
 * daemon under test with DEVSTACK_UI_URL and pass the per-install token in
 * DEVSTACK_E2E_TOKEN. The token is consumed as a URL fragment by the dashboard
 * and is removed from history before any assertion runs.
 *
 * The live spec skips itself unless DEVSTACK_E2E_LIVE=1, so the default mock
 * suite is unaffected and a bare `playwright test -c playwright.live.config.ts`
 * is safe on any machine.
 */
const BASE_URL = process.env.DEVSTACK_UI_URL?.trim() || 'http://127.0.0.1:7070';

export default defineConfig({
  testDir: './tests/live',
  fullyParallel: false,
  workers: 1,
  forbidOnly: true,
  retries: 0,
  timeout: 45_000,
  expect: { timeout: 10_000 },
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report-live' }]],
  use: {
    baseURL: BASE_URL,
    // The install token must never end up in a trace or video artifact.
    trace: 'off',
    screenshot: 'only-on-failure',
    video: 'off',
    ignoreHTTPSErrors: true,
  },
  projects: [
    {
      name: 'live-chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
