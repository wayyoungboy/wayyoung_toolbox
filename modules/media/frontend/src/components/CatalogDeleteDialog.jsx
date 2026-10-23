import { useEffect, useState } from 'react';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Button from '@douyinfe/semi-ui/lib/es/button';
import { api } from '../api';

export default function CatalogDeleteDialog({ ids, onClose, onDeleted }) {
 const [plan,setPlan]=useState(null);
 const [confirmation,setConfirmation]=useState('');
 const [busy,setBusy]=useState(false);
 const [error,setError]=useState('');
 const [result,setResult]=useState(null);
 useEffect(()=>{
  let active=true;
  api.catalogDeletePreview(ids).then(value=>{if(active)setPlan(value);}).catch(failure=>{if(active)setError(failure.message);});
  return ()=>{active=false;};
 },[ids]);
 const execute=async()=>{
  if(busy||!plan||confirmation!=='永久删除')return;
  setBusy(true);setError('');
  try{
   const value=await api.catalogDeleteExecute(ids,plan.token,confirmation);
   if(!Number.isInteger(value.deleted_files)||!Number.isInteger(value.deleted_works)||!Array.isArray(value.failed))throw new Error('删除结果不完整，当前状态未知');
   setResult(value);setPlan(null);onDeleted();
  }catch(failure){setError(`${failure.message}。没有自动重试；如有文件已删除，请重新扫描馆藏核对。`);setPlan(null);}
  finally{setBusy(false);}
 };
 return <Modal title="永久删除馆藏文件" visible width="min(780px, calc(100vw - 24px))" footer={null} maskClosable={!busy} closeOnEsc={!busy} closable={!busy} onCancel={()=>{if(!busy)onClose();}}>
  <p className="catalog-alert">不可恢复：删除所选作品在全部媒体库中的视频版本及对应配套文件，不经过回收站。不会删除整层目录或其他无关文件。</p>
  {error&&<p className="catalog-alert" role="alert">{error}</p>}
  {!plan&&!error&&!result&&<p role="status">正在核对文件与共享资料…</p>}
  {plan&&<>
   <p>已选 {plan.work_ids.length} 部作品，{plan.videos} 个视频，共 {plan.files.length} 个文件。</p>
   <div className="catalog-delete-files"><ul>{plan.files.map(file=><li key={`${file.library_id}/${file.path}`}><span>{file.kind==='video'?'视频':'配套资料'} · 媒体库 #{file.library_id}</span><code>{file.path}</code></li>)}</ul></div>
   {plan.retained.length>0&&<details><summary>保留 {plan.retained.length} 个共用或无法独占确认的资料文件</summary><ul>{plan.retained.map(file=><li key={file}><code>{file}</code></li>)}</ul></details>}
   {plan.missing.length>0&&<p className="catalog-help">{plan.missing.length} 个登记文件已经不存在。离线历史版本不进行文件删除；仅清理对应馆藏记录。</p>}
   <label className="catalog-delete-confirm">请输入“永久删除”确认<input aria-label="永久删除确认" value={confirmation} onChange={event=>setConfirmation(event.target.value)} autoComplete="off" /></label>
   <div className="catalog-actions"><Button type="danger" theme="solid" loading={busy} disabled={busy||confirmation!=='永久删除'} onClick={execute}>永久删除所选文件</Button><Button disabled={busy} onClick={onClose}>取消</Button></div>
  </>}
  {result&&<div role="status"><p>已删除 {result.deleted_files} 个文件，清理 {result.deleted_works} 部作品记录。</p>{result.failed?.length>0&&<div className="catalog-alert"><strong>部分操作未完成，已删除文件无法恢复。请重新扫描并核对。</strong><ul>{result.failed.map(item=><li key={item}>{item}</li>)}</ul></div>}</div>}
  {(!plan||result)&&<Button disabled={busy} onClick={onClose}>关闭</Button>}
 </Modal>;
}
