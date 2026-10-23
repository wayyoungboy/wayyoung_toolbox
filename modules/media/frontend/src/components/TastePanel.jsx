import {useState} from 'react';
import {CatalogPoster} from './CatalogCards';

const sourceLabels={collection:'馆藏与收藏',history:'播放记录',feedback:'评分与反馈',follows:'本地演员关注'};
const kindLabels={actor:'演员',tag:'标签',studio:'厂牌',era:'年代'};

function TasteSettings({report,busy,onSave,onClear}){
 const [draft,setDraft]=useState(()=>({...report.config,sources:{...report.config.sources}}));
 return <form className="taste-settings" onSubmit={event=>{event.preventDefault();if(!busy)onSave(draft);}}>
  <h3>分析来源</h3>
  <label className="taste-toggle"><input type="checkbox" aria-label="启用本地偏好分析" checked={draft.enabled} disabled={busy} onChange={event=>setDraft(value=>({...value,enabled:event.target.checked}))}/>启用本地偏好分析</label>
  <p className="catalog-help">只在本应用内计算，不会联网，不上传观看资料，也不会读取电脑浏览器。保存后重新计算。</p>
  <fieldset disabled={busy||!draft.enabled}><legend>参与分析的数据</legend>{Object.entries(sourceLabels).map(([key,label])=><label className="taste-source" key={key}><input type="checkbox" aria-label={`参与分析：${label}`} checked={Boolean(draft.sources[key])} onChange={event=>setDraft(value=>({...value,sources:{...value.sources,[key]:event.target.checked}}))}/><span>{label}<small>{report.sources.find(item=>item.key===key)?.records||0} 条来源记录</small></span></label>)}</fieldset>
  <button className="taste-primary" type="submit" disabled={busy}>保存分析设置</button>
  <button className="catalog-text-button" type="button" disabled={busy} onClick={onClear}>清除偏好分析</button>
  <p className="catalog-help">清除会关闭分析及全部来源，不删除原收藏、评分、观看历史或关注。重新选择来源并启用后，会用仍保留的资料重新计算。</p>
 </form>;
}

export default function TastePanel({report,busy=false,onSave,onClear,onOpen,onActor}){
 return <div className="taste-layout">
  <aside><TasteSettings key={report.config.version} report={report} busy={busy} onSave={onSave} onClear={onClear}/></aside>
  <div className="taste-main">
   {!report.config.enabled?<div className="catalog-empty"><h2>尚未启用偏好分析</h2><p>启用后，根据你允许参与的本地资料，寻找馆藏中相关的未看作品。没有证据时不会伪装成个性化推荐。</p></div>:<>
    <section aria-label="偏好依据"><h2>你的本地偏好</h2><p className="catalog-help">演员、标签、厂牌和年代来自已有资料。播放记录只表示有过播放，不代表累计观看时长。每部作品每个来源只计一次。</p>
     {report.facets.length?<ul className="taste-facets">{report.facets.slice(0,12).map(facet=><li key={`${facet.kind}:${facet.key}`}><span>{kindLabels[facet.kind]||facet.kind}</span>{facet.kind==='actor'&&onActor?<button className="catalog-text-button" type="button" onClick={()=>onActor(facet.key)}>{facet.label}</button>:<strong>{facet.label}</strong>}<small>{facet.evidence_count} 条依据 · {facet.sources.map(key=>sourceLabels[key]||key).join('、')}</small></li>)}</ul>:<p className="catalog-help">还没有足够的资料。可以收藏、评分，或在人物页选择本地关注；也可检查分析来源是否已启用。</p>}
    </section>
    <section aria-label="馆藏推荐"><div className="catalog-results-heading"><h2>馆藏里，接着看什么</h2><span>{report.total} 部相关作品</span></div><p className="catalog-help">只推荐资料中可用的未看作品，不会自动下载。排序分用于比较匹配程度，不是喜欢概率。关闭“评分与反馈”后，喜欢和不喜欢均不参与分析。</p>
     <ol className="taste-recommendations">{report.items.map(item=><li key={item.work.id}><button className="catalog-card" type="button" aria-label={`查看推荐：${item.work.title}`} onClick={()=>onOpen(item.work.id)}><CatalogPoster assetID={item.work.cover_asset_id}/><span className="catalog-card-code">{item.work.code||'番号待确认'}</span><span className="catalog-card-title">{item.work.title}</span></button><p className="taste-score">排序分 {Number(item.score).toFixed(2)}</p><ul className="taste-reasons">{item.reasons.slice(0,2).map((reason,index)=><li key={index}><span>{kindLabels[reason.kind]||reason.kind}：{reason.label}</span><small>来自{sourceLabels[reason.source]||reason.source}</small></li>)}</ul><details><summary>查看推荐依据</summary><ul className="taste-reasons">{item.reasons.map((reason,index)=><li key={index}><span>{kindLabels[reason.kind]||reason.kind}：{reason.label}（{sourceLabels[reason.source]||reason.source}）</span>{reason.work_ids.map(id=><button className="catalog-text-button" key={id} type="button" onClick={()=>onOpen(id)}>查看关联作品</button>)}{reason.source==='follows'&&<small>来自你在本应用明确设置的本地关注，不是联网订阅。</small>}</li>)}</ul></details></li>)}</ol>
     {report.total===0&&<p className="catalog-help">当前没有有依据的未看推荐。不会用随机影片填充结果，也不会虚构缺少的演员或标签。</p>}
    </section>
   </>}
  </div>
 </div>;
}
