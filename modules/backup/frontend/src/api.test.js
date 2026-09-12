import { describe, it, expect, vi } from "vitest";
import { createClient } from "./api.js";
const sdk = vi.hoisted(() => ({
  ready: vi.fn(async () => {}),
  isStandaloneWeb: true,
  pickSharedFile: vi.fn(),
}));
vi.mock("@trimjs/web-app", () => ({
  TrimApp: class {
    constructor() {
      return sdk;
    }
  },
}));

describe("application API client", () => {
  it("starts the native handshake at page load, before opening a picker", async () => {
    await vi.waitFor(() => expect(sdk.ready).toHaveBeenCalled());
  });
  it("passes CSRF for mutations, encodes IDs and keeps read requests read-only", async () => {
    const fetcher = vi.fn(async (url) => ({
      ok: true,
      json: async () => (url.endsWith("/state") ? { csrf: "fresh-token" } : {}),
    }));
    const c = createClient(fetcher);
    await c.state();
    await c.saveTask({ name: "new" });
    await c.saveTask({ id: "a/b" });
    await c.run("a/b", { kind: "backup" });
    await c.cancel("a/b");
    await c.removeTask("a/b");
    await c.setRetention(50);
    await c.snapshots("a/b");
    await c.directories("target", "/mount/空 格");
    await c.importRepository({ target: "/mount/x" });
    await c.rebind("a/b", "/mount/y");
    const calls = fetcher.mock.calls;
    expect(calls[0][1].headers).toEqual({});
    expect(calls[1][1].headers["X-Backup-CSRF"]).toBe("fresh-token");
    expect(calls[2][0]).toContain("a%2Fb");
    expect(JSON.parse(calls.at(-1)[1].body)).toEqual({ target: "/mount/y" });
    expect(calls[8][0]).toContain("path=%2Fmount%2F");
  });
  it("reports server failures and invalid responses without assuming success", async () => {
    for (const response of [
      { ok: false, status: 403, json: async () => ({ error: "未授权" }) },
      {
        ok: false,
        status: 502,
        json: async () => {
          throw Error("HTML");
        },
      },
      { ok: true, json: async () => null },
    ]) {
      await expect(
        createClient(async () => response).state(),
      ).rejects.toThrow();
    }
  });
  it("uses native shared-directory authorization and distinguishes standalone / cancel / error", async () => {
    const c = createClient();
    sdk.isStandaloneWeb = true;
    expect(await c.authorize("source")).toBeUndefined();
    sdk.isStandaloneWeb = false;
    sdk.pickSharedFile.mockResolvedValue({ code: 0, data: ["/local"] });
    expect(await c.authorize("source")).toBe("/local");
    sdk.pickSharedFile.mockResolvedValue({ code: 0, data: ["/remote"] });
    expect(await c.authorize("target")).toBe("/remote");
    expect(sdk.pickSharedFile.mock.lastCall[0].sidebarGroup).toEqual([
      "remote",
    ]);
    sdk.pickSharedFile.mockResolvedValue(undefined);
    expect(await c.authorize("restore")).toBeNull();
    sdk.pickSharedFile.mockResolvedValue({ code: 401, msg: "无权限" });
    await expect(c.authorize("target")).rejects.toThrow("无权限");
  });
});
