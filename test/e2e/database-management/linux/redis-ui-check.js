// Live Redis dashboard tab check for cycle 7. The disposable daemon runs with
// dashboard auth disabled; no token is read or written.
const { chromium } = require('playwright');

const base = process.env.REDIS_UI_URL || 'http://127.0.0.1:27170/';
const outDir = process.env.REDIS_UI_OUT || '.';
const users = (process.env.REDIS_UI_USERS || 'e2e_cache_ro,e2e_cache_full').split(',').filter(Boolean);

(async () => {
  const browser = await chromium.launch({ headless: true });
  const result = {
    url: base,
    auth: process.env.REDIS_UI_TOKEN ? 'token' : 'disabled',
    tabs: [],
    users: [],
    usersTabVisible: false,
    passwordTabVisible: false,
    forbiddenTabs: [],
    consoleErrors: [],
    screenshot: '',
  };
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    const page = await context.newPage();
    page.on('console', (msg) => { if (msg.type() === 'error') result.consoleErrors.push(msg.text().slice(0, 200)); });
    await page.goto(`${base}#/services/redis`, { waitUntil: 'domcontentloaded', timeout: 30000 });
    await page.waitForSelector('[role="tablist"][aria-label="Database management"]', { timeout: 30000 });
    if (users.length > 0) {
      await page.waitForFunction((user) => document.body.innerText.includes(user), users[0], { timeout: 30000 }).catch(() => {});
    }
    result.tabs = await page.locator('[role="tab"]').allInnerTexts();
    result.usersTabVisible = result.tabs.some((label) => label.trim() === 'Users');
    result.passwordTabVisible = result.tabs.some((label) => label.trim() === 'Password');
    result.forbiddenTabs = result.tabs.filter((label) => ['Databases', 'Backups'].includes(label.trim()));
    const body = await page.locator('body').innerText();
    result.users = users.filter((user) => body.includes(user));
    const shot = `${outDir}/redis-service-tabs.png`;
    await page.screenshot({ path: shot, fullPage: true });
    result.screenshot = shot;
    const passwordTab = page.getByRole('tab', { name: 'Password', exact: true });
    if (await passwordTab.count()) {
      await passwordTab.click();
      await page.waitForTimeout(500);
      result.passwordPanel = await page.locator('[role="tabpanel"]').innerText();
      const passwordShot = `${outDir}/redis-password-tab.png`;
      await page.screenshot({ path: passwordShot, fullPage: true });
      result.passwordScreenshot = passwordShot;
    }
    await context.close();
  } catch (err) {
    result.error = err.message.slice(0, 300);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
  console.log(JSON.stringify(result, null, 2));
})();
