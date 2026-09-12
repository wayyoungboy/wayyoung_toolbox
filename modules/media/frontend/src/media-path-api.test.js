import { afterEach, expect, it, vi } from 'vitest';
import { api } from './api';
import { resolveDestination, settingsTabs } from './information-architecture';

afterEach(() => vi.unstubAllGlobals());

it('opens shared paths as the default settings destination without removing advanced tabs', () => {
  expect(resolveDestination('settings')).toEqual({ page: 'settings', tab: 'paths' });
  expect(settingsTabs.map(tab => tab.key)).toEqual(['paths', 'general', 'intake', 'javsp', 'diagnostics', 'anime']);
  expect(settingsTabs[0].label).toBe('目录与命名');
  expect(resolveDestination('javsp')).toEqual({ page: 'settings', tab: 'javsp' });
});

it('reads, previews and saves a shared snapshot with request cancellation and its receipt', async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
  vi.stubGlobal('fetch', fetch);
  const controller = new AbortController();
  const draft = { config: { intake_revision: 2, javsp_revision: 3 }, sample: { media_id: 'TEST-001' } };
  await api.mediaPaths({ signal: controller.signal });
  await api.previewMediaPaths(draft, { signal: controller.signal });
  await api.updateMediaPaths({ ...draft, preview_digest: 'receipt' }, { signal: controller.signal });
  expect(fetch.mock.calls.map(([url]) => url)).toEqual(['/api/settings/paths', '/api/settings/paths/preview', '/api/settings/paths']);
  expect(fetch.mock.calls.every(([, options]) => options.signal === controller.signal)).toBe(true);
  expect(fetch.mock.calls[1][1]).toMatchObject({ method: 'POST', body: JSON.stringify(draft) });
  expect(fetch.mock.calls[2][1]).toMatchObject({ method: 'PUT', body: JSON.stringify({ ...draft, preview_digest: 'receipt' }) });
});

it('exposes save conflicts without automatically reissuing writes', async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: false, status: 409, json: async () => ({ error: '配置已更新' }) });
  vi.stubGlobal('fetch', fetch);
  await expect(api.updateMediaPaths({})).rejects.toMatchObject({ status: 409 });
  expect(fetch).toHaveBeenCalledTimes(1);
});
