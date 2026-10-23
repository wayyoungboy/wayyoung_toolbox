export default function CatalogMetadata({ work, onSearch, onActor }) {
  return <>
    <dl className="catalog-metadata">
      <div><dt>演员</dt><dd className="catalog-actor-links">{work.actors?.length ? work.actors.map(actor => <button key={actor} type="button" className="catalog-text-button" aria-label={`查找演员：${actor}`} onClick={() => onSearch({ actor })}>{actor}</button>) : '暂无演员资料'}</dd></div>
      {work.studio && <div><dt>厂牌</dt><dd>{work.studio}</dd></div>}
      {work.release_date && <div><dt>发行</dt><dd>{work.release_date}</dd></div>}
      <div><dt>资料来源</dt><dd>{work.metadata_source === 'local_nfo' ? '本地 NFO' : '文件名，等待补充资料'}</dd></div>
    </dl>
    {onActor&&work.actor_profiles?.length>0&&<div className="catalog-actor-links" aria-label="人物资料">{work.actor_profiles.map(actor=><button key={actor.id} className="catalog-text-button" type="button" aria-label={`查看演员资料：${actor.display_name}`} onClick={()=>onActor(actor.id)}>{actor.display_name} · 人物资料</button>)}</div>}
    {work.genres?.length > 0 && <div className="catalog-tags">{work.genres.map(tag => <button type="button" key={tag} aria-label={`按标签筛选：${tag}`} onClick={() => onSearch({ tag })}>{tag}</button>)}</div>}
  </>;
}
