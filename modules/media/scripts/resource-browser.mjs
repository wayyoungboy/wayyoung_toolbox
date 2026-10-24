import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { createRequire } from 'node:module';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CATALOG_PLAYWRIGHT || 'playwright');
const base = process.argv[2];
const report = process.env.CATALOG_REPORT_DIR || '/tmp/wayyoung-resource-qa';
await mkdir(report, { recursive: true });
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
const page = await context.newPage();
const errors = [];
const httpFailures = [];
page.on('response', response => { if (response.status() >= 400) httpFailures.push(`${response.request().method()} ${response.status()} ${response.url()}`); });
page.on('pageerror', error => errors.push(error.message));
page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
const fixture = mode => fetch(new URL(`/__fixture/resources${mode === undefined ? '' : `?mode=${mode}`}`, base), { method: mode === undefined ? 'GET' : 'POST' }).then(response => response.json());
const visible = async (text, scope = page) => { await scope.getByText(text, { exact: false }).first().waitFor({ state: 'visible' }); };
const untilFixture = async predicate => {
  for (const deadline = Date.now() + 3000; Date.now() < deadline;) {
    if (predicate(await fixture())) return;
    await delay(20);
  }
  throw new Error('fixture did not reach expected query lifecycle');
};
const entry = `${base}?page=resources&code=TEST-001`;
try {
  await page.goto(entry);
  assert.match(page.url(), /page=resources/);
  assert.ok(await page.title());
  await visible('资源搜索');
  assert.equal(await page.locator('vite-error-overlay').count(), 0);
  assert.equal((await fixture()).calls, 0, 'mount contacted external provider');
  await page.getByRole('button', { name: '读取上次结果', exact: true }).click();
  await visible('尚未查询');
  assert.equal((await fixture()).calls, 0, 'cache GET contacted provider');
  await page.getByRole('button', { name: '搜索 JavDB', exact: true }).click();
  await visible('找到 1 个候选');
  assert.equal((await fixture()).calls, 2);
  assert.equal(await page.getByRole('button', { name: '立即下载', exact: true }).count(), 0);
  await page.getByRole('button', { name: '复制磁力', exact: true }).click();
  await visible('复制');
  await page.screenshot({ path: path.join(report, 'resources-ready.png'), fullPage: false });

  await fixture(1);
  await page.getByRole('button', { name: '搜索 JavDB', exact: true }).click();
  await visible('站点拦截'); await visible('上次成功结果');
  await page.reload();
  assert.equal((await fixture()).calls, 3, 'reload contacted provider');
  await page.getByRole('button', { name: '读取上次结果', exact: true }).click();
  await visible('站点拦截'); await visible('上次成功结果');
  assert.equal((await fixture()).calls, 3);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: path.join(report, 'resources-stale-mobile.png'), fullPage: false });
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'horizontal overflow');
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
  await page.screenshot({ path: path.join(report, 'resources-stale-dark.png'), fullPage: false });
  await page.evaluate(() => document.documentElement.removeAttribute('data-theme'));

  await page.getByLabel('完整番号', { exact: true }).fill('OTHER-002');
  assert.equal(await page.getByRole('button', { name: '复制磁力', exact: true }).count(), 0, 'old code remained actionable');
  await page.getByLabel('完整番号', { exact: true }).fill('-646');
  assert.equal(await page.getByRole('button', { name: '搜索 JavDB', exact: true }).isEnabled(), false);
  await page.getByLabel('完整番号', { exact: true }).fill('TEST-001');
  await fixture(3);
  const beforeCancel = (await fixture()).calls;
  await page.getByRole('button', { name: '搜索 JavDB', exact: true }).click();
  await visible('正在查询 JavDB');
  await untilFixture(state => state.busy && state.calls > beforeCancel);
  await page.getByRole('button', { name: '取消查询', exact: true }).click();
  await visible('已停止等待');
  // Wait for the actual server-side cancellation before starting the separate
  // empty-result case; do not silently retry a concurrent 409 as a success.
  await untilFixture(state => !state.busy);
  await fixture(2);
  await page.getByRole('button', { name: '搜索 JavDB', exact: true }).click();
  await visible('详情页未提供可用磁力');
  assert.equal(await page.getByRole('button', { name: '复制磁力', exact: true }).count(), 0);

  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole('button', { name: '馆藏', exact: true }).click();
  await visible('中性测试作品');
  await page.getByRole('button', { name: /中性测试作品/ }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('button', { name: '搜索此作品的资源', exact: true }).click();
  assert.equal(await dialog.getByLabel('完整番号', { exact: true }).inputValue(), 'TEST-001');
  const count = (await fixture()).calls;
  await dialog.getByRole('button', { name: '读取上次结果', exact: true }).click();
  await visible('详情页未提供可用磁力', dialog);
  assert.equal((await fixture()).calls, count);
  await page.goto(`${base}?page=subscriptions`);
  await page.getByRole('heading', { name: 'RSS 订阅', exact: true }).waitFor();
  await page.getByRole('tab', { name: '资源搜索', exact: true }).click();
  await page.getByRole('heading', { name: '资源搜索', exact: true }).waitFor();
  assert.equal((await fixture()).calls, count, 'tab navigation contacted provider');
  assert.deepEqual(errors, [], JSON.stringify(httpFailures));
  console.log('PASS: resource entry → explicit search → cache/stale/reload → mobile/dark → cancel/empty → catalog detail; Browser plugin not available; local Playwright, actual HTTP/SQLite and synthetic provider only.');
} finally {
  await context.close(); await browser.close();
}
