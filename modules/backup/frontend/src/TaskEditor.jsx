import { useEffect, useState } from "react";
import { Modal, usePicker, Icon } from "./ui.jsx";

export default function TaskEditor({ task, client, onClose, onSaved }) {
  const [initial] = useState(() => task
      ? structuredClone(task)
      : {
          name: "",
          selection: { source: "", target: "", mode: "direct" },
          schedule: {
            enabled: false,
            kind: "daily",
            at: "02:00",
            weekday: 1,
            minutes: 60,
            zone: "Asia/Shanghai",
          },
          retries: 1,
        });
  const [value, setValue] = useState(initial);
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [discard, setDiscard] = useState(false);
  const dirty = JSON.stringify(value) !== JSON.stringify(initial);
  useEffect(() => {
    if (!dirty) return;
    const warn = event => { event.preventDefault(); event.returnValue = ''; };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);
  function close() { if (busy) return; if (dirty) setDiscard(true); else onClose(); }
  const [choose, picker] = usePicker(client, setError);
  const schedule = (key, v) =>
    setValue((prev) => ({ ...prev, schedule: { ...prev.schedule, [key]: v } }));
  const directory = (key, v) =>
    setValue((prev) => ({
      ...prev,
      selection: { ...prev.selection, [key]: v },
    }));
  async function submit(event) {
    event.preventDefault();
    setError("");
    setBusy(true);
    try {
      if (!value.selection.source || !value.selection.target)
        throw new Error("请选择源目录与目标目录");
      const saved = await client.saveTask(value);
      await onSaved(saved);
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <Modal
        title={task ? "编辑备份任务" : "新建备份任务"}
        subtitle="源文件不会修改。直拷只按文件大小和修改时间跳过未变化文件，不计算内容哈希。"
        onClose={close}
        busy={busy}
        className="task-editor"
      >
        <form onSubmit={submit}>
          <fieldset disabled={busy} className="editor-fields">
          {error && (
            <p role="alert" className="error">
              {error}
            </p>
          )}
          <label className="field">
            任务名称
            <input
              required
              maxLength={100}
              value={value.name}
              onChange={(e) => setValue({ ...value, name: e.target.value })}
            />
          </label>
          <h3 className="form-section-title"><span>1</span> 备份什么，存到哪里</h3>
          {task && !task.read_only && task.selection.mode !== 'direct' && <label className="field">
            备份方式
            <select value={value.selection.mode || 'versioned'} onChange={e => directory('mode', e.target.value)}>
              <option value="versioned">旧版增量仓库</option>
              <option value="direct">原样复制到目标目录</option>
            </select>
            <span className="hint">切换为原样复制后，不再生成新历史版本；已有仓库文件不会删除。</span>
          </label>}
          {!task && <p className="hint">备份方式：原样复制到目标目录；目标已有的其他文件不会删除。</p>}
          {[
            ["source", "源目录（只读）", "选择本地目录"],
            ["target", "目标目录", "选择已挂载的网盘目录"],
          ].map(([key, label, placeholder]) => (
            <div className="field" key={key}>
              <label htmlFor={`task-${key}`}>{label}</label>
              <div className="input-picker">
                <Icon name="folder" />
                <input
                  id={`task-${key}`}
                  readOnly
                  value={value.selection[key]}
                  placeholder={placeholder}
                />
                <button
                  type="button"
                  disabled={busy || !!task}
                  onClick={() => choose(key, (path) => directory(key, path))}
                >
                  选择目录
                </button>
              </div>
              {!task && <button type="button" className="text-button" disabled={busy}
                aria-label={`从已授权目录选择${key === 'source' ? '源目录' : '目标目录'}`}
                onClick={() => choose(key, (path) => directory(key, path), { existing: true })}>
                从已授权目录选择
              </button>}
              <p className="hint">{key === 'source' ? '只读本地文件，不会移动或删除源文件。' : '请选择已挂载的网盘目录；断开挂载时会停止写入。'}</p>
            </div>
          ))}
          {task && <p className="hint">更换挂载请使用任务的“重新选择挂载”。</p>}
          <div className="schedule-switch">
            <div>
              <h3 className="form-section-title"><span>2</span> 定时执行</h3>
              <p className="hint">
                {value.schedule.enabled ? '保存后按计划自动备份，请确认目录与执行时间。' : '当前仅手动执行，需要时再开启自动备份。'}
              </p>
            </div>
            <label className="switch">
              <input
                type="checkbox"
                aria-label="启用定时执行"
                disabled={busy || task?.read_only}
                checked={value.schedule.enabled}
                onChange={(e) => schedule("enabled", e.target.checked)}
              />
              <span />
            </label>
          </div>
          {value.schedule.enabled && <div className="form-grid">
            <label className="field">
              执行频率
              <select
                value={value.schedule.kind}
                onChange={(e) => schedule("kind", e.target.value)}
              >
                <option value="daily">每天</option>
                <option value="weekly">每周</option>
                <option value="interval">固定间隔</option>
              </select>
            </label>
            {value.schedule.kind === "interval" ? (
              <label className="field">
                间隔（分钟）
                <input
                  type="number"
                  min={1}
                  max={10080}
                  required
                  value={value.schedule.minutes}
                  onChange={(e) => schedule("minutes", Number(e.target.value))}
                />
              </label>
            ) : (
              <label className="field">
                执行时间
                <input
                  type="time"
                  required
                  value={value.schedule.at}
                  onChange={(e) => schedule("at", e.target.value)}
                />
              </label>
            )}
            {value.schedule.kind === "weekly" && (
              <label className="field">
                星期
                <select
                  value={value.schedule.weekday}
                  onChange={(e) => schedule("weekday", Number(e.target.value))}
                >
                  {["日", "一", "二", "三", "四", "五", "六"].map((d, i) => (
                    <option key={i} value={i}>
                      星期{d}
                    </option>
                  ))}
                </select>
              </label>
            )}
            <label className="field">
              时区
              <input
                required
                value={value.schedule.zone}
                onChange={(e) => schedule("zone", e.target.value)}
              />
            </label>
          </div>}
          <details className="advanced">
            <summary>重试与记录保留</summary>
            <label className="field">
              失败后重试次数
              <input
                type="number"
                min={0}
                max={3}
                required
                value={value.retries}
                onChange={(e) =>
                  setValue({ ...value, retries: Number(e.target.value) })
                }
              />
            </label>
            <p className="hint">
              最多 3
              次。取消、挂载失联不重试；恢复失败请更换新目录。运行记录保留量在设置中统一管理。
            </p>
          </details>
          <p className="hint">{value.selection.mode === 'direct' ? '直拷任务不生成历史版本，也不删除目标已有文件；中断时目标文件可能不完整。' : '旧版仓库的历史版本始终保留，不会自动删除。'}</p>
          </fieldset>
          <footer className="modal-footer">
            <p className="save-explanation">{value.schedule.enabled ? '保存后定时计划生效，不会立即开始备份。' : task ? '保存后仅手动执行，不会开始备份。' : '仅创建任务，不会开始备份。'}</p>
            <button type="button" onClick={close} disabled={busy}>
              取消
            </button>
            <button className="primary" disabled={busy}>
              {busy ? "保存中…" : task ? "保存任务" : "创建任务"}
            </button>
          </footer>
        </form>
      </Modal>
      {discard && <Modal title="放弃未保存的修改？" onClose={() => setDiscard(false)}>
        <p>任务尚未保存。继续编辑会保留刚才填写的目录和定时设置。</p>
        <footer className="modal-footer">
          <button type="button" onClick={onClose}>放弃修改</button>
          <button type="button" className="primary" onClick={() => setDiscard(false)}>继续编辑</button>
        </footer>
      </Modal>}
      {picker}
    </>
  );
}
