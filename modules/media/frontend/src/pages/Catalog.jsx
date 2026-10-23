import { useCallback, useEffect, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import { api } from '../api';
import CatalogCards from '../components/CatalogCards';
import CatalogDetails from '../components/CatalogDetails';
import CatalogLibraryManager from '../components/CatalogLibraryManager';
import '../catalog.css';

const views = [['all', '全部影片'], ['continue', '继续观看'], ['favorites', '我的收藏']];

export default function Catalog() {
  const [libraries, setLibraries] = useState([]);
  const [completedRoot, setCompletedRoot] = useState('');
  const [manager, setManager] = useState(false);
  const [selected, setSelected] = useState('');
  const [input, setInput] = useState('');
  const [query, setQuery] = useState('');
  const [actor, setActor] = useState('');
  const [tag, setTag] = useState('');
  const [view, setView] = useState('all');
  const [libraryID, setLibraryID] = useState('');
  const [offset, setOffset] = useState(0);
  const [revision, setRevision] = useState(0);
  const [page, setPage] = useState({ items: [], total: 0 });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const refresh = useCallback(() => setRevision(value => value + 1), []);
  const loadLibraries = useCallback(async (signal) => {
    const result = await api.catalogLibraries({ signal });
    if (!signal?.aborted) { setLibraries(result.items); setCompletedRoot(result.completed_root || ''); }
  }, []);
  const libraryChanged = async () => { await loadLibraries(); refresh(); };
  useEffect(() => {
    const controller = new AbortController();
    loadLibraries(controller.signal).catch(failure => { if (failure.name !== 'AbortError') setError(failure.message); });
    return () => controller.abort();
  }, [loadLibraries]);
  useEffect(() => {
    const timer = setTimeout(() => { setQuery(input); setOffset(0); }, 300);
    return () => clearTimeout(timer);
  }, [input]);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setError('');
    api.catalogWorks({ query, actor, tag, view, libraryID, offset, signal: controller.signal })
      .then(result => { if (!controller.signal.aborted) setPage(result); })
      .catch(failure => { if (!controller.signal.aborted) setError(failure.message); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [query, actor, tag, view, libraryID, offset, revision]);
  const relatedSearch = ({ actor = '', tag = '' }) => {
    setSelected(''); setInput(''); setQuery(''); setView('all'); setLibraryID(''); setOffset(0); setActor(actor); setTag(tag);
  };

  return <div className="page catalog-page">
    <header className="page-header"><div><h1>馆藏</h1><p>你的影片、资料与观看记录</p></div><Button onClick={() => setManager(true)}>媒体库管理</Button></header>
    <div className="catalog-toolbar">
      <nav className="catalog-views" aria-label="馆藏筛选">{views.map(([key, label]) => <button type="button" aria-pressed={view === key} className={view === key ? 'selected' : ''} key={key} onClick={() => { setView(key); setOffset(0); }}>{label}</button>)}</nav>
      <div className="catalog-search"><input type="search" aria-label="搜索馆藏" placeholder="搜索演员、番号、标题或关键词" maxLength={500} value={input} onChange={event => setInput(event.target.value)} /></div>
    </div>
    {(actor || tag || query) && <div className="catalog-filter-chips" aria-label="当前搜索条件">
      {actor && <button type="button" onClick={() => { setActor(''); setOffset(0); }}>演员：{actor} ×</button>}
      {tag && <button type="button" onClick={() => { setTag(''); setOffset(0); }}>标签：{tag} ×</button>}
      {query && <span>关键词：{query}</span>}
      <button type="button" className="catalog-text-button" onClick={() => { setActor(''); setTag(''); setInput(''); setQuery(''); setOffset(0); }}>清除搜索条件</button>
    </div>}
    <div className="catalog-results-heading"><span>{loading ? '正在读取馆藏…' : `${page.total} 部作品`}</span><label>媒体库<select aria-label="筛选媒体库" value={libraryID} onChange={event => { setLibraryID(event.target.value); setOffset(0); }}><option value="">全部媒体库</option>{libraries.map(library => <option key={library.id} value={library.id}>{library.name}</option>)}</select></label></div>
    {error && <div className="catalog-alert" role="alert">{error} <button className="catalog-text-button" type="button" onClick={refresh}>重试</button></div>}
    <section aria-label="馆藏作品" aria-busy={loading}>
      {!error && page.items.length > 0 && <CatalogCards items={page.items} onOpen={setSelected} />}
      {!loading && !error && page.items.length === 0 && <div className="catalog-empty"><span aria-hidden>▤</span><h2>{libraries.length ? '这里还没有影片' : '从已有影片开始'}</h2><p>{libraries.length ? '试试其他筛选条件，或在媒体库管理中扫描目录。' : '添加媒体目录，读取已有 NFO、封面和字幕。原文件不会被修改。'}</p><Button theme="solid" onClick={() => setManager(true)}>管理媒体库</Button></div>}
      {loading && page.items.length === 0 && <p className="catalog-loading" role="status">正在读取馆藏…</p>}
    </section>
    {page.total > 48 && <nav className="catalog-pagination" aria-label="馆藏分页"><Button disabled={loading || offset === 0} onClick={() => setOffset(value => Math.max(0, value - 48))}>上一页</Button><span>{Math.floor(offset / 48) + 1} / {Math.ceil(page.total / 48)}</span><Button disabled={loading || offset + 48 >= page.total} onClick={() => setOffset(value => value + 48)}>下一页</Button></nav>}
    {manager && <CatalogLibraryManager libraries={libraries} completedRoot={completedRoot} onChanged={libraryChanged} onClose={() => setManager(false)} />}
    {selected && <CatalogDetails key={selected} id={selected} onClose={() => setSelected('')} onChanged={refresh} onSearch={relatedSearch} />}
  </div>;
}
