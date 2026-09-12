import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
  window.history.replaceState({}, '', '/');
});

function response(payload, ok = true, status = 200) {
  return { ok, status, json: vi.fn().mockResolvedValue(payload) };
}

describe('API client', () => {
  it('calls every native endpoint with the expected methods and payloads', async () => {
    window.history.replaceState({}, '', '/app/music_converter/');
    const fetchMock = vi.fn().mockResolvedValue(response({ ok: true }));
    vi.stubGlobal('fetch', fetchMock);
    const { api } = await import('./api.js');

    await api.getHealth();
    await api.getSettings();
    await api.saveSettings({ concurrency: 2 });
    await api.scan();
    await api.startJob();
    await api.getCurrentJob();
    await api.cancelJob();
    await api.getRuns();
    await api.clearRuns();
    await api.getAbout();
    await api.listDirectories('/vol1/Music & Audio');

    expect(fetchMock).toHaveBeenCalledTimes(11);
    expect(fetchMock.mock.calls[0][0]).toBe('/app/music_converter/api/health');
    expect(fetchMock.mock.calls[2][1]).toMatchObject({ method: 'PUT', body: '{"concurrency":2}' });
    expect(fetchMock.mock.calls[3][1]).toMatchObject({ method: 'POST', body: '{}' });
    expect(fetchMock.mock.calls[8][1]).toMatchObject({ method: 'DELETE' });
    expect(fetchMock.mock.calls[10][0]).toContain('%2Fvol1%2FMusic%20%26%20Audio');
  });

  it('surfaces server error messages and handles non-JSON bodies', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ error: '目录不可用' }, false, 400))
      .mockResolvedValueOnce({ ok: false, status: 503, json: vi.fn().mockRejectedValue(new Error('not json')) });
    vi.stubGlobal('fetch', fetchMock);
    const { api } = await import('./api.js');

    await expect(api.getSettings()).rejects.toThrow('目录不可用');
    await expect(api.getHealth()).rejects.toThrow('请求失败（503）');
  });
});
