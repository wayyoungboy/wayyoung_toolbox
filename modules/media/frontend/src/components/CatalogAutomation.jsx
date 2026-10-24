import { useEffect, useRef, useState } from 'react';
import { api } from '../api';

export function CatalogAutomationForm({ config, enabled, loading = false, saving = false, locked = false, error = '', notice = '', onToggle, onSave, onReload }) {
  const disabled = loading || saving || locked || !config;
  return <section className="catalog-automation" aria-label="馆藏自动登记">
    <div className="section-heading"><h3>整理与馆藏衔接</h3><span>{loading ? '读取中' : !config ? '状态未知' : config.after_organize ? '已开启' : '已关闭'}</span></div>
    <p className="catalog-help">仅影响之后成功整理的影片：只登记已确认的输出文件，不会扫描已有目录，不会重新刮削或搬运。请先把成品目录添加为媒体库。</p>
    <form onSubmit={event => { event.preventDefault(); if (!disabled) onSave(); }}>
      <label className="catalog-check"><input type="checkbox" checked={enabled} disabled={disabled} onChange={event => onToggle(event.target.checked)} />整理后自动登记馆藏</label>
      <p className="catalog-help">关闭不会撤回已完成的登记；已排队的登记任务需在处理队列单独取消。登记失败也只重试登记。</p>
      {notice ? <p role="status" className="catalog-notice">{notice}</p> : null}
      {error ? <p role="alert" className="catalog-alert">{error}</p> : null}
      <div className="catalog-actions"><button type="submit" className="queue-button primary" disabled={disabled}>保存登记设置</button><button type="button" className="queue-button" disabled={loading || saving} onClick={onReload}>重新读取登记设置</button></div>
    </form>
  </section>;
}

export default function CatalogAutomation() {
  const [config, setConfig] = useState(null);
  const [enabled, setEnabled] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [locked, setLocked] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [revision, setRevision] = useState(0);
  const active = useRef(false);
  const savingRef = useRef(false);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setConfig(null); setError(''); setNotice(''); setLocked(false);
    api.catalogAutomation({ signal: controller.signal }).then(value => {
      if (!controller.signal.aborted) { setConfig(value); setEnabled(value.after_organize); }
    }).catch(failure => { if (!controller.signal.aborted) setError(failure.message); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [revision]);
  const save = async () => {
    if (!config || loading || locked || savingRef.current) return;
    savingRef.current = true; setSaving(true); setError(''); setNotice('');
    try {
      const value = await api.catalogAutomationSave({ after_organize: enabled, version: config.version });
      if (active.current) { setConfig(value); setEnabled(value.after_organize); setNotice('登记设置已保存。仅影响之后成功整理的影片。'); }
    } catch (failure) {
      if (active.current) { setLocked(true); setError(`${failure.message}；请重新读取登记设置后核对，没有自动重试。`); }
    } finally { savingRef.current = false; if (active.current) setSaving(false); }
  };
  return <CatalogAutomationForm config={config} enabled={enabled} loading={loading} saving={saving} locked={locked} error={error} notice={notice} onToggle={setEnabled} onSave={save} onReload={() => setRevision(value => value + 1)} />;
}
