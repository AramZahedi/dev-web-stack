import { expect, test } from '@playwright/test';

/**
 * Live phpMyAdmin signon check.
 *
 * The dashboard issues a short-lived HMAC token in the URL fragment. The
 * fragment is never sent to the server access log; signon.php reads it and
 * POSTs it once. The button wiring is covered by the UI unit test; this spec
 * proves the live token, replay rejection, and direct manual form.
 */
const LIVE = process.env.DEVSTACK_E2E_LIVE === '1';
const PMA_URL = (process.env.DEVSTACK_E2E_PMA_URL?.trim() || 'http://127.0.0.1:8082/').replace(/\/+$/, '') + '/';

test.skip(!LIVE, 'Set DEVSTACK_E2E_LIVE=1 to run the phpMyAdmin signon check.');

test('phpMyAdmin signs in with a one-time token and rejects replayed/direct access', async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto('/#/services', { waitUntil: 'networkidle' });

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
  try {
    await first.goto(signon.url ?? '', { waitUntil: 'commit', timeout: 10000 });
  } catch (error) {
    if (!String(error).includes('ERR_ABORTED')) throw error;
  }
  await expect.poll(async () => first.url(), { timeout: 30000 }).toMatch(/\/index\.php/);
  await expect(first.locator('input[name="user"], input[name="pma_username"]')).toHaveCount(0);
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
