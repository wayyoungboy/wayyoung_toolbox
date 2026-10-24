import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { createRequire } from 'node:module';

// Run only through the temporary loopback read-only acceptance bridge. No
// account/session is read here, and no mutation or live provider is requested.
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CATALOG_PLAYWRIGHT || 'playwright');
const base = process.argv[2];
assert.match(base, /^http:\/\/127\.0\.0\.1:\d+\/app\/wayyoung_toolbox\/modules\/media\/$/);
const report = process.env.CATALOG_REPORT_DIR || '/tmp/wayyoung-wants-installed';
await mkdir(report, { recursive: true });
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
const page = await context.newPage();
const errors = [], writes = [], external = [];
const origin = new URL(base).origin;
await context.route('**/*', route => {
  if (new URL(route.request().url()).origin === origin) return route.continue();
  external.push(route.request().url());
  return route.abort('blockedbyclient');
});
page.on('pageerror', error => errors.push(error.message));
page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
context.on('request', request => { if (request.method() !== 'GET') writes.push(request.url()); });
const readWants = async () => {
  const response = await context.request.get(`${base}api/wants`, { maxRedirects: 0 });
  assert.equal(response.status(), 200);
  assert.equal(new URL(response.url()).origin, origin);
  return response.json();
};
try {
  const state = await readWants();
  assert.equal(state.total, 0, 'this empty-state smoke must not capture personal wants');
  const entry = await page.goto(`${base}?page=subscriptions&tab=wants&code=TEST-001`);
  assert.equal(entry.status(), 200);
  assert.equal(new URL(entry.url()).origin, origin);
  await page.getByRole('heading', { name: '想要清单', exact: true }).waitFor();
  await page.getByText('版本 0.1.10', { exact: true }).waitFor();
  assert.equal(await page.getByLabel('想要的完整番号', { exact: true }).inputValue(), 'TEST-001');
  await page.getByRole('button', { name: '取消登记', exact: true }).click();
  assert.equal(new URL(page.url()).searchParams.has('code'), false);
  await page.reload();
  await page.getByRole('heading', { name: '还没有登记想要的作品', exact: true }).waitFor();
  assert.equal(await page.getByRole('form', { name: '添加想要作品', exact: true }).count(), 0);
  await page.getByRole('button', { name: '刷新清单', exact: true }).click();
  await page.getByRole('button', { name: /^已入库/ }).click();
  await page.getByRole('heading', { name: '此筛选下没有想要作品', exact: true }).waitFor();
  await page.screenshot({ path: `${report}/wants-desktop.png` });
  await page.setViewportSize({ width: 390, height: 844 });
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await page.screenshot({ path: `${report}/wants-mobile.png` });
  await page.evaluate(() => { document.documentElement.setAttribute('data-theme', 'dark'); document.body.setAttribute('theme-mode', 'dark'); });
  await page.screenshot({ path: `${report}/wants-dark.png` });
  assert.deepEqual(writes, [], 'installed acceptance must remain read-only');
  assert.deepEqual(external, [], 'installed page attempted an external browser request');
  assert.deepEqual(errors, []);
  assert.equal((await readWants()).total, 0);
  console.log('PASS installed media0.1.10: real assets, version, wants GET/refresh/filter, consumed prefill, mobile/dark; zero writes or external browser requests. Browser plugin not available; local Playwright.');
} finally { await context.close(); await browser.close(); }
