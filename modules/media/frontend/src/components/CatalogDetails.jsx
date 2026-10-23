import { useEffect, useState } from 'react';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Button from '@douyinfe/semi-ui/lib/es/button';
import { api } from '../api';
import { formatPlaybackTime, preferredAsset } from '../catalog-playback';
import { CatalogPoster } from './CatalogCards';
import CatalogPlayer from './CatalogPlayer';
import CatalogMetadata from './CatalogMetadata';
import CatalogPersonalControls from './CatalogPersonalControls';

const issueLabels = {
  identity_unknown: '番号待确认，不会猜测缺失前缀', identity_conflict: '文件名与 NFO 番号冲突',
  metadata_conflict: '不同版本的标题不一致', nfo_missing: '未找到适用的本地 NFO', nfo_invalid: 'NFO 格式无效',
  nfo_unreadable: 'NFO 暂不可读', title_missing: 'NFO 缺少标题', manual_title_conflict: '身份合并前的手工标题存在冲突，原记录保留',
  identity_retained: '本地资料暂不可用，沿用此文件上次已确认的番号与资料',
  personal_notes_conflict: '身份合并时个人评分或标记冲突：保留目标值，源记录在数据库中保留，未自动覆盖',
  personal_identity_conflict: '原身份拆分为多部作品：原个人记录与列表归属保留，未猜测迁移目标',
};

export default function CatalogDetails({ id, onClose, onChanged, onSearch, onActor, onManageLists }) {
  const [work, setWork] = useState(null);
  const [selectedID, setSelectedID] = useState('');
  const [playing, setPlaying] = useState(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    api.catalogWork(id, { signal: controller.signal }).then(value => {
      setWork(value); setTitle(value.title); setSelectedID(preferredAsset(value.assets)?.id || '');
    }).catch(failure => { if (failure.name !== 'AbortError') setError(failure.message); });
    return () => controller.abort();
  }, [id]);
  const update = async action => {
    setBusy(true); setError('');
    try { const value = await action(); setWork(value); setTitle(value.title); onChanged(); return true; }
    catch (failure) { setError(failure.message); return false; }
    finally { setBusy(false); }
  };
  const selected = work?.assets.find(asset => asset.id === selectedID);
  const issues = [...new Set((work?.assets || []).flatMap(asset => asset.issues || []))];
  const playerClosed = () => {
    setPlaying(null);
    update(() => api.catalogWork(id));
  };

  return <>
    {!playing && <Modal title="作品详情" visible width="min(1000px, calc(100vw - 24px))" className="catalog-detail-modal" onCancel={onClose} footer={null}>
      {error && <p className="catalog-alert" role="alert">{error}</p>}
      {!work && !error && <p role="status">正在读取作品资料…</p>}
      {work && <>
        <div className="catalog-detail-layout">
          <CatalogPoster key={work.cover_asset_id} assetID={work.cover_asset_id} title={work.title} />
          <div className="catalog-detail-copy">
            <p className="catalog-detail-code">{work.code || '番号待确认'}{work.year ? ` · ${work.year}` : ''}</p>
            <h2>{work.title}</h2>
            {editing ? <form className="catalog-title-form" onSubmit={async event => { event.preventDefault(); if (await update(() => api.catalogTitle(id, title))) setEditing(false); }}>
              <label>馆藏标题<input value={title} onChange={event => setTitle(event.target.value)} maxLength={500} required /></label>
              <div className="catalog-actions"><Button htmlType="submit" theme="solid" disabled={busy || !title.trim()}>保存标题</Button><Button disabled={busy} onClick={() => { setTitle(work.title); setEditing(false); }}>取消</Button></div>
              <small>手工标题优先保留。仅修改本应用馆藏，不改写原视频和 NFO。</small>
            </form> : <button type="button" className="catalog-text-button" onClick={() => setEditing(true)}>修正标题{work.title_locked ? ' · 已人工锁定' : ''}</button>}
            <CatalogMetadata work={work} onSearch={onSearch} onActor={onActor} />
            <div className="catalog-actions">
              <Button aria-pressed={work.favorite} disabled={busy} onClick={() => update(() => api.catalogPersonal(id, { favorite: !work.favorite }))}>{work.favorite ? '♥ 已收藏' : '♡ 收藏'}</Button>
              <Button aria-pressed={work.watch_later} disabled={busy} onClick={() => update(() => api.catalogPersonal(id, { watch_later: !work.watch_later }))}>{work.watch_later ? '已加入稍后观看' : '稍后观看'}</Button>
            </div>
            <CatalogPersonalControls work={work} busy={busy} update={update} onChanged={onChanged} onManageLists={onManageLists}/>
          </div>
        </div>
        {work.plot && <section className="catalog-synopsis"><h3>简介</h3><p>{work.plot}</p></section>}
        <section className="catalog-asset-section"><h3>版本与文件 <small>{work.asset_count} 个</small></h3>
          <label className="catalog-asset-select">选择播放文件<select value={selectedID} onChange={event => setSelectedID(event.target.value)}>
            {!selectedID && <option value="">没有可播放文件</option>}
            {work.assets.map(asset => <option key={asset.id} value={asset.id} disabled={!asset.available}>{asset.path}{!asset.available ? ' · 暂不可用' : ''}</option>)}
          </select></label>
          {selected && <p className="catalog-help">{(selected.size / (1024 ** 3)).toFixed(2)} GiB · {selected.subtitles?.length || 0} 个本地字幕{selected.progress?.position > 0 ? ` · 上次看到 ${formatPlaybackTime(selected.progress.position)}` : ''}</p>}
          <Button theme="solid" disabled={!selected?.available || busy} onClick={() => setPlaying(selected)}>{selected?.progress?.position > 0 && !selected.progress.completed ? '继续播放' : '播放'}</Button>
          {issues.length > 0 && <details className="catalog-issues"><summary>资料检查 · {issues.length} 项提示</summary><ul>{issues.map(issue => <li key={issue}>{issueLabels[issue] || issue}</li>)}</ul></details>}
        </section>
      </>}
    </Modal>}
    {playing && <CatalogPlayer key={playing.id} asset={playing} title={work.title} onClose={playerClosed} />}
  </>;
}
