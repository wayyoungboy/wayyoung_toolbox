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

it('validates JSON shape, preserves explicit headers, and reports network failures', async () => {
  for (const body of ['null', '[]', '"bad"']) {
    await expect(createRequest(async () => new Response(body, {headers:{'Content-Type':'application/json'}}))('/api/modules')).rejects.toThrow('响应无效');
  }
  await expect(createRequest(async () => new Response('{}', {status:503,headers:{'Content-Type':'application/json'}}))('/api/modules')).rejects.toThrow('HTTP 503');
  const fetcher = vi.fn(async () => new Response('{"ok":true}', {headers:{'Content-Type':'application/json'}}));
  vi.stubGlobal('fetch', fetcher);
  try {
    expect(await createRequest()('/api/modules', {headers:{'Content-Type':'application/custom'},timeoutMs:100})).toEqual({ok:true});
    expect(fetcher.mock.calls[0][1]).toMatchObject({credentials:'same-origin',headers:{'Content-Type':'application/custom'}});
  } finally { vi.unstubAllGlobals(); }
  await expect(createRequest(async () => {throw new Error('offline');})('/api/modules')).rejects.toThrow('offline');
});
