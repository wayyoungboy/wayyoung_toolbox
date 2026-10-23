import {useCallback,useEffect,useRef,useState} from 'react';
import {api,formatDate} from '../api';
import {useRemoteResource} from '../useRemoteResource';
import '../browser-sources.css';

export function BrowserSourceList({items,busy,onReading,onDelete}){
 return <section className="browser-source-list" aria-label="已导入来源"><h2>已导入来源</h2><p className="catalog-help">停止读取仍保留已有资料和推荐贡献；删除来源才移除这些资料，不删除影片，也不删除电脑上的原始导出文件。</p>
 {items.length?<ul>{items.map(source=><li key={source.id}><div><h3>{source.name}</h3><p>{source.kind==='history'?'访问历史':'书签'} · {source.records} 条资料 · {source.matched} 条关联馆藏</p><small>{source.reading?'可接受更新':'已停止读取'} · {formatDate(source.updated_at)}</small></div><div className="catalog-actions"><button type="button" disabled={busy} onClick={()=>onReading(source,!source.reading)}>{source.reading?'停止读取':'恢复读取'}</button><button type="button" className="browser-delete" disabled={busy} onClick={()=>onDelete(source)}>删除来源</button></div></li>)}</ul>:<p className="catalog-help">还没有导入来源。先从你自己的浏览器导出，再在上方选择文件；NAS 不会自动读取电脑。</p>}
 </section>;
}

export function BrowserImportForm({items,form,busy,preview,onChange,onFile,onPreview,onCommit}){
 const fileInput=useRef(null);
 useEffect(()=>{if(!form.filename&&fileInput.current)fileInput.current.value='';},[form.filename]);
 return <section className="browser-import"><h2>导入浏览器资料</h2><p className="catalog-help">只处理你选择上传的导出文件，不会直接读取电脑浏览器，也不访问文件中的网址。URL 会去掉用户凭据、片段及非必要查询参数；保留网址、标题和时间，仅供本应用管理员查看。</p>
 <form onSubmit={event=>{event.preventDefault();onPreview();}}><fieldset disabled={busy}>
 <label>导入到<select aria-label="导入目标来源" value={form.source_id} onChange={event=>onChange({source_id:event.target.value})}><option value="">创建新来源</option>{items.map(source=><option key={source.id} value={source.id}>{source.name}{!source.reading?'（已停止）':''}</option>)}</select></label>
 <label>来源名称<input aria-label="浏览器来源名称" required maxLength={160} value={form.name} onChange={event=>onChange({name:event.target.value})}/></label>
 <label>资料类型<select aria-label="浏览器资料类型" value={form.kind} disabled={Boolean(form.source_id)} onChange={event=>onChange({kind:event.target.value})}><option value="history">访问历史（增量合并）</option><option value="bookmarks">书签（替换此来源快照）</option></select></label>
 <label className="browser-file">浏览器资料文件<input ref={fileInput} aria-label="浏览器资料文件" type="file" onChange={event=>onFile(event.target.files?.[0])}/><span>{form.filename||'请选择已导出的文件'}</span></label>
 <p className="catalog-help browser-form-wide">支持 Chromium / Firefox / Safari 历史数据库、Firefox 书签数据库、Bookmarks JSON、Netscape HTML、browserexport JSON/JSONL、Google Takeout；可使用 ZIP、tar.gz、gzip、XZ、LZ4 或 Zstd。上传最多 32 MiB、展开最多 64 MiB、单次最多 50,000 条；超限需拆分。数据库请在关闭浏览器后复制完整文件，避免遗漏 WAL 中未导出的记录。</p>
 <button className="taste-primary" type="submit" disabled={busy||!form.filename||!form.name.trim()}>预览导入</button>
 </fieldset></form>
 {preview&&<section className="browser-preview" aria-label="浏览器导入预览"><h3>导入预览</h3><p>{preview.records} 条资料，{preview.matched} 条关联馆藏；跳过 {preview.skipped} 条无效网址，合并 {preview.duplicates} 条重复记录。</p><p className="catalog-help">只有已知媒体站点上完整、无歧义的番号才关联馆藏。未关联资料保留，但不猜演员或影片身份，也不代表已观看。</p><ul>{preview.samples.map((entry,index)=><li key={index}><strong>{entry.title||'无标题'}</strong><span>{entry.url}</span><small>{formatDate(entry.visited_at)}</small></li>)}</ul><p className="catalog-help">这里只展示前 5 条。历史追加新事件；书签成功导入后替换该来源全部旧书签。解析失败不会改动旧资料。</p><button className="taste-primary" type="button" disabled={busy} onClick={onCommit}>确认导入</button></section>}
 </section>;
}

export default function BrowserSources({onNavigate}){
 const load=useCallback(signal=>api.catalogBrowserSources({signal}),[]);
 const {data,loading,error,refresh}=useRemoteResource(load);
 const [form,setForm]=useState({name:'',kind:'history',source_id:'',version:0}),[preview,setPreview]=useState(null),[prepared,setPrepared]=useState(null),[mutating,setBusy]=useState(false),[notice,setNotice]=useState(''),[mutationError,setError]=useState('');
 const reader=useRef(null);
 useEffect(()=>()=>reader.current?.abort(),[]);
 const busy=mutating||loading;
 const items=data?.items||[];
 const selectedSource=items.find(source=>source.id===form.source_id);
 const staleSource=Boolean(form.source_id&&data&&!loading&&(!selectedSource||selectedSource.version!==form.version));
 const reload=()=>{if(mutating)return;setPreview(null);setPrepared(null);setForm(value=>({...value,source_id:'',name:'',version:0}));setError('');setNotice('');refresh();};
 const change=values=>{
  if(busy)return;setPreview(null);setPrepared(null);setNotice('');setError('');
  if(Object.hasOwn(values,'source_id')){const source=items.find(item=>item.id===values.source_id);setForm(current=>({...current,...values,name:source?.name||'',kind:source?.kind||current.kind,version:source?.version||0}));}
  else setForm(current=>({...current,...values}));
 };
 const chooseFile=async file=>{
  if(busy||!file)return;setPreview(null);setPrepared(null);setError('');setNotice('');
  if(file.size===0||file.size>32*1024*1024){setForm(value=>({...value,filename:undefined,data:undefined}));setError('文件为空或超过 32 MiB，请拆分导出。');return;}
  setBusy(true);
  try{const contents=await new Promise((resolve,reject)=>{const r=new FileReader();reader.current=r;r.onload=()=>resolve(String(r.result).split(',')[1]);r.onerror=()=>reject(new Error('读取所选文件失败'));r.onabort=()=>reject(new Error('已取消读取文件'));r.readAsDataURL(file);});setForm(value=>({...value,filename:file.name,data:contents}));}
  catch(failure){setError(failure.message);setForm(value=>({...value,filename:undefined,data:undefined}));}
  finally{reader.current=null;setBusy(false);}
 };
 const fail=failure=>{setError(`${failure.message}；没有自动重试。请重新读取来源后核对。`);setPreview(null);setPrepared(null);refresh();};
 const previewImport=async()=>{
  if(busy||staleSource||!form.filename||!form.data)return;setBusy(true);setError('');setNotice('');setPreview(null);setPrepared(null);
  try{const source=items.find(item=>item.id===form.source_id);const payload={...form,name:form.name.trim()};if(form.source_id&&!source)throw new Error('来源已不存在，请重新读取并选择');if(source&&!source.reading)throw new Error('此来源已停止读取，请先恢复读取');const result=await api.catalogBrowserPreview(payload);setPreview(result);setPrepared({...payload,digest:result.digest,confirmation:'导入资料'});}
  catch(failure){fail(failure);}finally{setBusy(false);}
 };
 const commit=async()=>{
  if(busy||staleSource||!prepared)return;setBusy(true);setError('');
  try{const result=await api.catalogBrowserImport(prepared);setNotice(result.duplicate?'这份资料已经导入，没有增加重复来源。':`已导入 ${result.added} 条新资料。偏好分析设置未改变。`);setForm({name:'',kind:form.kind,source_id:'',version:0});setPreview(null);setPrepared(null);refresh();}
  catch(failure){fail(failure);}finally{setBusy(false);}
 };
 const sourceAction=async(source,reading)=>{
  if(busy)return;
  if(reading===undefined&&!window.confirm(`删除来源“${source.name}”和全部已导入资料？会移除它对推荐的贡献，不删除影片。此操作不可撤回。`))return;
  setBusy(true);setError('');setNotice('');setPreview(null);setPrepared(null);
  try{if(reading===undefined){await api.catalogBrowserDelete(source.id,source.version,'删除来源');setNotice('来源及其已导入资料已删除。');}else{await api.catalogBrowserReading(source.id,source.version,reading);setNotice(reading?'已恢复接受更新。':'已停止读取，已有资料和贡献仍保留。');}refresh();}
  catch(failure){fail(failure);}finally{setBusy(false);}
 };
 return <div className="browser-workspace"><div className="section-heading"><p>导入不会自动启用分析；在偏好页明确选择“已导入浏览器资料”后才参与推荐。</p><div className="catalog-actions"><button type="button" disabled={busy} onClick={reload}>重新读取来源</button><button type="button" onClick={()=>onNavigate('personal','taste')}>查看偏好与推荐</button></div></div>
 {(error||mutationError)&&<p role="alert" className="catalog-alert">{error||mutationError}</p>}{notice&&<p role="status" className="catalog-notice">{notice}</p>}{loading&&!data&&<p role="status">正在读取来源…</p>}
 {staleSource&&<p role="status" className="catalog-alert">所选来源已更新或移除。请点击“重新读取来源”后重新选择，旧表单不会套用新版本。</p>}
 <BrowserImportForm items={items} form={form} busy={busy||staleSource} preview={preview} onChange={change} onFile={chooseFile} onPreview={previewImport} onCommit={commit}/>
 <BrowserSourceList items={items} busy={busy} onReading={sourceAction} onDelete={source=>sourceAction(source)}/>
 </div>;
}
