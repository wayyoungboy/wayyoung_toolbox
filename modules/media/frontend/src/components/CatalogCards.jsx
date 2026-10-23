import { useState } from 'react';
import { catalogAssetURL } from '../catalog-playback';

export function CatalogPoster({ assetID, title = '', className = '' }) {
  const [failed, setFailed] = useState(false);
  return <div className={`catalog-poster ${className}`}>
    {assetID && !failed ? <img src={catalogAssetURL(assetID, 'poster')} alt={title} loading="lazy" onError={() => setFailed(true)} /> : <span className="catalog-no-poster"><span aria-hidden>▤</span>暂无封面</span>}
  </div>;
}

export default function CatalogCards({ items, onOpen }) {
  return <div className="catalog-grid">
    {items.map(work => <button className="catalog-card" type="button" onClick={() => onOpen(work.id)} key={work.id}>
      <div className="catalog-cover-wrap">
        <CatalogPoster key={work.cover_asset_id} assetID={work.cover_asset_id} title="" />
        {work.favorite && <span className="catalog-favorite" aria-label="已收藏">♥</span>}
        {work.asset_count > 1 && <span className="catalog-editions">{work.asset_count} 个版本 / 分卷</span>}
        {work.available_count === 0 && <span className="catalog-offline">文件暂不可用</span>}
      </div>
      <span className="catalog-card-code">{work.code || '番号待确认'}</span>
      <span className="catalog-card-title">{work.title}</span>
      <span className="catalog-card-meta">{[work.year || '', work.actors?.slice(0, 2).join('、')].filter(Boolean).join(' · ') || (work.metadata_source === 'local_nfo' ? '本地 NFO' : '待补充资料')}</span>
    </button>)}
  </div>;
}
