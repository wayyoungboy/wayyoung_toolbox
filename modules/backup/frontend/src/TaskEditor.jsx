import { useState } from "react";
import { Modal, usePicker, Icon } from "./ui.jsx";

export default function TaskEditor({ task, client, onClose, onSaved }) {
  const [value, setValue] = useState(
    task
      ? structuredClone(task)
      : {
          name: "",
          selection: { source: "", target: "" },
          schedule: {
            enabled: false,
            kind: "daily",
            at: "02:00",
            weekday: 1,
            minutes: 60,
            zone: "Asia/Shanghai",
          },
          retries: 1,
        },
  );
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
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
      await client.saveTask(value);
      await onSaved();
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
        subtitle="选择授权目录，先预览，再开始备份。"
        onClose={onClose}
        busy={busy}
      >
        <form onSubmit={submit}>
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
            </div>
          ))}
          <p className="hint">
            执行前和执行中检查挂载身份；失联时停止写入。
            {task && " 更换挂载请使用任务的“重新选择挂载”。"}
          </p>
          <div className="schedule-switch">
            <div>
              <strong>定时执行</strong>
              <p className="hint">
                新任务默认关闭，可在此开启或以后编辑，也可随时手动执行。
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
          <div className="form-grid">
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
          </div>
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
          <p className="hint">历史版本始终保留。不会自动删除云端备份。</p>
          <footer className="modal-footer">
            <button type="button" onClick={onClose} disabled={busy}>
              取消
            </button>
            <button className="primary" disabled={busy}>
              {busy ? "保存中…" : task ? "保存任务" : "创建任务"}
            </button>
          </footer>
        </form>
      </Modal>
      {picker}
    </>
  );
}
