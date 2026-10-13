import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api.js";
import {
  Icon,
  Status,
  Modal,
  usePicker,
  date,
  active,
  runFiles,
} from "./ui.jsx";
import TaskEditor from "./TaskEditor.jsx";
import RunList from "./RunList.jsx";
import RestoreView from "./RestoreView.jsx";

const navigation = [
  ["tasks", "备份任务", "tasks"],
  ["runs", "运行记录", "clock"],
  ["restore", "恢复备份", "restore"],
  ["settings", "设置与关于", "settings"],
];
const subtitles = {
  tasks: "只读源文件，原样复制到目标目录；旧版仓库仍可恢复。",
  runs: "追踪每次执行，查看进度和错误详情。",
  restore: "旧版增量仓库可选择历史版本，恢复到新的本地目录。",
  settings: "管理记录保留，了解备份的安全边界。",
};
export default function App({ client = api }) {
  const [data, setData] = useState(null),
    [page, setPage] = useState("tasks"),
    [error, setError] = useState(""),
    [readError, setReadError] = useState(""),
    [feedback, setFeedback] = useState(null),
    [busy, setBusy] = useState(false),
    [editor, setEditor] = useState(null),
    [selected, setSelected] = useState(""),
    [menu, setMenu] = useState(""),
    [confirm, setConfirm] = useState(null);
  const inFlight = useRef(null);
  const refresh = useCallback(
    async (signal) => {
      if (inFlight.current && !inFlight.current.signal?.aborted)
        return inFlight.current.promise;
      const request = { signal };
      inFlight.current = request;
      request.promise = (async () => {
        try {
          const result = await client.state(signal);
          if (!signal?.aborted) { setData(result); setReadError(""); }
        } catch (e) {
          if (!signal?.aborted) setReadError(e.message);
          throw e;
        } finally {
          if (inFlight.current === request) inFlight.current = null;
        }
      })();
      return request.promise;
    },
    [client],
  );
  useEffect(() => {
    const controller = new AbortController();
    const poll = () =>
      refresh(controller.signal).catch(() => {});
    poll();
    const timer = setInterval(poll, 5000);
    return () => {
      controller.abort();
      clearInterval(timer);
    };
  }, [refresh]);
  const tasks = data?.state.tasks || [],
    runs = data?.state.runs || [];
  const chosen = tasks.find((t) => t.id === selected) || tasks[0];
  const latest = chosen && runs.find((r) => r.task_id === chosen.id);
  const [choose, picker] = usePicker(client, setError);
  async function act(action, message) {
    setBusy(true);
    setError("");
    setFeedback(null);
    try {
      await action();
      if (message) setFeedback({message});
      // A successful write remains successful even if the following read fails.
      // In particular, never invite the user to submit a new task twice.
      await refresh().catch(() => {});
    } catch (e) {
      setError(e.message);
      throw e;
    } finally {
      setBusy(false);
    }
  }
  const safeAct = (action, message) => act(action, message).catch(() => {});
  async function run(id, options) {
    await act(() => client.run(id, options), options.kind === 'preview' ? '预览已提交，仅检查文件变化，不写入备份。' : '任务已提交，请在运行记录中查看进度。');
    setPage("runs");
  }
  function rebind(task) {
    choose("target", (target) => setConfirm({ type: "rebind", task, target }));
    setMenu("");
  }
  async function confirmed() {
    await act(() =>
      confirm.type === "delete"
        ? client.removeTask(confirm.task.id)
        : client.rebind(confirm.task.id, confirm.target),
      confirm.type === 'delete'
        ? confirm.task.selection.mode === 'direct'
          ? '任务配置已删除，源文件和目标文件保留。'
          : '任务配置已删除，源文件和云端历史备份保留。'
        : '挂载已重新绑定，定时执行已关闭，请核对后再开启。',
    );
    setConfirm(null);
  }
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <Icon name="cloud" />
          <span>飞牛备份中心</span>
        </div>
        <nav aria-label="主导航">
          {navigation.map(([key, name, icon]) => (
            <button
              key={key}
              className={page === key ? "nav-active" : ""}
              aria-current={page === key ? "page" : undefined}
              onClick={() => {
                setPage(key);
                setMenu("");
              }}
            >
              <Icon name={icon} />
              {name}
            </button>
          ))}
        </nav>
        <p className="version">v{data?.version || "0.0.1"} · 测试版</p>
      </aside>
      <main>
        <header className="page-header">
          <div>
            <h1>{navigation.find((n) => n[0] === page)[1]}</h1>
            <p>{subtitles[page]}</p>
          </div>
          {page === "tasks" && (
            <button
              className="primary"
              disabled={!data || busy}
              onClick={() => setEditor({})}
            >
              <Icon name="plus" />
              新建任务
            </button>
          )}
        </header>
        <div className="cloud-warning">
          <Icon name="info" />
          <span>
            文件写入挂载目录不等于云端已持久化；直拷不进行内容哈希或回读校验，中断时目标文件可能不完整。
          </span>
        </div>
        {error && (
          <div role="alert" className="error">
            {error}
            <button
              className="icon-button"
              aria-label="关闭错误提示"
              onClick={() => setError("")}
            >
              <Icon name="close" />
            </button>
          </div>
        )}
        {readError && <div role="alert" className="error">
          <div>状态读取失败：{readError}<p className="hint">当前显示的是上次读取结果；不会因此重复提交任务。正在自动重连。</p></div>
          <button type="button" onClick={() => refresh().catch(() => {})}>重试连接</button>
        </div>}
        {feedback && <div className="feedback" role="status">
          <div>{feedback.message}</div>
          {feedback.taskID && <button type="button" disabled={busy} onClick={() => run(feedback.taskID, {kind:'preview'}).catch(() => {})}>预览此任务</button>}
          <button className="icon-button" aria-label="关闭成功提示" onClick={() => setFeedback(null)}><Icon name="close" /></button>
        </div>}
        {!data ? (
          <p role="status">正在连接备份服务…</p>
        ) : page === "tasks" ? (
          <>
            <ol className="backup-steps" aria-label="备份使用步骤">
              <li><b>1</b><span>新建任务<small>确定源目录、目标与时间</small></span></li>
              <li><b>2</b><span>预览变化<small>只检查，不写入文件</small></span></li>
              <li><b>3</b><span>执行并核对<small>查看记录，确认云端文件</small></span></li>
            </ol>
            <div className="toolbar">
              <strong>{tasks.length} 个任务</strong>
              <button
                className="text-button"
                disabled={busy}
                onClick={() => refresh().catch(() => {})}
              >
                <Icon name="refresh" />
                刷新
              </button>
            </div>
            {!tasks.length ? (
              <div className="empty">
                <Icon name="folder" />
                <h2>从第一份备份开始</h2>
                <p>
                  选择本地源目录与已挂载的网盘目录。新任务默认不启用定时执行。
                </p>
                <button onClick={() => setEditor({})}>新建任务</button>
              </div>
            ) : (
              <div className="table-scroll task-table">
                <table>
                  <thead>
                    <tr>
                      <th>任务 / 目录</th>
                      <th>定时执行</th>
                      <th>最近结果</th>
                      <th>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {tasks.map((task) => {
                      const last = runs.find((r) => r.task_id === task.id),
                        locked = runs.some(
                          (r) => r.task_id === task.id && active(r),
                        );
                      return (
                        <tr
                          key={task.id}
                          className={
                            chosen?.id === task.id ? "selected-row" : ""
                          }
                        >
                          <td>
                            <div className="task-cell">
                              <Icon name="folder" />
                              <div>
                                <button
                                  className="task-name"
                                  onClick={() => setSelected(task.id)}
                                >
                                  {task.name}
                                </button>
                                <small className="path">
                                  {task.selection.source || "只读仓库"}
                                </small>
                                <small className="path">
                                  → {task.selection.target}
                                </small>
                              </div>
                            </div>
                          </td>
                          <td>
                            {task.schedule.enabled ? (
                              <>
                                {task.schedule.kind === "interval"
                                  ? `每 ${task.schedule.minutes} 分钟`
                                  : `${task.schedule.kind === "weekly" ? "每周" : "每天"} ${task.schedule.at}`}
                                <small>下次：{date(task.next)}</small>
                              </>
                            ) : (
                              "未启用"
                            )}
                          </td>
                          <td>
                            <Status value={last?.status} />
                            {last && (
                              <small>
                                {runFiles(last)} 个文件 ·{" "}
                                {last.kind === "preview"
                                  ? "只读预览"
                                  : "详见运行记录"}
                              </small>
                            )}
                          </td>
                          <td>
                            <div className="actions">
                              <button
                                disabled={busy || locked || task.read_only}
                                onClick={() =>
                                  run(task.id, { kind: "preview" }).catch(
                                    () => {},
                                  )
                                }
                              >
                                预览
                              </button>
                              <button
                                className="outline-primary"
                                disabled={busy || locked || task.read_only}
                                onClick={() =>
                                  run(task.id, { kind: "backup" }).catch(
                                    () => {},
                                  )
                                }
                              >
                                立即备份
                              </button>
                              <div className="menu-anchor">
                                <button
                                  className="icon-button"
                                  aria-label={`${task.name} 更多操作`}
                                  aria-expanded={menu === task.id}
                                  disabled={busy || locked}
                                  onClick={() =>
                                    setMenu(menu === task.id ? "" : task.id)
                                  }
                                >
                                  <Icon name="more" />
                                </button>
                                {menu === task.id && (
                                  <div className="menu">
                                    <button
                                      disabled={task.read_only}
                                      onClick={() => {
                                        setEditor(task);
                                        setMenu("");
                                      }}
                                    >
                                      编辑任务
                                    </button>
                                    {task.selection.mode !== 'direct' && <button onClick={() => rebind(task)}>
                                      重新选择挂载
                                    </button>}
                                    <button
                                      className="danger"
                                      onClick={() => {
                                        setConfirm({ type: "delete", task });
                                        setMenu("");
                                      }}
                                    >
                                      删除任务配置
                                    </button>
                                  </div>
                                )}
                              </div>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
            {chosen && (
              <>
                <section className="task-detail">
                  <h2>
                    <Icon name="folder" />
                    {chosen.name}
                  </h2>
                  <dl className="stats">
                    <div>
                      <dt>备份方式</dt>
                      <dd>{chosen.read_only ? "只读导入" : chosen.selection.mode === 'direct' ? "原样复制" : "旧版增量仓库"}</dd>
                    </div>
                    <div>
                      <dt>历史版本</dt>
                      <dd>{chosen.selection.mode === 'direct' ? '不生成' : '始终保留'}</dd>
                    </div>
                    <div>
                      <dt>目标保护</dt>
                      <dd>挂载身份校验</dd>
                    </div>
                  </dl>
                  <details>
                    <summary className="hint">挂载标识</summary>
                    {chosen.selection.mode !== 'direct' && <p className="hint path">仓库 ID：{chosen.selection.repo_id}</p>}
                    <p className="hint path">
                      {chosen.selection.mount?.point} ·{" "}
                      {chosen.selection.mount?.source}
                    </p>
                  </details>
                </section>
                <section className="recent">
                  <h3>最近运行</h3>
                  <div>
                    <Status value={latest?.status} />
                    {latest?.preview ? (
                      <span>
                        新增 {latest.preview.new} · 变化{" "}
                        {latest.preview.changed} · 未变化{" "}
                        {latest.preview.unchanged}
                        {chosen.selection.mode === 'direct'
                          ? ' · 目标额外文件保留'
                          : ` · 源端删除 ${latest.preview.removed}（历史保留）`}
                      </span>
                    ) : (
                      <span>
                        写入 {latest?.result?.copied || 0} · 复用{" "}
                        {latest?.result?.unchanged || 0} · 失败{" "}
                        {latest?.result?.failed || 0}
                      </span>
                    )}
                    <button
                      className="text-button"
                      onClick={() => setPage("runs")}
                    >
                      查看记录 ›
                    </button>
                  </div>
                </section>
              </>
            )}
            <div className="safety">
              <Icon name="info" />
              源文件删除不会自动删除目标文件。直拷无历史版本；旧版仓库的恢复会创建新目录。
            </div>
          </>
        ) : page === "runs" ? (
          <>
            <div className="toolbar">
              <span>
                最近 {runs.length} 次 · 完成记录最多保留 {data.state.retain} 次
              </span>
              <button onClick={() => refresh().catch(() => {})}>刷新记录</button>
            </div>
            <RunList
              runs={runs}
              tasks={tasks}
              busy={busy}
              onCancel={(id) => safeAct(() => client.cancel(id))}
            />
          </>
        ) : page === "restore" ? (
          <RestoreView
            client={client}
            tasks={tasks.filter(task => task.selection.mode !== 'direct')}
            onRun={run}
            onRefresh={refresh}
          />
        ) : (
          <Settings
            key={data.state.retain}
            retain={data.state.retain}
            version={data.version}
            busy={busy}
            onSave={(count) => safeAct(() => client.setRetention(count), '设置已保存。仅影响本地运行记录保留，不删除备份版本。')}
          />
        )}
      </main>
      {editor && (
        <TaskEditor
          task={editor.id ? editor : null}
          client={client}
          onClose={() => setEditor(null)}
          onSaved={async (saved) => {
            setError('');
            setSelected(saved.id);
            setFeedback({taskID: saved.read_only ? null : saved.id, message: `${editor.id ? '任务已保存' : '任务已创建'}：${saved.name}。${saved.schedule.enabled ? '定时计划已启用，可在任务列表查看下次执行时间。' : '定时执行未启用；可先预览，再手动备份。'}`});
            setEditor(null);
            await refresh().catch(() => {});
          }}
        />
      )}
      {confirm && (
        <Modal
          title={
            confirm.type === "delete" ? "删除任务配置？" : "确认重新选择挂载"
          }
          onClose={() => setConfirm(null)}
          busy={busy}
        >
          <p>
            {confirm.type === "delete"
              ? confirm.task.selection.mode === 'direct' ? "只删除任务配置；源文件和目标目录里的文件都保留。" : "只删除此任务配置，源文件和云端历史备份均保留。请记下仓库 ID，以便日后只读导入恢复。"
              : "将在新目标中校验原仓库身份与备份清单。确认后保留历史记录，并关闭定时执行；不会创建另一份仓库。"}
          </p>
          <p className="path">
            {confirm.type === "delete"
              ? confirm.task.selection.mode === 'direct' ? confirm.task.selection.target : confirm.task.selection.repo_id
              : confirm.target}
          </p>
          <footer className="modal-footer">
            <button onClick={() => setConfirm(null)} disabled={busy}>
              取消
            </button>
            <button
              className="primary"
              disabled={busy}
              onClick={() => confirmed().catch(() => {})}
            >
              {confirm.type === "delete" ? "确认删除配置" : "验证并重新绑定"}
            </button>
          </footer>
        </Modal>
      )}
      {picker}
    </div>
  );
}

function Settings({ retain, version, busy, onSave }) {
  const [count, setCount] = useState(retain);
  return (
    <>
      <section className="settings-section">
        <h2>记录与保留</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            onSave(Number(count));
          }}
        >
          <label className="field short">
            运行记录保留量
            <input
              type="number"
              required
              min={1}
              max={1000}
              value={count}
              onChange={(e) => setCount(e.target.value)}
            />
          </label>
          <p className="hint">
            只清理本地已结束的运行记录，不影响任务、运行中的记录或备份版本。
          </p>
          <button className="primary" disabled={busy}>
            保存设置
          </button>
        </form>
        <p className="notice">
          云端历史版本删除：关闭。首版不提供自动删除云端版本。
        </p>
      </section>
      <section className="settings-section">
        <h2>飞牛备份中心 v{version}</h2>
        <p>
          测试阶段 · Go 原生服务 · 通过飞牛已挂载的网盘备份，不接收网盘密码或
          Cookie。
        </p>
        <ul>
          <li>新任务默认直接写入目标文件，按大小和修改时间跳过未变化文件，不做内容哈希或回读校验；中断时目标文件可能不完整。</li>
          <li>旧版增量仓库任务仍可读取历史版本并恢复，不会自动清理。</li>
          <li>
            不保留 ACL、扩展属性、硬链接关系，不跟随符号链接或跨越嵌套挂载。
          </li>
          <li>不是数据库一致性快照；备份数据库前需自行停写或导出。</li>
          <li>rclone 写回缓存无法独立确认；直拷完成只代表写入挂载层，不保证云端持久化。</li>
          <li>
            取消需要等待当前文件系统操作返回。请保留独立备份，不将测试版本作为唯一数据保障。
          </li>
        </ul>
        <p className="muted">
          原生目录授权需要 fnOS 1.2.0401
          或更新版本。具体网盘能力取决于飞牛挂载服务。
        </p>
      </section>
    </>
  );
}
