import { expect, test, type Page } from '@playwright/test';

/**
 * Live Site Detail / Add Site database checks for the cycle 5 disposable
 * daemon. The daemon has dashboard auth disabled, so this spec uses no install
 * token. It never prints or persists a password: the managed secret is read
 * from the API only to assert presence/absence in the DOM and browser storage.
 */
const LIVE = process.env.DEVSTACK_E2E_LIVE === '1';
const BASE_URL = (process.env.DEVSTACK_UI_URL?.trim() || 'http://127.0.0.1:27170').replace(/\/+$/, '');
const PROJECT_PATH = process.env.DEVSTACK_E2E_PROJECT_PATH?.trim() || '';

test.skip(!LIVE, 'Set DEVSTACK_E2E_LIVE=1 to run the live site database checks.');

async function noSecretInStorage(page: Page, secret: string): Promise<void> {
  const state = await page.evaluate(() => ({
    local: { ...localStorage },
    session: { ...sessionStorage },
  }));
  expect(JSON.stringify(state.local).includes(secret)).toBe(false);
  expect(JSON.stringify(state.session).includes(secret)).toBe(false);
  expect(page.url().includes(secret)).toBe(false);
}

async function managedSecret(page: Page, site: string): Promise<string> {
  const response = await page.request.get(`/api/v1/sites/${encodeURIComponent(site)}/database-env`);
  expect(response.ok()).toBe(true);
  const body = (await response.json()) as { env?: Record<string, string> };
  return body.env?.DB_PASSWORD ?? '';
}

test('Site Detail masks, reveals, copies, and clears a managed password', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: BASE_URL });
  const secret = await managedSecret(page, 'e2e-ui-managed');
  expect(secret.length).toBeGreaterThan(0);

  await page.goto(`${BASE_URL}/#/sites/e2e-ui-managed`);
  await expect(page.getByRole('heading', { name: 'e2e-ui-managed', exact: true })).toBeVisible();
  await expect(page.getByText('Password managed', { exact: true })).toBeVisible();

  const initialBody = await page.locator('body').innerText();
  expect(initialBody.includes(secret)).toBe(false);
  await noSecretInStorage(page, secret);

  await page.getByRole('button', { name: /reveal password for e2e_ui_app/i }).click();
  await expect
    .poll(async () => (await page.locator('body').innerText()).includes(secret))
    .toBe(true);
  await noSecretInStorage(page, secret);

  await page.getByRole('button', { name: /copy password for e2e_ui_app/i }).click();
  const clipboard = await page.evaluate(() => navigator.clipboard.readText());
  expect(clipboard === secret).toBe(true);

  await page.goto(`${BASE_URL}/#/services`);
  await page.goto(`${BASE_URL}/#/sites/e2e-ui-managed`);
  await expect(page.getByRole('heading', { name: 'e2e-ui-managed', exact: true })).toBeVisible();
  const afterRoute = await page.locator('body').innerText();
  expect(afterRoute.includes(secret)).toBe(false);
  await noSecretInStorage(page, secret);
});

test('metadata-only link shows Password not managed and the user editor Set password action', async ({ page }) => {
  await page.goto(`${BASE_URL}/#/sites/e2e-ui-external`);
  await expect(page.getByRole('heading', { name: 'e2e-ui-external', exact: true })).toBeVisible();
  await expect(page.getByText('Password not managed', { exact: true })).toBeVisible();
  await expect(page.getByText(/external password entry is not available in v1/i)).toBeVisible();
  await page.getByRole('link', { name: 'Manage user' }).click();
  await expect(page).toHaveURL(/tab=users/);
  await expect(page.getByRole('button', { name: /Set password for e2e_ui_external/i })).toBeVisible();
});

test('reserved database env keys show the link-override warning', async ({ page }) => {
  await page.goto(`${BASE_URL}/#/sites/e2e-ui-reserved`);
  await expect(page.getByRole('heading', { name: 'e2e-ui-reserved', exact: true })).toBeVisible();
  await expect(
    page.getByText(/A database link owns one or more of these keys at runtime/),
  ).toBeVisible();
});

test('Add Site review shows database, user, grant, host, and port without a password', async ({ page }) => {
  test.skip(!PROJECT_PATH, 'DEVSTACK_E2E_PROJECT_PATH is required for the Add Site review.');
  await page.goto(`${BASE_URL}/#/sites/new`);
  await page.getByLabel(/Site name/).fill('e2e-ui-review');
  await page.getByLabel(/Project folder/).fill(PROJECT_PATH);
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByLabel('Create or link a database').click();
  await page.getByLabel(/Database name/).fill('e2e_ui_review_app');
  await page.getByLabel(/Database username/).fill('e2e_ui_review_user');

  const review = page.locator('.review');
  await expect(review.getByText(/e2e_ui_review_app on/)).toBeVisible();
  await expect(review.getByText(/e2e_ui_review_user/)).toBeVisible();
  await expect(review.getByText(/127\.0\.0\.1:3306/)).toBeVisible();
  await expect(review.getByText(/Password will be generated when the site is created/)).toBeVisible();
  const text = await review.innerText();
  expect(text.includes('DB_PASSWORD')).toBe(false);
  expect(text.toLowerCase()).not.toContain('password:');
  const storage = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }));
  expect(storage.includes('e2e_ui_review_app')).toBe(false);
});
