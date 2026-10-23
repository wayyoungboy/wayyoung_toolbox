import { useEffect, useState } from 'react';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Button from '@douyinfe/semi-ui/lib/es/button';
import { api,formatDate } from '../api';
import { queueStatusLabel } from '../queue-view';

export default function QueueCleanupDialog({ onClose,onChanged }){
 const [days,setDays]=useState('30');
 const [page,setPage]=useState(null);
 const [selected,setSelected]=useState([]);
 const [loading,setLoading]=useState(true);
 const [busy,setBusy]=useState(false);
 const [error,setError]=useState('');
 const [notice,setNotice]=useState('');
 const [revision,setRevision]=useState(0);
 useEffect(()=>{
  const controller=new AbortController();setLoading(true);setError('');setPage(null);setSelected([]);
  api.queueCleanupCandidates(Number(days),{signal:controller.signal}).then(value=>{if(!controller.signal.aborted){setPage(value);setSelected(value.jobs.map(job=>job.id));}}).catch(failure=>{if(!controller.signal.aborted)setError(failure.message);}).finally(()=>{if(!controller.signal.aborted)setLoading(false);});
  return ()=>controller.abort();
 },[days,revision]);
 const clean=async()=>{
  if(!page||busy||!selected.length)return;
  setBusy(true);setError('');setNotice('');
  try{const result=await api.cleanupQueueJobs(page.jobs.filter(job=>selected.includes(job.id)).map(({id,version})=>({id,version})));setNotice(`已归档 ${result.archived} 个任务。影片和 qB 下载未变动。`);onChanged();setRevision(value=>value+1);}
  catch(failure){setError(`${failure.message}；请重新读取后核对，没有自动重试。`);setSelected([]);}
  finally{setBusy(false);}
 };
 return <Modal title="清理过期任务" visible width="min(820px, calc(100vw - 24px))" footer={null} closable={!busy} maskClosable={!busy} closeOnEsc={!busy} onCancel={()=>{if(!busy)onClose();}}>
  <p>仅归档已过期的异常任务，不删除影片、不停止 qB 下载。去重标识、关联关系及处理记录保留，可在处理队列的“已清理归档”中查看。运行中、排队中和仍占用文件的任务不会列入。</p>
  <label className="queue-cleanup-filter">距最后更新<select aria-label="任务过期时间" disabled={busy} value={days} onChange={event=>setDays(event.target.value)}><option value="30">超过 30 天</option><option value="7">超过 7 天</option><option value="1">超过 1 天</option><option value="0">不限时间，手动确认</option></select></label>
  {notice&&<p role="status" className="catalog-notice">{notice}</p>}
  {error&&<p role="alert" className="workspace-error">{error} <button type="button" onClick={()=>setRevision(value=>value+1)}>重新读取</button></p>}
  {loading&&<p role="status">正在核对可清理的任务…</p>}
  {page&&<><p>符合条件 {page.total} 项，本次最多选择 {page.jobs.length} 项。</p><div className="queue-cleanup-list">{page.jobs.map(job=><label key={job.id}><input type="checkbox" disabled={busy} checked={selected.includes(job.id)} onChange={()=>setSelected(previous=>previous.includes(job.id)?previous.filter(id=>id!==job.id):[...previous,job.id])}/><span><strong>#{job.id} {job.media_id||job.title}</strong><small>{queueStatusLabel(job.status)} · {formatDate(job.updated_at)}</small></span></label>)}</div></>}
  <div className="catalog-actions"><Button theme="solid" disabled={loading||busy||!selected.length} loading={busy} onClick={clean}>清理已选 {selected.length} 项</Button><Button disabled={busy} onClick={onClose}>关闭</Button></div>
 </Modal>;
}
