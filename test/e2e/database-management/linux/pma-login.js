// Live phpMyAdmin login matrix for the disposable Linux cycle 7 environment.
// Credentials come only from PMA_PASS_FILE (preferred) or PMA_PASS. The script
// prints metadata only and never logs the password.
const fs = require('fs');
const { chromium } = require('playwright');

const base = process.env.PMA_URL || 'http://127.0.0.1:8082/';
const user = process.env.PMA_USER || 'devstack';
const outDir = process.env.PMA_OUT || '.';
const phase = process.env.PMA_PHASE || 'login';
const expect = process.env.PMA_EXPECT || 'login';
let pass = process.env.PMA_PASS || '';
if (!pass && process.env.PMA_PASS_FILE) {
  pass = fs.readFileSync(process.env.PMA_PASS_FILE, 'utf8').trim();
}

function fail(message) {
  console.error(message);
  process.exitCode = 1;
}

async function login(page, password) {
  await page.goto(base, { waitUntil: 'domcontentloaded', timeout: 30000 });
  await page.fill('input[name="pma_username"]', user);
  await page.fill('input[name="pma_password"]', password);
  await Promise.all([
    page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {}),
    page.click('#input_go'),
  ]);
}

function connectionErrorText(body) {
  const match = body.match(/(#2002[^\n]*|#1045[^\n]*|Connection refused[^\n]*|Cannot log in[^\n]*|mysqli::real_connect[^\n]*|server is not responding[^\n]*)/i);
  return match ? match[1].trim().slice(0, 200) : '';
}

(async () => {
  if (!pass) {
    fail('PMA_PASS/PMA_PASS_FILE missing');
    return;
  }
  const browser = await chromium.launch({ headless: true });
  const result = {
    phase,
    expect,
    url: base,
    login: false,
    dashboard: false,
    logout: false,
    serverVersion: '',
    wrongPasswordRejected: false,
    wrongPasswordText: '',
    connectionError: false,
    connectionErrorText: '',
    httpStatus: 0,
    elapsedMs: 0,
    consoleErrors: [],
    screenshots: [],
  };
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    const page = await context.newPage();
    page.on('console', (msg) => { if (msg.type() === 'error') result.consoleErrors.push(msg.text().slice(0, 200)); });

    const started = Date.now();
    await login(page, pass);
    result.elapsedMs = Date.now() - started;
    result.httpStatus = 200;
    const body = await page.locator('body').innerText();
    result.connectionErrorText = connectionErrorText(body);
    result.connectionError = result.connectionErrorText !== '';
    result.login = await page.locator('#serverinfo, .serverinfo, a[href*="route=/logout"]').first().isVisible().catch(() => false);
    result.dashboard = /Server version/i.test(body) || /Server:/i.test(body);
    const versionMatch = body.match(/Server version:\s*([^\n]+)/i) || body.match(/Server:\s*([^\n]+)/i);
    result.serverVersion = versionMatch ? versionMatch[1].trim().slice(0, 100) : '';
    const logoutLink = page.locator('a[href*="route=/logout"]').first();
    result.logoutLink = (await logoutLink.count()) > 0;
    const dashboardShot = `${outDir}/pma-${phase}-dashboard.png`;
    await page.screenshot({ path: dashboardShot, fullPage: true });
    result.screenshots.push(dashboardShot);

    if (result.logoutLink) {
      await logoutLink.click();
      await page.waitForLoadState('domcontentloaded');
      await page.waitForTimeout(1000);
      result.logout = await page.locator('input[name="pma_username"]').isVisible().catch(() => false);
      const logoutShot = `${outDir}/pma-${phase}-logout.png`;
      await page.screenshot({ path: logoutShot, fullPage: true });
      result.screenshots.push(logoutShot);
    }

    if (expect === 'login' || expect === 'mariadb') {
      const wrongContext = await browser.newContext({ ignoreHTTPSErrors: true });
      const wrongPage = await wrongContext.newPage();
      await login(wrongPage, 'e2e-wrong-password-not-real');
      const wrongBody = await wrongPage.locator('body').innerText();
      result.wrongPasswordRejected = /Cannot log in|Access denied|#1045|login to the MySQL server/i.test(wrongBody);
      const wrongMatch = wrongBody.match(/(Cannot log in[^\n]*|Access denied[^\n]*|#1045[^\n]*)/i);
      result.wrongPasswordText = wrongMatch ? wrongMatch[1].trim().slice(0, 180) : '';
      const wrongShot = `${outDir}/pma-${phase}-wrong-password.png`;
      await wrongPage.screenshot({ path: wrongShot, fullPage: true });
      result.screenshots.push(wrongShot);
      await wrongContext.close();
    }
    await context.close();
  } catch (err) {
    fail(`playwright error: ${err.message}`);
    result.error = err.message.slice(0, 300);
  } finally {
    await browser.close();
  }
  console.log(JSON.stringify(result, null, 2));
})();
