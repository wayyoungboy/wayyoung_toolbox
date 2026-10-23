import {useCallback,useState} from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import {api,formatDate} from '../api';
import {useRemoteResource} from '../useRemoteResource';
import {formatPlaybackTime} from '../catalog-playback';
import {CatalogPoster} from '../components/CatalogCards';
import WorkspaceTabs from '../components/WorkspaceTabs';
import {offsetAfterRemoval,moveListWork} from '../personal-view';
import '../catalog.css';
import '../personal.css';

function History({onNavigate,onChanged}){
 const [query,setQuery]=useState(''),[offset,setOffset]=useState(0),[selected,setSelected]=useState([]),[mutating,setBusy]=useState(false),[notice,setNotice]=useState('');
 const load=useCallback(signal=>api.catalogHistory({query,offset,signal}),[query,offset]);
 const {data,loading,error,refresh}=useRemoteResource(load);
 const busy=mutating||loading;
 const clear=async()=>{
  if(!selected.length||busy||!window.confirm(`删除选中的 ${selected.length} 条观看历史？不删除影片，也不重置续播位置；播放中的会话之后仍可能产生记录。`))return;
  setBusy(true);setNotice('');
  try{const result=await api.catalogHistoryClear(selected);if(!Number.isInteger(result.deleted))throw new Error('清理结果未知');setOffset(offsetAfterRemoval(offset,data.total,result.deleted));setNotice(`已移除 ${result.deleted} 条观看历史`);setSelected([]);refresh();onChanged();}
  catch(failure){setNotice(`${failure.message}；没有自动重试，请重新读取核对。`);}
  finally{setBusy(false);}
 };
 return <>
  <div className="personal-list-toolbar"><input className="personal-search" type="search" aria-label="搜索观看历史" placeholder="搜索标题或番号" maxLength={500} value={query} onChange={event=>{setQuery(event.target.value);setOffset(0);setSelected([]);}}/><Button disabled={loading||busy||!data?.items.length} onClick={()=>setSelected(data.items.map(item=>item.id))}>选择本页记录</Button><Button disabled={!selected.length||busy} onClick={clear}>删除已选历史（{selected.length}）</Button><Button disabled={busy} onClick={()=>{setSelected([]);refresh();}}>刷新历史</Button></div>
  {notice&&<p role="status" className="catalog-help">{notice}</p>}
  {error&&<p className="catalog-alert" role="alert">{error}</p>}
  {loading&&!data&&<p role="status">正在读取观看历史…</p>}
  <ol className="personal-history">{(data?.items||[]).map(item=><li key={item.id}><input type="checkbox" aria-label={`选择观看记录：${item.title}`} disabled={busy} checked={selected.includes(item.id)} onChange={()=>setSelected(value=>value.includes(item.id)?value.filter(id=>id!==item.id):[...value,item.id])}/><CatalogPoster assetID={item.cover_asset_id}/><div className="personal-history-copy"><button className="catalog-text-button" type="button" onClick={()=>onNavigate('catalog','',null,{work:item.work_id})}>{item.title}</button><small>{item.code||'番号待确认'} · {formatPlaybackTime(item.position)} / {formatPlaybackTime(item.duration)} · {item.completed?'该次已看完':'该次未完成'}{!item.available?' · 文件暂不可用':''}</small><time>{formatDate(item.updated_at)}</time></div><Button onClick={()=>onNavigate('catalog','',null,{work:item.work_id})}>查看作品</Button></li>)}</ol>
  {!loading&&!error&&data?.total===0&&<div className="catalog-empty"><h2>还没有观看记录</h2><p>在本应用中播放后，这里会按播放会话记录；同一次播放不会每隔几秒增加一条。</p><Button onClick={()=>onNavigate('catalog')}>浏览馆藏</Button></div>}
  {(data?.total>48||offset>0)&&<nav className="catalog-pagination" aria-label="观看历史分页"><Button disabled={loading||offset===0} onClick={()=>{setOffset(value=>Math.max(0,value-48));setSelected([]);}}>上一页</Button><span>{offset/48+1} / {Math.max(1,Math.ceil((data?.total||0)/48))}</span><Button disabled={loading||offset+48>=(data?.total||0)} onClick={()=>{setOffset(value=>value+48);setSelected([]);}}>下一页</Button></nav>}
 </>;
}

function ListEditor({id,onChanged,onNavigate}){
 const [offset,setOffset]=useState(0);
 const load=useCallback(async signal=>{const [list,works]=await Promise.all([api.catalogList(id,{signal}),api.catalogWorks({listID:id,offset,signal})]);return {list,works};},[id,offset]);
 const {data,loading,error,refresh}=useRemoteResource(load);
 const [mutating,setBusy]=useState(false),[notice,setNotice]=useState(''),[editing,setEditing]=useState(false),[form,setForm]=useState({name:'',description:''});
 const busy=mutating||loading;
 const act=async action=>{if(loading||busy)return;setBusy(true);setNotice('');try{const value=await action();if(Number.isInteger(value.work_count))setOffset(offsetAfterRemoval(offset,value.work_count,0));setEditing(false);refresh();onChanged();}catch(failure){setNotice(`${failure.message}；请刷新核对，没有自动重试。`);}finally{setBusy(false);}};
 const replace=ids=>act(()=>api.catalogListItems(id,data.list.version,ids));
 const move=(workID,delta)=>replace(moveListWork(data.list.work_ids,workID,delta));
 return <>
  {(error||notice)&&<p className="catalog-alert" role="alert">{error||notice} <button className="catalog-text-button" type="button" onClick={refresh}>刷新列表</button></p>}
  {!data&&loading&&<p role="status">正在读取播放列表…</p>}
  {data&&<>
   <div className="section-heading"><div><h2>{data.list.name}</h2><p>{data.list.description||'按你设定的顺序保留作品，不移动视频文件。'}</p></div><span>{data.list.work_count} 部作品</span></div>
   <div className="catalog-actions"><Button disabled={busy} onClick={()=>onNavigate('catalog','',null,{list:id})}>浏览此列表</Button><Button disabled={busy} onClick={()=>{setEditing(true);setForm({name:data.list.name,description:data.list.description});}}>编辑列表资料</Button><Button type="danger" disabled={busy} onClick={()=>{if(window.confirm(`删除播放列表“${data.list.name}”？仅移除列表和其中的关联，不删除影片。`))act(()=>api.catalogListDelete(id,data.list.version));}}>删除播放列表</Button></div>
   {editing&&<form className="personal-edit-form" onSubmit={event=>{event.preventDefault();act(()=>api.catalogListUpdate(id,{...form,version:data.list.version}));}}><label>列表名称<input required aria-label="列表名称" maxLength={160} value={form.name} onChange={event=>setForm(value=>({...value,name:event.target.value}))}/></label><label>列表说明<textarea aria-label="列表说明" maxLength={2500} value={form.description} onChange={event=>setForm(value=>({...value,description:event.target.value}))}/></label><Button htmlType="submit" disabled={busy}>保存列表资料</Button></form>}
   <ol className="personal-list-items">{data.works.items.map((work,index)=><li key={work.id}><span><small>{offset+index+1}</small><button className="catalog-text-button" type="button" onClick={()=>onNavigate('catalog','',null,{work:work.id})}>{work.title}</button></span><div className="catalog-actions"><Button aria-label={`上移：${work.title}`} disabled={busy||loading||offset+index===0} onClick={()=>move(work.id,-1)}>上移</Button><Button aria-label={`下移：${work.title}`} disabled={busy||loading||offset+index+1===data.list.work_ids.length} onClick={()=>move(work.id,1)}>下移</Button><Button disabled={busy||loading} onClick={()=>replace(data.list.work_ids.filter(value=>value!==work.id))}>从列表移除</Button></div></li>)}</ol>
   {data.list.work_count===0&&<p className="catalog-help">在馆藏的作品详情中选择这个列表，添加你想保留的影片。</p>}
   {(data.list.work_count>48||offset>0)&&<nav className="catalog-pagination" aria-label="播放列表分页"><Button disabled={busy||loading||offset===0} onClick={()=>setOffset(value=>Math.max(0,value-48))}>上一页</Button><span>{offset/48+1} / {Math.max(1,Math.ceil(data.list.work_count/48))}</span><Button disabled={busy||loading||offset+48>=data.list.work_count} onClick={()=>setOffset(value=>value+48)}>下一页</Button></nav>}
  </>}
 </>;
}

function Playlists({lists,onChanged,onNavigate}){
 const [selected,setSelected]=useState(''),[creating,setCreating]=useState(false),[name,setName]=useState(''),[description,setDescription]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState('');
 const id=lists.some(list=>list.id===selected)?selected:lists[0]?.id;
 const create=async event=>{event.preventDefault();setBusy(true);setError('');try{const list=await api.catalogListCreate({name,description});setSelected(list.id);setCreating(false);setName('');setDescription('');onChanged();}catch(failure){setError(failure.message);}finally{setBusy(false);}};
 return <>
  <div className="personal-list-toolbar"><label>播放列表 <select aria-label="选择播放列表" value={id||''} onChange={event=>setSelected(event.target.value)}>{!lists.length&&<option value="">尚无列表</option>}{lists.map(list=><option key={list.id} value={list.id}>{list.name}（{list.work_count}）</option>)}</select></label><Button theme="solid" onClick={()=>setCreating(value=>!value)}>新建播放列表</Button></div>
  {error&&<p className="catalog-alert" role="alert">{error}</p>}
  {creating&&<form className="personal-edit-form" onSubmit={create}><label>新列表名称<input aria-label="新列表名称" required maxLength={160} value={name} onChange={event=>setName(event.target.value)}/></label><label>新列表说明<textarea aria-label="新列表说明" maxLength={2500} value={description} onChange={event=>setDescription(event.target.value)}/></label><Button htmlType="submit" disabled={busy} theme="solid">创建播放列表</Button></form>}
  {id?<ListEditor key={id} id={id} onChanged={onChanged} onNavigate={onNavigate}/>:!creating&&<div className="catalog-empty"><h2>建立自己的播放列表</h2><p>收藏之外，还可以按主题或观看顺序组织作品。列表不会移动或复制原视频。</p></div>}
 </>;
}

export default function Personal({tab,onTabChange,onNavigate}){
 const load=useCallback(async signal=>{const [stats,lists]=await Promise.all([api.catalogStats({signal}),api.catalogLists({signal})]);return {stats,lists:lists.items};},[]);
 const {data,error,refresh}=useRemoteResource(load);
 return <section className="page catalog-page personal-page"><header className="page-header"><div><h1>个人记录</h1><p>观看历史、已看状态与个人播放列表，与你的原视频分开保存</p></div><Button onClick={()=>onNavigate('catalog','',null,{view:'watched'})}>查看已看作品</Button></header>
  {error&&<p className="catalog-alert" role="alert">{error} <button type="button" onClick={refresh}>重试</button></p>}
  {data&&<dl className="personal-counts">{[['已看作品',data.stats.watched_works],['收藏作品',data.stats.favorites],['观看会话',data.stats.history_sessions],['已评分',data.stats.rated_works]].map(([label,value])=><div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>}
  <WorkspaceTabs label="个人记录工作区" items={[{key:'history',label:'观看历史'},{key:'lists',label:'播放列表'}]} selected={tab} onChange={onTabChange}/>
  {tab==='lists'?<Playlists lists={data?.lists||[]} onChanged={refresh} onNavigate={onNavigate}/>:<History onNavigate={onNavigate} onChanged={refresh}/>}
 </section>;
}
