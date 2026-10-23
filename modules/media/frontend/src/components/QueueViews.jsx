import { formatDate } from '../api';
import { downloadBytes, downloadPercent, queueActions, queueEventLabel, queuePhaseLabel, queueStatusLabel, queueStatusTone, queueSummary, safeSourceURL } from '../queue-view';

export function QueueStatus({ status }) {
  return <span className="state-text"><span className={`status-dot ${queueStatusTone(status)}`} />{queueStatusLabel(status)}</span>;
}

export function QueueJobsTable({ jobs, onSelect, selectedID }) {
  return (
    <div className="queue-table-scroll">
      <table className="queue-table">
        <thead><tr><th scope="col">影片 / 任务</th><th scope="col">当前阶段</th><th scope="col">状态与下一步</th><th scope="col">更新</th></tr></thead>
        <tbody>{jobs.map((job) => (
          <tr key={job.id} className={selectedID === job.id ? 'selected' : ''}>
            <td><button className="queue-link" type="button" aria-label={`查看任务 #${job.id} ${job.media_id || job.title}`} onClick={() => onSelect(job.id)}>{job.media_id || job.title || `任务 #${job.id}`}</button><div className="cell-secondary">#{job.id} · {job.kind === 'movie' ? '影片' : '订阅 / 下载'}{job.media_id && job.title ? ` · ${job.title}` : ''}</div></td>
            <td>{queuePhaseLabel(job)}</td>
            <td><QueueStatus status={job.status} /><p className="queue-summary">{queueSummary(job)}</p></td>
            <td className="queue-date">{formatDate(job.updated_at)}</td>
          </tr>
        ))}</tbody>
      </table>
    </div>
  );
}

function JobLinks({ ids, onSelect }) {
  return <span className="queue-job-links">{[...new Set(ids)].map((id) => <button className="queue-link" type="button" onClick={() => onSelect(id)} key={id}>任务 #{id}</button>)}</span>;
}

function PathList({ title, paths }) {
  if (!paths?.length) return null;
  return <section className="queue-paths"><h3>{title}</h3><ul>{paths.map((path, index) => <li key={`${path}-${index}`}><code>{path}</code></li>)}</ul></section>;
}

function QueueDownloadStatus({ job }) {
  const reading = job.download;
  if (job.phase !== 'download' || !reading) return null;
  const percent = downloadPercent(reading);
  return <section className="queue-download" aria-label="qB 下载状态">
    <h3>qB 下载状态</h3>
    {reading.error ? <div role="alert" className="error-box">{reading.error}</div> : null}
    {percent !== null ? <><strong>{percent}%</strong><progress aria-label="qB 下载进度" value={percent} max={100} /></> : <p className="muted-text">尚无有效下载读数</p>}
    {reading.error && reading.observed_at ? <p className="queue-footnote">上次读数，非实时状态</p> : null}
    <dl className="queue-facts">
      <div><dt>qB 报告状态</dt><dd>{reading.observed_at ? reading.state || '未知' : '--'}</dd></div>
      <div><dt>剩余下载量</dt><dd>{reading.observed_at ? downloadBytes(reading.amount_left) : '--'}</dd></div>
      <div><dt>最近检查</dt><dd>{formatDate(reading.checked_at)}</dd></div>
      <div><dt>上次成功读取</dt><dd>{formatDate(reading.observed_at)}</dd></div>
    </dl>
    <PathList title="qB 上次报告的内容路径" paths={reading.observed_at && reading.content_path ? [reading.content_path] : []} />
    <p className="queue-footnote">状态由后台定期只读检查；读数不代表已搬运或整理。实际入库前会再次核对下载完成、任务身份和文件。</p>
  </section>;
}

export function QueueJobDetails({ job, events, onSelect, onAction, busy = false }) {
  const sourceURL = safeSourceURL(job.source_url);
  const actions = queueActions(job);
  const shownEvents = events.slice(-100).reverse();
  return (
    <div className="queue-job-details">
      <h3>{job.media_id || job.title || '未识别影片'}</h3>
      {job.media_id && job.title ? <p className="queue-summary">{job.title}</p> : null}
      <div className="queue-detail-state"><QueueStatus status={job.status} /><span>{queuePhaseLabel(job)}</span></div>
      {job.archived&&<p className="queue-footnote">此任务已清理归档，保留记录以防止重复采集，不会继续执行。</p>}
      <p className="queue-summary">{queueSummary(job)}</p>
      {job.error ? <div role="note" className="error-box">{job.error}</div> : null}
      <QueueDownloadStatus job={job} />
      {job.waiting_for_intake?.length ? <div className="queue-dependency"><strong>等待搬运登记</strong><JobLinks ids={job.waiting_for_intake} onSelect={onSelect} /></div> : null}
      <div className="queue-detail-actions">{actions.map((action) => <button type="button" className={`queue-button ${action.key === 'cancel' ? 'danger' : 'primary'}`} disabled={busy} key={action.key} onClick={() => onAction(action.key, job)}>{action.label}</button>)}</div>
      {actions.length ? <p className="queue-footnote">重试只接续未完成阶段。取消不会停止 qB 下载，不会撤销已完成的文件操作，也不会删除视频。</p> : null}
      <dl className="queue-facts">
        <div><dt>任务类型</dt><dd>{job.kind === 'movie' ? '影片整理' : '订阅 / 下载 / 搬运'}</dd></div>
        <div><dt>执行次数 / 修订</dt><dd>{job.attempts || 0} / {job.version}</dd></div>
        <div><dt>创建时间</dt><dd>{formatDate(job.created_at)}</dd></div>
        <div><dt>更新时间</dt><dd>{formatDate(job.updated_at)}</dd></div>
        {job.rss_id ? <div><dt>订阅 ID</dt><dd>#{job.rss_id}</dd></div> : null}
        {job.rss_item_key ? <div><dt>订阅条目标识</dt><dd><code>{job.rss_item_key}</code></dd></div> : null}
        {sourceURL ? <div><dt>来源</dt><dd><a href={sourceURL} target="_blank" rel="noopener noreferrer">打开来源页面</a></dd></div> : null}
        {job.torrent_hash ? <div><dt>种子 Hash</dt><dd><code>{job.torrent_hash}</code></dd></div> : null}
        {typeof job.discovery?.download_path === 'string' ? <div><dt>投递保存目录</dt><dd><code>{job.discovery.download_path || 'qB 默认目录'}</code></dd></div> : null}
        {job.intake_source_root ? <div><dt>登记的来源边界</dt><dd><code>{job.intake_source_root}</code></dd></div> : null}
        {job.torrent_added_on ? <div><dt>种子添加时间</dt><dd>{formatDate(new Date(job.torrent_added_on * 1000).toISOString())}</dd></div> : null}
        {job.parent_id ? <div><dt>搬运来源任务</dt><dd><JobLinks ids={[job.parent_id]} onSelect={onSelect} /></dd></div> : null}
        {job.related_job_id ? <div><dt>关联已有任务</dt><dd><JobLinks ids={[job.related_job_id]} onSelect={onSelect} /></dd></div> : null}
        {job.child_job_ids?.length ? <div><dt>关联影片任务</dt><dd><JobLinks ids={job.child_job_ids} onSelect={onSelect} /></dd></div> : null}
        {job.last_javsp_run_id ? <div><dt>最近 JAVSP 记录</dt><dd>#{job.last_javsp_run_id}</dd></div> : null}
      </dl>
      <PathList title="来源文件（登记时）" paths={job.source_paths?.length ? job.source_paths : job.source_path ? [job.source_path] : []} />
      <PathList title="搬运目标" paths={job.target_path ? [job.target_path] : []} />
      <PathList title="预定整理输入" paths={job.planned_paths} />
      <PathList title="已确认的输出" paths={job.output_paths} />
      <section className="queue-events"><h3>处理记录</h3><p className="queue-footnote">最近 {shownEvents.length} 条事件，共 {events.length} 条。独立于扫描和运行日志。</p>
        {shownEvents.length ? <ol>{shownEvents.map((event) => <li key={event.id}><strong>{queueEventLabel(event.reason)}</strong><span>{queuePhaseLabel(event)} · {queueStatusLabel(event.status)}</span>{event.message ? <p>{event.message}</p> : null}<time>{formatDate(event.created_at)}</time></li>)}</ol> : <p className="muted-text">暂无处理事件</p>}
      </section>
    </div>
  );
}
