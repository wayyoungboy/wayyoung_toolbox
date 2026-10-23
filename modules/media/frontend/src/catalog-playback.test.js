import { describe, expect, it, vi } from 'vitest';
import { createProgressSaver, createPlaybackSession, formatPlaybackTime, preferredAsset } from './catalog-playback';

describe('playback persistence', () => {
  it('uses a stable session for one player and distinct sessions for reopening',async()=>{
    const first=createPlaybackSession(),second=createPlaybackSession();
    expect(first).toMatch(/^[a-z0-9_-]{16,80}$/i);expect(first).not.toBe(second);
    const save=vi.fn().mockResolvedValueOnce({revision:1}).mockResolvedValueOnce({revision:2});
    const saver=createProgressSaver(0,save,()=>{},first);
    await saver.save({position:10,duration:100});await saver.save({position:20,duration:100});
    expect(save.mock.calls.map(([value])=>value.session_id)).toEqual([first,first]);
  });
  it('serializes writes, coalesces pending updates and carries the returned revision', async () => {
    let release;
    const first = new Promise(resolve => { release = resolve; });
    const save = vi.fn()
      .mockImplementationOnce(() => first)
      .mockResolvedValueOnce({ revision: 9 });
    const saver = createProgressSaver(7, save);
    const pending = saver.save({ position: 10, duration: 100 });
    await Promise.resolve();
    saver.save({ position: 20, duration: 100 });
    saver.save({ position: 30, duration: 100 });
    release({ revision: 8 });
    await pending;
    expect(save.mock.calls.map(call => call[0])).toEqual([
      { position: 10, duration: 100, revision: 7 },
      { position: 30, duration: 100, revision: 8 },
    ]);
  });

  it('does not blindly resend after conflict or an uncertain network result', async () => {
    for (const failure of [Object.assign(new Error('Changed elsewhere'), { status: 409 }), new Error('Disconnected')]) {
      const save = vi.fn().mockRejectedValue(failure);
      const onError = vi.fn();
      const saver = createProgressSaver(0, save, onError);
      await saver.save({ position: 10, duration: 100 });
      await saver.save({ position: 20, duration: 100 });
      expect(save).toHaveBeenCalledTimes(1);
      expect(onError).toHaveBeenCalledWith(failure);
    }
  });

  it('ignores media events without a finite loaded duration', async () => {
    const save = vi.fn();
    const saver = createProgressSaver(0, save);
    for (const duration of [NaN, Infinity, 0, -1]) await saver.save({ position: 0, duration });
    expect(save).not.toHaveBeenCalled();
  });
});

it('resumes the most recently played available edition and retains unknown identifiers', () => {
  const assets = [
    { id: 'offline', available: false, progress: { played_at: '2026-01-03', position: 90 } },
    { id: 'original', available: true, progress: { played_at: '2026-01-01', position: 5 } },
    { id: 'subtitled', available: true, progress: { played_at: '2026-01-02', position: 20 } },
  ];
  expect(preferredAsset(assets)?.id).toBe('subtitled');
  expect(preferredAsset([])).toBeNull();
  expect(formatPlaybackTime(3672)).toBe('1:01:12');
  expect(formatPlaybackTime(72)).toBe('1:12');
  expect(formatPlaybackTime(NaN)).toBe('0:00');
});
