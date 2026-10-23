import {useCallback,useState} from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import {api} from '../api';
import {useRemoteResource} from '../useRemoteResource';
import ActorPortrait from '../components/ActorPortrait';
import CatalogCards from '../components/CatalogCards';
import CatalogDetails from '../components/CatalogDetails';
import '../catalog.css';
import '../personal.css';

function ActorProfile({id,onNavigate}){
 const [offset,setOffset]=useState(0),[workID,setWorkID]=useState('');
 const [editing,setEditing]=useState(false),[form,setForm]=useState({display_name:'',biography:'',aliases:''});
 const [mutating,setBusy]=useState(false),[notice,setNotice]=useState(''),[error,setError]=useState('');
 const load=useCallback(async signal=>{const [actor,works]=await Promise.all([api.catalogActor(id,{signal}),api.catalogWorks({actorID:id,offset,signal})]);return {actor,works};},[id,offset]);
 const {data,loading,error:loadError,refresh}=useRemoteResource(load);
 const busy=mutating||loading;
 const actor=data?.actor;
 const reload=()=>{setError('');setNotice('');refresh();};
 const mutate=async(action,message)=>{setBusy(true);setError('');setNotice('');try{await action();setNotice(message);setEditing(false);refresh();}catch(failure){setError(`${failure.message}；没有自动重试，请重新读取核对。`);}finally{setBusy(false);}};
 const upload=async event=>{
  const file=event.target.files?.[0];event.target.value='';if(!file||!actor)return;
  if(file.size>3*1024*1024){setError('头像不能超过 3 MiB');return;}
  await mutate(async()=>{
   const encoded=await new Promise((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(String(reader.result).split(',')[1]);reader.onerror=()=>reject(new Error('无法读取图片'));reader.readAsDataURL(file);});
   await api.catalogActorPortrait(id,{version:actor.version,image:encoded});
  },'人物头像已更新');
 };
 return <>
  <button className="catalog-text-button" type="button" onClick={()=>onNavigate('people')}>返回人物列表</button>
  {(error||loadError)&&<p className="catalog-alert" role="alert">{error||loadError} <button type="button" className="catalog-text-button" onClick={reload}>重新读取</button></p>}
  {notice&&<p className="catalog-notice" role="status">{notice}</p>}
  {!actor&&loading&&<p role="status">正在读取人物资料…</p>}
  {actor&&<>
   <header className="actor-profile-header">
    <ActorPortrait key={`${actor.id}:${actor.portrait_revision}`} actor={actor}/>
    <div><p className="catalog-help">演员 · {actor.work_count} 部馆藏作品</p><h1>{actor.display_name}</h1>{actor.display_name!==actor.name&&<p className="catalog-help">原始名称：{actor.name}</p>}
     {actor.aliases.length>0&&<p className="catalog-help">别名：{actor.aliases.join('、')}</p>}
     <p className="actor-biography">{actor.biography||'暂无人物简介，可以补充本地资料。'}</p>
     <div className="catalog-actions"><Button disabled={busy} onClick={()=>{setEditing(true);setForm({display_name:actor.display_name,biography:actor.biography,aliases:actor.aliases.join('\n')});}}>编辑人物资料</Button><Button onClick={()=>onNavigate('catalog','',null,{actor_name:actor.name})}>在馆藏中查看</Button><Button disabled={busy} onClick={()=>mutate(()=>api.catalogActorFollow(id,!actor.following,actor.follow_version),actor.following?'已取消本地关注':'已本地关注人物')}>{actor.following?'取消本地关注':'本地关注演员'}</Button></div>
     <p className="catalog-help">本地关注可参与偏好分析，不会联网订阅新作。<button className="catalog-text-button" type="button" onClick={()=>onNavigate('personal','taste')}>查看偏好与推荐</button></p>
     <div className="actor-image-actions"><label>上传人物头像<input type="file" aria-label="上传人物头像" accept="image/jpeg,image/png,image/webp" disabled={busy} onChange={upload}/></label>{actor.has_portrait&&<button type="button" className="catalog-text-button" disabled={busy} onClick={()=>{if(window.confirm('移除本应用保存的人物头像？不删除媒体目录中的文件。'))mutate(()=>api.catalogActorPortraitDelete(id,actor.version),'人物头像已移除');}}>移除头像</button>}<small>JPEG / PNG / WebP，最多 3 MiB；仅保存到本应用。</small></div>
    </div>
   </header>
   {editing&&<form className="personal-edit-form" onSubmit={event=>{event.preventDefault();mutate(()=>api.catalogActorPatch(id,{display_name:form.display_name,biography:form.biography,aliases:form.aliases.split('\n').map(value=>value.trim()).filter(Boolean),version:actor.version}),'人物资料已保存');}}>
    <label>显示名称<input aria-label="显示名称" required maxLength={160} value={form.display_name} onChange={event=>setForm(value=>({...value,display_name:event.target.value}))}/></label>
    <label>人物别名<textarea aria-label="人物别名" rows={3} maxLength={5000} placeholder="每行一个，不会自动合并其他演员" value={form.aliases} onChange={event=>setForm(value=>({...value,aliases:event.target.value}))}/></label>
    <label>人物简介<textarea aria-label="人物简介" rows={4} maxLength={5000} value={form.biography} onChange={event=>setForm(value=>({...value,biography:event.target.value}))}/></label>
    <div className="catalog-actions"><Button htmlType="submit" theme="solid" disabled={busy||!form.display_name.trim()}>保存人物资料</Button><Button disabled={busy} onClick={()=>setEditing(false)}>取消编辑</Button></div>
   </form>}
   <div className="catalog-results-heading"><h2>关联馆藏</h2><span>{data.works.total} 部作品</span></div>
   <CatalogCards items={data.works.items} onOpen={setWorkID}/>
   {data.works.total===0&&<p className="catalog-help">当前没有关联的馆藏，人物资料仍保留。</p>}
   {data.works.total>48&&<div className="catalog-pagination"><Button disabled={loading||offset===0} onClick={()=>setOffset(value=>Math.max(0,value-48))}>上一页</Button><span>{offset/48+1} / {Math.ceil(data.works.total/48)}</span><Button disabled={loading||offset+48>=data.works.total} onClick={()=>setOffset(value=>value+48)}>下一页</Button></div>}
  </>}
  {workID&&<CatalogDetails id={workID} onClose={()=>setWorkID('')} onChanged={refresh} onActor={actorID=>onNavigate('people','',null,{actor:actorID})} onSearch={filter=>onNavigate('catalog','',null,{actor_name:filter.actor||'',tag:filter.tag||''})}/>}
 </>;
}

export default function People({actorID,onNavigate}){
 const [query,setQuery]=useState(''),[offset,setOffset]=useState(0),[following,setFollowing]=useState(false);
 const load=useCallback(signal=>api.catalogActors({query,offset,following,signal}),[query,offset,following]);
 const {data,loading,error,refresh}=useRemoteResource(load);
 if(actorID)return <section className="page catalog-page people-page"><ActorProfile key={actorID} id={actorID} onNavigate={onNavigate}/></section>;
 return <section className="page catalog-page people-page">
  <header className="page-header"><div><h1>人物</h1><p>按演员浏览馆藏，保存本地人物资料与头像</p></div><input className="personal-search" type="search" aria-label="搜索人物" placeholder="搜索名字或别名" maxLength={500} value={query} onChange={event=>{setQuery(event.target.value);setOffset(0);}}/></header>
  {error&&<p className="catalog-alert" role="alert">{error} <button type="button" onClick={refresh}>重试</button></p>}
  <div className="catalog-actions people-filterbar"><button className="catalog-text-button" type="button" aria-pressed={!following} onClick={()=>{setFollowing(false);setOffset(0);}}>全部人物</button><button className="catalog-text-button" type="button" aria-pressed={following} onClick={()=>{setFollowing(true);setOffset(0);}}>本地已关注</button><button className="catalog-text-button" type="button" onClick={()=>onNavigate('personal','taste')}>偏好与推荐</button></div>
  <p className="catalog-help">{loading?'正在读取人物…':`${data?.total||0} 位演员`}</p>
  <div className="people-grid">{(data?.items||[]).map(actor=><button key={actor.id} type="button" className="person-card" aria-label={`查看演员：${actor.display_name}`} onClick={()=>onNavigate('people','',null,{actor:actor.id})}><ActorPortrait key={`${actor.id}:${actor.portrait_revision}`} actor={actor}/><strong>{actor.display_name}</strong><span>{actor.work_count} 部馆藏作品</span></button>)}</div>
  {!loading&&!error&&data?.total===0&&<p className="catalog-help">没有匹配的人物。扫描含演员资料的本地 NFO 后会建立人物索引，不会猜测缺失资料。</p>}
  {data?.total>48&&<div className="catalog-pagination"><Button disabled={loading||offset===0} onClick={()=>setOffset(value=>Math.max(0,value-48))}>上一页</Button><span>{offset/48+1} / {Math.ceil(data.total/48)}</span><Button disabled={loading||offset+48>=data.total} onClick={()=>setOffset(value=>value+48)}>下一页</Button></div>}
 </section>;
}
