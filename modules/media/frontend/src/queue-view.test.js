import { describe, expect, it } from 'vitest';
import { queueActions, queueActionPrompt, queueSummary, queueStatusLabel, queuePhaseLabel, safeSourceURL, loadQueueOverview, queueJobID } from './queue-view';

describe('per-film presentation rules', () => {
  it('accepts only positive integer job IDs in deep links', () => {
    expect(queueJobID('17')).toBe(17);
    for (const value of ['', null, '1x', '-1', '1.5', 'Infinity', '9007199254740993']) expect(queueJobID(value)).toBe(null);
  });
  it('loads active and attention tasks independently of old run histories with bounded parallel reads', async () => {
    const calls = [];
    const pending = [];
    const signal = new AbortController().signal;
    const promise = loadQueueOverview(({ status, limit, signal: usedSignal }) => {
      calls.push(status);
      expect(limit).toBeLessThanOrEqual(5);
      expect(usedSignal).toBe(signal);
      return new Promise((resolve) => pending.push(() => resolve({ total: status === 'failed' ? 8 : 1, jobs: [{ id: status, status, title: status }] })));
    }, signal);
    expect(calls).toEqual(['running', 'pending', 'needs_attention', 'failed', 'partial', 'timed_out']);
    for (const resolve of pending) resolve();
    const result = await promise;
    expect(result.running.total).toBe(1);
    expect(result.pending.total).toBe(1);
    expect(result.attentionTotal).toBe(11);
    expect(result.attention.map((job) => job.status)).toEqual(['needs_attention', 'failed', 'partial', 'timed_out']);
  });
  it('distinguishes every lifecycle state and unfinished phase', () => {
    expect(['pending', 'running', 'success', 'partial', 'failed', 'canceled', 'timed_out', 'needs_attention', 'skipped'].map(queueStatusLabel))
      .toEqual(['排队中', '运行中', '成功', '部分成功', '失败', '已取消', '已超时', '待人工处理', '已跳过']);
    expect(queuePhaseLabel({ phase: 'qb_cleanup' })).toBe('qB 记录清理');
    expect(queuePhaseLabel({ phase: 'organize' })).toBe('影片整理');
    expect(queuePhaseLabel({ phase: 'catalog' })).toBe('馆藏登记');
  });
  it('separates catalog registration from organization and retries only registration', () => {
    const job = { id: 12, kind: 'movie', phase: 'catalog', status: 'pending', version: 4 };
    expect(queueSummary(job)).toContain('整理已完成，等待登记馆藏');
    expect(queueSummary({ ...job, status: 'running' })).toContain('核对已完成输出');
    expect(queueActions({ ...job, status: 'needs_attention' })[0].label).toBe('重试馆藏登记');
    expect(queueActionPrompt('retry', job)).toContain('不会重新刮削或搬运');
    expect(queueSummary({ ...job, phase: 'done', status: 'success', catalog_registration: { asset_count: 2, work_count: 1 } })).toContain('已登记馆藏');
    expect(queueSummary({ ...job, phase: 'done', status: 'success' })).not.toContain('已登记馆藏');
  });
  it('offers only supported versioned actions and labels cleanup-only retry explicitly', () => {
    expect(queueActions({ phase: 'qb_cleanup', status: 'failed', version: 4 }).map((a) => a.label)).toEqual(['重试 qB 清理', '取消任务']);
    for (const status of ['success', 'skipped']) expect(queueActions({ phase: 'done', status, version: 2 })).toEqual([]);
    expect(queueActions({ phase: 'organize', status: 'canceled', version: 2 }).map((a) => a.key)).toEqual(['retry']);
    expect(queueActions({ phase: 'organize', status: 'running', version: 3 }).map((a) => a.key)).toEqual(['cancel']);
    expect(queueActions({ phase: 'organize', status: 'failed' })).toEqual([]);
  });
  it('explains pending intake dependencies and does not label moved torrents as organized films', () => {
    expect(queueSummary({ status: 'pending', waiting_for_intake: [18, 20] })).toContain('#18、#20');
    expect(queueSummary({ kind: 'torrent', status: 'success', phase: 'done', child_job_ids: [8] })).toContain('不代表影片整理成功');
    expect(queueSummary({ kind: 'torrent', status: 'pending', phase: 'download' })).toContain('等待下载完成');
    expect(queueSummary({ status: 'failed', error: '文件身份已变化' })).toBe('文件身份已变化');
  });
  it('never turns untrusted source schemes or embedded credentials into a clickable link', () => {
    expect(safeSourceURL('javascript:alert(1)')).toBe('');
    expect(safeSourceURL('file:///etc/passwd')).toBe('');
    expect(safeSourceURL('https://user:secret@example.invalid/item')).toBe('');
    expect(safeSourceURL('https://example.invalid/item?id=1')).toBe('https://example.invalid/item?id=1');
  });
  it('distinguishes download progress, stale observations, unknown paths and completed downloads awaiting intake', () => {
    const job = { kind: 'torrent', phase: 'download', status: 'pending', version: 2, download: { progress: .254, content_path: '/downloads/TEST-001.mp4', observed_at: '2026-09-06T10:00:00Z', state: 'downloading' } };
    expect(queueSummary(job)).toContain('25.4%');
    expect(queueSummary({ ...job, download: { ...job.download, complete: true, progress: 1 } })).toContain('下载完成，等待入库检查');
    expect(queueSummary({ ...job, download: { ...job.download, error: '连接失败' } })).toContain('状态获取失败');
    expect(queueSummary({ ...job, download: { ...job.download, content_path: '' } })).toContain('等待 qB 提供内容路径');
    expect(queueSummary({ ...job, download: { ...job.download, observed_at: '' } })).not.toContain('25.4%');
    for (const progress of [NaN, Infinity, -1, 2, '0.5']) expect(queueSummary({ ...job, download: { ...job.download, progress } })).not.toContain('%');
    expect(queueActions({ ...job, status: 'needs_attention' })[0].label).toBe('恢复下载检查与后续处理');
    expect(queueActionPrompt('retry', { ...job, id: 31 })).toContain('完成后可由手动或定时入库继续搬运');
    expect(queueActionPrompt('retry', { ...job, id: 31 })).toContain('不会重新投递种子');
    expect(queueSummary({ ...job, status: 'canceled' })).toContain('不会停止 qB 下载');
  });
});
