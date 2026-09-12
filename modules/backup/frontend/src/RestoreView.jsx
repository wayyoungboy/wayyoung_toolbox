import { useEffect, useState } from "react";
import { Modal, usePicker, date, bytes, Status } from "./ui.jsx";

export default function RestoreView({ client, tasks, onRun, onRefresh }) {
  const [selected, setSelected] = useState(tasks[0]?.id || ""),
    [snapshots, setSnapshots] = useState([]),
    [loading, setLoading] = useState(false),
    [error, setError] = useState(""),
    [reload, setReload] = useState(0);
  const [restore, setRestore] = useState(null),
    [importing, setImporting] = useState(false);
  useEffect(() => {
    if (!selected) return;
    let alive = true;
    setLoading(true);
    setError("");
    setSnapshots([]);
    client
      .snapshots(selected)
      .then((data) => {
        if (alive) setSnapshots([...(data || [])].reverse());
      })
      .catch((e) => {
        if (alive) setError(e.message);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [client, selected, reload]);
  return (
    <>
      <div className="toolbar">
        <label>
          备份任务{" "}
          <select
            aria-label="选择恢复任务"
            value={selected}
            onChange={(e) => setSelected(e.target.value)}
          >
            <option value="" disabled>
              请选择任务
            </option>
            {tasks.map((t) => (
              <option key={t.id} value={t.id}>
                {t.name}
                {t.read_only ? "（只读导入）" : ""}
              </option>
            ))}
          </select>
        </label>
        <div className="actions">
          <button
            onClick={() => setReload(reload + 1)}
            disabled={loading || !selected}
          >
            刷新版本
          </button>
          <button onClick={() => setImporting(true)}>导入已有仓库</button>
        </div>
      </div>
      <p className="hint">
        仅恢复到新的本地目录，不覆盖现有文件。版本列表来自当前挂载，不代表云端已独立确认。
      </p>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      {loading ? (
        <p role="status">正在读取备份清单…</p>
      ) : !snapshots.length ? (
        <div className="empty">
          <h2>暂无可恢复版本</h2>
          <p>
            完成首次备份后可在此恢复；应用重装丢失配置时，可导入原备份仓库。
          </p>
        </div>
      ) : (
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>版本时间 / ID</th>
                <th>文件</th>
                <th>验证状态</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {snapshots.map((s) => (
                <tr key={s.id}>
                  <td>
                    {date(s.created)}
                    <small className="path">{s.id}</small>
                  </td>
                  <td>
                    {s.files.length} 个 ·{" "}
                    {bytes(s.files.reduce((sum, f) => sum + f.size, 0))}
                  </td>
                  <td>
                    <Status value="pending" />
                  </td>
                  <td>
                    <button onClick={() => setRestore(s)}>恢复此版本</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {restore && (
        <RestoreModal
          client={client}
          snapshot={restore}
          onClose={() => setRestore(null)}
          onRun={(options) => onRun(selected, options)}
        />
      )}
      {importing && (
        <ImportModal
          client={client}
          onClose={() => setImporting(false)}
          onSaved={async () => {
            await onRefresh();
            setImporting(false);
          }}
        />
      )}
    </>
  );
}

function RestoreModal({ client, snapshot, onClose, onRun }) {
  const [parent, setParent] = useState(""),
    [name, setName] = useState(`恢复-${snapshot.id.slice(0, 8)}`),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const [choose, picker] = usePicker(client, setError);
  async function submit(e) {
    e.preventDefault();
    setError("");
    if (
      !parent ||
      !name.trim() ||
      [".", ".."].includes(name) ||
      /[/\\\u0000]/.test(name)
    ) {
      setError("请选择本地父目录，并填写不含斜杠的新目录名称");
      return;
    }
    setBusy(true);
    try {
      await onRun({
        kind: "restore",
        snapshot_id: snapshot.id,
        destination: `${parent}/${name}`,
      });
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <Modal
        title="恢复到新目录"
        subtitle="逐文件校验 SHA-256，不覆盖任何现有目录。"
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
            本地父目录
            <div className="input-picker">
              <input readOnly value={parent} placeholder="选择已授权本地目录" />
              <button
                type="button"
                onClick={() => choose("restore", setParent)}
              >
                选择目录
              </button>
            </div>
          </label>
          <label className="field">
            新目录名称
            <input
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <p className="hint path">{parent && `${parent}/${name}`}</p>
          <p className="notice">
            恢复失败时保留带有未完成标记的部分文件，不会自动覆盖重试。
          </p>
          <footer className="modal-footer">
            <button type="button" disabled={busy} onClick={onClose}>
              取消
            </button>
            <button className="primary" disabled={busy}>
              开始恢复
            </button>
          </footer>
        </form>
      </Modal>
      {picker}
    </>
  );
}

function ImportModal({ client, onClose, onSaved }) {
  const [name, setName] = useState("恢复仓库"),
    [target, setTarget] = useState(""),
    [repo, setRepo] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const [choose, picker] = usePicker(client, setError);
  async function submit(e) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await client.importRepository({ name, target, repo_id: repo });
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
        title="只读导入已有仓库"
        subtitle="只验证并读取，不接管写入，也不启用定时任务。"
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
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label className="field">
            原目标目录
            <div className="input-picker">
              <input readOnly value={target} />
              <button type="button" onClick={() => choose("target", setTarget)}>
                选择目录
              </button>
            </div>
          </label>
          <label className="field">
            仓库 ID
            <input
              required
              pattern="[0-9a-f]{32}"
              value={repo}
              onChange={(e) => setRepo(e.target.value.trim())}
            />
          </label>
          <p className="hint">
            选择原目标目录，再输入其 fnos-backup-center 子目录中的 32
            位仓库目录名。
          </p>
          <footer className="modal-footer">
            <button type="button" onClick={onClose} disabled={busy}>
              取消
            </button>
            <button className="primary" disabled={busy}>
              验证并导入
            </button>
          </footer>
        </form>
      </Modal>
      {picker}
    </>
  );
}
