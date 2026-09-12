import { useCallback } from 'react';
import { api } from '../api';
import { loadQueueOverview } from '../queue-view';
import { useRemoteResource } from '../useRemoteResource';
import { QueueJobsTable } from './QueueViews';

export default function QueueWorkbench({ onNavigate }) {
  const load = useCallback((signal) => loadQueueOverview(api.queue, signal), []);
  const { data, error, loading, refresh } = useRemoteResource(load, 5000);
  const open = (id) => onNavigate('queue', 'jobs', id);
  return <section className="queue-workbench" aria-label="影片任务概况">
    <div className="section-heading"><div><h2>影片处理概况</h2><p>读取持久任务的当前状态，不把历史失败当成仍未处理。</p></div><button type="button" disabled={loading} onClick={refresh}>刷新任务</button></div>
    {error ? <div className="workspace-error" role="alert"><div><strong>任务状态读取失败</strong><span>{error}</span></div><button type="button" className="queue-button" onClick={refresh}>重新读取任务</button></div> : null}
    {!data && loading ? <p role="status">正在读取影片任务…</p> : null}
    {data ? <>
      <dl className="queue-metrics"><div><dt>运行中任务</dt><dd>{data.running.total}</dd></div><div><dt>排队 / 等待下载</dt><dd>{data.pending.total}</dd></div><div><dt>需要处理</dt><dd>{data.attentionTotal}</dd></div></dl>
      <div className="section-heading"><h3>正在处理的任务</h3><button type="button" onClick={() => onNavigate('queue')}>查看处理队列</button></div>
      {data.running.jobs.length ? <QueueJobsTable jobs={data.running.jobs} onSelect={open} /> : <p className="queue-footnote">当前没有正在执行的任务。</p>}
      <div className="section-heading queue-attention-heading"><div><h3>需要处理的任务</h3><p>待人工处理、失败、部分成功和超时；点击任务查看原因及下一步。</p></div></div>
      {data.attention.length ? <QueueJobsTable jobs={data.attention} onSelect={open} /> : <p className="queue-footnote">当前没有需要处理的队列异常。</p>}
      {data.attentionTotal > data.attention.length ? <p className="queue-footnote">当前展示 {data.attention.length} 项，更多任务请打开处理队列筛选。</p> : null}
    </> : null}
  </section>;
}
