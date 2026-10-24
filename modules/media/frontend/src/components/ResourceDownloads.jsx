import { useCallback, useEffect, useRef, useState } from 'react';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Button from '@douyinfe/semi-ui/lib/es/button';
import { api, formatDate } from '../api';
import { useRemoteResource } from '../useRemoteResource';
import { resourceDestinationReady, resourceDownloadFailurePhase, resourceJobDestination } from '../resource-download-view';
import { downloadPercent, queuePhaseLabel, queueStatusLabel, queueSummary } from '../queue-view';

const loadIntake = signal => api.intake({ signal });

export function ResourceJobLink({ job, onNavigate }) {
  const label = `查看下载任务 #${job.id}`;
  return onNavigate ? <Button onClick={() => onNavigate('queue', 'jobs', job.id)}>{label}</Button>
    : <a href={`?page=queue&tab=jobs&job=${encodeURIComponent(job.id)}`}>{label}</a>;
}

// A mount only reads configuration. The sole POST is the confirmed event below;
// unknown outcomes are reconciled by identity with GET, never automatically replayed.
export function ResourceDownloadDialog({ selection, onClose, onRegistered, onNavigate }) {
  const { data, error: configError, loading, refresh } = useRemoteResource(loadIntake);
  const [confirmation, setConfirmation] = useState('');
  const [phase, setPhase] = useState('confirm');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [job, setJob] = useState(null);
  const [message, setMessage] = useState('');
  const [canReconfirm, setCanReconfirm] = useState(false);
  const lock = useRef(false);
  const mounted = useRef(false);
  const request = useRef(null);
  const epoch = useRef(0);
  const deadline = useRef(null);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; request.current?.abort(); window.clearTimeout(deadline.current); };
  }, []);
  const config = data?.config;
  const ready = resourceDestinationReady(config) && !loading && !configError;
  const stopWaiting = () => {
    ++epoch.current; request.current?.abort(); window.clearTimeout(deadline.current);
    lock.current = false; setBusy(false); setPhase('unknown'); setError(''); setCanReconfirm(false);
    setMessage('已停止等待，登记可能已经生效；请只读核对原任务，没有自动重发。');
  };
  const submit = async () => {
    if (lock.current || phase !== 'confirm' || !ready || confirmation !== '下载资源') return;
    lock.current = true; setBusy(true); setError(''); setCanReconfirm(false);
    const controller = new AbortController(); request.current = controller;
    const current = ++epoch.current;
    deadline.current = window.setTimeout(stopWaiting, 30000);
    try {
      const result = await api.resourceDownload(selection, config.revision, { signal: controller.signal });
      if (!Number.isSafeInteger(result.job?.id) || result.job.id < 1) throw new Error('响应缺少有效任务编号');
      if (!mounted.current || current !== epoch.current) return;
      setJob(result.job); setPhase('registered');
      setMessage(result.created ? '已登记到处理队列，尚未确认下载完成。' : '已关联已有任务；没有创建新的下载，也不会重启已取消或已完成的任务。');
      onRegistered();
    } catch (failure) {
      if (!mounted.current || current !== epoch.current) return;
      setPhase(resourceDownloadFailurePhase(failure)); setError(`${failure.message}；没有自动重发。`);
      onRegistered();
    } finally {
      if (current === epoch.current) { window.clearTimeout(deadline.current); lock.current = false; if (mounted.current) setBusy(false); }
    }
  };
  const reconcile = async () => {
    if (lock.current || phase !== 'unknown') return;
    lock.current = true; setBusy(true); setError(''); setCanReconfirm(false);
    const controller = new AbortController(); request.current = controller;
    const current = ++epoch.current;
    deadline.current = window.setTimeout(stopWaiting, 30000);
    try {
      const result = await api.resourceDownloadSelection(selection, { signal: controller.signal });
      if (!mounted.current || current !== epoch.current) return;
      if (Number.isSafeInteger(result.job?.id) && result.job.id > 0) {
        setJob(result.job); setPhase('registered'); setMessage('已找到原任务；没有重新提交下载。'); onRegistered();
      } else if (result.job === null) {
        setMessage('尚未找到登记记录，仍不能断言原请求失败；没有自动重发。可稍后再次核对，或重新确认同一选择。'); setCanReconfirm(true);
      } else throw new Error('核对结果不完整');
    } catch (failure) { if (mounted.current && current === epoch.current) setError(`${failure.message}；请稍后再次核对。`); }
    finally { if (current === epoch.current) { window.clearTimeout(deadline.current); lock.current = false; if (mounted.current) setBusy(false); } }
  };
  const closeAllowed = !busy && phase !== 'unknown';
  return <Modal title="确认资源下载" visible width="min(680px, calc(100vw - 24px))" className="resource-download-modal" onCancel={() => { if (closeAllowed) onClose(phase === 'rejected'); }} closable={closeAllowed} maskClosable={closeAllowed} closeOnEsc={closeAllowed} footer={null}>
    <dl className="resource-download-facts">
      <div><dt>作品 / 来源</dt><dd>{selection.code} · JavDB</dd></div>
      <div><dt>选中 BTIH</dt><dd><code>{selection.infohash}</code></dd></div>
      <div><dt>搜索快照</dt><dd>{formatDate(selection.searched_at)}</dd></div>
      <div><dt>下载目录</dt><dd><code>{resourceJobDestination(job, ready ? config : null)}</code></dd></div>
    </dl>
    {phase === 'confirm' && <>
      <p className="catalog-help">将通过当前配置的 qBittorrent 下载。候选内容、做种和画质尚未校验；只登记此资源，不会立即扫描、搬运或整理媒体目录。</p>
      <p className="catalog-help">下载目录来自“下载入库”的来源目录。配置改变时会拒绝新登记；已有任务将继续使用原目录和下载器身份。</p>
      {loading && <p role="status">正在只读核对下载目录…</p>}
      {(configError || (!loading && !ready)) && <p role="alert" className="catalog-alert">{configError || '下载目录配置无效，请先到下载入库设置核对。'} <Button onClick={refresh}>重新读取配置</Button></p>}
      <label className="resource-confirm-label">输入“下载资源”确认<input value={confirmation} disabled={busy} onChange={event => setConfirmation(event.target.value)} autoComplete="off" /></label>
      <div className="catalog-actions"><Button disabled={busy} onClick={() => onClose(false)}>返回</Button><Button theme="solid" disabled={!ready || busy || confirmation !== '下载资源'} onClick={submit}>{busy ? '正在登记…' : '确认登记下载'}</Button></div>
    </>}
    {phase === 'unknown' && <>
      <h3 role="alert">登记结果未确认</h3>
      <p className="catalog-help">请求可能已被服务器接受。请先核对原任务，不要把没有收到响应当成下载失败。关闭页面也不会自动重发。</p>
      <div className="catalog-actions"><Button disabled={busy} onClick={reconcile}>{busy ? '正在只读核对…' : '核对登记结果'}</Button>{canReconfirm && <Button disabled={busy} onClick={() => { setPhase('confirm'); setConfirmation(''); setError(''); setMessage(''); refresh(); }}>重新确认同一选择</Button>}</div>
    </>}
    {phase === 'rejected' && <><h3 role="alert">登记未通过</h3><p>服务器已明确拒绝本次选择。请重新读取候选和配置，不会循环重发失效快照。</p><Button onClick={() => onClose(true)}>返回重新选择</Button></>}
    {busy && <Button onClick={stopWaiting}>停止等待</Button>}
    {phase === 'registered' && job && <><h3 role="status">{message}</h3><p className="catalog-help">任务 #{job.id} · {queuePhaseLabel(job)} · {queueStatusLabel(job.status)}。停止本地处理不会停止 qB 下载，也不会删除文件。</p>{job.torrent_hash && job.torrent_hash !== selection.infohash && <p className="catalog-help">该作品已由其他种子处理，本次选择未投递。原任务 BTIH：<code>{job.torrent_hash}</code></p>}<div className="catalog-actions"><ResourceJobLink job={job} onNavigate={onNavigate} /><Button onClick={() => onClose(false)}>关闭</Button></div></>}
    {phase === 'unknown' && message && <p role="status">{message}</p>}
    {error && <p role="alert" className="catalog-alert">{error}</p>}
  </Modal>;
}

export function ResourceDownloadList({ jobs, onNavigate }) {
  return <ul className="resource-download-list">{jobs.map(job => {
    const percent = job.phase === 'download' ? downloadPercent(job.download) : null;
    return <li key={job.id}>
      <div className="resource-download-heading"><strong>{job.media_id || job.title} · #{job.id}</strong><span>{queuePhaseLabel(job)} · {queueStatusLabel(job.status)}</span></div>
      <p>{queueSummary(job)}</p>
      {percent !== null && <><progress aria-label={`下载任务 #${job.id} 进度`} value={percent} max={100} /><p className="catalog-help">{percent}% · {job.download.error ? '上次读数，非实时状态' : '上次成功读取'} · {formatDate(job.download.observed_at)}</p></>}
      <p className="catalog-help">下载目录：<code>{job.resource?.download_path || '查看原任务'}</code> · 更新 {formatDate(job.updated_at)}</p>
      <ResourceJobLink job={job} onNavigate={onNavigate} />
    </li>;
  })}</ul>;
}

export default function ResourceDownloads({ revision, onNavigate }) {
  const load = useCallback(signal => { void revision; return api.resourceDownloads({ signal }); }, [revision]);
  const { data, error, loading, refresh } = useRemoteResource(load, 5000);
  return <section className="resource-downloads" aria-label="资源下载记录">
    <div className="resource-download-heading"><h3>资源下载记录</h3><Button disabled={loading} onClick={refresh}>刷新下载记录</Button></div>
    <p className="catalog-help">最近 20 项独立资源任务，每 5 秒只读刷新。已登记 ≠ 下载完成 ≠ 整理成功；完整进度与关联影片在处理队列中查看。</p>
    {loading && !data && <p role="status">正在读取下载记录…</p>}
    {error && <p role="alert" className="catalog-alert">下载记录暂不可读：{error}。没有自动提交下载。</p>}
    {data?.items?.length ? <ResourceDownloadList jobs={data.items} onNavigate={onNavigate} /> : data && !error && <p className="catalog-help">尚无独立资源下载；搜索或打开页面不会自动创建任务。</p>}
  </section>;
}
