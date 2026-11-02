import { expect, test } from '@playwright/test';

/**
 * Live phpMyAdmin signon check.
 *
 * The dashboard issues a short-lived HMAC token in the URL fragment. The
 * fragment is never sent to the server access log; signon.php reads it and
 * POSTs it once. Direct access shows the manual form, and replay is rejected.
 */
const LIVE = process.env.DEVSTACK_E2E_LIVE === '1';
const PMA_URL = (process.env.DEVSTACK_E2E_PMA_URL?.trim() || 'http://127.0.0.1:8082/').replace(/\/+$/, '') + '/';

test.skip(!LIVE, 'Set DEVSTACK_E2E_LIVE=1 to run the phpMyAdmin signon check.');

test('phpMyAdmin signs in from the dashboard and rejects replayed/direct access', async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();

  await page.goto('/#/services', { waitUntil: 'networkidle' });
  const button = page.getByRole('button', { name: /Open phpMyAdmin/ });
  await expect(button).toBeVisible();
  const [popup] = await Promise.all([context.waitForEvent('page'), button.click()]);
  await popup.waitForLoadState('domcontentloaded');
  await expect(popup.locator('input[name="user"], input[name="pma_username"]')).toHaveCount(0);
  await expect(popup.locator('body')).toContainText('Server: db:3306');
  await popup.close();

  const sessionResponse = await context.request.get('/api/v1/session');
  expect(sessionResponse.ok()).toBe(true);
  const session = (await sessionResponse.json()) as { csrf_token?: string };
  expect(session.csrf_token).toMatch(/^[a-f0-9]{64}$/);
  const signonResponse = await context.request.post('/api/v1/services/phpmyadmin/signon', {
    headers: { 'X-CSRF-Token': session.csrf_token ?? '' },
  });
  expect(signonResponse.ok()).toBe(true);
  const signon = (await signonResponse.json()) as { url?: string };
  expect(signon.url).toContain('/signon.php#token=');

  const firstContext = await browser.newContext();
  const first = await firstContext.newPage();
  await first.goto(signon.url ?? '', { waitUntil: 'domcontentloaded' });
  await expect(first.locator('body')).toContainText('Server: db:3306');
  await firstContext.close();

  const replayContext = await browser.newContext();
  const replay = await replayContext.newPage();
  await replay.goto(signon.url ?? '', { waitUntil: 'domcontentloaded' });
  await expect(replay.locator('body')).not.toContainText('Server: db:3306');
  await expect(replay.locator('body')).toContainText(/invalid|expired|already been used/i);
  await replayContext.close();

  const directContext = await browser.newContext();
  const direct = await directContext.newPage();
  await direct.goto(PMA_URL, { waitUntil: 'domcontentloaded' });
  await expect(direct.locator('input[name="user"]')).toBeVisible();
  await expect(direct.locator('body')).not.toContainText('Server: db:3306');
  await directContext.close();
  await context.close();
});
