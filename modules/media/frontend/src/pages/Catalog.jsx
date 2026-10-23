import { useCallback, useEffect, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import { api } from '../api';
import CatalogCards from '../components/CatalogCards';
import CatalogDetails from '../components/CatalogDetails';
import CatalogLibraryManager from '../components/CatalogLibraryManager';
import CatalogDeleteDialog from '../components/CatalogDeleteDialog';
import '../catalog.css';
import {catalogView,catalogLocation,catalogPageOffset} from '../catalog-view';

const views = [['all', '全部影片'], ['continue', '继续观看'], ['later','稍后观看'], ['favorites', '我的收藏'], ['watched','已看'], ['unwatched','未看']];

export default function Catalog({initial = {}, onNavigate}) {
  const [initialView]=useState(()=>catalogView(initial));
  const [libraries, setLibraries] = useState([]);
  const [lists, setLists] = useState([]);
  const [completedRoot, setCompletedRoot] = useState('');
  const [manager, setManager] = useState(false);
  const [selected, setSelected] = useState(initial.work || '');
  const [selecting, setSelecting] = useState(false);
  const [selectedIDs, setSelectedIDs] = useState([]);
  const [deleting, setDeleting] = useState(null);
  const [input, setInput] = useState(initialView.query);
  const [query, setQuery] = useState(initialView.query);
  const [actor, setActor] = useState(initialView.actor);
  const [tag, setTag] = useState(initialView.tag);
  const [listID, setListID] = useState(initialView.listID);
  const [view, setView] = useState(initialView.view);
  const [sort, setSort] = useState(initialView.sort);
  const [libraryID, setLibraryID] = useState(initialView.libraryID);
  const [offset, setOffset] = useState(initialView.offset);
  const [revision, setRevision] = useState(0);
  const [page, setPage] = useState({ items: [], total: 0 });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const refresh = useCallback(() => setRevision(value => value + 1), []);
  const loadLibraries = useCallback(async (signal) => {
    const [result, savedLists] = await Promise.all([api.catalogLibraries({ signal }), api.catalogLists({signal})]);
    if (!signal?.aborted) { setLibraries(result.items); setCompletedRoot(result.completed_root || ''); setLists(savedLists.items); }
  }, []);
  const libraryChanged = async () => { await loadLibraries(); refresh(); };
  useEffect(() => {
    const controller = new AbortController();
    loadLibraries(controller.signal).catch(failure => { if (failure.name !== 'AbortError') setError(failure.message); });
    return () => controller.abort();
  }, [loadLibraries]);
  useEffect(() => {
    if(input===query)return;
    const timer = setTimeout(() => { setQuery(input); setOffset(0); }, 300);
    return () => clearTimeout(timer);
  }, [input,query]);
  useEffect(()=>{
    window.history.replaceState(null,'',catalogLocation(window.location.href,{query,actor,tag,listID,view,sort,libraryID,offset},selected));
  },[query,actor,tag,listID,view,sort,libraryID,offset,selected]);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setError('');
    api.catalogWorks({ query, actor, tag, sort, view, libraryID, listID, offset, signal: controller.signal })
      .then(result => { if (!controller.signal.aborted) { const validOffset=catalogPageOffset(offset,result.total);if(validOffset!==offset)setOffset(validOffset);else setPage(result); } })
      .catch(failure => { if (!controller.signal.aborted) setError(failure.message); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [query, actor, tag, sort, view, libraryID, listID, offset, revision]);
  const relatedSearch = ({ actor = '', tag = '' }) => {
    setSelected(''); setInput(''); setQuery(''); setView('all'); setLibraryID(''); setListID(''); setOffset(0); setActor(actor); setTag(tag);
  };
  const toggle = id => setSelectedIDs(previous => previous.includes(id) ? previous.filter(value => value !== id) : previous.length < 100 ? [...previous,id] : previous);
  const toggleSelection = () => { setSelecting(value => !value); setSelectedIDs([]); };

  return <div className="page catalog-page">
    <header className="page-header"><div><h1>馆藏</h1><p>你的影片、资料与观看记录</p></div><div className="catalog-actions"><Button onClick={toggleSelection}>{selecting ? '退出多选' : '多选管理'}</Button><Button onClick={() => setManager(true)}>媒体库管理</Button></div></header>
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
    <div className="catalog-results-heading"><span aria-live="polite">{loading ? '正在读取馆藏…' : `${page.total} 部作品`}{view==='continue'?' · 最近观看优先':''}</span><div className="catalog-result-filters"><label>排序<select aria-label="馆藏排序" value={sort} disabled={view==='continue'} onChange={event=>{setSort(event.target.value);setOffset(0);}}><option value="recent">最近加入</option><option value="title">标题</option><option value="year">发行年份</option></select></label><label>媒体库<select aria-label="筛选媒体库" value={libraryID} onChange={event => { setLibraryID(event.target.value); setOffset(0); }}><option value="">全部媒体库</option>{libraries.map(library => <option key={library.id} value={library.id}>{library.name}</option>)}</select></label><label>播放列表<select aria-label="馆藏播放列表" value={listID} onChange={event=>{setListID(event.target.value);setOffset(0);}}><option value="">全部作品</option>{lists.map(list=><option key={list.id} value={list.id}>{list.name}</option>)}</select></label><button className="catalog-text-button" type="button" onClick={()=>onNavigate('personal','lists')}>管理列表</button></div></div>
    {listID&&<p className="catalog-help">“最近加入”排序下按列表自定义顺序展示。</p>}
    {selecting&&<div className="catalog-selection-bar" role="region" aria-label="馆藏批量操作"><strong>已选 {selectedIDs.length} 部</strong><Button disabled={loading||!!error} onClick={()=>setSelectedIDs(previous=>[...new Set([...previous,...page.items.map(item=>item.id)])].slice(0,100))}>选择本页</Button><Button disabled={!selectedIDs.length} onClick={()=>setSelectedIDs([])}>取消选择</Button><Button type="danger" disabled={!selectedIDs.length||loading||!!error} onClick={()=>setDeleting([...selectedIDs])}>永久删除</Button><small>最多 100 部；跨页保留选择，删除包含作品的所有版本。</small></div>}
    {error && <div className="catalog-alert" role="alert">{error} <button className="catalog-text-button" type="button" onClick={refresh}>重试</button></div>}
    <section aria-label="馆藏作品" aria-busy={loading}>
      {!error && page.items.length > 0 && <CatalogCards items={page.items} onOpen={setSelected} selecting={selecting} selectedIDs={selectedIDs} onToggle={toggle} />}
      {!loading && !error && page.items.length === 0 && <div className="catalog-empty"><span aria-hidden>▤</span><h2>{libraries.length ? '这里还没有影片' : '从已有影片开始'}</h2><p>{libraries.length ? '试试其他筛选条件，或在媒体库管理中扫描目录。' : '添加媒体目录，读取已有 NFO、封面和字幕。原文件不会被修改。'}</p><Button theme="solid" onClick={() => setManager(true)}>管理媒体库</Button></div>}
      {loading && page.items.length === 0 && <p className="catalog-loading" role="status">正在读取馆藏…</p>}
    </section>
    {page.total > 48 && <nav className="catalog-pagination" aria-label="馆藏分页"><Button disabled={loading || offset === 0} onClick={() => setOffset(value => Math.max(0, value - 48))}>上一页</Button><span>{Math.floor(offset / 48) + 1} / {Math.ceil(page.total / 48)}</span><Button disabled={loading || offset + 48 >= page.total} onClick={() => setOffset(value => value + 48)}>下一页</Button></nav>}
    {manager && <CatalogLibraryManager libraries={libraries} completedRoot={completedRoot} onChanged={libraryChanged} onClose={() => setManager(false)} />}
    {selected && <CatalogDetails key={selected} id={selected} onClose={() => setSelected('')} onChanged={refresh} onSearch={relatedSearch} onActor={id=>onNavigate('people','',null,{actor:id})} onManageLists={()=>onNavigate('personal','lists')} />}
    {deleting && <CatalogDeleteDialog ids={deleting} onClose={()=>setDeleting(null)} onDeleted={()=>{setSelectedIDs([]);setOffset(0);refresh();loadLibraries().catch(failure=>setError(failure.message));}} />}
  </div>;
}
