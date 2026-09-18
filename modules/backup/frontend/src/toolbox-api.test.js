import { expect, it, vi } from "vitest";
import { createRequest } from "../../../../web/api.js";

it("rejects HTML login pages with HTTP 200 instead of reporting success", async () => {
  const request = createRequest(vi.fn(async () => new Response("<html>login</html>", { headers: { "Content-Type": "text/html" } })));
  await expect(request("/api/modules")).rejects.toThrow(/登录|JSON/);
});

it("reports forbidden responses and fails bounded requests", async () => {
  const request = createRequest(vi.fn(async () => new Response('{"error":"需要管理员权限"}', { status: 403, headers: { "Content-Type": "application/json" } })));
  await expect(request("/api/modules")).rejects.toThrow("需要管理员权限");
  const stalled = createRequest((_url, { signal }) => new Promise((_, reject) => signal.addEventListener("abort", () => reject(signal.reason))), 10);
  await expect(stalled("/api/modules")).rejects.toThrow(/超时/);
});
