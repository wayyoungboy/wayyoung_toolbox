import { afterEach, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

it('passes catalog filters and abort signals to the real HTTP API', async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ items: [], total: 0 }) });
  vi.stubGlobal('fetch', fetch);
  const controller = new AbortController();
  await api.catalogWorks({ query: 'test & title', actor: 'Test Actor', tag: '测试样本', view: 'favorites', libraryID: '3', offset: 48, signal: controller.signal });
  const [target, options] = fetch.mock.calls[0];
  const url = new URL(target, 'http://localhost');
  expect(url.pathname).toMatch(/\/api\/catalog\/works$/);
  expect(url.searchParams.get('query')).toBe('test & title');
  expect(url.searchParams.get('favorite')).toBe('true');
  expect(url.searchParams.get('library_id')).toBe('3');
  expect(url.searchParams.get('offset')).toBe('48');
  expect(url.searchParams.get('actor')).toBe('Test Actor');
  expect(url.searchParams.get('tag')).toBe('测试样本');
  expect(options.signal).toBe(controller.signal);
});

it('sends progress revisions and explicit personal changes without enabling scans', async () => {
  const fetch = vi.fn().mockImplementation(async url => ({ ok: true, json: async () => url.endsWith('/session') ? { csrf: 'test-csrf' } : { revision: 2 } }));
  vi.stubGlobal('fetch', fetch);
  await api.catalogProgress('asset', { position: 25, duration: 100, revision: 1 });
  await api.catalogPersonal('work', { favorite: true });
  const writes = fetch.mock.calls.filter(([, options]) => options.method);
  expect(writes[0][1].method).toBe('PUT');
  expect(JSON.parse(writes[0][1].body)).toEqual({ position: 25, duration: 100, revision: 1 });
  expect(writes[0][1].headers['X-Catalog-CSRF']).toBe('test-csrf');
  expect(writes[1][1].method).toBe('PATCH');
  expect(JSON.parse(writes[1][1].body)).toEqual({ favorite: true });
  expect(fetch.mock.calls.every(([url]) => !url.endsWith('/scan'))).toBe(true);
});
