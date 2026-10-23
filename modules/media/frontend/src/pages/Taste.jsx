import {useCallback,useState} from 'react';
import {api} from '../api';
import {useRemoteResource} from '../useRemoteResource';
import TastePanel from '../components/TastePanel';
import '../taste.css';

export default function Taste({onNavigate}){
 const [offset,setOffset]=useState(0),[mutating,setBusy]=useState(false),[notice,setNotice]=useState(''),[mutationError,setMutationError]=useState('');
 const load=useCallback(signal=>api.catalogTaste({offset,signal}),[offset]);
 const {data,loading,error,refresh}=useRemoteResource(load);
 const busy=mutating||loading;
 const reload=()=>{setMutationError('');setNotice('');refresh();};
 const mutate=async(action,message)=>{
  if(busy)return;setBusy(true);setNotice('');setMutationError('');
  try{await action();setOffset(0);refresh();setNotice(message);}
  catch(failure){setMutationError(`${failure.message}；没有自动重试，请重新读取核对。`);refresh();}
  finally{setBusy(false);}
 };
 const clear=()=>{if(!data||busy||!window.confirm('清除偏好分析并关闭所有分析来源？不删除影片、收藏、评分、观看历史或本地关注。重新启用后会从保留的原资料重新计算。'))return;mutate(()=>api.catalogTasteReset(data.config.version,'清除偏好'),'已清除偏好分析并关闭来源，原始个人记录保留。');};
 return <div className="taste-workspace">
  <div className="section-heading"><div><h2>偏好与推荐</h2><p>用你允许的本地资料，找到馆藏中相关的未看作品</p></div><button className="catalog-text-button" type="button" disabled={busy} onClick={reload}>重新读取偏好</button></div>
  {(error||mutationError)&&<p role="alert" className="catalog-alert">{error||mutationError} <button type="button" className="catalog-text-button" onClick={reload}>重新读取</button></p>}
  {notice&&<p role="status" className="catalog-notice">{notice}</p>}
  {loading&&!data&&<p role="status">正在读取本地偏好…</p>}
  {data&&<TastePanel report={data} busy={busy} onSave={config=>mutate(()=>api.catalogTasteSave(config),'分析设置已保存。')} onClear={clear} onOpen={id=>onNavigate('catalog','',null,{work:id})} onActor={id=>onNavigate('people','',null,{actor:id})}/>}
  {(data?.total>24||offset>0)&&<nav className="catalog-pagination" aria-label="馆藏推荐分页"><button type="button" disabled={busy||offset===0} onClick={()=>setOffset(value=>Math.max(0,value-24))}>上一页</button><span>{offset/24+1} / {Math.max(1,Math.ceil((data?.total||0)/24))}</span><button type="button" disabled={busy||offset+24>=(data?.total||0)} onClick={()=>setOffset(value=>value+24)}>下一页</button></nav>}
 </div>;
}
