import { Status, date, bytes, active, runFiles } from "./ui.jsx";

const kinds = { backup: "备份", preview: "预览", restore: "恢复" };
const phases = {
  scanning: "读取源文件并计算校验",
  copying: "写入挂载并校验",
  copying_direct: "原样复制到目标目录",
  restoring: "恢复并校验",
  committing: "发布备份清单",
};
export default function RunList({ runs, tasks = [], onCancel, busy }) {
  if (!runs.length)
    return (
      <div className="empty">
        <h2>尚无运行记录</h2>
        <p>先在备份任务中执行预览，确认范围后再备份。</p>
      </div>
    );
  return (
    <div className="run-list">
      {runs.map((run) => (
        <details key={run.id} className="run" open={active(run) || !!run.error}>
          <summary>
            <div>
              <strong>
                {run.task_name} · {kinds[run.kind] || run.kind}
              </strong>
              <small>
                {date(run.started)} ·{" "}
                {run.trigger === "schedule" ? "定时" : "手动"}
              </small>
            </div>
            <Status value={run.status} />
            <span className="summary-count">{runFiles(run)} 个文件</span>
          </summary>
          <div className="run-content">
            {active(run) && (
              <>
                <p>
                  {phases[run.progress?.phase] || "正在准备任务"}
                  {run.progress?.current && (
                    <span className="path">{run.progress.current}</span>
                  )}
                </p>
                <progress
                  aria-label="文件处理进度"
                  max={Math.max(1, run.progress?.total || 0)}
                  value={run.progress?.files || 0}
                />
                <p className="hint">
                  {run.progress?.files || 0} / {run.progress?.total || 0} 个文件
                  · {bytes(run.progress?.bytes)} /{" "}
                  {bytes(run.progress?.total_bytes)}
                </p>
                <button
                  disabled={busy || run.status === "cancel_requested"}
                  onClick={() => onCancel(run.id)}
                >
                  {run.status === "cancel_requested"
                    ? "等待当前文件操作返回…"
                    : "取消任务"}
                </button>
                <p className="hint">
                  取消将在当前文件系统操作返回后生效。断网时可能需要等待挂载超时。
                </p>
              </>
            )}
            {run.preview && (
              <dl className="stats">
                <div>
                  <dt>新增</dt>
                  <dd>{run.preview.new}</dd>
                </div>
                <div>
                  <dt>变化</dt>
                  <dd>{run.preview.changed}</dd>
                </div>
                <div>
                  <dt>未变化</dt>
                  <dd>{run.preview.unchanged}</dd>
                </div>
                {tasks.find(task => task.id === run.task_id)?.selection.mode !== 'direct' && <div>
                  <dt>源端已删除（历史保留）</dt>
                  <dd>{run.preview.removed}</dd>
                </div>}
              </dl>
            )}
            {!active(run) && !run.preview && (
              <p>
                写入 / 恢复 {run.result?.copied || 0} · 复用{" "}
                {run.result?.unchanged || 0} · 失败 {run.result?.failed || 0} ·{" "}
                {bytes(run.result?.bytes)}
              </p>
            )}
            {run.error && <p className="error">{run.error}</p>}
            {run.result?.notice && (
              <p className="notice">{run.result.notice}</p>
            )}
            <p className="hint">
              尝试次数：{run.attempts || 0} · 结束：{date(run.ended)}
            </p>
            {run.result?.snapshot_id && (
              <p className="hint path">版本 ID：{run.result.snapshot_id}</p>
            )}
          </div>
        </details>
      ))}
    </div>
  );
}
