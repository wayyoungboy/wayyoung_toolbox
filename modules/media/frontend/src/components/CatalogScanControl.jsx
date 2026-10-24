import { useEffect, useRef, useState } from 'react';
import { api } from '../api';

const scanLabels = { pending: '等待后台扫描', running: '后台扫描中', paused: '扫描已暂停', success: '扫描已完成', canceled: '扫描已取消', failed: '扫描未完成' };

export function CatalogScanStatus({ job, ready = false, busy = false, locked = false, error = '', notice = '', onStart, onAction, onReload }) {
  const disabled = !ready || busy || locked;
  const active = job && ['pending', 'running', 'paused'].includes(job.status);
  return <section className="catalog-scan-status" aria-label="后台馆藏扫描">
    <strong>{!ready ? '读取扫描状态…' : job ? scanLabels[job.status] || '扫描状态未知' : '尚未扫描'}</strong>
    {job ? <>
      <p className="catalog-help">已读取 {job.processed_files} / {job.discovered_files} 个候选视频 · 已列出 {job.entries} 个目录条目</p>
      {job.current_path ? <code title={job.current_path}>{job.current_path}</code> : null}
      {job.result && job.status === 'success' ? <p className="catalog-notice">{job.result.work_count} 部作品 · {job.result.asset_count} 个文件 · {job.result.issue_count} 个文件资料需检查</p> : null}
      {job.error ? <p className="catalog-alert">{job.error}</p> : null}
    </> : null}
    <p className="catalog-help">关闭窗口不会停止扫描；可稍后回来查看进度。完整成功后才更新馆藏，暂停或失败时保留上次馆藏。原文件不会修改。</p>
    {notice ? <p role="status" className="catalog-notice">{notice}</p> : null}
    {error ? <p role="alert" className="catalog-alert">{error}</p> : null}
    <div className="catalog-actions">
      {!active && (!job || job.status === 'success') ? <button type="button" className="queue-button primary" disabled={disabled} onClick={onStart}>{job ? '重新扫描' : '扫描馆藏'}</button> : null}
      {job?.status === 'failed' || job?.status === 'canceled' ? <button type="button" className="queue-button primary" disabled={disabled} onClick={() => onAction('retry')}>从头重试</button> : null}
      {job?.status === 'paused' ? <button type="button" className="queue-button primary" disabled={disabled} onClick={() => onAction('resume')}>继续扫描</button> : null}
      {job?.status === 'pending' || job?.status === 'running' ? <button type="button" className="queue-button" disabled={disabled} onClick={() => onAction('pause')}>暂停扫描</button> : null}
      {active ? <button type="button" className="queue-button" disabled={disabled} onClick={() => onAction('cancel')}>取消扫描</button> : null}
      <button type="button" className="queue-button" disabled={busy} onClick={onReload}>重新读取扫描状态</button>
    </div>
  </section>;
}

export default function CatalogScanControl({ libraryID, onChanged }) {
  const [job, setJob] = useState(null);
  const [ready, setReady] = useState(false);
  const [busy, setBusy] = useState(false);
  const [locked, setLocked] = useState(false);
  const [error, setError] = useState('');
  const [readError, setReadError] = useState('');
  const [notice, setNotice] = useState('');
  const [revision, setRevision] = useState(0);
  const live = useRef(false);
  const saving = useRef(false);
  const epoch = useRef(0);
  const previous = useRef(null);
  const changed = useRef(onChanged);
  useEffect(() => { changed.current = onChanged; }, [onChanged]);
  useEffect(() => { live.current = true; return () => { live.current = false; }; }, []);
  useEffect(() => {
    const controller = new AbortController();
    let timer;
    setReady(false);
    const load = async () => {
      const currentEpoch = epoch.current;
      try {
        if (!saving.current) {
          const result = await api.catalogLibraryScans(libraryID, { signal: controller.signal });
          if (!controller.signal.aborted && currentEpoch === epoch.current) {
            const value = result.active || result.items[0] || null;
            const completed = value?.status === 'success' && previous.current && (previous.current.id !== value.id || previous.current.status !== 'success');
            previous.current = value;
            setJob(value); setReady(true); setReadError('');
            if (completed) await changed.current?.();
          }
        }
      } catch (failure) {
        if (!controller.signal.aborted && currentEpoch === epoch.current) { setReady(false); setReadError(failure.message); }
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(load, 5000);
      }
    };
    load();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [libraryID, revision]);

  const mutate = async action => {
    if (!ready || locked || saving.current || (action !== 'start' && !job)) return;
    saving.current = true; epoch.current++; setBusy(true); setError(''); setNotice('');
    try {
      const value = action === 'start' ? await api.catalogStartScan(libraryID) : await api.catalogScanAction(job.id, job.version, action);
      if (live.current) {
        previous.current = value; setJob(value);
        setNotice(action === 'cancel' ? '扫描已取消，原文件与上次馆藏保留。' : action === 'pause' ? '扫描已暂停，进度已保存。' : '后台任务已提交，可关闭此窗口。');
      }
    } catch (failure) {
      if (live.current) { setLocked(true); setError(`${failure.message}；请重新读取扫描状态后核对，没有自动重试。`); }
    } finally {
      saving.current = false; epoch.current++; if (live.current) setBusy(false);
    }
  };
  const reload = () => { epoch.current++; setLocked(false); setError(''); setReadError(''); setNotice(''); setRevision(value => value + 1); };
  return <CatalogScanStatus job={job} ready={ready} busy={busy} locked={locked} error={error || readError} notice={notice} onStart={() => mutate('start')} onAction={mutate} onReload={reload} />;
}
