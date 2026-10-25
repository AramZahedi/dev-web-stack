import { expect, test, type Page } from '@playwright/test';

/**
 * Browser coverage for the mock-mode dashboard. The mock API lives in the page,
 * so each test starts from the seeded state after a fresh navigation.
 */

const SITE_NAME = 'portfolio';
const SITE_PATH = '/home/dev/projects/scratch';
const PHP_SITE_NAME = 'phpdemo';
const PHP_BUILD = 'php@8.4.4-r1';
const PHP_FAILING_BUILD = 'php@8.5.0-r1';

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

test('PHP site creation, ini/extensions, and failed build switch roll back', async ({ page }) => {
  await open(page, '#/sites/new');

  await expect(page.getByRole('heading', { name: 'Project details' })).toBeVisible();
  await page.locator('#site-name').fill(PHP_SITE_NAME);

  await page.getByRole('button', { name: 'Browse' }).click();
  const browser = page.getByRole('dialog', { name: 'Choose a project folder' });
  await browser.getByRole('button', { name: 'projects', exact: true }).click();
  await browser.getByRole('button', { name: 'scratch', exact: true }).click();
  await browser.getByRole('button', { name: 'Use this folder' }).click();
  await expect(browser).toBeHidden();

  await page.locator('label.kind-card').filter({ hasText: 'A PHP-FPM pool' }).click();
  await expect(page.locator('#kind-php')).toBeChecked();
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByRole('heading', { name: 'How should it run?' })).toBeVisible();

  const build = page.locator('#php-build');
  await expect(build).toBeVisible();
  await build.selectOption(PHP_BUILD);
  await page.locator('#php-document-root').fill('public');
  await expect(page.getByRole('switch', { name: 'Front controller' })).toHaveAttribute(
    'aria-checked',
    'true',
  );

  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByRole('heading', { name: 'Domain and options' })).toBeVisible();
  await page.getByRole('button', { name: 'Create site' }).click();
  await page.waitForURL(/#\/sites\/s_/);

  await expect(page.getByRole('heading', { name: PHP_SITE_NAME })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Runtime configuration' })).toBeVisible();
  await expect(page.getByText(PHP_BUILD, { exact: true }).first()).toBeVisible();

  // The create payload carries the selected line, build, document root, and
  // front-controller flag.
  const created = await page.evaluate(async (name) => {
    const modulePath = '/src/lib/mock.ts';
    const mock = (await import(modulePath)) as {
      mockApi: {
        getSite(ref: string): Promise<{
          kind: string;
          php_version: string;
          php_build: string;
          document_root: string;
          front_controller: boolean;
        }>;
      };
    };
    return mock.mockApi.getSite(name);
  }, PHP_SITE_NAME);
  expect(created).toMatchObject({
    kind: 'php',
    php_version: '8.4',
    php_build: PHP_BUILD,
    document_root: 'public',
    front_controller: true,
  });

  // Extensions apply immediately; xdebug starts inherited (off).
  const xdebug = page.getByRole('group', { name: 'xdebug' });
  await expect(xdebug.getByText('Inherit (off)')).toBeVisible();
  await xdebug.getByRole('button', { name: 'Enable xdebug' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'PHP settings applied' })).toBeVisible();

  const raw = page.getByLabel('Site php.ini section');
  await expect(raw).not.toHaveValue(/devstack extensions \(managed\)/);
  await raw.fill('memory_limit = 512M\ndisplay_errors = On\n');
  await page.getByRole('button', { name: 'Apply PHP settings' }).click();
  // The editor disables itself while the mutation is in flight; wait for the
  // round-trip and the follow-up reload before reading the record back.
  await expect(page.getByRole('button', { name: 'Apply PHP settings' })).toBeEnabled();
  await expect(raw).toHaveValue('memory_limit = 512M\ndisplay_errors = On\n');

  const applied = await page.evaluate(async (name) => {
    const modulePath = '/src/lib/mock.ts';
    const mock = (await import(modulePath)) as {
      mockApi: {
        getSite(ref: string): Promise<{
          php_extensions?: Record<string, boolean>;
          php_ini_raw: string;
        }>;
      };
    };
    return mock.mockApi.getSite(name);
  }, PHP_SITE_NAME);
  expect(applied.php_extensions).toMatchObject({ xdebug: true });
  expect(applied.php_ini_raw).toContain('memory_limit = 512M');

  // The mock rejects this build to exercise validate-then-swap rollback.
  await page.getByRole('button', { name: 'Switch build' }).click();
  const dialog = page.getByRole('dialog', { name: `Switch ${PHP_SITE_NAME} to another PHP build?` });
  await dialog.locator('#switch-php-build').selectOption(PHP_FAILING_BUILD);
  await dialog.getByRole('button', { name: 'Switch build', exact: true }).click();

  await expect(
    page.getByText(/The previous build was kept; validate-then-swap rolled back/),
  ).toBeVisible();
  await expect(page.getByText(/PHP_CONFIG_INVALID/)).toBeVisible();
  await expect(page.getByText('Line 4', { exact: true })).toBeVisible();

  const afterFailure = await page.evaluate(async (name) => {
    const modulePath = '/src/lib/mock.ts';
    const mock = (await import(modulePath)) as {
      mockApi: { getSite(ref: string): Promise<{ php_build: string }> };
    };
    return mock.mockApi.getSite(name);
  }, PHP_SITE_NAME);
  expect(afterFailure.php_build).toBe(PHP_BUILD);
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
  await page.evaluate(() => window.dispatchEvent(new Event('devstack:refresh')));
  await expect(page.getByRole('button', { name: 'Resume log stream' })).toBeVisible();
  expect(await lineCount()).toBe(pausedAt);

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

test('Site detail reloads when the route ref changes', async ({ page }) => {
  await open(page, '#/sites/blog');
  await expect(page.getByRole('heading', { name: 'blog' })).toBeVisible();
  await expect(page.getByRole('log', { name: 'Live output from blog' })).toBeVisible();

  await page.evaluate(() => {
    window.location.hash = '#/sites/shop';
  });

  await expect(page.getByRole('heading', { name: 'shop' })).toBeVisible();
  await expect(page.getByRole('log', { name: 'Live output from shop' })).toBeVisible();
  await expect(page.getByRole('log', { name: 'Live output from blog' })).toHaveCount(0);
});

test('Site detail clears revealed secrets on route change', async ({ page }) => {
  await open(page, '#/sites/shop');
  await expect(page.getByRole('heading', { name: 'shop' })).toBeVisible();
  await page.getByRole('button', { name: 'Reveal SESSION_SECRET' }).click();
  await expect(page.getByText('shop_dev_2f9c4a6e8b1d')).toBeVisible();

  await page.evaluate(() => {
    window.location.hash = '#/sites/blog';
  });
  await expect(page.getByRole('heading', { name: 'blog' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Reveal SESSION_SECRET' })).toBeVisible();
  await expect(page.getByText('shop_dev_2f9c4a6e8b1d')).toHaveCount(0);
});

test('Services groups containers, reveals credentials and reports activation conflicts', async ({
  page,
}) => {
  await open(page, '#/services');

  await expect(page.getByRole('heading', { name: 'Infrastructure services' })).toBeVisible();
  for (const group of ['Web servers', 'MySQL family', 'Standalone']) {
    await expect(page.getByRole('heading', { name: group })).toBeVisible();
  }

  const nginx = page.locator('article').filter({ hasText: 'Nginx Web Server' });
  const mysql = page.locator('article').filter({ hasText: 'MySQL 8.4' });
  const apache = page.locator('article').filter({ hasText: 'Apache HTTP Server' });

  await expect(nginx.getByText('Active', { exact: true })).toBeVisible();
  await expect(nginx.getByText('Health: healthy')).toBeVisible();

  await nginx.getByRole('button', { name: 'Logs' }).click();
  const logs = page.getByRole('dialog', { name: 'Nginx Web Server logs' });
  await expect(logs).toBeVisible();
  await expect(logs.locator('pre')).toContainText('[nginx]');
  await logs.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(logs).toBeHidden();

  await mysql.getByRole('button', { name: 'Credentials' }).click();
  const credentials = page.getByRole('dialog', { name: 'MySQL 8.4 credentials' });
  await expect(credentials).toBeVisible();
  await expect(credentials.getByText('••••••••••••')).toBeVisible();
  await credentials.getByRole('button', { name: 'Reveal password' }).click();
  await expect(credentials.getByText('mock_7f3c9a2e5b8d1a4c6e9f0b2d')).toBeVisible();
  await credentials.getByRole('button', { name: 'Rotate credentials' }).click();
  await credentials.getByRole('button', { name: 'Rotate credentials' }).click();
  await expect(credentials.getByRole('alert')).toContainText('CREDENTIAL_ROTATION_UNAVAILABLE');
  await expect(credentials.getByRole('alert')).toContainText(
    'database credential rotation is not available until Wave 2',
  );
  await credentials.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(credentials).toBeHidden();

  await apache.getByRole('button', { name: 'Activate' }).click();
  await expect(apache.getByRole('alert')).toContainText('SERVICE_CONFLICT');
  await expect(apache.getByRole('alert')).toContainText('Nginx Web Server is active');
  await expect(apache.getByText('Inactive', { exact: true })).toBeVisible();

  await expect(page.getByRole('heading', { name: 'Performance' })).toBeVisible();
  await expect(page.getByText('No benchmark runs yet')).toBeVisible();
});

test('Service configuration lists files, diffs, applies, and resets', async ({ page }) => {
  await open(page, '#/services');
  const nginx = page.locator('article').filter({ hasText: 'Nginx Web Server' }).first();
  await nginx.getByRole('button', { name: 'Configuration' }).click();

  const dialog = page.getByRole('dialog', { name: 'Nginx Web Server configuration' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('nginx.conf', { exact: true })).toBeVisible();
  await dialog.getByRole('button', { name: /conf\.d\/devstack\.conf/ }).click();

  const editor = dialog.getByLabel('File content');
  await expect(editor).toHaveValue(/listen 80/);
  await editor.fill('# custom nginx\nserver {\n    listen 8080;\n}\n');
  await dialog.getByRole('button', { name: 'Preview diff' }).click();
  await expect(dialog.getByText('Unified diff')).toBeVisible();
  await expect(dialog.locator('.diff-panel pre')).toContainText('+    listen 8080;');

  await dialog.getByRole('button', { name: 'Apply', exact: true }).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'Configuration applied' }),
  ).toBeVisible();
  await expect(dialog.getByText('Configuration validated and reloaded.')).toBeVisible();

  await dialog.getByRole('button', { name: 'Reset', exact: true }).click();
  const reset = page.getByRole('dialog', { name: /Reset conf\.d\/devstack\.conf/ });
  await reset.getByRole('textbox').fill('RESET');
  await reset.getByRole('button', { name: 'Reset file' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'Configuration reset' }),
  ).toBeVisible();
  await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(dialog).toBeHidden();
});

test('Performance previews, applies, resets, and records a measured benchmark', async ({ page }) => {
  test.setTimeout(45_000);
  await open(page, '#/services');
  const performance = page.locator('.performance-card');
  await expect(performance.getByRole('heading', { name: 'Performance' })).toBeVisible();
  await expect(page.getByText('No benchmark runs yet')).toBeVisible();

  await performance.getByRole('button', { name: 'Preview' }).click();
  await expect(performance.getByText('Host tuning diff')).toBeVisible();
  await expect(performance.getByText(/Host tuning changes are global/)).toBeVisible();
  await performance.getByText(/Managed include files/).click();
  await expect(performance.getByText(/worker_processes 4/)).toBeVisible();

  await performance.getByRole('button', { name: 'Apply profile' }).click();
  const applyDialog = page.getByRole('dialog', { name: 'Apply performance profile?' });
  await expect(applyDialog).toBeVisible();
  await applyDialog.getByRole('checkbox').check();
  await expect(applyDialog.getByText(/Host tuning changes kernel/)).toBeVisible();
  await applyDialog.getByRole('textbox').fill('APPLY');
  await applyDialog.getByRole('button', { name: 'Apply profile' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'Performance applied with host tuning' }),
  ).toBeVisible();
  await expect(performance.getByText('Apply complete')).toBeVisible();
  await expect(performance.getByText('Host tuned')).toBeVisible();

  const benchmark = page.locator('.benchmark-card');
  await benchmark.getByLabel('Duration (s)').fill('2');
  await benchmark.getByLabel('Connections').fill('8');
  await benchmark.getByRole('button', { name: 'Run benchmark' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Benchmark complete' })).toBeVisible();
  const row = benchmark.locator('tbody tr').first();
  await expect(row).toContainText('blog');
  await expect(row).toContainText('devstack-go-http');

  await performance.getByRole('button', { name: 'Reset', exact: true }).click();
  const resetDialog = page.getByRole('dialog', { name: 'Reset performance?' });
  await resetDialog.getByRole('textbox').fill('RESET');
  await resetDialog.getByRole('button', { name: 'Reset performance' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Performance reset' })).toBeVisible();
  await expect(performance.getByText('Reset complete')).toBeVisible();
});

test('Site detail edits configuration and runs a static build', async ({ page }) => {
  await open(page, '#/sites/blog');
  await expect(page.getByRole('heading', { name: 'blog' })).toBeVisible();

  await expect(page.getByRole('heading', { name: 'Static build' })).toBeVisible();
  await expect(page.getByLabel('Build log tail')).toContainText('built in');
  await page.getByRole('button', { name: 'Run build' }).click();
  await expect(page.getByText('Build succeeded')).toBeVisible();
  await expect(page.locator('.build-duration')).toHaveText('4.3 s');
  await expect(page.getByText('/home/dev/projects/blog/public').first()).toBeVisible();

  await page.getByRole('button', { name: 'Edit configuration' }).click();
  const command = page.getByLabel(/Build command/);
  await command.fill('npm ci && npm run build');
  await page.getByLabel('Output directory').fill('public');
  await page.getByRole('button', { name: 'Save configuration' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'Site configuration saved' }),
  ).toBeVisible();

  const saved = await page.evaluate(async () => {
    const modulePath = '/src/lib/mock.ts';
    const mock = (await import(modulePath)) as {
      mockApi: { getSite(ref: string): Promise<{ build?: { command: string } }> };
    };
    return mock.mockApi.getSite('blog');
  });
  expect(saved.build?.command).toBe('npm ci && npm run build');
});

test('Settings manages the global PHP default, ini and Composer', async ({ page }) => {
  await open(page, '#/settings');

  const phpCard = page.locator('section').filter({ hasText: 'PHP CLI default' }).first();
  await expect(phpCard.getByRole('heading', { name: 'PHP CLI default' })).toBeVisible();
  await phpCard.getByLabel('Default PHP build').selectOption('php@8.3.14-r2');
  await phpCard.getByRole('button', { name: 'Save default' }).click();
  await expect(
    page.getByRole('status').filter({ hasText: 'PHP CLI default saved' }),
  ).toBeVisible();
  await expect(phpCard.locator('.tooling-list')).toContainText('PHP 8.3.14');

  const ini = page.getByLabel('Global php.ini content');
  await ini.fill('[PHP]\nmemory_limit = 1G\n');
  await page.getByRole('button', { name: 'Save php.ini' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Global php.ini saved' })).toBeVisible();

  await page.getByRole('button', { name: 'Update', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: 'composer updated' })).toBeVisible();

  await expect(page.getByRole('heading', { name: 'ImageMagick CLI' })).toHaveCount(0);
});

test('Settings edits listener ports and shows restart guidance', async ({ page }) => {
  await open(page, '#/settings');

  await page.getByLabel('HTTP proxy port').fill('18080');
  await page.getByLabel('HTTPS proxy port').fill('18443');
  await page.getByLabel('Dashboard/API port').fill('17070');
  await page.getByRole('button', { name: 'Save ports' }).click();

  await expect(
    page.getByRole('status').filter({ hasText: 'Ports saved — restart required' }),
  ).toBeVisible();
  const banner = page.locator('.restart-banner');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText('devstack daemon restart');
  await expect(page.getByLabel('HTTP proxy port')).toHaveValue('18080');
  await expect(page.getByLabel('HTTPS proxy port')).toHaveValue('18443');
  await expect(page.getByLabel('Dashboard/API port')).toHaveValue('17070');

  const spacing = await page.locator('.native-tools-card').evaluate((element) => {
    const style = getComputedStyle(element);
    return Number.parseFloat(style.marginTop);
  });
  expect(spacing).toBeGreaterThanOrEqual(12);
});

test('Runtimes asks for confirmation and installs a build', async ({ page }) => {
  await open(page, '#/runtimes');

  await expect(page.getByRole('heading', { name: 'Verified runtimes' })).toBeVisible();
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

test('Settings Serving switches modes, syncs, and previews vhosts', async ({ page }) => {
  await open(page, '#/settings');

  const serving = page.locator('.serving-card');
  await expect(serving.getByRole('heading', { name: 'Serving' })).toBeVisible();
  await expect(serving.getByText('Built-in proxy', { exact: true }).first()).toBeVisible();
  await expect(serving.getByRole('button', { name: 'Use Built-in serving' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );

  await serving.getByRole('button', { name: 'Use nginx serving' }).click();
  const dialog = page.getByRole('dialog', { name: 'Switch serving to nginx?' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('nginx takes ownership of ports 80 and 443.')).toBeVisible();
  await expect(dialog.getByText(/Brief downtime is possible/)).toBeVisible();

  await dialog.getByRole('button', { name: 'Switch to nginx' }).click();
  await expect(dialog).toBeHidden();
  await expect(
    page.getByRole('status').filter({ hasText: 'nginx is now serving sites' }),
  ).toBeVisible();
  await expect(serving.getByText('nginx 1.28.0', { exact: true })).toBeVisible();
  await expect(serving.getByText('Present', { exact: true })).toBeVisible();
  await expect(serving.getByText('Passed', { exact: true })).toBeVisible();

  await serving.getByRole('button', { name: 'Sync now' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Vhosts synced' })).toBeVisible();

  const previewButton = serving.getByRole('button', { name: 'Preview vhost' });
  await previewButton.click();
  const drawer = page.getByRole('dialog', { name: 'blog vhost preview' });
  await expect(drawer).toBeVisible();
  const closePreview = drawer.getByRole('button', { name: 'Close vhost preview' });
  await expect(closePreview).toBeFocused();

  await page.keyboard.press('Tab');
  const nginxTab = drawer.getByRole('tab', { name: 'nginx' });
  await expect(nginxTab).toBeFocused();
  const focusRing = await nginxTab.evaluate((element) => {
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

  await expect(drawer.locator('pre')).toContainText('server_name blog.test;');
  await expect(drawer.locator('pre')).toContainText('root /home/dev/projects/blog/public;');

  await drawer.getByRole('tab', { name: 'Apache' }).click();
  await expect(drawer.locator('pre')).toContainText('<VirtualHost *:443>');
  await expect(drawer.locator('pre')).toContainText('ServerName blog.test');

  await page.keyboard.press('Escape');
  await expect(drawer).toBeHidden();
  await expect(previewButton).toBeFocused();
});

test('Site detail opens a site-scoped terminal and runs a command', async ({ page }) => {
  await open(page, '#/sites/blog');
  await expect(page.getByRole('heading', { name: 'blog', exact: true })).toBeVisible();

  const terminalCard = page.locator('.terminal-card');
  await expect(terminalCard.getByText('Open a shell in this site')).toBeVisible();
  await terminalCard.getByRole('button', { name: 'Start terminal' }).click();
  await expect(terminalCard.getByText('Connected', { exact: true })).toBeVisible();

  await page.keyboard.type('pwd');
  await page.keyboard.press('Enter');
  await expect(page.locator('.xterm-rows')).toContainText('/home/dev/projects/blog');

  await terminalCard.getByRole('button', { name: 'Close' }).click();
  await expect(terminalCard.getByText('Closed', { exact: true })).toBeVisible();
});

test('Site detail previews its rendered vhost', async ({ page }) => {
  await open(page, '#/sites/blog');
  await expect(page.getByRole('heading', { name: 'blog' })).toBeVisible();

  const previewButton = page.getByRole('button', { name: 'Preview vhost' });
  await previewButton.click();
  const drawer = page.getByRole('dialog', { name: 'blog vhost preview' });
  await expect(drawer).toBeVisible();
  await expect(drawer.locator('pre')).toContainText('root /home/dev/projects/blog/public;');

  await drawer.getByRole('tab', { name: 'Apache' }).click();
  await expect(drawer.locator('pre')).toContainText(
    'DocumentRoot "/home/dev/projects/blog/public"',
  );

  await drawer.getByRole('button', { name: 'Close vhost preview' }).click();
  await expect(drawer).toBeHidden();
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
      name: 'Services',
      hash: '#/services',
      ready: async (page: Page) => {
        await expect(page.getByRole('heading', { name: 'Infrastructure services' })).toBeVisible();
      },
    },
    {
      name: 'Runtimes',
      hash: '#/runtimes',
      ready: async (page: Page) => {
        await expect(page.getByRole('heading', { name: 'Verified runtimes' })).toBeVisible();
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

  test('site terminal stays within 375px after connecting', async ({ page }) => {
    await open(page, '#/sites/blog');
    await expect(page.getByRole('heading', { name: 'blog', exact: true })).toBeVisible();
    await page.locator('.terminal-card').getByRole('button', { name: 'Start terminal' }).click();
    await expect(page.locator('.terminal-card').getByText('Connected', { exact: true })).toBeVisible();
    const width = await page.evaluate(() => ({
      viewport: window.innerWidth,
      document: document.documentElement.scrollWidth,
      body: document.body.scrollWidth,
    }));
    expect(width.document).toBeLessThanOrEqual(width.viewport);
    expect(width.body).toBeLessThanOrEqual(width.viewport);
  });

  test('Settings native tools card keeps vertical spacing at 375px', async ({ page }) => {
    await open(page, '#/settings');
    await expect(page.getByRole('heading', { name: 'Native tools' })).toBeVisible();
    const spacing = await page.locator('.native-tools-card').evaluate((element) => {
      return Number.parseFloat(getComputedStyle(element).marginTop);
    });
    expect(spacing).toBeGreaterThanOrEqual(12);
  });

  test('vhost preview drawer has no horizontal overflow', async ({ page }) => {
    await open(page, '#/settings');
    await expect(page.getByRole('heading', { name: 'Serving' })).toBeVisible();

    await page.getByRole('button', { name: 'Preview vhost' }).click();
    const drawer = page.getByRole('dialog', { name: 'blog vhost preview' });
    await expect(drawer).toBeVisible();
    await expect(drawer.locator('pre')).toBeVisible();

    const width = await page.evaluate(() => ({
      viewport: window.innerWidth,
      document: document.documentElement.scrollWidth,
      body: document.body.scrollWidth,
    }));
    expect(width.document, 'preview drawer document overflow').toBeLessThanOrEqual(width.viewport);
    expect(width.body, 'preview drawer body overflow').toBeLessThanOrEqual(width.viewport);
  });

  test('mobile navigation drawer opens, closes with Escape, and follows links', async ({ page }) => {
    await open(page, '#/sites');
    await waitForSites(page);

    const sidebar = page.locator('aside[aria-label="Sidebar"]');
    await expect(sidebar).toBeHidden();

    await page.getByRole('button', { name: 'Open navigation' }).click();
    await expect(sidebar).toBeVisible();
    await expect(sidebar.getByRole('link', { name: 'Sites' })).toBeVisible();
    await expect(page.locator('.backdrop')).toBeVisible();
    expect(await page.evaluate(() => document.body.style.overflow)).toBe('hidden');

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
