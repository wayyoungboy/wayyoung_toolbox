import { afterEach, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());
it('uses GET for local cache and one CSRF-protected explicit POST with cancellation', async () => {
  const fetch = vi.fn().mockImplementation(async url => ({ ok: true, json: async () => url.endsWith('/session') ? { csrf: 'resource-token' } : { state: 'empty', items: [] } }));
  vi.stubGlobal('fetch', fetch);
  const signal = new AbortController().signal;
  await api.resourceCache('TEST-001', { signal });
  expect(fetch).toHaveBeenCalledOnce();
  expect(fetch.mock.calls[0][0]).toMatch(/\/resources\/search\?code=TEST-001&provider=javdb$/);
  expect(fetch.mock.calls[0][1].method).toBeUndefined();
  await api.resourceSearch('TEST-001', { signal });
  const writes = fetch.mock.calls.filter(([, options]) => options.method);
  expect(writes).toHaveLength(1);
  expect(writes[0][1]).toMatchObject({ method: 'POST', signal, headers: { 'X-Catalog-CSRF': 'resource-token' } });
  expect(JSON.parse(writes[0][1].body)).toEqual({ code: 'TEST-001', provider: 'javdb' });
  expect(fetch.mock.calls.some(([url]) => /downloads|torrents|scan/.test(url))).toBe(false);
});
