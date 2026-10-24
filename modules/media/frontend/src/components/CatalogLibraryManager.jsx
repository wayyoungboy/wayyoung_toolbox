import { useCallback, useEffect, useRef, useState } from 'react';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Button from '@douyinfe/semi-ui/lib/es/button';
import FolderPicker from './FolderPicker';
import CatalogAutomation from './CatalogAutomation';
import { api } from '../api';

export default function CatalogLibraryManager({ libraries, completedRoot, onChanged, onClose }) {
  const [name, setName] = useState('我的影片');
  const [root, setRoot] = useState('');
  const [picker, setPicker] = useState(false);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [relink, setRelink] = useState(null);
  const scanController = useRef(null);
  useEffect(() => () => scanController.current?.abort(), []);
  const directories = useCallback(path => api.javspDirectories(path || root || completedRoot || undefined, { target: 'output' }), [root, completedRoot]);

  const add = async event => {
    event.preventDefault();
    setBusy('add'); setError(''); setNotice('');
    try {
      if (relink) await api.catalogRelinkLibrary(relink.id, root);
      else await api.catalogAddLibrary({ name, root });
      setRoot('');
      setNotice(relink ? '目录已重新关联。原馆藏和观看记录保留，扫描馆藏后恢复播放。' : '媒体库已添加。点击“扫描馆藏”后才会读取目录；不会移动或改名。');
      setRelink(null);
      await onChanged();
    } catch (failure) { setError(failure.message); }
    finally { setBusy(''); }
  };
  const scan = async library => {
    setBusy(String(library.id)); setError(''); setNotice('');
    scanController.current = new AbortController();
    try {
      const result = await api.catalogScan(library.id, { signal: scanController.current.signal });
      setNotice(`扫描完成：${result.work_count} 部作品、${result.asset_count} 个文件，${result.issue_count} 个文件资料需检查。原文件未修改。`);
      await onChanged();
    } catch (failure) { setError(failure.name === 'AbortError' ? '扫描已取消，上次馆藏记录保留。' : failure.message); }
    finally { setBusy(''); scanController.current = null; }
  };

  return <>
    <Modal title="媒体库管理" visible width="min(760px, calc(100vw - 24px))" maskClosable={false} onCancel={() => { scanController.current?.abort(); onClose(); }} footer={null}>
      <p className="catalog-help">添加已有目录，读取视频、本地 NFO、封面与字幕。扫描不会启动下载或 JAVSP，也不会修改原文件。</p>
      {error && <p className="catalog-alert" role="alert">{error}</p>}
      {notice && <p className="catalog-notice" role="status">{notice}</p>}
      <div className="catalog-library-list">
        {libraries.map(library => <div key={library.id} className="catalog-library-row">
          <div><strong>{library.name}</strong><code>{library.root}</code><small>{library.scanned_at ? `${library.asset_count} 个文件 · ${new Date(library.scanned_at).toLocaleString()} 扫描` : '尚未扫描'}{library.scan_error ? ` · ${library.scan_error}` : ''}</small></div>
          <div className="catalog-actions"><Button disabled={Boolean(busy)} onClick={() => { setRelink(library); setRoot(library.root); setName(library.name); }}>重新关联</Button><Button disabled={Boolean(busy)} loading={busy === String(library.id)} onClick={() => scan(library)}>扫描馆藏</Button></div>
        </div>)}
        {busy && busy !== 'add' && <Button onClick={() => scanController.current?.abort()}>取消扫描</Button>}
      </div>
      <CatalogAutomation />
      <form className="catalog-library-form" onSubmit={add}>
        <h3>{relink ? `重新关联 · ${relink.name}` : '添加媒体库'}</h3>
        {relink && <p className="catalog-help">仅在目录迁移、挂载恢复或旧版本升级后需要重新确认时操作。馆藏与观看记录保留；确认后，重新扫描此媒体库才能播放。</p>}
        <label>名称<input value={name} onChange={event => setName(event.target.value)} required maxLength={160} disabled={Boolean(busy) || Boolean(relink)} /></label>
        <div className="catalog-field"><label htmlFor="catalog-library-root">媒体目录</label><div className="catalog-path-input"><input id="catalog-library-root" value={root} onChange={event => setRoot(event.target.value)} placeholder="选择本应用已获授权的目录" required disabled={Boolean(busy)} /><Button disabled={Boolean(busy)} onClick={() => setPicker(true)}>选择文件夹</Button></div></div>
        {completedRoot && !libraries.some(library => library.root === completedRoot) && <Button className="catalog-use-existing" disabled={Boolean(busy)} onClick={() => setRoot(completedRoot)}>使用当前 JAVSP 成品目录</Button>}
        <div className="catalog-actions"><Button htmlType="submit" theme="solid" disabled={Boolean(busy) || !name.trim() || !root.trim()} loading={busy === 'add'}>{relink ? '确认关联' : '添加媒体库'}</Button>{relink && <Button disabled={Boolean(busy)} onClick={() => { setRelink(null); setRoot(''); }}>取消关联</Button>}</div>
      </form>
    </Modal>
    <FolderPicker visible={picker} title="选择馆藏媒体目录" loadDirectories={directories} onCancel={() => setPicker(false)} onSelect={path => { setRoot(path); setPicker(false); }} />
  </>;
}
