import {useCallback, useState} from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import {api} from '../api';
import {useRemoteResource} from '../useRemoteResource';
import '../personal.css';

export default function CatalogPersonalControls({work, busy, update, onChanged, onManageLists}) {
  const [listID,setListID] = useState('');
  const [adding,setAdding] = useState(false);
  const [notice,setNotice] = useState('');
  const load = useCallback(signal=>api.catalogLists({signal}),[]);
  const {data,error,refresh} = useRemoteResource(load);
  const saveNotes = patch => update(()=>api.catalogPersonal(work.id,{...patch,notes_revision:work.notes_revision}));
  const add = async () => {
    if (!listID || adding) return;
    setAdding(true); setNotice('');
    try {
      const list = await api.catalogList(listID);
      if (!list.work_ids.includes(work.id)) await api.catalogListItems(list.id,list.version,[...list.work_ids,work.id]);
      setNotice('已加入播放列表'); onChanged(); refresh();
    } catch (failure) { setNotice(`${failure.message}；没有自动重试，请刷新列表核对。`); }
    finally { setAdding(false); }
  };
  return <div className="personal-controls">
    <Button aria-pressed={work.watched} disabled={busy} onClick={()=>saveNotes({watched:!work.watched})}>{work.watched?'已看过':'标记已看'}</Button>
    <label>个人评分<select aria-label="个人评分" value={work.rating} disabled={busy} onChange={event=>saveNotes({rating:Number(event.target.value)})}><option value="0">未评分</option>{Array.from({length:10},(_,index)=><option key={index+1} value={index+1}>{index+1} 分</option>)}</select></label>
    <Button aria-pressed={work.feedback==='like'} disabled={busy} onClick={()=>saveNotes({feedback:work.feedback==='like'?'':'like'})}>喜欢</Button>
    <Button aria-pressed={work.feedback==='dislike'} disabled={busy} onClick={()=>saveNotes({feedback:work.feedback==='dislike'?'':'dislike'})}>不喜欢</Button>
    <label>加入播放列表<select aria-label="加入播放列表" value={listID} disabled={adding} onChange={event=>{setListID(event.target.value);setNotice('');}}><option value="">选择列表</option>{(data?.items||[]).map(list=><option key={list.id} value={list.id}>{list.name}</option>)}</select></label>
    <Button disabled={adding||!listID} onClick={add}>添加到播放列表</Button>
    {onManageLists&&<button type="button" className="catalog-text-button" onClick={onManageLists}>管理播放列表</button>}
    {error&&<p role="alert">{error} <button type="button" onClick={refresh}>重新读取列表</button></p>}
    {notice&&<p role="status">{notice}</p>}
  </div>;
}
