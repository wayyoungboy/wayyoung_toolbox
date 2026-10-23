import { createRequire } from 'node:module';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';

// Browser plugin not available in the task's tool inventory; use the installed
// Playwright runtime without downloading browsers or connecting to user tabs.
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CATALOG_PLAYWRIGHT || 'playwright');
const [url, fixtureRoot] = process.argv.slice(2);
assert.match(url, /^http:\/\/127\.0\.0\.1:\d+\/app\/media_acquisition_center\/$/);
const report = path.resolve(process.env.CATALOG_REPORT_DIR || 'catalog-browser-report');
await fs.mkdir(report, { recursive: true });
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
const page = await context.newPage();
const errors = [];
page.on('pageerror', error => errors.push(error.message));
page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
const check = async (predicate, message) => {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 80));
  }
  throw new Error(message);
};
const screenshot = async name => {
  await page.evaluate(async () => {
    await Promise.all(document.getAnimations().map(animation => animation.finished.catch(() => {})));
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  await page.screenshot({ path: path.join(report, `${name}.png`), fullPage: true });
};

try {
  await page.goto(url);
  await page.getByRole('heading', { name: '从已有影片开始', exact: true }).waitFor();
  await page.getByRole('button', { name: '媒体库管理', exact: true }).click();
  await page.getByLabel('名称', { exact: true }).fill('验收媒体库');
  await page.getByLabel('媒体目录', { exact: true }).fill(fixtureRoot);
  await page.getByRole('button', { name: '添加媒体库', exact: true }).click();
  await page.getByText('媒体库已添加。', { exact: false }).waitFor();
  assert.equal(await page.locator('.catalog-card').count(), 0, 'adding a library must not scan automatically');
  await page.getByRole('button', { name: '扫描馆藏', exact: true }).click();
  await page.getByText('扫描完成：9 部作品、10 个文件', { exact: false }).waitFor();
  await page.getByRole('button', { name: 'close', exact: true }).click();
  await check(async () => await page.locator('.catalog-card').count() === 9, 'cover wall did not refresh');
  await screenshot('desktop-catalog');
  assert.equal(await page.locator('img[src=""]').count(), 0);
  await page.getByRole('searchbox', { name: '搜索馆藏' }).fill('TEST-001');
  await check(async () => await page.locator('.catalog-card').count() === 1, 'search did not filter');
  await page.locator('.catalog-card').click();
  await page.getByRole('heading', { name: '雾中的山谷', exact: true }).waitFor();
  assert.equal(await page.getByLabel('选择播放文件').locator('option').count(), 2);
  await page.getByRole('button', { name: '♡ 收藏', exact: true }).click();
  await page.getByRole('button', { name: '♥ 已收藏', exact: true }).waitFor();
  await page.getByRole('button', { name: '修正标题', exact: true }).click();
  await page.getByLabel('馆藏标题', { exact: true }).fill('雾中的山谷 · 手工修正');
  await page.getByRole('button', { name: '保存标题', exact: true }).click();
  await page.getByRole('heading', { name: '雾中的山谷 · 手工修正', exact: true }).waitFor();
  await screenshot('desktop-detail');
  await page.getByLabel('选择播放文件').selectOption({ label: 'TEST-001.mp4' });
  await page.getByRole('button', { name: '播放', exact: true }).click();
  const video = page.locator('video');
  await video.waitFor();
  await check(() => video.evaluate(element => element.readyState >= 2 && element.duration === 15), 'actual H.264 sample did not load');
  await video.evaluate(async element => {
    element.currentTime = 4;
    await element.play();
  });
  await check(() => video.evaluate(element => element.currentTime > 4.5 && !element.paused), 'playback did not advance');
  await video.evaluate(element => element.pause());
  assert.equal(await page.locator('track[kind="subtitles"]').count(), 1);
  await screenshot('desktop-player');
  await page.getByRole('button', { name: 'close', exact: true }).click();
  await page.getByRole('button', { name: '继续播放', exact: true }).waitFor();
  await page.getByRole('button', { name: 'close', exact: true }).click();
  await page.getByRole('searchbox', { name: '搜索馆藏' }).fill('');
  await page.getByRole('button', { name: '继续观看', exact: true }).click();
  await check(async () => await page.locator('.catalog-card').count() === 1, 'continue watching did not use saved progress');
  await page.reload();
  await check(async () => await page.locator('.catalog-card').count() === 9, 'reload catalog failed');
  await page.getByRole('button', { name: '我的收藏', exact: true }).click();
  await check(async () => await page.locator('.catalog-card').count() === 1, 'favorite was not persisted');
  await page.locator('.catalog-card').click();
  await page.getByRole('button', { name: '继续播放', exact: true }).click();
  await check(() => page.locator('video').evaluate(element => element.currentTime >= 4), 'playback did not resume saved position');
  await page.getByRole('button', { name: 'close', exact: true }).click();
  await page.getByRole('button', { name: 'close', exact: true }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: '全部影片', exact: true }).click();
  await check(async () => await page.locator('.catalog-card').count() === 9, 'mobile catalog failed');
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'mobile horizontal overflow');
  await screenshot('mobile-catalog');
  await page.getByRole('searchbox', { name: '搜索馆藏' }).fill('TEST-001');
  await check(async () => await page.locator('.catalog-card').count() === 1, 'mobile search failed');
  await page.locator('.catalog-card').click();
  await page.getByRole('heading', { name: '雾中的山谷 · 手工修正', exact: true }).waitFor();
  await screenshot('mobile-detail');
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'mobile detail horizontal overflow');
  await page.getByRole('button', { name: 'close', exact: true }).click();
  await page.getByRole('button', { name: '媒体库管理', exact: true }).click();
  await page.getByRole('button', { name: '重新关联', exact: true }).click();
  await page.getByRole('button', { name: '确认关联', exact: true }).click();
  await page.getByText('目录已重新关联。', { exact: false }).waitFor();
  await page.getByRole('button', { name: '扫描馆藏', exact: true }).click();
  await page.getByText('扫描完成：9 部作品、10 个文件', { exact: false }).waitFor();
  await page.getByRole('button', { name: 'close', exact: true }).click();
  await page.locator('.catalog-card').click();
  await page.getByRole('button', { name: '♥ 已收藏', exact: true }).waitFor();
  await page.getByRole('button', { name: '继续播放', exact: true }).waitFor();
  assert.deepEqual(errors, [], 'browser runtime errors');
  console.log(JSON.stringify({ passed: true, fixtures: { works: 9, assets: 10 }, playback: 'real H.264 + AAC', checks: ['import', 'manual scan', 'native Host rewrite + CSRF', 'search', 'editions', 'favorite', 'manual title', 'play', 'seek', 'SRT track', 'progress', 'resume', 'reload', 'mobile', 'console'], screenshots: report }));
} catch (error) {
  await screenshot('failure');
  console.error(await page.locator('body').innerText());
  throw error;
} finally {
  await browser.close();
}
