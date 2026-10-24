import { expect, it } from 'vitest';
import { resourceSelectionFromResult, resourceDestinationReady } from './resource-download-view';
import { queueActions, queuePhaseLabel } from './queue-view';

const hash = 'a'.repeat(40);
const result = { code: 'TEST-001', provider: 'javdb', state: 'ready', searched_at: 'snapshot', items: [{ infohash: hash, magnet: 'not-forwarded' }] };
it('locks each download choice to a fresh accepted search snapshot and its actual candidate', () => {
  expect(resourceSelectionFromResult(result, hash, 'TEST-001', true)).toEqual({ code: 'TEST-001', provider: 'javdb', infohash: hash, searched_at: 'snapshot' });
  for (const invalid of [null, { ...result, stale: true }, { ...result, state: 'blocked' }, { ...result, provider: 'other' }, { ...result, searched_at: '' }, { ...result, items: [] }]) {
    expect(resourceSelectionFromResult(invalid, hash, 'TEST-001', true)).toBeNull();
  }
  expect(resourceSelectionFromResult(result, hash, 'TEST-002', true)).toBeNull();
  expect(resourceSelectionFromResult(result, hash, 'TEST-001', false)).toBeNull();
  expect(resourceSelectionFromResult(result, 'bad', 'TEST-001', true)).toBeNull();
});
it('requires the actual destination and an integer revision before confirmation', () => {
  expect(resourceDestinationReady({ revision: 0, source_directory: '/downloads' })).toBe(true);
  for (const config of [null, {}, { revision: null, source_directory: '/downloads' }, { revision: -1, source_directory: '/downloads' }, { revision: 0, source_directory: '/' }, { revision: 0, source_directory: 'relative' }]) expect(resourceDestinationReady(config)).toBe(false);
});
it('distinguishes independent resources and uncertain verification from local cancellation', () => {
  const job = { id: 1, version: 1, kind: 'torrent', phase: 'discovered', status: 'needs_attention', resource: { submission_state: 'dispatching' } };
  expect(queuePhaseLabel(job)).toBe('资源确认与投递');
  expect(queueActions(job)).toContainEqual({ key: 'retry', label: '核对投递结果' });
  expect(queueActions(job)).toContainEqual({ key: 'cancel', label: '停止本地处理' });
});
