import { afterEach, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

it('loads bounded read-only results and explicitly refreshes without starting RSS or JAVSP', async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ items: [] }) });
  vi.stubGlobal('fetch', fetch);
  const controller = new AbortController();
  await api.completedLibrary({ query: 'TEST-001', offset: 50, signal: controller.signal });
  await api.scanCompletedLibrary({ signal: controller.signal });
  expect(fetch.mock.calls[0][0]).toBe('/api/library/completed?query=TEST-001&offset=50&limit=50');
  expect(fetch.mock.calls[0][1].signal).toBe(controller.signal);
  expect(fetch.mock.calls[1][0]).toBe('/api/library/completed/scan');
  expect(fetch.mock.calls[1][1]).toMatchObject({ method: 'POST', body: '{}', signal: controller.signal });
});

it('surfaces unavailable directories, with no silent stale-index fallback or retry writes', async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: false, status: 409, json: async () => ({ error: '整理完成目录不可用' }) });
  vi.stubGlobal('fetch', fetch);
  await expect(api.scanCompletedLibrary()).rejects.toMatchObject({ status: 409 });
  expect(fetch).toHaveBeenCalledTimes(1);
});
