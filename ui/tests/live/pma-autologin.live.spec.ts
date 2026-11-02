import { expect, test } from '@playwright/test';

/**
 * Live phpMyAdmin auto-login check.
 *
 * The dashboard's "Open phpMyAdmin" link points at this URL. With config
 * authentication, the page must render the active database directly and never
 * show a username/password form. This spec never reads or prints the root
 * password; it only checks the effective server and the absence of a login
 * form.
 */
const LIVE = process.env.DEVSTACK_E2E_LIVE === '1';
const PMA_URL = (process.env.DEVSTACK_E2E_PMA_URL?.trim() || 'http://127.0.0.1:8082/').replace(/\/+$/, '') + '/';

test.skip(!LIVE, 'Set DEVSTACK_E2E_LIVE=1 to run the phpMyAdmin auto-login check.');

test('phpMyAdmin auto-login opens the active database as root', async ({ page }) => {
  const errors: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error') errors.push(message.text());
  });

  await page.goto(PMA_URL, { waitUntil: 'networkidle' });
  await expect(page.locator('input[name="pma_username"], input[name="pma_password"]')).toHaveCount(0);
  await expect(page.locator('body')).toContainText('Server: db:3306');
  await expect(page.locator('body')).not.toContainText('Access denied');
  await expect(page.locator('body')).not.toContainText('Cannot connect: invalid settings');
  expect(errors, `console errors: ${errors.join(' | ')}`).toEqual([]);
});
