import { useCallback, useEffect, useRef, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import { api, formatDate } from '../api';
import { useRemoteResource } from '../useRemoteResource';
import { ResourceJobLink } from '../components/ResourceDownloads';
import '../catalog.css';
import '../wants.css';

const phases = { searching: '待查找', unreleased: '未发售', given_up: '已放弃', acquired: '已入库' };
const emptyForm = { code: '', title: '', release_date: '', actors: '', studio: '' };

function MetadataFields({ form, setForm, disabled }) {
  const change = event => setForm(previous => ({ ...previous, [event.target.name]: event.target.value }));
  return <div className="wants-fields">
    <label>作品标题（可选）<input aria-label="作品标题（可选）" name="title" maxLength={1000} value={form.title} disabled={disabled} onChange={change} /></label>
    <label>发行日期（可选）<input aria-label="发行日期（可选）" name="release_date" type="date" value={form.release_date} disabled={disabled} onChange={change} /></label>
    <label>演员（可选）<input aria-label="演员（可选）" name="actors" maxLength={20100} value={form.actors} disabled={disabled} onChange={change} placeholder="多位演员用逗号或顿号分隔" /></label>
    <label>厂牌（可选）<input aria-label="厂牌（可选）" name="studio" maxLength={300} value={form.studio} disabled={disabled} onChange={change} /></label>
  </div>;
}

function metadata(form) {
  return { title: form.title.trim(), release_date: form.release_date, studio: form.studio.trim(), actors: form.actors.split(/[,，、\n]/).map(value => value.trim()).filter(Boolean) };
}

function consumePrefill() {
  const url = new URL(window.location.href);
  if (url.searchParams.get('page') === 'subscriptions' && url.searchParams.get('tab') === 'wants' && url.searchParams.has('code')) {
    url.searchParams.delete('code');
    window.history.replaceState(null, '', url);
  }
}

export function WantCard({ want, disabled, onNavigate, onEdit, onState, onRemove }) {
  const acquired = want.state === 'acquired';
  const searchable = !acquired && want.phase !== 'unreleased' && want.state !== 'given_up';
  return <article className="want-card" aria-label={`想要作品 ${want.code}`}>
    <div className="want-card-heading"><div><span className="want-code">{want.code}</span><h2>{want.title || '标题待补齐'}</h2></div><span className={`want-phase want-phase-${want.phase}`}>{phases[want.phase] || '状态待确认'}</span></div>
    <dl className="want-facts">
      <div><dt>演员</dt><dd>{want.actors?.length ? want.actors.join(' / ') : '未提供'}</dd></div>
      <div><dt>发行 / 厂牌</dt><dd>{want.release_date || '日期未提供'} · {want.studio || '厂牌未提供'}</dd></div>
      <div><dt>更新</dt><dd><time>{formatDate(want.updated_at)}</time> · {want.origin === 'manual' ? '手工登记' : '来源记录'}</dd></div>
    </dl>
    {acquired ? <p className="catalog-help">{want.available ? '文件已登记到馆藏，下载记录不是入库依据。' : '保留历史入库凭据；对应文件当前不可用或资料已变更，不会自动重新下载。'}</p>
      : <p className="catalog-help">{want.phase === 'unreleased' ? '尚未到填写的发行日期，暂不从此处搜索。' : want.state === 'given_up' ? '已暂停查找，可随时恢复；不会取消已经存在的下载。' : '尚未登记入库。这里不自动查询、下载或运行整理。'}</p>}
    {want.acquisition && <details className="want-receipt"><summary>入库凭据</summary><dl className="want-facts"><div><dt>来源</dt><dd>{({ scan: '馆藏扫描', registration: '文件登记', organizer: '整理任务' })[want.acquisition.source] || '文件登记'}</dd></div><div><dt>时间</dt><dd>{formatDate(want.acquisition.acquired_at)}</dd></div><div><dt>批次</dt><dd><code>{want.acquisition.batch}</code></dd></div></dl></details>}
    <div className="catalog-actions">
      <Button disabled={disabled || !searchable} onClick={() => onNavigate('subscriptions', 'resources', null, { code: want.code })}>搜索资源</Button>
      {acquired && <Button disabled={disabled || !want.available || !want.acquisition?.work_id} onClick={() => onNavigate('catalog', '', null, { work: want.acquisition.work_id })}>查看馆藏作品</Button>}
      {want.last_job_id > 0 && <ResourceJobLink job={{ id: want.last_job_id }} onNavigate={onNavigate} />}
      <Button disabled={disabled} onClick={() => onEdit(want.id)}>编辑资料</Button>
      {!acquired && <Button disabled={disabled} onClick={() => onState(want)}>{want.state === 'given_up' ? '继续查找' : '暂时放弃'}</Button>}
      <Button disabled={disabled} onClick={() => onRemove(want)}>移出想要</Button>
    </div>
  </article>;
}

function WantEditForm({ want, onClose, onChanged, onReload }) {
  const [form, setForm] = useState({ ...want, actors: (want.actors || []).join('、') });
  const [busy, setBusy] = useState(false), [error, setError] = useState(''), [locked, setLocked] = useState(false);
  const request = useRef(null), lock = useRef(false), alive = useRef(false);
  useEffect(() => { alive.current = true; return () => { alive.current = false; request.current?.abort(); }; }, []);
  const save = async event => {
    event.preventDefault();
    if (lock.current || locked) return;
    lock.current = true; setBusy(true); setError('');
    const controller = new AbortController(); request.current = controller;
    const deadline = window.setTimeout(() => controller.abort(), 30000);
    try {
      await api.wantPatch(want.id, { version: want.version, ...metadata(form) }, { signal: controller.signal });
      if (alive.current) { onChanged(); onClose(); }
    } catch (failure) {
      if (alive.current) { setLocked(true); setError(`${failure.message}；没有自动重试。请重新读取资料后再编辑，避免覆盖其他更新。`); }
    } finally { window.clearTimeout(deadline); lock.current = false; if (alive.current) setBusy(false); }
  };
  return <>
    <p className="catalog-help">{want.code} · 只修改想要清单里的资料，不改原视频、NFO 或馆藏标题。</p>
    <form className="wants-form" onSubmit={save}>
      <MetadataFields form={form} setForm={setForm} disabled={busy || locked} />
      {error && <p className="catalog-alert" role="alert">{error}</p>}
      <div className="catalog-actions"><Button htmlType="submit" theme="solid" disabled={busy || locked}>保存想要资料</Button><Button disabled={busy} onClick={onReload}>重新读取资料</Button><Button disabled={busy} onClick={onClose}>取消</Button></div>
    </form>
  </>;
}

function WantEditor({ id, onClose, onChanged }) {
  const [revision, setRevision] = useState(0);
  const load = useCallback(signal => { void revision; return api.want(id, { signal }); }, [id, revision]);
  const { data, loading, error } = useRemoteResource(load);
  return <Modal title="编辑想要资料" visible footer={null} width="min(700px, calc(100vw - 24px))" closable={false} maskClosable={false} closeOnEsc={false}>
    {loading && <p role="status">正在读取最新资料…</p>}
    {error && <><p className="catalog-alert" role="alert">{error}</p><div className="catalog-actions"><Button onClick={() => setRevision(value => value + 1)}>重新读取资料</Button><Button onClick={onClose}>取消</Button></div></>}
    {data && !loading && !error && <WantEditForm key={`${revision}:${data.version}`} want={data} onClose={onClose} onChanged={onChanged} onReload={() => setRevision(value => value + 1)} />}
  </Modal>;
}

export default function Wants({ initial = {}, onNavigate }) {
  const query = (initial.query || '').slice(0, 500);
  const phase = Object.hasOwn(phases, initial.view) ? initial.view : '';
  const parsedOffset = Number(initial.offset);
  const offset = Number.isSafeInteger(parsedOffset) && parsedOffset >= 0 && parsedOffset <= 10000 ? parsedOffset : 0;
  const [search, setSearch] = useState(query), [adding, setAdding] = useState(Boolean(initial.code));
  const [form, setForm] = useState({ ...emptyForm, code: (initial.code || '').slice(0, 128) });
  const [busy, setBusy] = useState(false), [uncertain, setUncertain] = useState(false), [notice, setNotice] = useState(''), [editing, setEditing] = useState(null);
  const lock = useRef(false), request = useRef(null), alive = useRef(false), pendingAdd = useRef('');
  const load = useCallback(signal => api.wants({ query, phase, offset, signal }), [query, phase, offset]);
  const { data, loading, error, refresh } = useRemoteResource(load);
  useEffect(() => { alive.current = true; return () => { alive.current = false; request.current?.abort(); }; }, []);
  useEffect(() => {
    if (!data || loading || error) return;
    const lastOffset = Math.max(0, Math.ceil(data.total / 48) - 1) * 48;
    if (offset > lastOffset) onNavigate('subscriptions', 'wants', null, { query, view: phase, offset: lastOffset ? String(lastOffset) : '' });
  }, [data, loading, error, offset, onNavigate, query, phase]);
  const disabled = busy || loading || uncertain || Boolean(error) || Boolean(editing);
  const filter = (view, nextOffset = 0, nextQuery = query) => { if (!busy) onNavigate('subscriptions', 'wants', null, { view, query: nextQuery, offset: nextOffset ? String(nextOffset) : '' }); };
  const act = async (action, success, after, code = '') => {
    if (lock.current || disabled) return;
    lock.current = true; setBusy(true); setNotice('');
    const controller = new AbortController(); request.current = controller;
    const deadline = window.setTimeout(() => controller.abort(), 30000);
    try {
      const result = await action(controller.signal);
      if (alive.current) { pendingAdd.current = ''; setNotice(typeof success === 'function' ? success(result) : success); after?.(); refresh(); }
    } catch (failure) {
      if (alive.current) { pendingAdd.current = code; setUncertain(true); setNotice(`${failure.status === 409 ? '资料版本或状态已变化' : '结果尚未确认'}：${failure.message}；没有自动重试。请刷新清单核对后再操作。`); }
    } finally { window.clearTimeout(deadline); lock.current = false; if (alive.current) setBusy(false); }
  };
  const add = event => {
    event.preventDefault();
    const code = form.code.trim().toUpperCase();
    act(signal => api.wantAdd({ code, ...metadata(form) }, { signal }), value => value.created ? '已登记想要，没有提交下载。' : '清单已存在，保留原有资料，没有覆盖。', () => { consumePrefill(); setAdding(false); setForm(emptyForm); }, code);
  };
  const changeState = want => act(signal => api.wantPatch(want.id, { version: want.version, state: want.state === 'given_up' ? 'wanted' : 'given_up' }, { signal }), '想要状态已更新，没有改变已有下载。');
  const remove = want => {
    if (disabled || !window.confirm(`移除想要“${want.code}”？只移除清单记录，不删除影片、不取消下载，也不删除观看记录。`)) return;
    act(signal => api.wantRemove(want.id, want.version, { signal }), '已移出想要清单，影片和下载任务保持不变。', () => { if (data?.items.length === 1 && offset > 0) filter(phase, Math.max(0, offset - 48)); });
  };
  const reread = async () => {
    if (lock.current || busy) return;
    if (!pendingAdd.current) { setUncertain(false); setNotice('已发起只读刷新，没有重新提交操作。'); refresh(); return; }
    const code = pendingAdd.current;
    lock.current = true; setBusy(true);
    const controller = new AbortController(); request.current = controller;
    const deadline = window.setTimeout(() => controller.abort(), 30000);
    try {
      // Reconcile the submitted identity, not the currently filtered page. A
      // missing result never automatically causes another POST.
      const result = await api.wantByCode(code, { signal: controller.signal });
      if (!alive.current) return;
      const found = result.items.some(want => want.code === code);
      pendingAdd.current = ''; setUncertain(false);
      if (found) {
        consumePrefill();
        onNavigate('subscriptions', 'wants', null, { query: code });
      } else {
        setNotice(`尚未找到 ${code}，不能断言原请求没有生效；保留原番号供你再次确认，没有自动重试。`);
        setAdding(true); refresh();
      }
    } catch (failure) { if (alive.current) setNotice(`核对 ${code} 失败：${failure.message}；仍未解锁原操作，没有自动重试。`); }
    finally { window.clearTimeout(deadline); lock.current = false; if (alive.current) setBusy(false); }
  };
  return <section className="page catalog-page wants-page">
    <header className="page-header"><div><h1>想要清单</h1><p>先登记想找的作品，再搜索资源；与馆藏的“稍后观看”分开。</p></div><div className="catalog-actions"><Button disabled={disabled} theme="solid" onClick={() => setAdding(value => !value)}>添加想要</Button><Button disabled={busy || loading || Boolean(editing)} onClick={reread}>刷新清单</Button></div></header>
    <p className="catalog-help">登记只写入本地清单，筛选与刷新只读本地资料；不自动访问外站、投递下载或扫描媒体。已入库状态由实际文件登记确认；元数据自动补齐和演员新作发现尚未接入此清单。</p>
    {notice && <p className={uncertain ? 'catalog-alert' : 'catalog-notice'} role="status">{notice}</p>}
    {adding && <form className="wants-form wants-add" onSubmit={add} aria-label="添加想要作品"><h2>登记作品</h2><label>想要的完整番号<input aria-label="想要的完整番号" required maxLength={128} value={form.code} disabled={busy || uncertain} autoComplete="off" spellCheck="false" placeholder="例如 TEST-001 / FC2-1234567" onChange={event => setForm(value => ({ ...value, code: event.target.value }))} /></label><MetadataFields form={form} setForm={setForm} disabled={busy || uncertain} /><p className="catalog-help">请保留完整前缀，不会猜测或修复缺失的番号。资料可留空，稍后手动补充。</p><div className="catalog-actions"><Button htmlType="submit" theme="solid" disabled={disabled || !form.code.trim()}>登记想要</Button><Button disabled={busy || uncertain} onClick={() => { consumePrefill(); setAdding(false); }}>取消登记</Button></div></form>}
    <div className="wants-toolbar"><form onSubmit={event => { event.preventDefault(); filter(phase, 0, search); }}><label className="wants-search">搜索想要清单<input aria-label="搜索想要清单" type="search" maxLength={500} value={search} disabled={busy} placeholder="番号、标题、演员或厂牌" onChange={event => setSearch(event.target.value)} /></label><Button htmlType="submit" disabled={busy || loading}>筛选清单</Button></form><div className="want-filters" aria-label="想要阶段筛选">{[['', '全部'], ...Object.entries(phases)].map(([key, label]) => <button type="button" key={key} aria-pressed={phase === key} disabled={busy || loading} onClick={() => filter(key)}>{label}<span>{data ? key ? data.counts[key] : Object.values(data.counts).reduce((sum, n) => sum + n, 0) : '—'}</span></button>)}</div></div>
    {error && <p role="alert" className="catalog-alert">清单暂不可读：{error}。没有提交下载或自动重试。</p>}
    {loading && !data && <p role="status" className="catalog-loading">正在读取想要清单…</p>}
    {data && <><p className="catalog-help">当前筛选 {data.total} 项 · 每页 48 项</p><div className="wants-grid">{data.items.map(want => <WantCard key={want.id} want={want} disabled={disabled} onNavigate={onNavigate} onEdit={setEditing} onState={changeState} onRemove={remove} />)}</div></>}
    {!loading && !error && data?.total === 0 && <div className="catalog-empty"><h2>{query || phase ? '此筛选下没有想要作品' : '还没有登记想要的作品'}</h2><p>登记完整番号即可保留查找意图；已有馆藏请使用收藏或稍后观看。</p></div>}
    {(data?.total > 48 || offset > 0) && <nav className="catalog-pagination" aria-label="想要清单分页"><Button disabled={busy || loading || offset === 0} onClick={() => filter(phase, Math.max(0, offset - 48))}>上一页</Button><span>{Math.floor(offset / 48) + 1} / {Math.max(1, Math.ceil((data?.total || 0) / 48))}</span><Button disabled={busy || loading || offset + 48 >= (data?.total || 0)} onClick={() => filter(phase, offset + 48)}>下一页</Button></nav>}
    {editing && <WantEditor id={editing} onClose={() => setEditing(null)} onChanged={refresh} />}
  </section>;
}
