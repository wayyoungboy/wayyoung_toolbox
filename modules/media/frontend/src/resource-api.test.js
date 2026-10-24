import { afterEach, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());
it('registers only a confirmed snapshot and reconciles uncertain responses using GET, never a retry POST', async () => {
  const fetch = vi.fn().mockImplementation(async url => ({ ok: true, json: async () => url.endsWith('/session') ? { csrf: 'resource-token' } : { job: null } }));
  vi.stubGlobal('fetch', fetch);
  const selection = { code: 'TEST-001', provider: 'javdb', infohash: 'a'.repeat(40), searched_at: 'snapshot', magnet: 'must-not-send', path: '/must-not-send' };
  const signal = new AbortController().signal;
  await api.resourceDownload(selection, 7, { signal });
  const post = fetch.mock.calls.filter(([, options]) => options.method === 'POST');
  expect(post).toHaveLength(1);
  expect(JSON.parse(post[0][1].body)).toEqual({ code: 'TEST-001', provider: 'javdb', infohash: 'a'.repeat(40), searched_at: 'snapshot', intake_revision: 7, confirmation: '下载资源' });
  expect(post[0][1]).toMatchObject({ signal, headers: { 'X-Catalog-CSRF': 'resource-token' } });
  await api.resourceDownloadSelection(selection, { signal });
  await api.resourceDownloads({ signal });
  expect(fetch.mock.calls.filter(([, options]) => options.method)).toHaveLength(1);
  expect(fetch.mock.calls.at(-2)[0]).toMatch(/\/downloads\/selection\?code=TEST-001&provider=javdb&infohash=a{40}$/);
  expect(fetch.mock.calls.at(-1)[0]).toMatch(/\/resources\/downloads\?limit=20$/);
});

it('does not automatically replay a failed resource registration', async () => {
  const fetch = vi.fn().mockImplementation(async url => {
    if (url.endsWith('/session')) return { ok: true, json: async () => ({ csrf: 'token' }) };
    throw new TypeError('response lost');
  });
  vi.stubGlobal('fetch', fetch);
  await expect(api.resourceDownload({ code: 'TEST-001' }, 0)).rejects.toThrow('response lost');
  expect(fetch.mock.calls.filter(([, options]) => options.method === 'POST')).toHaveLength(1);
});
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
