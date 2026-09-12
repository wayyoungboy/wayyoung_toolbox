import { useCallback, useEffect, useState } from 'react';
import Button from '@douyinfe/semi-ui/lib/es/button';
import Modal from '@douyinfe/semi-ui/lib/es/modal';
import Spin from '@douyinfe/semi-ui/lib/es/spin';
import { IconArrowLeft, IconFolder, IconRefresh } from '@douyinfe/semi-icons';
import { api } from '../api';

export default function FolderPicker({ visible, onCancel, onSelect, title = '选择 JAVSP 扫描目录', loadDirectories = api.javspDirectories }) {
  const [listing, setListing] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(async (path) => {
    setLoading(true);
    setError('');
    try {
      setListing(await loadDirectories(path));
    } catch (loadError) {
      setError(loadError.message);
    } finally {
      setLoading(false);
    }
  }, [loadDirectories]);

  useEffect(() => {
    if (visible) load();
  }, [visible, load]);

  return (
    <Modal
      title={title}
      visible={visible}
      width={620}
      maskClosable={false}
      onCancel={onCancel}
      footer={(
        <div className="folder-picker-footer">
          <Button onClick={onCancel}>取消</Button>
          <Button theme="solid" type="primary" disabled={!listing || loading || Boolean(error)} onClick={() => onSelect(listing.path)}>选择此文件夹</Button>
        </div>
      )}
    >
      <div className="folder-picker-toolbar">
        <Button icon={<IconArrowLeft />} disabled={!listing?.parent || loading} onClick={() => load(listing.parent)}>上一级</Button>
        <code title={listing?.path || ''}>{listing?.path || '正在读取目录…'}</code>
        <Button icon={<IconRefresh />} disabled={loading} onClick={() => load(listing?.path)} aria-label="刷新目录" />
      </div>
      <div className="folder-picker-list" role="list" aria-label="子文件夹">
        {loading ? <div className="folder-picker-state"><Spin size="large" />正在读取目录…</div> : null}
        {!loading && error ? <div className="folder-picker-state danger-text">{error}</div> : null}
        {!loading && !error && listing?.items.map((item) => (
          <button type="button" key={item.path} onClick={() => load(item.path)}>
            <IconFolder aria-hidden="true" />
            <span>{item.name}</span>
          </button>
        ))}
        {!loading && !error && listing && listing.items.length === 0 ? <div className="folder-picker-state muted-text">当前目录没有子文件夹</div> : null}
      </div>
      {listing?.truncated ? <div className="folder-picker-warning">目录数量较多，仅显示前 1000 个子文件夹。</div> : null}
      <p className="folder-picker-hint">可以返回上一级并选择当前文件夹；浏览范围限定在当前存储空间内本应用可访问的目录。</p>
    </Modal>
  );
}
