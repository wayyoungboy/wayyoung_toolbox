export default function NamingPreviewResult({ data }) {
  return <section className="naming-preview-result full-field" aria-label="命名预览结果">
    <strong>{data.plan.move_files ? '按当前规则整理' : '原地生成元数据，不移动视频'}</strong>
    {data.warnings?.map(warning => <p role="alert" className="folder-picker-warning" key={warning}>{warning}</p>)}
    <dl>
      {data.plan.files.map((file, i) => <div key={file.source}><dt>来源 {i + 1}</dt><dd><code>{file.source}</code></dd><dt>视频 {i + 1}</dt><dd><code>{file.destination}</code></dd></div>)}
      <div><dt>NFO 标题</dt><dd>{data.nfo_title}</dd></div>
      <div><dt>主 NFO</dt><dd><code>{data.plan.nfo_path}</code></dd></div>
      {data.same_name_nfo?.map(path => <div key={path}><dt>同名 NFO</dt><dd><code>{path}</code></dd></div>)}
      <div><dt>封面</dt><dd><code>{data.plan.poster_path}</code></dd></div>
      <div><dt>剧照</dt><dd><code>{data.plan.fanart_path}</code></dd></div>
    </dl>
    <small>这是示例路径，不代表文件已存在或任务已完成。新规则保存后用于尚未规划的影片；已有整理凭据不会被改写。</small>
  </section>;
}
