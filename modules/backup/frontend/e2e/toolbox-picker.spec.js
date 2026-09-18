import { test, expect } from "@playwright/test";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const root = path.resolve(frontend, "../../..");
const prefix = "/app/wayyoung_toolbox";
const desktop = `<!doctype html><meta charset="utf-8"><title>飞牛桌面测试宿主</title>
<style>html,body{margin:0;height:100%}iframe{border:0;width:100%;height:100%}dialog{z-index:9999}</style>
<iframe title="工具箱" src="${prefix}/"></iframe><script>
window.nativeCalls=[];
window.addEventListener('message', event => {
  if (event.source !== document.querySelector('iframe').contentWindow || event.origin !== location.origin) return;
  const data=event.data;
  if(data?.penpal==='syn') event.source.postMessage({penpal:'synAck',id:'test-host',appName:'wayyoung_toolbox',config:{},methodNames:['pickSharedFile']},event.origin);
  if(data?.penpal!=='call') return;
  window.nativeCalls.push({method:data.methodName,params:data.args[0]});
  const dialog=document.createElement('dialog');
  dialog.setAttribute('aria-label',data.args[0].title);
  const text=document.createElement('p');text.textContent=data.args[0].title;dialog.append(text);
  for(const [label,selected] of [['选择测试目录',true],['取消',false]]) {
    const button=document.createElement('button');button.textContent=label;dialog.append(button);
    button.onclick=()=>{event.source.postMessage({penpal:'reply',id:data.id,resolution:'fulfilled',returnValue:{code:0,data:selected?[data.args[0].sidebarGroup[0]==='remote'?'/mount/example':'/data/example']:[]}},event.origin);dialog.remove()};
  }
  document.body.append(dialog);dialog.showModal();
});</script>`;

async function setup(page) {
  const errors = [], writes = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("console", message => { if (["error", "warning"].includes(message.type())) errors.push(message.text()); });
  await page.route("**/*", async route => {
    const url = new URL(route.request().url()), pathname = url.pathname;
    if (pathname === "/native-desktop") return route.fulfill({ contentType: "text/html", body: desktop });
    if (pathname === "/silent-desktop") return route.fulfill({ contentType: "text/html", body: desktop.split("<script>")[0] });
    if (pathname === `${prefix}/api/modules`) return route.fulfill({ json: { modules: [{ id: "backup", name: "飞牛备份中心", version: "0.0.1", running: true, enabled: true, description: "目录选择回归测试" }] } });
    if (pathname.startsWith("/app/backup_center/api/")) {
      if (route.request().method() !== "GET") writes.push(pathname);
      if (pathname.endsWith("/state")) return route.fulfill({ json: { version: "0.0.1", csrf: "test", state: { tasks: [], runs: [], retain: 100 } } });
      if (pathname.endsWith("/directories")) {
        const target = url.searchParams.get("kind") === "target";
        return route.fulfill({ json: { path: url.searchParams.get("path") || "", parent: "", roots: [{ name: "已授权测试目录", path: target ? "/mount/example" : "/data/example" }], entries: [] } });
      }
      return route.fulfill({ status: 500, json: { error: "测试禁止写入" } });
    }
    let file;
    if (pathname === `${prefix}/directory-authorization.js`) file = path.join(frontend, "src/directory-authorization.js");
    else if (pathname === `${prefix}/vendor/trimjs-web-app.js`) file = path.join(frontend, "node_modules/@trimjs/web-app/dist/index.js");
    else if (pathname.startsWith(`${prefix}/modules/backup/`)) file = path.join(frontend, "dist", pathname.slice(`${prefix}/modules/backup/`.length) || "index.html");
    else if (pathname.startsWith(prefix + "/")) file = path.join(root, "web", pathname.slice(prefix.length + 1) || "index.html");
    if (!file) return route.abort();
    const ext = path.extname(file), contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css" }[ext];
    return route.fulfill({ contentType, body: await fs.readFile(file) });
  });
  return { errors, writes };
}

for (const viewport of [{ width: 1536, height: 1024 }, { width: 390, height: 844 }]) {
  test(`nested fnOS picker chooses both directories (${viewport.width}px)`, async ({ page }, info) => {
    await page.setViewportSize(viewport);
    const { errors, writes } = await setup(page);
    await page.goto("/native-desktop");
    await expect(page).toHaveTitle("飞牛桌面测试宿主");
    const module = page.frameLocator('iframe[title="工具箱"]').frameLocator('iframe[title="飞牛备份中心"]');
    await expect(module.getByRole("heading", { name: "备份任务", exact: true })).toBeVisible();
    await module.locator("header").getByRole("button", { name: "新建任务", exact: true }).click();
    await module.getByLabel("任务名称", { exact: true }).fill("目录选择测试");
    for (const [i, title, field, expected] of [[0, "选择本地授权目录", "源目录（只读）", "/data/example"], [1, "选择备份目标目录", "目标目录", "/mount/example"]]) {
      await module.getByRole("button", { name: "选择目录", exact: true }).nth(i).click();
      await expect(page.getByRole("dialog", { name: title })).toBeVisible();
      await page.getByRole("button", { name: "选择测试目录" }).click();
      await expect(module.getByLabel(field, { exact: true })).toHaveValue(expected);
    }
    // Cancel must preserve the previous selection, not open a fallback dialog.
    await module.getByRole("button", { name: "选择目录", exact: true }).first().click();
    await page.getByRole("button", { name: "取消", exact: true }).click();
    await expect(module.getByLabel("源目录（只读）")).toHaveValue("/data/example");
    await expect(module.getByRole("dialog", { name: "选择已授权目录" })).toHaveCount(0);
    expect(await page.evaluate(() => window.nativeCalls.length)).toBe(3);
    expect(await page.evaluate(() => window.nativeCalls.every(x => x.method === "pickSharedFile"))).toBe(true);
    await expect(module.locator("vite-error-overlay")).toHaveCount(0);
    await page.screenshot({ path: info.outputPath(`picker-${viewport.width}.png`) });
    expect(writes).toEqual([]);
    expect(errors).toEqual([]);
  });
}

test("unresponsive desktop falls back to authorized roots without submitting a task", async ({ page }, info) => {
  const { errors, writes } = await setup(page);
  await page.goto("/silent-desktop");
  const module = page.frameLocator('iframe[title="工具箱"]').frameLocator('iframe[title="飞牛备份中心"]');
  await module.locator("header").getByRole("button", { name: "新建任务", exact: true }).click();
  await module.getByRole("button", { name: "选择目录", exact: true }).first().click();
  const picker = module.getByRole("dialog", { name: "选择已授权目录" });
  await expect(picker).toBeVisible({ timeout: 8000 });
  await picker.getByRole("button", { name: /已授权测试目录/ }).click();
  await picker.getByRole("button", { name: "选择此目录" }).click();
  await expect(module.getByLabel("源目录（只读）")).toHaveValue("/data/example");
  await page.screenshot({ path: info.outputPath("picker-fallback.png") });
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
});
