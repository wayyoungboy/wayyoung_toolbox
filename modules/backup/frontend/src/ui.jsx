import { useEffect, useRef, useState } from "react";

const drawings = {
  cloud: (
    <>
      <path d="M6 18a5 5 0 0 1-1-10 7 7 0 0 1 13-1 5.5 5.5 0 0 1 0 11" />
      <path d="M8 13h8v8H8zM10 16h4m-4 2h4" />
    </>
  ),
  tasks: (
    <>
      <rect x="3" y="7" width="18" height="14" rx="2" />
      <path d="M8 7V4h8v3M3 12h18" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 6v7l4 2" />
    </>
  ),
  restore: (
    <>
      <path d="M6 18a5 5 0 0 1-1-10 7 7 0 0 1 13-1 5.5 5.5 0 0 1 0 11M12 22V11m-4 4 4-4 4 4" />
    </>
  ),
  settings: (
    <>
      <path d="m9 3-1 3-3 1-2 3 2 3v3l3 2 1 3h6l1-3 3-2v-3l2-3-2-3-3-1-1-3z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
  folder: <path d="M3 6h6l3 3h9v11H3zM3 9h18" />,
  plus: <path d="M12 4v16M4 12h16" />,
  refresh: (
    <>
      <path d="M20 9a8 8 0 0 0-14-3L3 9m0-5v5h5M4 15a8 8 0 0 0 14 3l3-3m0 5v-5h-5" />
    </>
  ),
  info: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 11v6m0-11v1" />
    </>
  ),
  close: <path d="m6 6 12 12M6 18 18 6" />,
  more: (
    <>
      <circle cx="12" cy="5" r="1" />
      <circle cx="12" cy="12" r="1" />
      <circle cx="12" cy="19" r="1" />
    </>
  ),
};
export function Icon({ name, ...props }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      {...props}
    >
      {drawings[name]}
    </svg>
  );
}
const statuses = {
  pending: "待确认",
  publication_unknown: "提交待核对",
  record_error: "记录保存失败",
  running: "运行中",
  cancel_requested: "取消中",
  canceled: "已取消",
  interrupted: "已中断",
  failed: "失败",
  restored: "已恢复并校验",
  previewed: "预览完成",
  preview: "预览完成",
  idle: "尚未运行",
};
export function Status({ value = "idle" }) {
  return (
    <span className={`status status-${value}`}>
      <span className="dot" />
      {statuses[value] || value}
    </span>
  );
}
export function date(value) {
  return !value || value.startsWith("0001")
    ? "—"
    : new Date(value).toLocaleString("zh-CN", { hour12: false });
}
export const active = (run) =>
  ["running", "cancel_requested"].includes(run?.status);
export const runFiles = (run) =>
  run?.preview?.files ??
  (active(run)
    ? run?.progress?.files || 0
    : (run?.result?.copied || 0) + (run?.result?.unchanged || 0));
export function bytes(value = 0) {
  if (value < 1024) return `${value} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let n = value / 1024,
    i = 0;
  while (n >= 1024 && i < 3) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(1)} ${units[i]}`;
}

export function Modal({ title, subtitle, children, onClose, busy = false }) {
  const ref = useRef(null);
  const latest = useRef({ onClose, busy });
  latest.current = { onClose, busy };
  useEffect(() => {
    const previous = document.activeElement;
    const el = ref.current;
    el.focus();
    function key(event) {
      if (event.key === "Escape" && !latest.current.busy) {
        event.stopPropagation();
        latest.current.onClose();
      }
      if (event.key === "Tab") {
        const items = [
          ...el.querySelectorAll(
            'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex="0"]',
          ),
        ];
        const first = items[0],
          last = items.at(-1);
        if (!items.length) {
          event.preventDefault();
          return;
        }
        if (
          event.shiftKey &&
          (document.activeElement === first || document.activeElement === el)
        ) {
          event.preventDefault();
          last.focus();
        } else if (
          !event.shiftKey &&
          (document.activeElement === last || document.activeElement === el)
        ) {
          event.preventDefault();
          first.focus();
        }
      }
    }
    el.addEventListener("keydown", key);
    return () => {
      el.removeEventListener("keydown", key);
      previous?.focus();
    };
  }, []);
  return (
    <div className="overlay">
      <section
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        ref={ref}
      >
        <button
          className="icon-button close"
          aria-label="关闭对话框"
          onClick={onClose}
          disabled={busy}
        >
          <Icon name="close" />
        </button>
        <h2>{title}</h2>
        {subtitle && <p className="muted">{subtitle}</p>}
        {children}
      </section>
    </div>
  );
}

export function FolderPicker({ client, kind, onSelect, onClose }) {
  const [listing, setListing] = useState(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const sequence = useRef(0);
  async function load(path = "") {
    const current = ++sequence.current;
    setBusy(true);
    setError("");
    try {
      const result = await client.directories(kind, path);
      if (sequence.current === current) setListing(result);
    } catch (e) {
      if (sequence.current === current) setError(e.message);
    } finally {
      if (sequence.current === current) setBusy(false);
    }
  }
  useEffect(() => {
    load();
    return () => {
      sequence.current++;
    };
  }, [client, kind]); // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <Modal
      title="选择已授权目录"
      subtitle="仅列出已授权的本地目录或当前远程挂载。新增授权请从飞牛桌面打开本应用。"
      onClose={onClose}
    >
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      <div className="folder-location">
        <button
          disabled={busy || !listing?.path}
          onClick={() => load(listing?.parent || "")}
        >
          上一级
        </button>
        <span>{listing?.path || "可用目录"}</span>
      </div>
      <div className="folder-list" aria-busy={busy}>
        {(listing?.path ? listing?.entries : listing?.roots)?.map((item) => (
          <button
            key={item.path}
            disabled={busy}
            onClick={() => load(item.path)}
          >
            <Icon name="folder" />
            {item.name}
            <span>›</span>
          </button>
        ))}
        {!busy &&
          listing &&
          (listing.path
            ? !listing.entries?.length
            : !listing.roots?.length) && (
            <p className="muted">
              {listing.path
                ? "没有下级文件夹，可选择当前目录。"
                : "尚无可用目录，请检查挂载或应用目录授权。"}
            </p>
          )}
      </div>
      {listing?.truncated && (
        <p className="muted">目录较多，仅显示前 2000 项。</p>
      )}
      <footer className="modal-footer">
        <button onClick={onClose}>取消</button>
        <button
          className="primary"
          disabled={busy || !listing?.path || !!error}
          onClick={() => onSelect(listing.path)}
        >
          选择此目录
        </button>
      </footer>
    </Modal>
  );
}

export function usePicker(client, reportError) {
  const [picker, setPicker] = useState(null);
  const [waiting, setWaiting] = useState(false);
  const requestID = useRef(0);
  const inFlight = useRef(false);
  useEffect(() => () => { requestID.current++; }, []);
  function cancel() {
    requestID.current++;
    inFlight.current = false;
    setWaiting(false);
    setPicker(null);
  }
  async function choose(kind, done) {
    if (inFlight.current) return;
    const current = ++requestID.current;
    inFlight.current = true;
    setWaiting(true);
    reportError("");
    try {
      const path = await client.authorize(kind);
      if (current !== requestID.current) return;
      if (path === undefined) setPicker({ kind, done });
      else if (path) done(path);
    } catch (e) {
      if (current === requestID.current) reportError(e.message);
    } finally {
      if (current === requestID.current) {
        inFlight.current = false;
        setWaiting(false);
      }
    }
  }
  return [
    choose,
    waiting ? (
      <Modal title="正在打开目录选择" onClose={cancel}>
        <p role="status">正在连接飞牛目录选择器；无法连接时将显示已授权目录。</p>
        <footer className="modal-footer">
          <button type="button" onClick={cancel}>取消目录选择</button>
        </footer>
      </Modal>
    ) : picker && (
      <FolderPicker
        client={client}
        kind={picker.kind}
        onClose={() => setPicker(null)}
        onSelect={(path) => {
          picker.done(path);
          setPicker(null);
        }}
      />
    ),
  ];
}
