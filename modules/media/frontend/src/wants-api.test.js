import { afterEach, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());
it('reads only local wants and protects explicit versioned writes without download side effects', async () => {
  const fetch = vi.fn().mockImplementation(async url => ({ ok: true, json: async () => url.endsWith('/session') ? { csrf: 'wants-token' } : { items: [] } }));
  vi.stubGlobal('fetch', fetch);
  expect(typeof api.wants).toBe('function');
  const signal = new AbortController().signal;
  await api.wants({ query: 'Test Actor', phase: 'searching', offset: 48, signal });
  await api.want(7, { signal });
  expect(fetch.mock.calls.every(([, options]) => !options.method)).toBe(true);
  expect(fetch.mock.calls[0][0]).toMatch(/\/wants\?query=Test\+Actor&phase=searching&limit=48&offset=48$/);
  const payload = { code: 'TEST-001', title: 'Neutral title' };
  await api.wantAdd(payload, { signal });
  await api.wantPatch(7, { version: 2, state: 'given_up' }, { signal });
  await api.wantRemove(7, 3, { signal });
  const writes = fetch.mock.calls.filter(([, options]) => options.method);
  expect(writes.map(([url, options]) => [url, options.method, JSON.parse(options.body)])).toEqual([
    ['/api/wants', 'POST', payload], ['/api/wants/7', 'PATCH', { version: 2, state: 'given_up' }], ['/api/wants/7', 'DELETE', { version: 3 }],
  ]);
  for (const [, options] of writes) expect(options).toMatchObject({ signal, headers: { 'X-Catalog-CSRF': 'wants-token' } });
  expect(fetch.mock.calls.some(([url]) => /resources|queue|scan|torrent/.test(url))).toBe(false);
});

it('never retries a lost or conflicting want mutation', async () => {
  expect(typeof api.wantPatch).toBe('function');
  for (const status of [0, 409]) {
    const fetch = vi.fn().mockImplementation(async url => {
      if (url.endsWith('/session')) return { ok: true, json: async () => ({ csrf: 'token' }) };
      if (!status) throw new TypeError('response lost');
      return { ok: false, status, json: async () => ({ error: '版本已变化' }) };
    });
    vi.stubGlobal('fetch', fetch);
    await expect(api.wantPatch(7, { version: 2, title: 'Updated' })).rejects.toThrow(status ? '版本已变化' : 'response lost');
    expect(fetch.mock.calls.filter(([, options]) => options.method === 'PATCH')).toHaveLength(1);
  }
});
