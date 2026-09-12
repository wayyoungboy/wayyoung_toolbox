import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('persistent film queue', () => {
  it('uses bounded cursor reads and versioned actions without restarting a whole scan', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();
    await api.queue({ status: 'needs_attention', before: 42, limit: 25, signal: controller.signal });
    await api.queueJob(12, { signal: controller.signal });
    await api.retryQueueJob(12, 7);
    await api.cancelQueueJob(12, 8);
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/queue?status=needs_attention&before_id=42&limit=25',
      '/api/queue/12', '/api/queue/12/retry', '/api/queue/12/cancel',
    ]);
    expect(fetchMock.mock.calls[0][1].signal).toBe(controller.signal);
    expect(fetchMock.mock.calls[1][1].signal).toBe(controller.signal);
    expect(fetchMock.mock.calls[2][1]).toMatchObject({ method: 'POST', body: '{"version":7}' });
    expect(fetchMock.mock.calls[3][1]).toMatchObject({ method: 'POST', body: '{"version":8}' });
  });

  it('preserves conflict status so the UI can refresh a stale job without retrying mutations', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: false, status: 409, json: async () => ({ error: '任务已变化' }) });
    vi.stubGlobal('fetch', fetchMock);
    await expect(api.retryQueueJob(12, 3)).rejects.toMatchObject({ message: '任务已变化', status: 409 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe('JAVSP API sequence', () => {
  it('cancels only the observed discovery run and keeps its API separate from organization', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ accepted: true }) });
    vi.stubGlobal('fetch', fetchMock);
    const controller = new AbortController();
    await api.javsp({ signal: controller.signal });
    await api.cancelJavspDiscovery(42);
    expect(fetchMock.mock.calls[0][1].signal).toBe(controller.signal);
    expect(fetchMock).toHaveBeenLastCalledWith('/api/javsp/discovery/cancel', expect.objectContaining({ method: 'POST', body: '{"run_id":42}' }));
  });

  it('supports saving the configuration before starting a run', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({}),
    });
    vi.stubGlobal('fetch', fetchMock);

    await api.updateJavsp({ move_files: false });
    await api.runJavsp();
    await api.cancelJavsp();

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      '/api/javsp/config',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ move_files: false }) }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      '/api/javsp/run',
      expect.objectContaining({ method: 'POST' }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      '/api/javsp/cancel',
      expect.objectContaining({ method: 'POST' }),
    );
  });

  it('encodes the directory selected for browsing', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ items: [] }) });
    vi.stubGlobal('fetch', fetchMock);

    await api.javspDirectories('/vol1/shared/media/incoming');

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/javsp/directories?path=%2Fvol1%2Fshared%2Fmedia%2Fincoming',
      expect.any(Object),
    );
  });
});

describe('RSS manual submission', () => {
  it('exposes separate preview and real-submission actions', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ accepted: true }) });
    vi.stubGlobal('fetch', fetchMock);

    await api.previewRunTask(7);
    await api.submitTask(7);

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      '/api/tasks/7/run',
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ dry_run: true }) }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      '/api/tasks/7/run',
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ dry_run: false }) }),
    );
  });
});

describe('AniDB anime scraper', () => {
  it('supports directory browsing, title refresh, scanning and NFO generation', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    vi.stubGlobal('fetch', fetchMock);

    await api.animeDirectories('/vol1/shared/media/anime');
    await api.refreshAnimeTitles();
    await api.scanAnime('/vol1/shared/media/anime');
    await api.writeAnimeNFO({ directory: '/vol1/shared/media/anime', items: [] });

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      '/api/anime/directories?path=%2Fvol1%2Fshared%2Fmedia%2Fanime',
      expect.any(Object),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/anime/titles/refresh', expect.objectContaining({ method: 'POST' }));
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/anime/scan', expect.objectContaining({ method: 'POST' }));
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/anime/nfo', expect.objectContaining({ method: 'POST' }));
  });
});

describe('media maintenance', () => {
  it('supports duplicate scans, permanent batch deletion and empty-directory cleanup', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    vi.stubGlobal('fetch', fetchMock);

    await api.maintenance();
    await api.scanDuplicates('/vol1/shared/media/library');
    await api.deleteDuplicateVideos([{ path: '/vol1/shared/media/library/example.mp4', size: 1024 }]);
    await api.previewEmptyDirectories();
    await api.cleanupEmptyDirectories();

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/maintenance', expect.any(Object));
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/maintenance/duplicates/scan', expect.objectContaining({ method: 'POST' }));
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/maintenance/duplicates/delete', expect.objectContaining({ method: 'POST' }));
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/maintenance/empty-directories/preview', expect.objectContaining({ method: 'POST' }));
    expect(fetchMock).toHaveBeenNthCalledWith(5, '/api/maintenance/empty-directories/cleanup', expect.objectContaining({ method: 'POST' }));
  });
});

describe('completed-download intake', () => {
  it('supports configuration, preview and real execution', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    vi.stubGlobal('fetch', fetchMock);

    await api.intake();
    await api.updateIntake({ source_directory: '/vol1/shared/downloads' });
    await api.previewIntake(7);
    await api.runIntake(8);

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/intake', expect.any(Object));
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/intake/config', expect.objectContaining({ method: 'PUT' }));
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/intake/preview', expect.objectContaining({ method: 'POST', body: '{"revision":7}' }));
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/intake/run', expect.objectContaining({ method: 'POST', body: '{"revision":8}' }));
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/intake/run', expect.objectContaining({ method: 'POST' }));
  });
});

describe('unified native workspace', () => {
  it('loads the pipeline summary and filtered unified run history', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ items: [] }) });
    vi.stubGlobal('fetch', fetchMock);

    await api.pipeline();
    await api.unifiedRuns({ module: 'javsp', status: 'failed', limit: 40 });

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/pipeline', expect.any(Object));
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      '/api/runs/unified?module=javsp&status=failed&limit=40',
      expect.any(Object),
    );
  });
});
