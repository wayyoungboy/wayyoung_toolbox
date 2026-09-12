import { useEffect, useState } from 'react';
import { IconArrowLeft, IconClose, IconFolder, IconRefresh } from '@douyinfe/semi-icons';

export default function FolderPicker({ client, open, title, initialPath, onCancel, onSelect }) {
  const [listing, setListing] = useState({ current: '', parent: '', entries: [] });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const load = async (path) => {
    setLoading(true);
    setError('');
    try {
      setListing(await client.listDirectories(path));
    } catch (reason) {
      setError(reason.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (open) load(initialPath || '');
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!open) return null;
  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onCancel}>
      <section className="folder-dialog" role="dialog" aria-modal="true" aria-labelledby="folder-title" onMouseDown={(event) => event.stopPropagation()}>
        <header className="dialog-header">
          <div>
            <h2 id="folder-title">{title}</h2>
            <p>{listing.current || '已授权的存储空间'}</p>
          </div>
          <button className="icon-button" type="button" aria-label="关闭" onClick={onCancel}><IconClose /></button>
        </header>
        <div className="folder-toolbar">
          <button className="secondary-button" type="button" disabled={!listing.parent || loading} onClick={() => load(listing.parent)}>
            <IconArrowLeft /> 上一级
          </button>
          <button className="icon-button" type="button" aria-label="刷新目录" disabled={loading} onClick={() => load(listing.current)}><IconRefresh /></button>
        </div>
        {error && <div className="inline-error">{error}</div>}
        <div className="folder-list" aria-busy={loading}>
          {!loading && listing.entries.length === 0 && <div className="empty-state">当前目录没有子文件夹</div>}
          {listing.entries.map((entry) => (
            <button className="folder-row" type="button" key={entry.path} onDoubleClick={() => load(entry.path)} onClick={() => load(entry.path)}>
              <IconFolder />
              <span>{entry.name}</span>
            </button>
          ))}
        </div>
        <footer className="dialog-actions">
          <button className="secondary-button" type="button" onClick={onCancel}>取消</button>
          <button className="primary-button" type="button" disabled={!listing.current} onClick={() => onSelect(listing.current)}>选择当前文件夹</button>
        </footer>
      </section>
    </div>
  );
}
