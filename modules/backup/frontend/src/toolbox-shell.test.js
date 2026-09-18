import { afterEach, expect, it, vi } from "vitest";
import fs from "node:fs";
import { startToolbox } from "../../../../web/shell.js";

const markup = fs.readFileSync(new URL("../../../../web/index.html", import.meta.url), "utf8");
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
