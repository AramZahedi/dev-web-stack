import { expect, test, type Page } from '@playwright/test';

/**
 * Browser coverage for the mock-mode dashboard. The mock API lives in the page,
 * so each test starts from the seeded state after a fresh navigation.
 */

const SITE_NAME = 'portfolio';
const SITE_PATH = '/home/dev/projects/scratch';

interface MockSite {
  name: string;
  domain: string;
  tls: boolean;
  www: boolean;
  force_https: boolean;
  spa_fallback: boolean;
  kind: string;
  document_root: string;
  status: string;
}

async function open(page: Page, hash: string): Promise<void> {
  await page.goto(`/${hash}`);
}

async function waitForSites(page: Page): Promise<void> {
  await expect(page.locator('tbody tr:visible, .mobile-list .site-card:visible').first()).toBeVisible();
}

async function ready(page: Page): Promise<void> {
  await page.evaluate(async () => {
    await document.fonts.ready;
  });
}

test('Sites loads the seeded sites and their summary', async ({ page }) => {
  await open(page, '');

  await expect(page.getByRole('heading', { name: 'Sites', exact: true })).toBeVisible();
  await expect(page.locator('tbody tr')).toHaveCount(3);

  for (const [name, domain, status] of [
    ['blog', 'blog.test', 'Running'],
    ['shop', 'shop.test', 'Running'],
    ['api', 'api.test', 'Crashed'],
  ] as const) {
    const row = page.locator('tbody tr').filter({ hasText: domain });
    await expect(row).toHaveCount(1);
    await expect(row.getByRole('link', { name, exact: true })).toBeVisible();
    await expect(row.getByLabel(`Status: ${status}`)).toBeVisible();
  }

  await expect(page.getByRole('button', { name: 'Add site' })).toBeVisible();
});

test('Add Site browses a folder, applies options, and navigates to the new site', async ({ page }) => {
  await open(page, '#/sites/new');

  await expect(page.getByRole('heading', { name: 'Project details' })).toBeVisible();
  await page.locator('#site-name').fill(SITE_NAME);

  await page.getByRole('button', { name: 'Browse' }).click();
  const dialog = page.getByRole('dialog', { name: 'Choose a project folder' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('/home/dev', { exact: true })).toBeVisible();

  await dialog.getByRole('button', { name: 'projects', exact: true }).click();
  await dialog.getByRole('button', { name: 'scratch', exact: true }).click();
  await dialog.getByRole('button', { name: 'Use this folder' }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator('#site-path')).toHaveValue(SITE_PATH);

  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByRole('heading', { name: 'How should it run?' })).toBeVisible();

  const spaOnStepTwo = page.getByRole('switch', { name: 'SPA fallback' });
  await expect(spaOnStepTwo).toHaveAttribute('aria-checked', 'false');
  await spaOnStepTwo.click();
  await expect(spaOnStepTwo).toHaveAttribute('aria-checked', 'true');

  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByRole('heading', { name: 'Domain and options' })).toBeVisible();

  const tls = page.getByRole('switch', { name: 'Serve over HTTPS' });
  const forceHttps = page.getByRole('switch', { name: 'Force HTTPS' });
  const www = page.getByRole('switch', { name: 'Add www alias' });
  const spaOnStepThree = page.getByRole('switch', { name: 'SPA fallback' });

  await expect(tls).toHaveAttribute('aria-checked', 'true');
  await expect(spaOnStepThree).toHaveAttribute('aria-checked', 'true');

  // Turning TLS off must immediately change the preview and disable Force HTTPS.
  await tls.click();
  await expect(tls).toHaveAttribute('aria-checked', 'false');
  await expect(forceHttps).toBeDisabled();
  await expect(page.getByText(`http://${SITE_NAME}.test`, { exact: true }).first()).toBeVisible();

  await tls.click();
  await expect(forceHttps).toBeEnabled();
  await forceHttps.click();
  await www.click();

  await expect(forceHttps).toHaveAttribute('aria-checked', 'true');
  await expect(www).toHaveAttribute('aria-checked', 'true');
  await expect(page.getByText(`https://${SITE_NAME}.test`, { exact: true }).first()).toBeVisible();
  await expect(page.getByText('TLS · force HTTPS · www alias', { exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Create site' }).click();
  await page.waitForURL(/#\/sites\/s_/);

  await expect(page.getByRole('heading', { name: SITE_NAME })).toBeVisible();
  await expect(page.getByText(`https://${SITE_NAME}.test`, { exact: true }).first()).toBeVisible();
  await expect(page.getByText(`${SITE_NAME}.test`, { exact: true }).first()).toBeVisible();
  await expect(page.getByLabel('Status: Running')).toBeVisible({ timeout: 10_000 });
  await expect(page.locator('.chips').getByText(`www.${SITE_NAME}.test`, { exact: true })).toBeVisible();

  // Verify the submitted payload, not just the review UI. Vite serves the same
  // mock module the dashboard imports, so this reads the created record.
  const created = await page.evaluate(async (name): Promise<MockSite> => {
    const modulePath = '/src/lib/mock.ts';
    const mock = (await import(modulePath)) as {
      mockApi: { getSite(ref: string): Promise<MockSite> };
    };
    return mock.mockApi.getSite(name);
  }, SITE_NAME);

  expect(created).toMatchObject({
    name: SITE_NAME,
    domain: `${SITE_NAME}.test`,
    kind: 'static',
    document_root: '.',
    tls: true,
    www: true,
    force_https: true,
    spa_fallback: true,
  });
});

test('Site detail streams logs, pauses/resumes, and stops/starts the site', async ({ page }) => {
  await open(page, '#/sites/blog');

  await expect(page.getByRole('heading', { name: 'blog' })).toBeVisible();
  await expect(page.getByLabel('Status: Running')).toBeVisible();
  await expect(page.locator('.url-row .url')).toHaveText('https://blog.test');

  const logs = page.getByRole('log', { name: 'Live output from blog' });
  await expect(logs).toBeVisible();
  await expect(logs.locator('.log-line').first()).toBeVisible();
  const lineCount = () => logs.locator('.log-line').count();

  await expect(page.getByText('Live', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Pause log stream' }).click();
  await expect(page.getByRole('button', { name: 'Resume log stream' })).toBeVisible();

  const pausedAt = await lineCount();
  expect(pausedAt).toBeGreaterThan(0);
  await page.waitForTimeout(1_200);
  expect(await lineCount()).toBe(pausedAt);
  await expect(page.getByText(/buffered while paused/)).toBeVisible();

  await page.getByRole('button', { name: 'Resume log stream' }).click();
  await expect.poll(lineCount, { timeout: 5_000 }).toBeGreaterThan(pausedAt);
  await expect(page.getByRole('button', { name: 'Pause log stream' })).toBeVisible();

  const headActions = page.locator('.head-actions');
  await headActions.getByRole('button', { name: 'Stop', exact: true }).click();
  await expect(page.getByLabel('Status: Stopped')).toBeVisible();
  await expect(page.getByRole('status').filter({ hasText: 'blog stopped' })).toBeVisible();

  await headActions.getByRole('button', { name: 'Start', exact: true }).click();
  await expect(page.getByLabel('Status: Running')).toBeVisible();
});

test('Runtimes asks for confirmation and installs a build', async ({ page }) => {
  await open(page, '#/runtimes');

  await expect(page.getByRole('heading', { name: 'Verified Node runtimes' })).toBeVisible();
  const installed = page.locator('section[aria-labelledby="installed-heading"]');
  const available = page.locator('section[aria-labelledby="available-heading"]');
  await expect(installed.getByRole('heading', { name: 'Node 24.9.0' })).toBeVisible();
  await expect(installed.getByRole('heading', { name: 'Node 22.20.0' })).toBeVisible();

  await available.getByRole('button', { name: 'Install' }).first().click();
  const dialog = page.getByRole('dialog', { name: 'Install Node 24.11.0?' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('linux-x64', { exact: true })).toBeVisible();
  await expect(dialog.getByText('50 MB', { exact: true })).toBeVisible();
  await expect(dialog.locator('.full-hash')).toHaveText(/^[a-f0-9]{64}$/);

  await dialog.getByRole('button', { name: 'Install Node 24.11.0', exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole('status').filter({ hasText: 'Node 24.11.0 installed' })).toBeVisible();
  await expect(installed.getByRole('heading', { name: 'Node 24.11.0' })).toBeVisible();
});

test('Doctor runs checks from the dashboard', async ({ page }) => {
  await open(page, '#/doctor');

  await expect(page.getByRole('heading', { name: 'Mostly healthy' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Checks', exact: true })).toBeVisible();
  await expect(page.locator('.check-row')).toHaveCount(8);

  await page.getByRole('button', { name: 'Run checks' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Checks complete' })).toBeVisible();
  await expect(page.getByText(/Last run just now/)).toBeVisible();
});

test('Settings theme toggle applies and persists', async ({ page }) => {
  await open(page, '#/settings');

  await expect(page.getByRole('heading', { name: 'Machine' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Trust' })).toBeVisible();

  await page.getByRole('button', { name: 'Light theme' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  expect(await page.evaluate(() => localStorage.getItem('devstack.theme'))).toBe('light');

  await page.reload();
  await expect(page.getByRole('heading', { name: 'Machine' })).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');

  await page.getByRole('button', { name: 'Dark theme' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  expect(await page.evaluate(() => localStorage.getItem('devstack.theme'))).toBe('dark');
});

test.describe('375px viewport', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  const routes = [
    { name: 'Sites', hash: '#/sites', ready: waitForSites },
    {
      name: 'Add Site',
      hash: '#/sites/new',
      ready: async (page: Page) => {
        await expect(page.getByRole('heading', { name: 'Project details' })).toBeVisible();
      },
    },
    {
      name: 'Site detail',
      hash: '#/sites/blog',
      ready: async (page: Page) => {
        await expect(page.getByRole('heading', { name: 'blog' })).toBeVisible();
      },
    },
    {
      name: 'Doctor',
      hash: '#/doctor',
      ready: async (page: Page) => {
        await expect(page.getByRole('heading', { name: 'Checks', exact: true })).toBeVisible();
      },
    },
    {
      name: 'Settings',
      hash: '#/settings',
      ready: async (page: Page) => {
        await expect(page.getByRole('heading', { name: 'Machine' })).toBeVisible();
      },
    },
  ];

  for (const route of routes) {
    test(`${route.name} has no horizontal overflow`, async ({ page }) => {
      await open(page, route.hash);
      await route.ready(page);
      await ready(page);

      const width = await page.evaluate(() => ({
        viewport: window.innerWidth,
        document: document.documentElement.scrollWidth,
        body: document.body.scrollWidth,
      }));
      expect(width.document, `${route.name} document overflow`).toBeLessThanOrEqual(width.viewport);
      expect(width.body, `${route.name} body overflow`).toBeLessThanOrEqual(width.viewport);
    });
  }

  test('mobile navigation drawer opens, closes with Escape, and follows links', async ({ page }) => {
    await open(page, '#/sites');
    await waitForSites(page);

    const sidebar = page.locator('aside[aria-label="Sidebar"]');
    await expect(sidebar).toBeHidden();

    await page.getByRole('button', { name: 'Open navigation' }).click();
    await expect(sidebar).toBeVisible();
    await expect(sidebar.getByRole('link', { name: 'Sites' })).toBeVisible();
    await expect(page.locator('.backdrop')).toBeVisible();

    await page.keyboard.press('Escape');
    await expect(sidebar).toBeHidden();
    await expect(page.locator('.backdrop')).toBeHidden();
    expect(await page.evaluate(() => document.body.style.overflow)).toBe('');

    await page.getByRole('button', { name: 'Open navigation' }).click();
    await expect(sidebar).toBeVisible();
    await sidebar.getByRole('link', { name: 'Doctor' }).click();
    await expect(page).toHaveURL(/#\/doctor$/);
    await expect(sidebar).toBeHidden();
  });
});

test('keyboard users get a skip link and visible focus', async ({ page }) => {
  await open(page, '#/sites');
  await waitForSites(page);

  await page.keyboard.press('Tab');
  const skip = page.getByRole('link', { name: 'Skip to content' });
  await expect(skip).toBeFocused();

  await page.keyboard.press('Enter');
  await expect(page.locator('#main')).toBeFocused();

  await page.reload();
  await waitForSites(page);

  await page.keyboard.press('Tab');
  await page.keyboard.press('Tab');
  const brand = page.getByRole('link', { name: 'devstack home' });
  await expect(brand).toBeFocused();

  const focusRing = await brand.evaluate((element) => {
    const style = getComputedStyle(element);
    return {
      focusVisible: element.matches(':focus-visible'),
      outlineStyle: style.outlineStyle,
      outlineWidth: style.outlineWidth,
    };
  });
  expect(focusRing.focusVisible).toBe(true);
  expect(focusRing.outlineStyle).not.toBe('none');
  expect(parseFloat(focusRing.outlineWidth)).toBeGreaterThanOrEqual(2);
});

test('modal Escape closes the dialog and restores focus', async ({ page }) => {
  await open(page, '#/sites/new');
  await expect(page.getByRole('heading', { name: 'Project details' })).toBeVisible();

  const browse = page.getByRole('button', { name: 'Browse' });
  await browse.focus();
  await browse.click();

  const dialog = page.getByRole('dialog', { name: 'Choose a project folder' });
  await expect(dialog).toBeVisible();
  await expect
    .poll(() => dialog.evaluate((element) => element.contains(document.activeElement)))
    .toBe(true);

  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(browse).toBeFocused();
});
