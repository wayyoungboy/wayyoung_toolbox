import { describe, expect, it, vi, afterEach } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

describe('automatic duplicate cleanup', () => {
  it('uses separate preview and permanent cleanup endpoints', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ groups: [] }) });
    vi.stubGlobal('fetch', fetchMock);
    await api.previewAutomaticDuplicates();
    await api.cleanupAutomaticDuplicates();
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/maintenance/duplicates/preview', expect.objectContaining({ method: 'POST' }));
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/maintenance/duplicates/cleanup', expect.objectContaining({ method: 'POST' }));
  });
});
