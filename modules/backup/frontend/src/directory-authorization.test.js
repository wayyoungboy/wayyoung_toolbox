import { afterEach, expect, it, vi } from "vitest";
import { createDirectoryAuthorizer, installToolboxDirectoryBridge, toolboxParent } from "./directory-authorization.js";

afterEach(() => vi.useRealTimers());

it("only forwards directory selection from the active same-origin backup frame", async () => {
  const child = { location: { origin: "http://nas", pathname: "/app/wayyoung_toolbox/modules/backup/" } };
  const frame = { contentWindow: child };
  const host = { location: { origin: "http://nas" }, document: { querySelector: () => frame } };
  const authorize = vi.fn(async () => "/authorized");
  installToolboxDirectoryBridge(host, authorize);
  for (const kind of ["source", "target", "restore"]) {
    expect(await host.wayyoungToolboxPickDirectory(child, kind)).toBe("/authorized");
  }
  await expect(host.wayyoungToolboxPickDirectory({}, "source")).rejects.toThrow("来源无效");
  await expect(host.wayyoungToolboxPickDirectory(child, "deleteFile")).rejects.toThrow("来源无效");
  child.location.origin = "http://other";
  await expect(host.wayyoungToolboxPickDirectory(child, "source")).rejects.toThrow("来源无效");
  child.location.origin = "http://nas";
  child.location.pathname = "/app/unrelated/";
  await expect(host.wayyoungToolboxPickDirectory(child, "source")).rejects.toThrow("来源无效");
  host.document.querySelector = () => null;
  await expect(host.wayyoungToolboxPickDirectory(child, "source")).rejects.toThrow("来源无效");
  expect(authorize).toHaveBeenCalledTimes(3);
});

it("does not mistake another parent or a cross-origin desktop for the toolbox", () => {
  const child = { location: { origin: "http://nas" } };
  child.parent = child;
  expect(toolboxParent(child)).toBeNull();
  child.parent = { location: { origin: "http://nas", pathname: "/desktop" } };
  expect(toolboxParent(child)).toBeNull();
  child.parent = { get location() { throw new Error("SecurityError"); } };
  expect(toolboxParent(child)).toBeNull();
});

it("rejects invalid picker operations and preserves native permission failures", async () => {
  const pickSharedFile = vi.fn(async () => ({ code: 403 }));
  const authorize = createDirectoryAuthorizer(async () => ({ ready: async () => {}, pickSharedFile }));
  await expect(authorize("remove")).rejects.toThrow("无效");
  expect(pickSharedFile).not.toHaveBeenCalled();
  await expect(authorize("restore")).rejects.toThrow("目录授权未完成");
  expect(pickSharedFile.mock.lastCall[0].sidebarGroup).toEqual(["myFiles", "otherShare", "favorites"]);
});

it("falls back on failed SDK initialization without making native calls", async () => {
  const authorize = createDirectoryAuthorizer(async () => { throw Error("host missing"); });
  expect(await authorize("source")).toBeUndefined();
});
