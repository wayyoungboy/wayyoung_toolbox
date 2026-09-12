import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { QueueJobDetails, QueueJobsTable } from './QueueViews';

const movie = { id: 7, kind: 'movie', title: 'TEST-001.mp4', media_id: 'TEST-001', status: 'pending', phase: 'organize', version: 2, source_paths: ['/pending/TEST-001.1.mp4', '/pending/TEST-001.2.mp4'], waiting_for_intake: [5], parent_id: 5 };

describe('queue views', () => {
  it('shows the film, phase, waiting reason and an accessible detail action', () => {
    const html = renderToStaticMarkup(<QueueJobsTable jobs={[movie]} onSelect={() => {}} />);
    expect(html).toContain('TEST-001');
    expect(html).toContain('影片整理');
    expect(html).toContain('等待搬运任务 #5');
    expect(html).toContain('查看任务 #7');
  });
  it('shows every part and linked task without executing or treating untrusted input as markup', () => {
    const html = renderToStaticMarkup(<QueueJobDetails job={{ ...movie, title: '<img src=x>', source_url: 'javascript:alert(1)', error: '<script>bad()</script>' }} events={[]} onSelect={() => {}} onAction={() => {}} />);
    expect(html).toContain('/pending/TEST-001.1.mp4');
    expect(html).toContain('/pending/TEST-001.2.mp4');
    expect(html).toContain('任务 #5');
    expect(html).toContain('&lt;script&gt;');
    expect(html).not.toContain('<script>');
    expect(html).not.toContain('href="javascript:');
    expect(html).toContain('取消任务');
  });
  it('offers cleanup-only retry, renders events and explains that a torrent is not a completed movie', () => {
    const job = { ...movie, kind: 'torrent', phase: 'qb_cleanup', status: 'failed', child_job_ids: [8, 9], waiting_for_intake: [] };
    const html = renderToStaticMarkup(<QueueJobDetails job={job} events={[{ id: 1, reason: 'intake_movies_published', status: 'running', phase: 'qb_cleanup', message: '已登记' }]} onSelect={() => {}} onAction={() => {}} />);
    expect(html).toContain('重试 qB 清理');
    expect(html).toContain('任务 #8');
    expect(html).toContain('任务 #9');
    expect(html).toContain('影片任务已登记');
    const done = renderToStaticMarkup(<QueueJobDetails job={{ ...job, phase: 'done', status: 'success' }} events={[]} onSelect={() => {}} onAction={() => {}} />);
    expect(done).toContain('不代表影片整理成功');
    expect(done).not.toContain('取消任务');
    expect(done).not.toContain('重试 qB 清理');
  });
  it('renders a read-only, timestamped download observation without disguising stale data as live', () => {
    const job = { ...movie, kind: 'torrent', phase: 'download', waiting_for_intake: [], discovery: { download_path: '/downloads' }, intake_source_root: '/old-downloads', download: { progress: .25, amount_left: 1048576, state: 'downloading', content_path: '/downloads/<TEST>.mp4', checked_at: '2026-09-06T10:01:00Z', observed_at: '2026-09-06T10:00:00Z', error: '连接失败' } };
    const html = renderToStaticMarkup(<QueueJobDetails job={job} events={[]} onSelect={() => {}} onAction={() => {}} />);
    for (const text of ['qB 下载状态', 'qB 下载进度', 'value="25"', 'max="100"', '上次成功读取', '最近检查', '上次读数，非实时状态', '1 MiB', '投递保存目录', '/old-downloads', '不会停止 qB 下载']) expect(html).toContain(text);
    expect(html).toContain('&lt;TEST&gt;');
    expect(html).not.toContain('<TEST>');
    const unknown = renderToStaticMarkup(<QueueJobDetails job={{ ...job, discovery: { download_path: '' }, download: { ...job.download, observed_at: '' } }} events={[]} onSelect={() => {}} onAction={() => {}} />);
    expect(unknown).not.toContain('<progress');
    expect(unknown).toContain('qB 默认目录');
    const advanced = renderToStaticMarkup(<QueueJobDetails job={{ ...job, phase: 'transfer' }} events={[]} onSelect={() => {}} onAction={() => {}} />);
    expect(advanced).not.toContain('<progress');
  });
});
