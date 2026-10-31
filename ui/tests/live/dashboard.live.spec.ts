import { expect, test, type Page } from '@playwright/test';

/**
 * Live dashboard smoke against a daemon under test.
 *
 * Safety rules:
 * - The install token comes only from DEVSTACK_E2E_TOKEN.
 * - The token is passed in the URL fragment and must disappear from history.
 * - No mutation reaches a handler: CSRF-negative requests are rejected by the
 *   API middleware before any state change.
 * - The spec never starts, stops, installs, upgrades, or reconfigures anything.
 */
const LIVE = process.env.DEVSTACK_E2E_LIVE === '1';
const TOKEN = process.env.DEVSTACK_E2E_TOKEN?.trim() ?? '';
const BASE_URL = (process.env.DEVSTACK_UI_URL?.trim() || 'http://127.0.0.1:7070').replace(/\/+$/, '');

test.skip(!LIVE, 'Set DEVSTACK_E2E_LIVE=1 to run the live dashboard smoke.');
test.skip(LIVE && !TOKEN, 'DEVSTACK_E2E_TOKEN is required for the live dashboard smoke.');

function tokenURL(): string {
  return `${BASE_URL}/#token=${encodeURIComponent(TOKEN)}`;
}

async function expectNoTokenLeak(page: Page): Promise<void> {
  expect(page.url()).not.toContain(TOKEN);
  const state = await page.evaluate(() => ({
    local: { ...localStorage },
    session: { ...sessionStorage },
    body: document.body.innerText,
  }));
  expect(JSON.stringify(state.local)).not.toContain(TOKEN);
  expect(JSON.stringify(state.session)).not.toContain(TOKEN);
  expect(state.body).not.toContain(TOKEN);
}

async function expectNoConsoleErrors(page: Page, errors: string[]): Promise<void> {
  expect(errors, `console errors: ${errors.join(' | ')}`).toEqual([]);
}

test('live dashboard authenticates, enforces CSRF, expires safely, and reads core pages', async ({
  page,
}) => {
  const consoleErrors: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });
  page.on('pageerror', (error) => consoleErrors.push(error.message));

  // 1. Authenticate with the install token in the URL fragment.
  await page.goto(tokenURL());
  await expect(page.getByRole('heading', { name: 'Sites', exact: true })).toBeVisible();
  await expectNoTokenLeak(page);

  const cookies = await page.context().cookies();
  const authCookie = cookies.find((cookie) => cookie.name === 'devstack_auth');
  const sessionCookie = cookies.find((cookie) => cookie.name === 'devstack_session');
  expect(authCookie, 'install-token cookie').toBeTruthy();
  expect(authCookie?.httpOnly).toBe(true);
  expect(authCookie?.sameSite).toBe('Strict');
  expect(sessionCookie, 'dashboard session cookie').toBeTruthy();
  expect(sessionCookie?.httpOnly).toBe(true);
  expect(sessionCookie?.sameSite).toBe('Strict');

  // 2. The session endpoint returns a CSRF token that is not the install token.
  const sessionResponse = await page.request.get('/api/v1/session');
  expect(sessionResponse.ok()).toBe(true);
  const session = (await sessionResponse.json()) as { csrf_token?: string };
  expect(session.csrf_token).toMatch(/^[a-f0-9]{64}$/);
  expect(session.csrf_token).not.toBe(TOKEN);
  const csrf = session.csrf_token ?? '';
  expect(await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))).not.toContain(
    csrf,
  );

  // 3. Mutations without the exact CSRF token are rejected before any handler.
  const siteBody = {
    name: 'e2e-live-should-not-exist',
    path: '/tmp/e2e-live-should-not-exist',
    kind: 'static',
  };
  const withoutCSRF = await page.request.post('/api/v1/sites', { data: siteBody });
  expect(withoutCSRF.status()).toBe(403);
  const withoutCSRFBody = (await withoutCSRF.json()) as { code?: string };
  expect(withoutCSRFBody.code).toBe('FORBIDDEN');

  const wrongCSRF = await page.request.post('/api/v1/sites', {
    data: siteBody,
    headers: { 'X-CSRF-Token': 'not-the-session-token' },
  });
  expect(wrongCSRF.status()).toBe(403);
  const wrongCSRFBody = (await wrongCSRF.json()) as { code?: string };
  expect(wrongCSRFBody.code).toBe('FORBIDDEN');

  // The rejected request must not have created anything.
  const sitesAfter = await page.request.get('/api/v1/sites');
  expect(sitesAfter.ok()).toBe(true);
  const sitesJSON = await sitesAfter.text();
  expect(sitesJSON).not.toContain('e2e-live-should-not-exist');

  // 4. Expiry: an invalid session value is replaced and the old CSRF stops working.
  await page.context().addCookies([
    {
      name: 'devstack_session',
      value: '0'.repeat(64),
      url: BASE_URL,
      httpOnly: true,
      sameSite: 'Strict',
    },
  ]);
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Sites', exact: true })).toBeVisible();
  const expiredCSRF = await page.request.post('/api/v1/sites', {
    data: siteBody,
    headers: { 'X-CSRF-Token': csrf },
  });
  expect(expiredCSRF.status()).toBe(403);
  const refreshedSession = await page.request.get('/api/v1/session');
  const refreshed = (await refreshedSession.json()) as { csrf_token?: string };
  expect(refreshed.csrf_token).toMatch(/^[a-f0-9]{64}$/);
  expect(refreshed.csrf_token).not.toBe(csrf);

  // 5. Logout: clearing the auth and session cookies locks the dashboard and
  // the API rejects the next read with the install-token gate.
  await page.context().clearCookies();
  await page.reload();
  await expect(page.getByText(/Dashboard locked/)).toBeVisible();
  const lockedRead = await page.request.get('/api/v1/services');
  expect(lockedRead.status()).toBe(403);
  const lockedBody = (await lockedRead.json()) as { code?: string };
  expect(lockedBody.code).toBe('FORBIDDEN');

  // Restore the authenticated context with the one-shot fragment.
  await page.goto(tokenURL());
  await expect(page.getByRole('heading', { name: 'Sites', exact: true })).toBeVisible();
  await expectNoTokenLeak(page);

  // 6. Core read-only dashboard pages.
  const siteRows = page.locator('tbody tr');
  if ((await siteRows.count()) > 0) {
    const firstSite = siteRows.first().getByRole('link').first();
    const siteName = (await firstSite.textContent())?.trim() ?? '';
    await firstSite.click();
    await expect(page.getByRole('heading', { name: siteName, exact: true })).toBeVisible();
    await page.getByRole('link', { name: 'All sites' }).click();
    await expect(page.getByRole('heading', { name: 'Sites', exact: true })).toBeVisible();
  } else {
    await expect(page.getByText(/No sites yet/i)).toBeVisible();
  }

  await page.goto(`${BASE_URL}/#/services`);
  await expect(page.getByRole('heading', { name: 'Infrastructure services' })).toBeVisible();
  const mysqlCard = page.locator('article').filter({ hasText: 'MySQL 8.4' }).first();
  if (await mysqlCard.count()) {
    await expect(mysqlCard).toBeVisible();
    await page.goto(`${BASE_URL}/#/services/mysql`);
    await expect(page.getByRole('heading', { name: 'MySQL 8.4' })).toBeVisible();
    for (const tab of ['Databases', 'Users', 'Backups']) {
      await expect(page.getByRole('tab', { name: tab, exact: true })).toBeVisible();
    }
  }

  await page.goto(`${BASE_URL}/#/doctor`);
  await expect(page.getByRole('heading', { name: 'Checks', exact: true })).toBeVisible();

  await expectNoConsoleErrors(page, consoleErrors);
});
