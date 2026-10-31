import { expect, test, type Locator, type Page } from '@playwright/test';

/**
 * Database-management dashboard coverage against the in-page mock API.
 *
 * Each test starts from the seeded mock state because the mock module is
 * re-evaluated on a fresh navigation. Tests that need failures, delays, or
 * overlapping responses monkey-patch the same module instance the dashboard
 * imports; no product code is bypassed.
 */

const MYSQL = '#/services/mysql';
const POSTGRES = '#/services/postgresql';
const REDIS = '#/services/redis';
const PASSWORD = 'mock_db_password';

const browserIssues = new WeakMap<Page, string[]>();

test.beforeEach(async ({ page }) => {
  const issues: string[] = [];
  browserIssues.set(page, issues);
  page.on('console', (message) => {
    if (message.type() === 'error') issues.push(`console: ${message.text()}`);
  });
  page.on('pageerror', (error) => issues.push(`pageerror: ${error.message}`));
  page.on('requestfailed', (request) => {
    const type = request.resourceType();
    if (type === 'fetch' || type === 'xhr') {
      issues.push(`requestfailed: ${request.url()} ${request.failure()?.errorText ?? ''}`);
    }
  });
  page.on('response', (response) => {
    const type = response.request().resourceType();
    if (response.status() >= 400 && (type === 'fetch' || type === 'xhr' || type === 'document')) {
      issues.push(`http ${response.status()}: ${response.url()}`);
    }
  });
});

test.afterEach(async ({ page }) => {
  expect(browserIssues.get(page) ?? [], 'unexpected browser errors or failed requests').toEqual([]);
});

async function open(page: Page, hash: string): Promise<void> {
  await page.goto(`/${hash}`);
}

async function selectTab(page: Page, name: string): Promise<void> {
  await page.getByRole('tab', { name, exact: true }).click();
}

async function panel(page: Page, name: string) {
  return page.getByRole('tabpanel', { name });
}

async function installMockHooks(page: Page): Promise<void> {
  await page.evaluate(async () => {
    const modulePath = '/src/lib/databaseMock.ts';
    const mod = (await import(modulePath)) as { mockDatabaseApi: Record<string, unknown> };
    const target = window as unknown as {
      __dbMock: Record<string, unknown>;
      __dbOriginal: Record<string, unknown>;
    };
    target.__dbMock = mod.mockDatabaseApi;
    target.__dbOriginal = { ...mod.mockDatabaseApi };
  });
}

async function refresh(page: Page): Promise<void> {
  await page.evaluate(() => window.dispatchEvent(new Event('devstack:refresh')));
}

async function activateService(page: Page, id: string): Promise<void> {
  await page.evaluate(async (serviceID) => {
    const modulePath = '/src/lib/mock.ts';
    const mod = (await import(modulePath)) as {
      mockApi: {
        listServices: () => Promise<Array<{ id: string; installed: boolean }>>;
        installService: (value: string) => Promise<void>;
        activateService: (value: string) => Promise<void>;
      };
    };
    const service = (await mod.mockApi.listServices()).find((entry) => entry.id === serviceID);
    if (!service?.installed) await mod.mockApi.installService(serviceID);
    await mod.mockApi.activateService(serviceID);
  }, id);
}

async function expectNoSecret(page: Page, secret = PASSWORD): Promise<void> {
  expect(page.url()).not.toContain(secret);
  const snapshot = await page.evaluate(() => ({
    body: document.body.innerText,
    local: { ...localStorage },
    session: { ...sessionStorage },
  }));
  expect(snapshot.body).not.toContain(secret);
  expect(JSON.stringify(snapshot.local)).not.toContain(secret);
  expect(JSON.stringify(snapshot.session)).not.toContain(secret);
}

async function stubClipboard(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const writes: string[] = [];
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: async (value: string) => {
          writes.push(value);
          (window as unknown as { __clipboardWrites: string[] }).__clipboardWrites = writes;
        },
      },
    });
  });
}

function databaseRow(scope: Locator, name: string): Locator {
  return scope.locator('tbody tr').filter({ hasText: name }).first();
}

function backupRow(scope: Locator, id: string): Locator {
  return scope
    .getByRole('button', { name: `Delete app backup ${id}` })
    .locator('xpath=ancestor::tr');
}

test('MySQL exposes Databases, Users and Backups with seeded inventory', async ({ page }) => {
  await open(page, MYSQL);

  await expect(page.getByRole('heading', { name: 'MySQL 8.4' })).toBeVisible();
  await expect(page.getByRole('tab')).toHaveCount(3);
  await expect(page.getByRole('tab', { name: 'Password', exact: true })).toHaveCount(0);

  const databases = await panel(page, 'Databases');
  await expect(databases.locator('tbody tr')).toHaveCount(3);
  const app = databaseRow(databases, 'app');
  await expect(app.locator('.db-name')).toHaveText('app');
  await expect(app.getByRole('button', { name: 'Drop app' })).toBeEnabled();
  const blog = databaseRow(databases, 'blog');
  await expect(blog.getByRole('link', { name: 'Open blog' })).toBeVisible();
  const devstack = databaseRow(databases, 'devstack');
  await expect(devstack.getByText('Protected', { exact: true })).toBeVisible();
  for (const action of ['Drop devstack', 'Back up devstack', 'Export devstack', 'Import devstack']) {
    await expect(devstack.getByRole('button', { name: action })).toBeDisabled();
  }

  await selectTab(page, 'Users');
  const users = await panel(page, 'Users');
  await expect(users.locator('tbody tr')).toHaveCount(2);
  const appUser = databaseRow(users, 'app');
  await expect(appUser.getByText('Managed', { exact: true })).toBeVisible();
  await expect(appUser.getByText('read/write + schema on app')).toBeVisible();
  await expect(appUser.getByRole('button', { name: 'Reveal Password for app' })).toBeVisible();
  const external = databaseRow(users, 'ext');
  await expect(external.getByText('External', { exact: true })).toBeVisible();
  await expect(external.getByText('Not managed', { exact: true })).toBeVisible();
  await expect(external.getByRole('button', { name: 'Set password for ext' })).toBeVisible();

  await selectTab(page, 'Backups');
  const backups = await panel(page, 'Backups');
  const backup = backupRow(backups, 'bkp_1');
  await expect(backup).toContainText('app');
  await expect(backup).toContainText('manual');
  await expect(backup).toContainText('ready');
  await expect(backup.getByRole('button', { name: 'Download app backup bkp_1' })).toBeVisible();
  await expect(backup.getByRole('button', { name: 'Restore app backup bkp_1' })).toBeVisible();
  await expect(backup.getByRole('button', { name: 'Delete app backup bkp_1' })).toBeVisible();

  await expectNoSecret(page);
});

test('PostgreSQL renames with a typed confirmation and protected actions stay disabled', async ({
  page,
}) => {
  await open(page, POSTGRES);

  await expect(page.getByRole('heading', { name: 'PostgreSQL 18' })).toBeVisible();
  await expect(page.getByRole('tab')).toHaveCount(3);
  const databases = await panel(page, 'Databases');
  const app = databaseRow(databases, 'app');
  await expect(app.getByRole('button', { name: 'Rename app' })).toBeDisabled();

  await activateService(page, 'postgresql');
  await refresh(page);
  await expect(app.getByRole('button', { name: 'Rename app' })).toBeEnabled();

  await app.getByRole('button', { name: 'Rename app' }).click();
  const dialog = page.getByRole('dialog', { name: 'Rename app' });
  await expect(dialog).toBeVisible();
  await dialog.locator('#database-name').fill('e2e_pg_renamed');
  await dialog.locator('#database-rename-confirm').fill('wrong');
  const submit = dialog.getByRole('button', { name: 'Rename database' });
  await expect(submit).toBeDisabled();
  await expect(dialog.getByText('The typed name does not match the current database name.')).toBeVisible();
  await dialog.locator('#database-rename-confirm').fill('app');
  await expect(submit).toBeEnabled();
  await submit.click();

  await expect(page.getByRole('status').filter({ hasText: 'Database renamed to e2e_pg_renamed' })).toBeVisible();
  await expect(databaseRow(databases, 'e2e_pg_renamed').locator('.db-name')).toHaveText('e2e_pg_renamed');
});

test('create and drop database validate names, require typed confirmation and honour backup-first', async ({
  page,
}) => {
  await open(page, MYSQL);
  const databases = await panel(page, 'Databases');

  await databases.getByRole('button', { name: 'Create database' }).click();
  const createDialog = page.getByRole('dialog', { name: 'Create database' });
  await expect(createDialog).toBeVisible();
  await createDialog.getByRole('button', { name: 'Create database' }).click();
  await expect(createDialog.getByText(/Use letters, numbers and underscores/)).toBeVisible();
  await createDialog.locator('#database-name').fill('bad name');
  await createDialog.getByRole('button', { name: 'Create database' }).click();
  await expect(createDialog.getByText(/Use letters, numbers and underscores/)).toBeVisible();
  await createDialog.locator('#database-name').fill('e2e_created');
  await createDialog.getByRole('button', { name: 'Create database' }).click();

  await expect(page.getByRole('status').filter({ hasText: 'Database e2e_created created' })).toBeVisible();
  const created = databaseRow(databases, 'e2e_created');
  await expect(created.locator('.db-name')).toHaveText('e2e_created');

  await created.getByRole('button', { name: 'Drop e2e_created' }).click();
  const dropDialog = page.getByRole('dialog', { name: 'Drop database e2e_created?' });
  const drop = dropDialog.getByRole('button', { name: 'Drop database' });
  await expect(drop).toBeDisabled();
  await dropDialog.getByRole('textbox').fill('wrong');
  await expect(drop).toBeDisabled();
  await dropDialog.getByRole('textbox').fill('e2e_created');
  await expect(drop).toBeEnabled();
  const backupFirst = dropDialog.getByRole('switch', { name: 'Back up first' });
  await expect(backupFirst).toHaveAttribute('aria-checked', 'true');
  await backupFirst.click();
  await expect(backupFirst).toHaveAttribute('aria-checked', 'false');
  await drop.click();

  await expect(page.getByRole('status').filter({ hasText: 'Database e2e_created dropped' })).toBeVisible();
  await expect(databaseRow(databases, 'e2e_created')).toHaveCount(0);
});

test('a linked database cannot be dropped until its linked site is stopped', async ({ page }) => {
  await open(page, MYSQL);
  const databases = await panel(page, 'Databases');
  const blog = databaseRow(databases, 'blog');

  await blog.getByRole('button', { name: 'Drop blog' }).click();
  const dialog = page.getByRole('dialog', { name: 'Drop database blog?' });
  await expect(dialog.getByText('Linked sites must be stopped before this operation.')).toBeVisible();
  await expect(dialog.getByText('Running', { exact: true })).toBeVisible();
  const drop = dialog.getByRole('button', { name: 'Drop database' });
  await dialog.getByRole('textbox').fill('blog');
  await expect(drop).toBeDisabled();

  await dialog.getByRole('button', { name: 'Stop site blog' }).click();
  await expect(dialog.getByText('Stopped', { exact: true })).toBeVisible();
  await expect(drop).toBeEnabled();
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).toBeHidden();
});

test('users can be created, edited and deleted with grants and typed confirmation', async ({
  page,
}) => {
  await open(page, MYSQL);
  await selectTab(page, 'Users');
  const users = await panel(page, 'Users');

  await users.getByRole('button', { name: 'Create user' }).click();
  const createDialog = page.getByRole('dialog', { name: 'Create database user' });
  await expect(createDialog).toBeVisible();
  await createDialog.locator('#user-username').fill('e2e_user');
  await expect(createDialog.locator('#user-host')).toHaveValue('%');
  await createDialog.getByLabel('Grant 1 preset').selectOption('read_only');
  await createDialog.getByRole('button', { name: 'Create user' }).click();

  await expect(page.getByRole('status').filter({ hasText: 'User e2e_user created' })).toBeVisible();
  const created = databaseRow(users, 'e2e_user');
  await expect(created.getByText('Managed', { exact: true })).toBeVisible();
  await expect(created).toContainText('read-only on app');

  await created.getByRole('button', { name: 'Edit e2e_user' }).click();
  const editDialog = page.getByRole('dialog', { name: 'Edit e2e_user' });
  const enabled = editDialog.getByRole('switch', { name: 'Enabled' });
  await expect(enabled).toHaveAttribute('aria-checked', 'true');
  await enabled.click();
  await editDialog.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'User e2e_user updated' })).toBeVisible();
  await expect(created.getByText('Disabled', { exact: true })).toBeVisible();

  await created.getByRole('button', { name: 'Delete e2e_user' }).click();
  const deleteDialog = page.getByRole('dialog', { name: 'Delete user e2e_user@%?' });
  const deleteButton = deleteDialog.getByRole('button', { name: 'Delete user' });
  await expect(deleteButton).toBeDisabled();
  await deleteDialog.getByRole('textbox').fill('wrong');
  await expect(deleteButton).toBeDisabled();
  await deleteDialog.getByRole('textbox').fill('e2e_user');
  await expect(deleteButton).toBeEnabled();
  await deleteButton.click();
  await expect(page.getByRole('status').filter({ hasText: 'User e2e_user deleted' })).toBeVisible();
  await expect(databaseRow(users, 'e2e_user')).toHaveCount(0);
});

test('password reveal, copy, hide, reset and metadata-only password state stay masked', async ({
  page,
}) => {
  await stubClipboard(page);
  await open(page, MYSQL);
  await selectTab(page, 'Users');
  const users = await panel(page, 'Users');
  const app = databaseRow(users, 'app');
  const external = databaseRow(users, 'ext');

  await expect(app.getByText('••••••••')).toBeVisible();
  await expectNoSecret(page);
  await app.getByRole('button', { name: 'Reveal Password for app' }).click();
  await expect(app.getByText(PASSWORD)).toBeVisible();
  await app.getByRole('button', { name: 'Copy Password for app' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Password copied' })).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { __clipboardWrites: string[] }).__clipboardWrites)).toContain(
    PASSWORD,
  );
  await app.getByRole('button', { name: 'Hide Password for app' }).click();
  await expect(app.getByText('••••••••')).toBeVisible();
  await expectNoSecret(page);

  await external.getByRole('button', { name: 'Set password for ext' }).click();
  const secretDialog = page.getByRole('dialog', { name: 'Password for ext' });
  await expect(secretDialog.getByText(PASSWORD)).toBeVisible();
  await expect(page.getByRole('status').filter({ hasText: 'Password set for ext' })).toBeVisible();
  await secretDialog.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(secretDialog).toBeHidden();
  await expectNoSecret(page);
});

test('linked users show their site and metadata-only links never expose a password', async ({
  page,
}) => {
  await open(page, MYSQL);
  await installMockHooks(page);
  await page.evaluate(async () => {
    const api = (window as unknown as { __dbMock: Record<string, (...args: unknown[]) => Promise<unknown>> })
      .__dbMock;
    await api.linkSiteDatabase('s_blog', {
      service_id: 'mysql',
      database: 'app',
      user_id: 'usr_ext',
    });
  });

  await selectTab(page, 'Users');
  await refresh(page);
  const users = await panel(page, 'Users');
  const external = databaseRow(users, 'ext');
  await expect(external.getByRole('link').first()).toBeVisible();
  await expect(external.getByRole('button', { name: 'Delete ext' })).toBeDisabled();
  await expect(external.getByText(/Unlink the user from its sites first/)).toHaveCount(0);
  await expect(external.getByRole('button', { name: 'Unlink sites' })).toBeVisible();

  await page.evaluate(() => {
    window.location.hash = '#/sites/blog';
  });
  await expect(page.getByRole('heading', { name: 'blog' })).toBeVisible();
  const card = page.locator('article[aria-label="Database app"]');
  await expect(card.getByText('Password not managed')).toBeVisible();
  await expect(card.getByText(/External password entry is not available in v1/)).toBeVisible();
  await expect(card.getByRole('button', { name: /Reveal password/ })).toHaveCount(0);
  await expectNoSecret(page);
});

test('missing engine objects and missing linked users render stale-data states without recreating them', async ({
  page,
}) => {
  await open(page, MYSQL);
  await installMockHooks(page);
  await page.evaluate(() => {
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    api.listServiceDatabases = async () => ({
      databases: [
        {
          name: 'app',
          service_id: 'mysql',
          engine: 'mysql',
          size_bytes: 0,
          table_count: 0,
          linked_sites: [{ id: 's_blog', name: 'blog' }],
          managed: true,
          missing: true,
        },
      ],
    });
  });
  await refresh(page);
  const databases = await panel(page, 'Databases');
  const app = databaseRow(databases, 'app');
  await expect(app.getByText('Missing on engine', { exact: true })).toBeVisible();
  await expect(app.getByRole('button', { name: 'Drop app' })).toBeDisabled();
  await expect(app.getByRole('button', { name: 'Back up app' })).toBeDisabled();
  await app.getByRole('button', { name: 'Unlink app' }).click();
  const unlink = page.getByRole('dialog', { name: 'Unlink sites from app' });
  await expect(unlink.getByRole('link', { name: 'Open blog' })).toBeVisible();
  await unlink.getByRole('button', { name: 'Close', exact: true }).click();

  await page.evaluate(() => {
    window.location.hash = '#/sites/blog';
  });
  await expect(page.getByRole('heading', { name: 'blog' })).toBeVisible();
  await page.evaluate(() => {
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    api.listSiteDatabases = async () => ({
      links: [
        {
          id: 'lnk_missing',
          site_id: 's_blog',
          service_id: 'mysql',
          engine: 'mysql',
          database: 'app',
          user_id: 'usr_missing',
          username: 'gone',
          primary: true,
          password_managed: false,
          available: false,
          error: 'DATABASE_USER_NOT_FOUND: database user gone was not found',
          connection: {
            engine: 'mysql',
            host: '127.0.0.1',
            port: 3306,
            database: 'app',
            username: 'gone',
            password_managed: false,
            url: 'mysql://gone@127.0.0.1:3306/app',
            env: {},
          },
        },
      ],
    });
  });
  await refresh(page);
  const linkCard = page.locator('article[aria-label="Database app"]');
  await expect(linkCard.getByText('Offline', { exact: true })).toBeVisible();
  await expect(linkCard.getByText('Database offline.')).toBeVisible();
  await expect(linkCard.getByText(/DATABASE_USER_NOT_FOUND/)).toBeVisible();
});

test('Add Site reviews a new database without a password, then Site Detail reveals and unlinks it', async ({
  page,
}) => {
  await stubClipboard(page);
  await open(page, '#/sites/new');
  await page.locator('#site-name').fill('dbsite');
  await page.getByRole('button', { name: 'Browse' }).click();
  const browser = page.getByRole('dialog', { name: 'Choose a project folder' });
  await browser.getByRole('button', { name: 'scratch', exact: true }).click();
  await browser.getByRole('button', { name: 'Use this folder' }).click();
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByRole('heading', { name: 'How should it run?' })).toBeVisible();
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByRole('heading', { name: 'Domain and options' })).toBeVisible();

  const databaseToggle = page.getByRole('switch', { name: 'Create or link a database' });
  await databaseToggle.click();
  await expect(page.locator('#database-service')).toHaveValue('mysql');
  await page.locator('#database-name').fill('e2e_site_db');
  await page.locator('#database-username').fill('e2e_site_user');
  await page.locator('#database-grant').selectOption('read_only');

  const review = page.locator('.database-review-row');
  await expect(review).toContainText('e2e_site_db on MySQL 8.4');
  await expect(review).toContainText('e2e_site_user · Read-only · 127.0.0.1:3306');
  await expect(review).toContainText('Password will be generated when the site is created.');
  await expect(review).not.toContainText(PASSWORD);
  await expect(review.locator('input[type="password"]')).toHaveCount(0);

  await page.getByRole('button', { name: 'Create site' }).click();
  const createdDialog = page.getByRole('dialog', { name: 'Database created' });
  await expect(createdDialog).toBeVisible();
  await expect(createdDialog.getByRole('region', { name: 'Connection' })).toContainText('e2e_site_db');
  await expect(createdDialog.getByText('••••••••')).toBeVisible();
  await createdDialog.getByRole('button', { name: 'Reveal password' }).click();
  await expect(createdDialog.getByText(PASSWORD)).toBeVisible();
  await createdDialog.getByRole('button', { name: 'Copy password' }).click();
  expect(await page.evaluate(() => (window as unknown as { __clipboardWrites: string[] }).__clipboardWrites)).toContain(
    PASSWORD,
  );
  await createdDialog.getByRole('button', { name: 'Continue' }).click();
  await page.waitForURL(/#\/sites\/s_/);
  await expect(page.getByRole('heading', { name: 'dbsite' })).toBeVisible();
  // The mock flips a new site to running shortly after creation; wait for that
  // refresh so the reveal/hide assertions are not racing a devstack:refresh.
  await expect(page.getByLabel('Status: Running')).toBeVisible({ timeout: 10_000 });
  const siteURL = page.url();

  const card = page.locator('article[aria-label="Database e2e_site_db"]');
  await expect(card).toBeVisible();
  await expect(card.getByText('Primary', { exact: true })).toBeVisible();
  await expect(card.getByText('Password managed', { exact: true })).toBeVisible();
  await expect(card.getByText('••••••••')).toBeVisible();
  await card.getByRole('button', { name: 'Reveal password for e2e_site_db' }).click();
  await expect(card.getByText(PASSWORD)).toBeVisible();
  await card.getByRole('button', { name: 'Hide password for e2e_site_db' }).click();
  await expectNoSecret(page);

  await page.evaluate(() => {
    window.location.hash = '#/sites/blog';
  });
  await expect(page.getByRole('heading', { name: 'blog' })).toBeVisible();
  await page.evaluate((hash) => {
    window.location.hash = hash;
  }, new URL(siteURL).hash);
  await expect(page.getByRole('heading', { name: 'dbsite' })).toBeVisible();
  await expect(card.getByText('••••••••')).toBeVisible();
  await expectNoSecret(page);

  await card.getByRole('button', { name: 'Unlink e2e_site_db' }).click();
  const unlink = page.getByRole('dialog', { name: 'Unlink e2e_site_db?' });
  await expect(unlink).toContainText('The database, user, grants, password, backups, and data remain.');
  await unlink.getByRole('button', { name: 'Unlink' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Database unlinked' })).toBeVisible();
  await expect(page.getByText('No databases linked')).toBeVisible();
  await expectNoSecret(page);
});

test('Add Site warns about an existing external user without changing grants', async ({ page }) => {
  await open(page, '#/sites/new');
  await page.locator('#site-name').fill('dbwarn');
  await page.getByRole('button', { name: 'Browse' }).click();
  const browser = page.getByRole('dialog', { name: 'Choose a project folder' });
  await browser.getByRole('button', { name: 'scratch', exact: true }).click();
  await browser.getByRole('button', { name: 'Use this folder' }).click();
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByRole('heading', { name: 'Domain and options' })).toBeVisible();

  await page.getByRole('switch', { name: 'Create or link a database' }).click();
  await page.getByLabel('Use existing database').check();
  await page.getByLabel('Use existing user').check();
  await page.locator('#database-user').selectOption('usr_ext');
  const warning = page.getByRole('status').filter({ hasText: 'ext does not have access to app' });
  await expect(warning).toContainText('devstack will not change grants during site creation');
  await expect(page.getByRole('link', { name: 'Manage database user' })).toHaveAttribute(
    'href',
    /#\/services\/mysql\?tab=users&user=usr_ext/,
  );
  await expect(page.getByText(/External password entry is not available in v1/)).toBeVisible();
  const review = page.locator('.database-review-row');
  await expect(review).toContainText('Password not managed.');
  await expect(review).not.toContainText(PASSWORD);
});

test('backups create, list, download and delete with typed confirmation', async ({ page }) => {
  await open(page, MYSQL);
  await selectTab(page, 'Backups');
  const backups = await panel(page, 'Backups');
  await expect(backupRow(backups, 'bkp_1')).toHaveCount(1);

  await backups.getByRole('button', { name: 'Create backup' }).click();
  const createDialog = page.getByRole('dialog', { name: 'Create backup' });
  await expect(createDialog.locator('#backup-database')).toHaveValue('app');
  await createDialog.getByRole('button', { name: 'Create backup' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'backup succeeded' })).toBeVisible();
  const created = backupRow(backups, 'bkp_2');
  await expect(created).toContainText('app');

  await backupRow(backups, 'bkp_1').getByRole('button', { name: 'Download app backup bkp_1' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Downloading app backup' })).toBeVisible();

  await backupRow(backups, 'bkp_1').getByRole('button', { name: 'Delete app backup bkp_1' }).click();
  const deleteDialog = page.getByRole('dialog', { name: 'Delete backup bkp_1?' });
  const deleteButton = deleteDialog.getByRole('button', { name: 'Delete backup' });
  await expect(deleteButton).toBeDisabled();
  await deleteDialog.getByRole('textbox').fill('bkp_2');
  await expect(deleteButton).toBeDisabled();
  await deleteDialog.getByRole('textbox').fill('bkp_1');
  await expect(deleteButton).toBeEnabled();
  await deleteButton.click();
  await expect(page.getByRole('status').filter({ hasText: 'Backup bkp_1 deleted' })).toBeVisible();
  await expect(backupRow(backups, 'bkp_1')).toHaveCount(0);
  await expect(backupRow(backups, 'bkp_2')).toHaveCount(1);
});

test('restore, overwrite, import replace and export use the documented confirmations', async ({
  page,
}) => {
  await open(page, MYSQL);
  await selectTab(page, 'Backups');
  const backups = await panel(page, 'Backups');

  await backupRow(backups, 'bkp_1').getByRole('button', { name: 'Restore app backup bkp_1' }).click();
  const restore = page.getByRole('dialog', { name: 'Restore app backup?' });
  await expect(restore.locator('#restore-target')).toHaveValue(/^app_restore_/);
  await restore.locator('#restore-target').fill('e2e_restored');
  await restore.getByRole('textbox').last().fill('app');
  await restore.getByRole('button', { name: 'Restore backup' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Restore queued for app' })).toBeVisible();
  await expect(page.getByRole('status').filter({ hasText: 'restore succeeded' })).toBeVisible();

  await backupRow(backups, 'bkp_1').getByRole('button', { name: 'Restore app backup bkp_1' }).click();
  const overwrite = page.getByRole('dialog', { name: 'Restore app backup?' });
  await overwrite.getByLabel('Overwrite existing database').check();
  const preRestore = overwrite.getByRole('switch', { name: 'Pre-restore backup' });
  await expect(preRestore).toHaveAttribute('aria-checked', 'true');
  await expect(preRestore).toBeDisabled();
  await overwrite.getByRole('textbox').last().fill('app');
  await overwrite.getByRole('button', { name: 'Restore backup' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'dropped and recreated' })).toBeVisible();

  await selectTab(page, 'Databases');
  const databases = await panel(page, 'Databases');
  const importAction = databaseRow(databases, 'app').getByRole('button', { name: 'Import app' });

  const firstChooser = page.waitForEvent('filechooser');
  await importAction.click();
  await (await firstChooser).setFiles({
    name: 'seed.sql',
    mimeType: 'application/sql',
    buffer: Buffer.from('CREATE TABLE e2e_marker (id INT);'),
  });
  const importDialog = page.getByRole('dialog', { name: 'Import into app' });
  await expect(importDialog.getByText('seed.sql')).toBeVisible();
  await importDialog.getByRole('button', { name: 'Import database' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Import queued for app' })).toBeVisible();

  const secondChooser = page.waitForEvent('filechooser');
  await importAction.click();
  await (await secondChooser).setFiles({
    name: 'seed.sql.gz',
    mimeType: 'application/gzip',
    buffer: Buffer.from('not-really-gzip'),
  });
  const replaceDialog = page.getByRole('dialog', { name: 'Import into app' });
  await replaceDialog.getByLabel('Replace contents').check();
  const importButton = replaceDialog.getByRole('button', { name: 'Import database' });
  await expect(importButton).toBeDisabled();
  await expect(replaceDialog.getByRole('switch', { name: 'Back up first' })).toBeDisabled();
  await replaceDialog.locator('#import-confirm').fill('app');
  await expect(importButton).toBeEnabled();
  await importButton.click();
  await expect(page.getByRole('status').filter({ hasText: 'Replace mode drops and recreates' })).toBeVisible();

  await databaseRow(databases, 'app').getByRole('button', { name: 'Export app' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Exporting app' })).toBeVisible();
});

test('job progress, failure, cancellation and refresh recovery use persisted job state', async ({
  page,
}) => {
  await open(page, MYSQL);
  await installMockHooks(page);

  await page.evaluate(() => {
    const api = (window as unknown as { __dbMock: Record<string, (...args: unknown[]) => Promise<unknown>> })
      .__dbMock;
    let polls = 0;
    const stamp = new Date().toISOString();
    api.createServiceBackup = async () => ({
      id: 'job_progress',
      service_id: 'mysql',
      database: 'app',
      kind: 'backup',
      status: 'running',
      progress: 35,
      message: 'Dumping tables',
      created_at: stamp,
      updated_at: stamp,
    });
    api.getDatabaseJob = async () => {
      polls += 1;
      return {
        id: 'job_progress',
        service_id: 'mysql',
        database: 'app',
        kind: 'backup',
        status: polls < 2 ? 'running' : 'succeeded',
        progress: polls < 2 ? 70 : 100,
        message: polls < 2 ? 'Dumping tables' : 'Backup ready',
        created_at: stamp,
        updated_at: stamp,
      };
    };
  });

  const databases = await panel(page, 'Databases');
  await databaseRow(databases, 'app').getByRole('button', { name: 'Back up app' }).click();
  const jobCard = page.getByRole('status', { name: 'Database job' });
  await expect(jobCard).toContainText('backup · app');
  await expect(jobCard).toContainText('running');
  await expect(jobCard.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '35');
  await expect(page.getByRole('status').filter({ hasText: 'backup succeeded' })).toBeVisible();
  await expect(jobCard).toContainText('succeeded');
  await expect(jobCard.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '100');

  await page.evaluate(() => {
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    const stamp = new Date().toISOString();
    api.createServiceBackup = async () => ({
      id: 'job_failed',
      service_id: 'mysql',
      database: 'app',
      kind: 'backup',
      status: 'failed',
      progress: 100,
      message: 'Disk full',
      created_at: stamp,
      updated_at: stamp,
    });
  });
  await databaseRow(databases, 'app').getByRole('button', { name: 'Back up app' }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'backup failed' })).toBeVisible();
  await expect(jobCard).toContainText('failed');

  await page.evaluate(() => {
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    const stamp = new Date().toISOString();
    api.createServiceBackup = async () => ({
      id: 'job_cancel',
      service_id: 'mysql',
      database: 'app',
      kind: 'backup',
      status: 'running',
      progress: 10,
      created_at: stamp,
      updated_at: stamp,
    });
  });
  await databaseRow(databases, 'app').getByRole('button', { name: 'Back up app' }).click();
  await expect(jobCard).toContainText('running');
  await jobCard.getByRole('button', { name: 'Cancel job' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Job canceled' })).toBeVisible();
  await expect(jobCard).toContainText('canceled');

  await page.evaluate(async () => {
    const modulePath = '/src/lib/databaseMock.ts';
    const mod = (await import(modulePath)) as {
      seedMockDatabaseJobForTests: (job: Record<string, unknown>) => void;
    };
    const stamp = new Date().toISOString();
    const failed = {
      id: 'job_recover',
      service_id: 'mysql',
      database: 'app',
      kind: 'restore',
      status: 'failed',
      progress: 100,
      backup_id: 'bkp_1',
      message: 'Restore failed',
      created_at: stamp,
      updated_at: stamp,
    };
    mod.seedMockDatabaseJobForTests(failed);
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    api.restoreServiceBackup = async () => failed;
  });
  await selectTab(page, 'Backups');
  const backups = await panel(page, 'Backups');
  await backupRow(backups, 'bkp_1').getByRole('button', { name: 'Restore app backup bkp_1' }).click();
  const restore = page.getByRole('dialog', { name: 'Restore app backup?' });
  await restore.getByRole('textbox').last().fill('app');
  await restore.getByRole('button', { name: 'Restore backup' }).click();
  await expect(page.getByText('Restore incomplete.')).toBeVisible();
  expect(
    await page.evaluate(() => sessionStorage.getItem('devstack.database.recovery.mysql')),
  ).toBe('job_recover');

  await page.reload();
  await expect(page.getByRole('heading', { name: 'MySQL 8.4' })).toBeVisible();
  await expect(page.getByText('Restore incomplete.')).toBeVisible();
  await page.getByRole('button', { name: 'Restore pre-operation backup' }).click();
  const recovery = page.getByRole('dialog', { name: 'Restore pre-operation backup for app?' });
  await recovery.getByRole('textbox').fill('app');
  await recovery.getByRole('button', { name: 'Restore backup' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'restore succeeded' })).toBeVisible();
});

test('Redis exposes only Users and Password, and ACL persistence requires confirmation', async ({
  page,
}) => {
  await open(page, REDIS);
  await expect(page.getByRole('heading', { name: 'Redis 8.8' })).toBeVisible();
  await expect(page.getByRole('tab')).toHaveCount(2);
  await expect(page.getByRole('tab', { name: 'Databases', exact: true })).toHaveCount(0);
  await expect(page.getByRole('tab', { name: 'Backups', exact: true })).toHaveCount(0);

  await activateService(page, 'redis');
  await refresh(page);

  const users = await panel(page, 'Users');
  await expect(users.getByRole('heading', { name: 'ACL users' })).toBeVisible();
  const defaultUser = databaseRow(users, 'default');
  await expect(defaultUser.getByText('Protected', { exact: true })).toBeVisible();
  await expect(defaultUser.getByRole('button', { name: 'Delete default' })).toBeDisabled();
  await expect(defaultUser.getByRole('button', { name: 'Edit default' })).toBeDisabled();

  const enable = page.getByRole('button', { name: 'Enable ACL persistence' });
  await expect(enable).toBeEnabled();
  await enable.click();
  const aclDialog = page.getByRole('dialog', { name: 'Enable Redis ACL persistence?' });
  await expect(aclDialog).toContainText('restarts Redis once');
  await aclDialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(aclDialog).toBeHidden();
  await expect(page.getByRole('status').filter({ hasText: 'Redis ACL persistence enabled' })).toHaveCount(0);

  await enable.click();
  await page
    .getByRole('dialog', { name: 'Enable Redis ACL persistence?' })
    .getByRole('button', { name: 'Enable and restart' })
    .click();
  await expect(page.getByRole('status').filter({ hasText: 'Redis ACL persistence enabled' })).toBeVisible();

  await selectTab(page, 'Password');
  await expect(page.getByText('Redis has no logical database management.')).toBeVisible();
  const passwordPanel = await panel(page, 'Password');
  await expect(passwordPanel.getByRole('heading', { name: 'Default password' })).toBeVisible();
  await passwordPanel.getByRole('button', { name: 'Reveal default password' }).click();
  await expect(passwordPanel.getByText(PASSWORD)).toBeVisible();
  await passwordPanel.getByRole('button', { name: 'Hide default password' }).click();
  await expect(passwordPanel.getByText('••••••••')).toBeVisible();
  await passwordPanel.getByRole('button', { name: 'Reset default password' }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Redis default password reset' })).toBeVisible();
  await passwordPanel.getByRole('button', { name: 'Hide default password' }).click();
  await expect(passwordPanel.getByText('••••••••')).toBeVisible();
  await expectNoSecret(page);

  await selectTab(page, 'Users');
  await selectTab(page, 'Password');
  await expect(passwordPanel.getByText('••••••••')).toBeVisible();
  await expectNoSecret(page);
});

test('loading, empty, error, retry, mutation failure and overlapping responses are deterministic', async ({
  page,
}) => {
  await open(page, '');
  await installMockHooks(page);

  await page.evaluate(() => {
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    const delayedEmpty = async () => {
      await new Promise((resolve) => setTimeout(resolve, 300));
      return { databases: [] };
    };
    api.listServiceDatabases = delayedEmpty;
    api.listServiceUsers = async () => {
      await new Promise((resolve) => setTimeout(resolve, 300));
      return { users: [] };
    };
    api.listServiceBackups = async () => {
      await new Promise((resolve) => setTimeout(resolve, 300));
      return { backups: [] };
    };
  });
  await page.evaluate(() => {
    window.location.hash = '#/services/mysql';
  });
  const databases = await panel(page, 'Databases');
  await expect(databases.getByText('Loading databases…')).toBeVisible();
  await expect(databases.getByText('No databases yet')).toBeVisible();
  await selectTab(page, 'Users');
  const users = await panel(page, 'Users');
  await expect(users.getByText('No users yet')).toBeVisible();
  await selectTab(page, 'Backups');
  const backups = await panel(page, 'Backups');
  await expect(backups.getByText('No backups yet')).toBeVisible();

  await page.evaluate(() => {
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    const original = (window as unknown as { __dbOriginal: Record<string, unknown> }).__dbOriginal;
    api.listServiceDatabases = original.listServiceDatabases;
    api.listServiceUsers = original.listServiceUsers;
    api.listServiceBackups = original.listServiceBackups;
  });
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await selectTab(page, 'Databases');
  await expect(databaseRow(await panel(page, 'Databases'), 'app')).toHaveCount(1);

  await page.evaluate(async () => {
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    const modulePath = '/src/lib/errors.ts';
    const mod = (await import(modulePath)) as {
      ApiError: new (code: string, message: string, status: number) => Error;
    };
    api.listServiceDatabases = async () => {
      throw new mod.ApiError('DATABASE_UNAVAILABLE', 'The database inventory is unavailable.', 503);
    };
  });
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  const errorBanner = page.getByRole('alert').filter({ hasText: 'Could not load databases' });
  await expect(errorBanner).toBeVisible();
  await expect(errorBanner).toContainText('DATABASE_UNAVAILABLE');
  await page.evaluate(() => {
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    const original = (window as unknown as { __dbOriginal: Record<string, unknown> }).__dbOriginal;
    api.listServiceDatabases = original.listServiceDatabases;
  });
  await errorBanner.getByRole('button', { name: 'Retry' }).click();
  await expect(databaseRow(await panel(page, 'Databases'), 'app')).toHaveCount(1);

  await page.evaluate(() => {
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    let calls = 0;
    api.listServiceDatabases = async () => {
      calls += 1;
      const call = calls;
      if (call === 1) {
        (window as unknown as { __firstListStarted: boolean }).__firstListStarted = true;
      }
      await new Promise((resolve) => setTimeout(resolve, call === 1 ? 700 : 40));
      return {
        databases: [
          {
            name: call === 1 ? 'stale_db' : 'fresh_db',
            service_id: 'mysql',
            engine: 'mysql',
            size_bytes: 0,
            table_count: 0,
            linked_sites: [],
            managed: true,
          },
        ],
      };
    };
  });
  const refreshButton = page.getByRole('button', { name: 'Refresh', exact: true });
  await refreshButton.click();
  await page.waitForFunction(
    () => (window as unknown as { __firstListStarted?: boolean }).__firstListStarted === true,
  );
  await refreshButton.click();
  await expect(databaseRow(await panel(page, 'Databases'), 'fresh_db')).toHaveCount(1);
  await expect(databaseRow(await panel(page, 'Databases'), 'stale_db')).toHaveCount(0);

  await page.evaluate(async () => {
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    const modulePath = '/src/lib/errors.ts';
    const mod = (await import(modulePath)) as {
      ApiError: new (code: string, message: string, status: number) => Error;
    };
    api.createServiceDatabase = async () => {
      throw new mod.ApiError('DATABASE_CONFLICT', 'Database already exists.', 409);
    };
  });
  await page.evaluate(() => {
    const api = (window as unknown as { __dbMock: Record<string, unknown> }).__dbMock;
    const original = (window as unknown as { __dbOriginal: Record<string, unknown> }).__dbOriginal;
    api.listServiceDatabases = original.listServiceDatabases;
  });
  await refresh(page);
  await (await panel(page, 'Databases')).getByRole('button', { name: 'Create database' }).click();
  const createDialog = page.getByRole('dialog', { name: 'Create database' });
  await createDialog.locator('#database-name').fill('e2e_conflict');
  await createDialog.getByRole('button', { name: 'Create database' }).click();
  await expect(createDialog.getByText('Database already exists.')).toBeVisible();
  await expect(createDialog).toBeVisible();
  await createDialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(databaseRow(await panel(page, 'Databases'), 'e2e_conflict')).toHaveCount(0);
});

test('tabs, dialogs, skip link and focus restoration are keyboard accessible', async ({
  page,
  browserName,
}) => {
  await open(page, MYSQL);
  await expect(page.getByRole('heading', { name: 'MySQL 8.4' })).toBeVisible();

  const skip = page.getByRole('link', { name: 'Skip to content' });
  if (browserName === 'webkit') {
    // WebKit's default tab order skips links.
    await skip.focus();
  } else {
    await page.keyboard.press('Tab');
  }
  await expect(skip).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.locator('#main')).toBeFocused();

  const databasesTab = page.getByRole('tab', { name: 'Databases', exact: true });
  await databasesTab.focus();
  await page.keyboard.press('ArrowRight');
  const usersTab = page.getByRole('tab', { name: 'Users', exact: true });
  await expect(usersTab).toHaveAttribute('aria-selected', 'true');
  await expect(usersTab).toBeFocused();
  await page.keyboard.press('End');
  const backupsTab = page.getByRole('tab', { name: 'Backups', exact: true });
  await expect(backupsTab).toHaveAttribute('aria-selected', 'true');
  await expect(backupsTab).toBeFocused();
  await page.keyboard.press('Home');
  await expect(databasesTab).toHaveAttribute('aria-selected', 'true');
  await expect(databasesTab).toBeFocused();
  await page.keyboard.press('ArrowLeft');
  await expect(backupsTab).toHaveAttribute('aria-selected', 'true');

  await selectTab(page, 'Databases');
  const createButton = (await panel(page, 'Databases')).getByRole('button', { name: 'Create database' });
  await createButton.focus();
  await createButton.press('Enter');
  const dialog = page.getByRole('dialog', { name: 'Create database' });
  await expect(dialog).toBeVisible();
  await expect.poll(() => dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true);
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(createButton).toBeFocused();

  const dropButton = databaseRow(await panel(page, 'Databases'), 'app').getByRole('button', {
    name: 'Drop app',
  });
  await dropButton.focus();
  await dropButton.press('Enter');
  const dropDialog = page.getByRole('dialog', { name: 'Drop database app?' });
  await expect(dropDialog).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dropDialog).toBeHidden();
  await expect(dropButton).toBeFocused();
});

test('service and site database cards stay usable at desktop, tablet and mobile widths', async ({
  page,
}) => {
  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 768, height: 1024 },
    { width: 390, height: 844 },
  ]) {
    await page.setViewportSize(viewport);
    await open(page, MYSQL);
    await expect(page.getByRole('heading', { name: 'MySQL 8.4' })).toBeVisible();
    for (const tab of ['Databases', 'Users', 'Backups']) {
      await expect(page.getByRole('tab', { name: tab, exact: true })).toBeVisible();
    }
    const width = await page.evaluate(() => ({
      viewport: window.innerWidth,
      document: document.documentElement.scrollWidth,
      body: document.body.scrollWidth,
    }));
    expect(width.document, `service detail ${viewport.width}px overflow`).toBeLessThanOrEqual(
      width.viewport,
    );
    expect(width.body, `service detail ${viewport.width}px body overflow`).toBeLessThanOrEqual(
      width.viewport,
    );

    await open(page, '#/sites/blog');
    await expect(page.getByRole('heading', { name: 'blog' })).toBeVisible();
    await expect(page.getByText('No databases linked')).toBeVisible();
    const siteWidth = await page.evaluate(() => ({
      viewport: window.innerWidth,
      document: document.documentElement.scrollWidth,
      body: document.body.scrollWidth,
    }));
    expect(siteWidth.document, `site detail ${viewport.width}px overflow`).toBeLessThanOrEqual(
      siteWidth.viewport,
    );
    expect(siteWidth.body, `site detail ${viewport.width}px body overflow`).toBeLessThanOrEqual(
      siteWidth.viewport,
    );
  }
});
