import { useCallback, useEffect, useState } from 'react';
import { api } from '../api';
import { queueActionPrompt, queueStatuses } from '../queue-view';
import { useRemoteResource } from '../useRemoteResource';
import { QueueJobDetails, QueueJobsTable } from '../components/QueueViews';
import WorkspaceTabs from '../components/WorkspaceTabs';
import Runs from './Runs';
import QueueCleanupDialog from '../components/QueueCleanupDialog';

const tabs = [{ key: 'jobs', label: '影片队列' }, { key: 'history', label: '运行记录' }];

function QueueDetail({ id, onClose, onSelect, onChanged, onNavigate }) {
  const load = useCallback((signal) => api.queueJob(id, { signal }), [id]);
  const { data, loading, error, refresh } = useRemoteResource(load, 5000);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState('');
  const jobID = data?.job?.id;
  const jobVersion = data?.job?.version;
  // A newly observed detail revision also invalidates the list snapshot. Do
  // not wait for its independent poll or refetch on unchanged detail reads.
  useEffect(() => { if (jobID && jobVersion) onChanged(); }, [jobID, jobVersion, onChanged]);
  const act = async (action, job) => {
    const prompt = queueActionPrompt(action, job);
    if (busy || loading || !window.confirm(prompt)) return;
    setBusy(true);
    setNotice('');
    try {
      if (action === 'retry') await api.retryQueueJob(job.id, job.version);
      else await api.cancelQueueJob(job.id, job.version);
      onChanged();
    } catch (error) {
      setNotice(error.status === 409 ? '任务已变化，正在刷新详情。请核对后重新操作；没有自动重发。' : `${error.message}；请刷新核对任务状态后再操作。`);
    } finally { refresh(); setBusy(false); }
  };
  return <aside className="queue-inspector" role="region" aria-label="任务详情">
    <div className="section-heading"><h2>任务 #{id}</h2><div className="header-actions"><button type="button" disabled={loading || busy} onClick={refresh}>刷新详情</button><button type="button" disabled={busy} onClick={onClose} aria-label="关闭详情">关闭</button></div></div>
    {notice ? <div className="workspace-error" role="alert">{notice}</div> : null}
    {error ? <div className="workspace-error" role="alert"><div><strong>无法读取任务</strong><span>{error}</span></div><button type="button" className="queue-button" onClick={refresh}>重新读取详情</button></div> : null}
    {!data && loading ? <p role="status">正在读取任务详情…</p> : null}
    {data ? <QueueJobDetails job={data.job} events={data.events || []} busy={busy || loading} onSelect={onSelect} onAction={act} onNavigate={onNavigate} /> : null}
  </aside>;
}

function Jobs({ onNavigate, selectedID }) {
  const [cleanup,setCleanup]=useState(false);
  const [paging, setPaging] = useState({ status: '', before: 0, previous: [] });
  const setSelectedID = (id) => onNavigate('queue', 'jobs', id);
  const load = useCallback((signal) => api.queue({ status: paging.status, before: paging.before, limit: 25, signal }), [paging.status, paging.before]);
  const { data, loading, error, refresh } = useRemoteResource(load, 5000);
  const next = () => setPaging((previous) => ({ ...previous, before: data.next_cursor, previous: [...previous.previous, previous.before] }));
  const previous = () => setPaging((value) => ({ ...value, before: value.previous.at(-1) || 0, previous: value.previous.slice(0, -1) }));
  return <>
    <div className="queue-toolbar">
      <label>状态<select aria-label="队列状态" value={paging.status} onChange={(event) => setPaging({ status: event.target.value, before: 0, previous: [] })}><option value="">全部状态</option>{queueStatuses.map(([key, label]) => <option value={key} key={key}>{label}</option>)}<option value="archived">已清理归档</option></select></label>
      <button type="button" className="queue-button" disabled={loading} onClick={refresh}>刷新队列</button>
      <button type="button" className="queue-button" onClick={()=>setCleanup(true)}>清理过期任务</button>
      <span className="queue-footnote">每 5 秒只读刷新</span>
      <button className="queue-link" type="button" onClick={() => onNavigate('settings', 'intake')}>入库设置与操作</button>
      <button className="queue-link" type="button" onClick={() => onNavigate('settings', 'javsp')}>扫描设置与入队</button>
    </div>
    <div className={`queue-workspace${selectedID ? ' with-detail' : ''}`}>
      <div className="queue-list">
        {error ? <div className="workspace-error" role="alert"><div><strong>无法读取队列</strong><span>{error}</span></div><button type="button" className="queue-button" onClick={refresh}>重新读取</button></div> : null}
        {!data && loading ? <div className="queue-empty" role="status">正在读取队列…</div> : null}
        {data?.jobs?.length ? <QueueJobsTable jobs={data.jobs} selectedID={selectedID} onSelect={setSelectedID} /> : data && !error ? <div className="queue-empty">当前筛选条件下没有任务</div> : null}
        {data ? <div className="queue-pagination"><span>当前 {data.jobs?.length || 0} 项 · 此状态共 {data.total} 项</span><div><button type="button" className="queue-button" disabled={loading || !paging.previous.length} onClick={previous}>上一页</button><button type="button" className="queue-button" disabled={loading || !data.next_cursor} onClick={next}>下一页</button></div></div> : null}
      </div>
      {selectedID ? <QueueDetail key={selectedID} id={selectedID} onClose={() => setSelectedID(null)} onSelect={setSelectedID} onChanged={refresh} onNavigate={onNavigate} /> : null}
    </div>
    {cleanup&&<QueueCleanupDialog onClose={()=>setCleanup(false)} onChanged={refresh}/>}
  </>;
}

export default function Queue({ tab = 'jobs', jobID, onTabChange, onNavigate }) {
  return <section className="page queue-page"><header className="page-header"><div><h1>处理队列</h1><p>按影片接续未完成阶段。搬运任务与影片任务分别记录结果，打开页面不会触发扫描。</p></div></header>
    <WorkspaceTabs label="队列工作区" items={tabs} selected={tab} onChange={onTabChange} />
    {tab === 'history' ? <Runs embedded /> : <Jobs onNavigate={onNavigate} selectedID={jobID} />}
  </section>;
}
