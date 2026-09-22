import { afterEach, expect, it, vi } from "vitest";
import fs from "node:fs";
import path from "node:path";
import { startToolbox } from "../../../../web/shell.js";

const markup = fs.readFileSync(path.resolve(process.cwd(), '../../../web/index.html'), 'utf8');

let stop;
afterEach(() => { stop?.(); document.body.innerHTML = ""; history.replaceState(null, "", "/"); vi.useRealTimers(); });

async function setup(hash = "#backup") {
  document.body.innerHTML = new DOMParser().parseFromString(markup, "text/html").body.innerHTML;
  history.replaceState(null, "", hash);
  const modules = ["backup", "music"].map(id => ({ id, name: id, enabled: true, running: true, version: "1", started_at: "first" }));
  const request = vi.fn(async () => ({ modules, version: "0.6.0", is_admin: true }));
  const app = startToolbox({ request });
  stop = app.destroy;
  await app.ready;
  return { app, modules, request };
}

it('keeps expanded diagnostic details across background refreshes', async () => {
  const {app, request, modules} = await setup('#overview');
  request.mockImplementation(async path => path === '/api/modules' ? {modules,version:'0.6.0'} : path.endsWith('state') ? {state:{tasks:[],runs:[]}} : {roots:[]});
  document.querySelector('[data-check="backup"]').click();
  await vi.waitFor(() => expect(document.querySelector('details[data-details="backup"]')).not.toBeNull());
  document.querySelector('details[data-details="backup"]').open = true;
  await app.refresh();
  expect(document.querySelector('details[data-details="backup"]').open).toBe(true);
});

it('does not replace the overview DOM during unchanged background status polling', async () => {
  const {app} = await setup('#overview');
  const open = document.querySelector('[data-open="backup"]');
  open.focus();
  await app.refresh();
  expect(document.querySelector('[data-open="backup"]')).toBe(open);
  expect(document.activeElement).toBe(open);
});

it("keeps module frames and unsaved state across navigation and quiet refresh", async () => {
  const { app } = await setup();
  const frame = document.querySelector('iframe[title="backup"]');
  frame.dataset.unsaved = "draft";
  document.querySelector('[data-module="music"]').click();
  expect(frame.isConnected).toBe(true);
  expect(frame.hidden).toBe(true);
  document.querySelector('[data-module="backup"]').click();
  await app.refresh();
  expect(document.querySelector('iframe[title="backup"]')).toBe(frame);
  expect(frame.hidden).toBe(false);
  expect(frame.dataset.unsaved).toBe("draft");
});

it("invalidates cached pages when the module generation changes or stops", async () => {
  const { app, modules } = await setup();
  const old = document.querySelector("iframe");
  modules[0].started_at = "restarted";
  await app.refresh();
  expect(document.querySelector("iframe")).not.toBe(old);
  modules[0].running = false;
  modules[0].enabled = false;
  await app.refresh();
  expect(document.querySelector('iframe[title="backup"]')).toBeNull();
});

it("starts at overview, shows package version, and never probes or writes automatically", async () => {
  const { request } = await setup("#%invalid");
  expect(document.querySelector("#module-name")).toHaveTextContent("总览");
  expect(document.body).toHaveTextContent("0.6.0");
  expect(document.querySelectorAll("iframe")).toHaveLength(0);
  expect(request.mock.calls.every(([path]) => path === "/api/modules")).toBe(true);
  document.querySelector('[data-module="music"]').focus();
});

it("preserves focused navigation and surfaces module errors even while runtime exists", async () => {
  const { app, modules } = await setup();
  const button = document.querySelector('[data-module="music"]');
  button.focus();
  await app.refresh();
  expect(document.activeElement).toBe(button);
  modules[0].last_error = "stop timed out";
  await app.refresh();
  expect(document.querySelector("#module-status")).toHaveTextContent("异常");
});

it("requires confirmation to stop, and cancellation sends no mutation", async () => {
  const { request } = await setup();
  const dialog = document.querySelector("#stop-dialog");
  dialog.showModal = vi.fn(() => dialog.setAttribute("open", ""));
  dialog.close = vi.fn(() => dialog.removeAttribute("open"));
  document.querySelector("#module-toggle").click();
  expect(dialog.showModal).toHaveBeenCalledOnce();
  document.querySelector("#stop-cancel").click();
  await Promise.resolve();
  expect(request.mock.calls.every(([, opts]) => !opts?.method || opts.method === "GET")).toBe(true);
  expect(document.querySelector("#module-toggle").checked).toBe(true);
});

it('serializes confirmed mutations and keeps the requested target when navigating', async () => {
  const { request, modules } = await setup();
  const dialog = document.querySelector('#stop-dialog');
  dialog.showModal = () => dialog.setAttribute('open', '');
  dialog.close = () => dialog.removeAttribute('open');
  let complete;
  request.mockImplementation((path, options) => options?.method === 'PUT' ? new Promise(resolve => { complete = resolve; }) : Promise.resolve({modules, version:'0.6.0'}));
  document.querySelector('#module-toggle').click();
  document.querySelector('#stop-confirm').click();
  await Promise.resolve();
  document.querySelector('[data-module="music"]').click();
  expect(document.querySelector('#module-toggle').disabled).toBe(true);
  modules[0].running = false; modules[0].enabled = false;
  complete({modules,version:'0.6.0'});
  await vi.waitFor(() => expect(document.querySelector('#module-toggle').disabled).toBe(false));
  expect(request.mock.calls.filter(([,opts]) => opts?.method === 'PUT')).toHaveLength(1);
  expect(document.querySelector('iframe[title="backup"]')).toBeNull();
});

it('supports keyboard navigation and reports invalid/failed reads', async () => {
  const { app, request } = await setup();
  document.querySelector('#module-tabs').dispatchEvent(new KeyboardEvent('keydown', {key:'End',bubbles:true}));
  expect(document.querySelector('#module-name')).toHaveTextContent('music');
  document.querySelector('#module-tabs').dispatchEvent(new KeyboardEvent('keydown', {key:'Home',bubbles:true}));
  expect(document.querySelector('#module-name')).toHaveTextContent('总览');
  request.mockResolvedValue({modules:'invalid'});
  await app.refresh();
  expect(document.querySelector('#notice')).toHaveTextContent('响应无效');
  request.mockRejectedValue(new Error('forbidden'));
  await app.refresh();
  expect(document.querySelector('#module-status')).toHaveTextContent('连接异常');
});

it('explicit checks fail visibly and do not execute a task', async () => {
  const { request } = await setup('#overview');
  request.mockRejectedValue(new Error('not authorized'));
  document.querySelector('[data-check-all]').click();
  await vi.waitFor(() => expect(document.body).toHaveTextContent('检查失败，状态未知'));
  expect(document.body).toHaveTextContent('not authorized');
  expect(request.mock.calls.every(([, options]) => !options?.method)).toBe(true);
});

it('requires the hardware model confirmation and displays enable failures', async () => {
  const {app, modules, request} = await setup();
  modules.push({id:'ha500',name:'hardware',enabled:false,running:false,version:'1'});
  await app.refresh();
  document.querySelector('[data-module="ha500"]').click();
  const dialog = document.querySelector('#ha500-dialog');
  dialog.showModal = () => dialog.setAttribute('open','');
  dialog.close = () => dialog.removeAttribute('open');
  document.querySelector('[data-enable="ha500"]').click();
  document.querySelector('#ha500-form').dispatchEvent(new Event('submit',{cancelable:true}));
  expect(document.querySelector('#ha500-error')).toHaveTextContent('请输入 HA500');
  expect(request.mock.calls.some(([,o]) => o?.method === 'PUT')).toBe(false);
  request.mockImplementation(async (_path,o) => {
    if (o?.method === 'PUT') throw new Error('driver unavailable');
    return {modules,version:'0.6.0'};
  });
  document.querySelector('#ha500-confirmation').value = 'ha500';
  document.querySelector('#ha500-form').dispatchEvent(new Event('submit',{cancelable:true}));
  await vi.waitFor(() => expect(document.querySelector('#notice')).toHaveTextContent('driver unavailable'));
  expect(JSON.parse(request.mock.calls.find(([,o]) => o?.method === 'PUT')[1].body)).toMatchObject({confirmation:'HA500',enabled:true});
  document.querySelector('[data-enable="ha500"]').click();
  dialog.dispatchEvent(new Event('cancel',{cancelable:true}));
  await Promise.resolve();
  expect(dialog.hasAttribute('open')).toBe(false);
});

it('decorates embedded documents only once and supports hash/back navigation', async () => {
  await setup();
  const frame = document.querySelector('iframe');
  const child = document.implementation.createHTMLDocument('child');
  Object.defineProperty(frame,'contentDocument',{value:child});
  frame.dispatchEvent(new Event('load'));
  frame.dispatchEvent(new Event('load'));
  expect(child.querySelectorAll('#toolbox-embedded-style')).toHaveLength(1);
  expect(child.documentElement.dataset.toolboxModule).toBe('backup');
  history.replaceState(null,'','#music');
  window.dispatchEvent(new Event('hashchange'));
  expect(document.querySelector('#module-name')).toHaveTextContent('music');
  document.querySelector('#module-tabs').dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowLeft',bubbles:true}));
  expect(document.querySelector('#module-name')).toHaveTextContent('backup');
  document.querySelector('#module-tabs').dispatchEvent(new KeyboardEvent('keydown',{key:'Tab',bubbles:true}));
  expect(document.querySelector('#module-name')).toHaveTextContent('backup');
});
