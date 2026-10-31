import { defineConfig, devices } from '@playwright/test';

const PORT = 4173;
const BASE_URL = `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: './tests',
  // Live-daemon specs have their own config and must never join the mock run.
  testIgnore: ['**/live/**'],
  // The mock API lives in the page, so each browser context starts from the
  // seeded state. A single worker keeps the shared Vite server predictable.
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  timeout: 30_000,
  expect: { timeout: 7_000 },
  reporter: process.env.CI
    ? [['line'], ['html', { open: 'never' }]]
    : [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: BASE_URL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'firefox',
      use: { ...devices['Desktop Firefox'] },
    },
    {
      name: 'webkit',
      use: { ...devices['Desktop Safari'] },
    },
  ],
  webServer: {
    command: `npm run dev -- --host 127.0.0.1 --port ${PORT} --strictPort`,
    url: BASE_URL,
    timeout: 120_000,
    reuseExistingServer: !process.env.CI,
    env: {
      VITE_API_MODE: 'mock',
    },
  },
});
